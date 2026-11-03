// Package licence implements the Duihao desktop licensing client (B6).
//
// 与服务端契约（DuihaoCMS app/licensing/*，v42）：
//
//   - 令牌格式：base64url(payloadJSON) "." base64url(sig)，无 padding；
//     签名消息 = 第 1 段原文（不含点号）。ed25519 为 64B detached，
//     es256 为 DER (ASN.1)。
//   - payload 字段：iss/aud/sub/prod/plan/ent/seats/dvh/iat/exp/alg/kid/jti。
//     ent = 权益到期（0 = 永久）；exp = 令牌过期（与权益无关，30 天滑动）。
//   - 设备指纹 dvh = 64 位 sha256 十六进制（服务端 device_hash_clean 口径）。
//   - 客户端必须自校验：签名 → aud == duihao-desktop → dvh == 本机 → exp > now。
//
// 设计铁律（docs/用户体系与软件授权-实施清单.md §B6）：
//
//   - Allows(cap) 是门禁唯一入口，前端不参与判权，只看 plan / ent；
//   - 到期降级：关付费能力，本地文件 / 既有站点照常打开（不锁死）；
//   - 过期前 7 天静默 refresh，失败不回退登录态；
//   - 令牌文件拷到另一台机器必须被拒（设备指纹 2/3 部件表决）。
package licence

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// AudDuihaoDesktop 是桌面版令牌的 aud（products.token_aud）。
const AudDuihaoDesktop = "duihao-desktop"

// 令牌与权益的常量（与服务端语义一一对应）。
const (
	PlanFree     = "free"
	PlanTrial    = "trial"
	PlanAnnual   = "annual"
	PlanLifetime = "lifetime"

	// RefreshAhead：令牌 exp 剩余不足 7 天时静默续签。
	RefreshAhead = 7 * 24 * time.Hour
)

// planRank：档位强弱（与服务端 entitlement_plan_rank 一致）。
func planRank(plan string) int {
	switch plan {
	case PlanLifetime:
		return 3
	case PlanAnnual:
		return 2
	case PlanTrial:
		return 1
	default:
		return 0
	}
}

// Claims 是令牌 payload 的客户端视图（与服务端 token_issue 的字段一一对应）。
type Claims struct {
	Iss   string `json:"iss"`
	Aud   string `json:"aud"`
	Sub   string `json:"sub"`
	Prod  string `json:"prod"`
	Plan  string `json:"plan"`
	Ent   int64  `json:"ent"`
	Seats int    `json:"seats"`
	Dvh   string `json:"dvh"`
	Iat   int64  `json:"iat"`
	Exp   int64  `json:"exp"`
	Alg   string `json:"alg"`
	Kid   string `json:"kid"`
	Jti   string `json:"jti"`
}

// snapshot 是持久化到系统凭据库的内容（token + 验签过的 claims + 登录时的设备部件哈希）。
//
// ★ Parts 为什么跟着快照走：服务器只存 dvh 单值，无法做部件级表决；
//
//	把登录时采集的三个部件哈希与令牌一起加密存本机，Verify 时用
//	「当前机器部件 vs 快照部件」的命中数做 2/3 表决 —— 换一个部件（重装
//	系统盘/换 CPU）仍然通过，整盘拷贝到另一台机器 0/3 命中被拒。
type snapshot struct {
	Token    string    `json:"token"`
	Claims   *Claims   `json:"claims"`
	Parts    [3]string `json:"parts"`    // 登录时采集的部件哈希（已排序）
	Nickname string    `json:"nickname"` // 登录响应里的昵称（展示用）
	Mobile   string    `json:"mobile"`   // 登录响应里的打码手机号
	SavedAt  int64     `json:"saved_at"`
}

