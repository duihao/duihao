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
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
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
//   把登录时采集的三个部件哈希与令牌一起加密存本机，Verify 时用
//   「当前机器部件 vs 快照部件」的命中数做 2/3 表决 —— 换一个部件（重装
//   系统盘/换 CPU）仍然通过，整盘拷贝到另一台机器 0/3 命中被拒。
type snapshot struct {
	Token   string    `json:"token"`
	Claims  *Claims   `json:"claims"`
	Parts   [3]string `json:"parts"` // 登录时采集的部件哈希（已排序）
	SavedAt int64     `json:"saved_at"`
}

// State 是暴露给上层（Wails 绑定 / 前端只读）的授权状态。
type State struct {
	LoggedIn bool   `json:"loggedIn"`
	Plan     string `json:"plan"`     // free/trial/annual/lifetime
	PlanText string `json:"planText"`
	Ent      int64  `json:"ent"`      // 权益到期，0 = 永久
	TokenExp int64  `json:"tokenExp"` // 令牌过期（30 天滑动）
	Sub      string `json:"sub"`
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
	mu       sync.RWMutex
	store    Storage
	client   *Client
	fpr      Fingerprinter
	snap     *snapshot
	keys     *keyCache
	nowFunc  func() time.Time // 测试可注入
}

// New 创建 Manager。dataDir 用于公钥缓存（keys.json，可离线验签）。
func New(dataDir string, opts ...Option) *Manager {
	m := &Manager{
		store:   defaultStorage(),
		client:  NewClient(defaultBaseURL),
		fpr:     defaultFingerprinter{},
		nowFunc: time.Now,
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
	if _, err := m.acceptToken(resp.Token, resp.Entitlement, nil); err != nil {
		return // 新令牌验签失败：保留旧状态到 exp
	}
}

// ─────────────────────────────────────────────────────────────
//  登录 / 退出
// ─────────────────────────────────────────────────────────────

// Login 用手机号 + 验证码登录并绑定本机设备。
func (m *Manager) Login(mobile, code string) (State, error) {
	parts, err := m.fpr.Parts()
	if err != nil || len(parts) != 3 {
		return State{}, errors.New("无法读取设备信息，请稍后再试")
	}
	resp, err := m.client.Login(mobile, code, dvhFromParts(parts), deviceName(), deviceOS())
	if err != nil {
		return State{}, err
	}
	return m.acceptToken(resp.Token, resp.Entitlement, &parts)
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

// acceptToken：验签 → 存快照（含公钥缓存刷新）。parts 为 nil 时沿用已有快照部件。
func (m *Manager) acceptToken(token string, ent Entitlement, parts *[3]string) (State, error) {
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