// State 是暴露给上层（Wails 绑定 / 前端只读）的授权状态。
type State struct {
	LoggedIn bool   `json:"loggedIn"`
	Plan     string `json:"plan"` // free/trial/annual/lifetime
	PlanText string `json:"planText"`
	Ent      int64  `json:"ent"`      // 权益到期，0 = 永久
	TokenExp int64  `json:"tokenExp"` // 令牌过期（30 天滑动）
	Sub      string `json:"sub"`
	Nickname string `json:"nickname"` // 登录时服务端返回的昵称（refresh 不更新）
	Mobile   string `json:"mobile"`   // 打码手机号（同上）
	Reason   string `json:"reason"`   // 当前状态成因（诊断用）
}

// EffectivePlan 返回计入权益到期后的实际档位：ent 已过期 → free。
func (c *Claims) EffectivePlan(now time.Time) string {
	if c == nil {
		return PlanFree
	}
	if planRank(c.Plan) >= planRank(PlanAnnual) && c.Ent > 0 && c.Ent <= now.Unix() {
		return PlanFree // 权益到期：annual/lifetime 降为 free（不删记录，续费即恢复）
	}
	if c.Plan == PlanTrial && c.Ent > 0 && c.Ent <= now.Unix() {
		return PlanFree
	}
	return c.Plan
}

// Manager 是授权门禁的唯一入口。并发安全。
type Manager struct {
	mu      sync.RWMutex
	store   Storage
	client  *Client
	fpr     Fingerprinter
	snap    *snapshot
	keys    *keyCache
	nowFunc func() time.Time // 测试可注入

	// paidRefreshed 记录「已因支付到账强制续签过」的订单号（本次会话内）。
	// 轮询会反复看到 paid，这里挡住重复续签；续签失败不标记，下轮轮询重试。
	paidRefreshed map[string]bool

	// 设备指纹/描述缓存。platformParts() 每次要起 3 个 wmic/PowerShell 进程、
	// deviceOS() 再起一个，合计 1.5~2s；放到 1s 一拍的扫码轮询里重算，
	// 会把轮询间隔拖成 2s+（表现为「没在检测」）。设备信息本就不会变，只采一次。
	devOnce  sync.Once
	devParts [3]string
	devName  string
	devOS    string
	devErr   error
}

// device 返回设备指纹与描述（首次采集后缓存）。
func (m *Manager) device() ([3]string, string, string, error) {
	m.devOnce.Do(func() {
		m.devParts, m.devErr = m.fpr.Parts()
		m.devName = deviceName()
		m.devOS = deviceOS()
	})
	return m.devParts, m.devName, m.devOS, m.devErr
}

// New 创建 Manager。dataDir 用于公钥缓存（keys.json，可离线验签）。
func New(dataDir string, opts ...Option) *Manager {
	m := &Manager{
		store:         defaultStorage(),
		client:        NewClient(defaultBaseURL),
		fpr:           defaultFingerprinter{},
		nowFunc:       time.Now,
		paidRefreshed: map[string]bool{},
	}
	m.keys = newKeyCache(dataDir)
	for _, o := range opts {
		o(m)
	}
	return m
}

// Option 定制 Manager（测试注入用）。
type Option func(*Manager)

// WithBaseURL 覆盖 API 地址。
func WithBaseURL(u string) Option { return func(m *Manager) { m.client = NewClient(u) } }

// WithStore 覆盖存储实现。
func WithStore(s Storage) Option { return func(m *Manager) { m.store = s } }

// WithFingerprinter 覆盖设备指纹采集器。
func WithFingerprinter(f Fingerprinter) Option { return func(m *Manager) { m.fpr = f } }

// WithNow 注入时钟（测试）。
func WithNow(f func() time.Time) Option { return func(m *Manager) { m.nowFunc = f } }

// ─────────────────────────────────────────────────────────────
//  状态
// ─────────────────────────────────────────────────────────────

// State 返回当前授权状态（永不报错：任何异常都折叠成 free，绝不 panic 卡启动）。
func (m *Manager) State() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stateLocked()
}

func (m *Manager) stateLocked() State {
	now := m.nowFunc()
	if m.snap == nil || m.snap.Claims == nil {
		return State{LoggedIn: false, Plan: PlanFree, PlanText: "免费版", Reason: "no_token"}
	}
	c := m.snap.Claims
	plan := c.EffectivePlan(now)
	st := State{
		LoggedIn: true,
		Plan:     plan,
		Ent:      c.Ent,
		TokenExp: c.Exp,
		Sub:      c.Sub,
		Nickname: m.snap.Nickname,
		Mobile:   m.snap.Mobile,
	}
	if plan != c.Plan {
		st.Reason = "entitlement_expired"
	}
	// 令牌本身过期但权益未到期：仍按已登录权益放行（验收①：30 天内拔网线照常），
	// 但标记原因，等待下一次联网 refresh。
	if c.Exp > 0 && c.Exp <= now.Unix() && st.Reason == "" {
		st.Reason = "token_expired_offline_grace"
	}
	st.PlanText = planText(plan)
	return st
}

func planText(p string) string {
	switch p {
	case PlanTrial:
		return "试用"
	case PlanAnnual:
		return "年费"
	case PlanLifetime:
		return "永久"
	default:
		return "免费版"
	}
}

// Allows 是门禁唯一入口。cap 传能力名：
//
//	"paid"   —— 年费及以上（annual/lifetime，且权益未到期）
//	"annual" —— 年费及以上
//	"lifetime" —— 仅永久
//
// 前端不参与判权；任何未知能力一律拒绝。
func (m *Manager) Allows(cap string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st := m.stateLocked()
	minPlan, ok := map[string]int{"paid": planRank(PlanAnnual), "annual": planRank(PlanAnnual), "lifetime": planRank(PlanLifetime)}[cap]
	if !ok {
		return false
	}
	return planRank(st.Plan) >= minPlan
}

// ─────────────────────────────────────────────────────────────
//  启动校验 / 静默续签
// ─────────────────────────────────────────────────────────────

// Verify 在启动时调用：加载快照 → 验签 → aud/dvh/exp 校验。
// 任何失败都折叠为未登录（free），**绝不弹死循环提示、不锁死本地能力**。
func (m *Manager) Verify() State {
	m.mu.Lock()
	defer m.mu.Unlock()

	raw, err := m.store.Load()
	if err != nil || raw == "" {
		m.snap = nil
		return m.stateLocked()
	}
	var snap snapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil || snap.Claims == nil {
		m.snap = nil
		return m.stateLocked()
	}

	// ① 验签 + 结构校验（alg 分派，防 alg 混淆由 verifyToken 保证）
	if err := m.verifyToken(snap.Token, snap.Claims); err != nil {
		m.snap = nil
		return m.stateLocked()
	}

	// ② 设备指纹 2/3 部件表决（拷贝到别的机器 0/3 → 拒）
	if err := m.checkDevice(&snap); err != nil {
		m.snap = nil
		return m.stateLocked()
	}

	m.snap = &snap
	return m.stateLocked()
}

// MaybeRefresh 在 exp 剩余不足 7 天时静默续签；失败不影响当前登录态。
// 由启动流程与低频定时器调用，网络错误静默。
func (m *Manager) MaybeRefresh() {
	m.mu.RLock()
	snap := m.snap
	if snap == nil || snap.Claims == nil {
		m.mu.RUnlock()
		return
	}
	due := snap.Claims.Exp <= 0 || time.Until(time.Unix(snap.Claims.Exp, 0)) < RefreshAhead
	if !due {
		m.mu.RUnlock()
		return
	}
	token := snap.Token
	m.mu.RUnlock()

	resp, err := m.client.Refresh(token)
	if err != nil {
		return // 静默：离线宽限到 exp
	}
	if _, err := m.acceptToken(resp.Token, resp.Entitlement, nil, "", ""); err != nil {
		return // 新令牌验签失败：保留旧状态到 exp
	}
}

// ─────────────────────────────────────────────────────────────
//  登录 / 退出
// ─────────────────────────────────────────────────────────────

// Login 用手机号 + 验证码登录并绑定本机设备。
func (m *Manager) Login(mobile, code string) (State, error) {
	parts, devName, devOS, err := m.device()
	if err != nil {
		return State{}, errors.New("无法读取设备信息，请稍后再试")
	}
	resp, err := m.client.Login(mobile, code, dvhFromParts(parts), devName, devOS)
	if err != nil {
		return State{}, err
	}
	return m.acceptToken(resp.Token, resp.Entitlement, &parts, resp.User.Nickname, resp.User.Mobile)
}

// PasswordLogin 用用户名（或邮箱）+ 密码登录并绑定本机设备。
// 服务端与验证码登录同端点（mode=password），无账号则报 E_LOGIN 不自动注册。
func (m *Manager) PasswordLogin(username, password string) (State, error) {
	parts, devName, devOS, err := m.device()
	if err != nil {
		return State{}, errors.New("无法读取设备信息，请稍后再试")
	}
	resp, err := m.client.PasswordLogin(username, password, dvhFromParts(parts), devName, devOS)
	if err != nil {
		return State{}, err
	}
	return m.acceptToken(resp.Token, resp.Entitlement, &parts, resp.User.Nickname, resp.User.Mobile)
}

// WechatQR 取桌面扫码登录二维码。qrURL 是微信/中转的原图地址，
// 由调用方（App 层）经 FetchQRData 转成 data URL 再交给前端。
func (m *Manager) WechatQR() (qrURL, scene, key string, err error) {
	// 预热设备指纹缓存：扫码命中后要立即签令牌，首拍轮询不该再等 WMI/PowerShell
	_, _, _, _ = m.device()
	r, err := m.client.WechatQrcode()
	if err != nil {
		return "", "", "", err
	}
	return r.QRURL, r.Scene, r.Key, nil
}

// WechatPoll 轮询扫码结果。
// waiting=true：用户尚未扫码，客户端稍后继续轮询（不产生状态变化）；
// 命中：服务端已按与验证码/密码登录完全相同的链路（权益归并 + 设备绑定 + 签令牌）
// 签发令牌，这里接受令牌即登录完成并广播 licence:state。
func (m *Manager) WechatPoll(scene, key string) (waiting bool, st State, err error) {
	parts, devName, devOS, err := m.device()
	if err != nil {
		return false, State{}, errors.New("无法读取设备信息，请稍后再试")
	}
	resp, waiting, err := m.client.WechatPoll(scene, key, dvhFromParts(parts), devName, devOS)
	if err != nil || waiting {
		return waiting, State{}, err
	}
	st, err = m.acceptToken(resp.Token, resp.Entitlement, &parts, resp.User.Nickname, resp.User.Mobile)
	return false, st, err
}

// WechatQRInfo / WechatPollResult：App 绑定层给前端的返回结构。
// QRInfo.DataURL 已由 Go 拉图转 base64（前端 <img> 直接用，不受 CSP 限制）。
type WechatQRInfo struct {
	DataURL string `json:"dataURL"`
	Scene   string `json:"scene"`
	Key     string `json:"key"`
}

type WechatPollResult struct {
	Waiting bool  `json:"waiting"` // true=尚未扫码，继续轮询
	State   State `json:"state"`   // 命中时的登录态（Waiting=false 时有效）
}

// ─────────────────────────────────────────────────────────────
//  订单 API 透传（会员中心应用内购买）
//  令牌取当前登录快照；未登录一律报错（服务端同样 401 兜底）。
// ─────────────────────────────────────────────────────────────

// currentToken 返回当前登录令牌（快照里的原文，供 Bearer 头使用）。
func (m *Manager) currentToken() (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.snap == nil || m.snap.Token == "" {
		return "", errors.New("未登录，请先登录")
	}
	return m.snap.Token, nil
}

// Products 拉取当前产品的可售规格（套餐卡）。
// 未登录也能拉：GET /api/order/product 在服务端是公开面（套餐价目本就公开展示），
// 会员中心左栏「会员权益」未登录同样要渲染。tok 为空走匿名请求。
func (m *Manager) Products() (*ProductsResp, error) {
	tok, _ := m.currentToken()
	return m.client.Products(tok)
}

// OrderCreate 下单并发起支付（价格服务端重算）。
func (m *Manager) OrderCreate(skuCode string, qty int) (*OrderCreateResp, error) {
	tok, err := m.currentToken()
	if err != nil {
		return nil, err
	}
	return m.client.OrderCreate(tok, skuCode, qty)
}

// OrderStatus 查单 + 触发服务端网关核对（入账只认服务端 payment_query）。
// ★ 支付到账（paid && has_license）→ 立即强制续签拉新权益：MaybeRefresh 只在
//
//	exp 临近才出网，不补这一步，用户付完款界面会一直停在旧档位。
func (m *Manager) OrderStatus(orderNo string) (*OrderStatusResp, error) {
	tok, err := m.currentToken()
	if err != nil {
		return nil, err
	}
	resp, err := m.client.OrderStatus(tok, orderNo)
	if err != nil {
		return nil, err
	}
	if resp.Paid && resp.HasLicense {
		m.refreshOnceForOrder(orderNo)
	}
	return resp, nil
}

// refreshOnceForOrder 每个订单号只触发一次到账续签（失败不标记，下轮重试）。
func (m *Manager) refreshOnceForOrder(orderNo string) {
	m.mu.RLock()
	already := m.paidRefreshed[orderNo]
	m.mu.RUnlock()
	if already {
		return
	}
	if _, err := m.ForceRefresh(); err != nil {
		return // 下轮轮询 paid 时重试
	}
	m.mu.Lock()
	m.paidRefreshed[orderNo] = true
	m.mu.Unlock()
}

// ForceRefresh 无视 7 天提前量立即续签（支付到账 / 用户手动刷新权益用）。
// 失败返回错误但保留旧状态（离线宽限到 exp）。
func (m *Manager) ForceRefresh() (State, error) {
	tok, err := m.currentToken()
	if err != nil {
		return State{}, err
	}
	resp, err := m.client.Refresh(tok)
	if err != nil {
		return m.State(), err
	}
	return m.acceptToken(resp.Token, resp.Entitlement, nil, "", "")
}

// FetchQRData 把服务端下发的二维码图 URL 拉成本地 data URL（前端 <img> 直接用）。
// ★ 不让前端直连外链：Wails CSP 可能拦远程图、断网时白框；由 Go 侧拉一次转
//
//	base64，顺带把出站连接收进客户端自己的网络栈（Proxy:nil，不走系统代理）。
func (m *Manager) FetchQRData(qrURL string) (string, error) {
	qrURL = strings.TrimSpace(qrURL)
	if qrURL == "" {
		return "", errors.New("二维码地址为空")
	}
	if !strings.HasPrefix(qrURL, "https://") && !strings.HasPrefix(qrURL, "http://") {
		return "", errors.New("二维码地址不是 http(s) 链接")
	}
	tr := &http.Transport{Proxy: nil}
	hc := &http.Client{Transport: tr, Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodGet, qrURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "DuihaoDesktop")
	hresp, err := hc.Do(req)
	if err != nil {
		return "", errors.New("二维码下载失败：" + err.Error())
	}
	defer hresp.Body.Close()
	if hresp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("二维码下载失败：HTTP %d", hresp.StatusCode)
	}
	buf, err := io.ReadAll(io.LimitReader(hresp.Body, 2<<20)) // 上限 2MB
	if err != nil {
		return "", errors.New("二维码读取失败：" + err.Error())
	}
	if len(buf) == 0 {
		return "", errors.New("二维码内容为空")
	}
	mime := "image/png"
	if ct := strings.TrimSpace(hresp.Header.Get("Content-Type")); strings.HasPrefix(ct, "image/") {
		mime = ct
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(buf), nil
}

// Logout 清除本机令牌（不解绑服务器侧设备；自助解绑走后台）。
func (m *Manager) Logout() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snap = nil
	return m.store.Save("")
}

// SendCode 发送验证码。
func (m *Manager) SendCode(mobile string) error { return m.client.SendCode(mobile) }

// acceptToken：验签 → 存快照（含公钥缓存刷新）。parts 为 nil 时沿用已有快照部件；
// nick/mob 为登录响应的昵称与打码手机号（refresh 不带 → 传空串沿用旧值）。
func (m *Manager) acceptToken(token string, ent Entitlement, parts *[3]string, nick, mob string) (State, error) {
	claims, err := m.parseAndFetchKey(token)
	if err != nil {
		return State{}, err
	}
	if claims.Aud != AudDuihaoDesktop {
		return State{}, errors.New("令牌不属于对号桌面版")
	}
	// 服务端返回的 entitlement 是权威值，覆盖 payload（二者本应一致）
	if ent.Plan != "" {
		claims.Plan = ent.Plan
		claims.Ent = ent.Ent
		claims.Seats = ent.Seats
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	snap := snapshot{Token: token, Claims: claims, SavedAt: m.nowFunc().Unix()}
	if parts != nil {
		snap.Parts = *parts
	} else if m.snap != nil {
		snap.Parts = m.snap.Parts
	}
	if nick != "" {
		snap.Nickname = nick
	} else if m.snap != nil {
		snap.Nickname = m.snap.Nickname
	}
	if mob != "" {
		snap.Mobile = mob
	} else if m.snap != nil {
		snap.Mobile = m.snap.Mobile
	}
	if err := m.checkDevice(&snap); err != nil {
		return State{}, err
	}
	raw, _ := json.Marshal(snap)
	if err := m.store.Save(string(raw)); err != nil {
		return State{}, errors.New("本机凭据库写入失败：" + err.Error())
	}
	m.snap = &snap
	return m.stateLocked(), nil
}

// ─────────────────────────────────────────────────────────────
//  设备指纹
// ─────────────────────────────────────────────────────────────

// Fingerprinter 采集本机三个硬件部件的稳定哈希。
type Fingerprinter interface {
	// Parts 返回 [CPU, 主板, 系统盘] 三个部件哈希（64hex，已排序输入）。
	Parts() ([3]string, error)
}

// dvhFromParts：部件哈希排序后拼接再 sha256 → 64hex 小写。
// 排序保证部件顺序无关（WMI 枚举顺序不应影响结果）。
func dvhFromParts(parts [3]string) string {
	s := append([]string(nil), parts[:]...)
	sort.Strings(s)
	h := sha256.Sum256([]byte("dvh-v1:" + s[0] + ":" + s[1] + ":" + s[2]))
	return hex.EncodeToString(h[:])
}

// checkDevice：当前机器部件 vs 快照部件，≥2 命中算同机；
// 命中后用**快照部件**合成 dvh 与令牌内的 dvh 比对（令牌只认登录那台机器的指纹）。
func (m *Manager) checkDevice(snap *snapshot) error {
	if snap.Claims == nil || snap.Claims.Dvh == "" {
		return errors.New("令牌缺少设备指纹")
	}
	cur, err := m.fpr.Parts()
	if err != nil {
		return errors.New("无法读取设备信息")
	}
	old := append([]string(nil), snap.Parts[:]...)
	sort.Strings(old)
	curS := append([]string(nil), cur[:]...)
	sort.Strings(curS)
	hit := 0
	for i := range old {
		if old[i] != "" && old[i] == curS[i] {
			hit++
		}
	}
	if hit < 2 {
		return errors.New("设备不符（该令牌属于另一台机器）")
	}
	if dvhFromParts(snap.Parts) != snap.Claims.Dvh {
		return errors.New("设备指纹与令牌不一致")
	}
	return nil
}
