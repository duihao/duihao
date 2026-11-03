package main

// IPv6 直连（桌面弹窗）的后端：读写网盘实例 hub.db 里的 remote_settings，
// 并把公网总开关的缓存立刻刷新，避免「点了开启还是 403」。
//
// 为什么在 App 层重写一遍读写：弹窗是 Wails 窗口，走的是 Go 绑定，不是网盘
// 网页的 HTTP 接口（`/-api/remote/*` 要登录 cookie）。同一个库两处写，规则必须
// 与 apps/remote 对齐 —— 键名、uid、取值都在这里注释清楚，改一边要改两边。
//
// 口径（与 apps/remote 一致，见 store/migrations/018_remote.sql）：
//   public.enabled   "1"/"0"   公网总开关，guard 对非内网来源的放行依据
//   ddns.domain      string    主域名（可空）
//   ddns.sub         string    子域前缀（可空 = 根域）
//   ddns.provider    string    "" = 手动解析；dnspod / aliyun = 自动改 AAAA
//   ddns.token       密文      credentials 包产出，只进不出
//   ddns.interval    string    自动更新的间隔分钟（最小 5）
//   ddns.last_*      string    上一轮结果（last_ip/last_ok/last_msg/last_at）
//
// 动态域名两种玩法，界面上就是「解析方式」这一项：
//   · 选了 DNSPod / 阿里云 且密钥在手 -> 后台循环盯着本机 IPv6，变了就改 AAAA；
//   · 只填域名不选解析方式        -> 手动解析，界面把当前该填的 AAAA 摆出来自己加。
//
// 地址列表来自 sysinfo.GlobalIPv6All（本机所有 GUA），端口取实例 registry 的
// dav 端口 —— 与首屏地址卡同一来源，用户看到的端口不会两处不一致。

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ys-ll/uniterm/backend/log"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/apps/remote"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/credentials"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/sysinfo"
)

// V6State IPv6 直连弹窗一次渲染所需的全部状态。
type V6State struct {
	Enabled  bool     `json:"enabled"`  // 公网总开关
	Addrs    []string `json:"addrs"`    // 本机 GUA 列表（可空）
	Port     int      `json:"port"`     // 网盘 dav 端口
	Domain   string   `json:"domain"`   // DDNS 主域名
	Sub      string   `json:"sub"`      // DDNS 子域前缀
	Instance string   `json:"instance"` // 网盘实例 id（无实例时为空）

	// ── 动态域名（DDNS）完整态 ──
	Provider string `json:"provider"` // "" 手动 / dnspod / aliyun 自动
	TokenSet bool   `json:"tokenSet"` // 密钥已存（密文），永不回明文
	Interval int    `json:"interval"` // 自动更新间隔（分钟）
	LastIP   string `json:"lastIP"`   // 上一轮写进去的地址
	LastOK   bool   `json:"lastOK"`   // 上一轮是否成功
	LastMsg  string `json:"lastMsg"`  // 上一轮的人话结果
	LastAt   string `json:"lastAt"`   // 上一轮时间
	Full     string `json:"full"`     // 完整域名 sub.domain（空 = 没配）
	Auto     bool   `json:"auto"`     // 会自动改 AAAA（选了服务商且密钥在手）
}

// v6Open 打开网盘实例的 hub.db，返回库、管理员 uid、实例 id。
// 只读场景也要开库（库不存在说明网盘从没建过），错误直接给出人话。
func (a *App) v6Open() (*sql.DB, string, string, error) {
	if a.dhMgr == nil {
		return nil, "", "", fmt.Errorf("环境工具尚未就绪")
	}
	id := ""
	for _, v := range a.dhMgr.List() {
		if v.Plugin == "webdav" {
			id = v.ID
			break
		}
	}
	if id == "" {
		return nil, "", "", fmt.Errorf("还没有创建网盘，先建一个再来")
	}
	p := filepath.Join(a.dhMgr.InstanceDirPath(id), "hub.db")
	dsn := "file:" + filepath.ToSlash(p) +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, "", "", fmt.Errorf("打开个人空间库失败: %w", err)
	}
	// 网盘服务没在跑时库可能是空的（表还没迁移）：读不到就按默认值，不报错。
	uid := "admin"
	var u string
	if err := db.QueryRow(`select uid from hub_users where role='admin' order by uid limit 1`).Scan(&u); err == nil && u != "" {
		uid = u
	} else if err := db.QueryRow(`select uid from remote_settings limit 1`).Scan(&u); err == nil && u != "" {
		uid = u
	}
	return db, uid, id, nil
}

func v6Get(db *sql.DB, uid, k string) string {
	var v string
	_ = db.QueryRow(`select v from remote_settings where uid=? and k=?`, uid, k).Scan(&v)
	return v
}

func v6Put(db *sql.DB, uid, k, v string) error {
	_, err := db.Exec(`insert into remote_settings(uid,k,v,updated_at) values(?,?,?,?)
		on conflict(uid,k) do update set v=excluded.v, updated_at=excluded.updated_at`,
		uid, k, v, time.Now().Format(time.RFC3339))
	return err
}

// v6Port 网盘 dav 端口（registry 里的实际值，取不到时用插件默认 9111）。
func (a *App) v6Port(instanceID string) int {
	if a.dhMgr == nil {
		return 9111
	}
	if rec := a.dhMgr.GetRecord(instanceID); rec != nil {
		if p := rec.Ports["dav"]; p > 0 {
			return p
		}
	}
	return 9111
}

// v6Full 完整域名：sub 为空 = 根域，@ 等同根域（与 remote.fullDomain 同口径）。
func v6Full(domain, sub string) string {
	if domain == "" {
		return ""
	}
	if sub == "" || sub == "@" {
		return domain
	}
	return sub + "." + domain
}

// V6Probe 读当前状态（不写库）：地址、端口、开关、DDNS 全套。
// 出口探测单独一个 V6CheckOut —— 那个要真拨 IPv6（最长 6 秒），
// 放在这一步会让弹窗打开就卡住。
func (a *App) V6Probe() (*V6State, error) {
	db, uid, id, err := a.v6Open()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	st := &V6State{
		Enabled:  v6Get(db, uid, "public.enabled") == "1",
		Addrs:    sysinfo.GlobalIPv6All(),
		Port:     a.v6Port(id),
		Domain:   v6Get(db, uid, "ddns.domain"),
		Sub:      v6Get(db, uid, "ddns.sub"),
		Instance: id,

		Provider: v6Get(db, uid, "ddns.provider"),
		TokenSet: v6Get(db, uid, "ddns.token") != "",
		LastIP:   v6Get(db, uid, "ddns.last_ip"),
		LastOK:   v6Get(db, uid, "ddns.last_ok") == "1",
		LastMsg:  v6Get(db, uid, "ddns.last_msg"),
		LastAt:   v6Get(db, uid, "ddns.last_at"),
	}
	if n, err := strconv.Atoi(v6Get(db, uid, "ddns.interval")); err == nil && n >= 5 {
		st.Interval = n
	} else {
		st.Interval = 10
	}
	st.Full = v6Full(st.Domain, st.Sub)
	st.Auto = st.Provider != "" && st.TokenSet
	if st.Addrs == nil {
		st.Addrs = []string{}
	}
	return st, nil
}

// V6CheckOut 本机 IPv6 能不能真出公网（拨公共 IPv6 DNS 的 443，任一通即真）。
func (a *App) V6CheckOut() bool {
	ok := sysinfo.IPv6Outbound()
	log.Writef("v6: outbound check -> %v (addrs=%v)", ok, sysinfo.GlobalIPv6All())
	return ok
}

// V6Enable 开启公网直连：写 public.enabled=1 并立刻刷新 guard 缓存。
func (a *App) V6Enable() (*V6State, error) {
	db, uid, _, err := a.v6Open()
	if err != nil {
		return nil, err
	}
	if err := v6Put(db, uid, "public.enabled", "1"); err != nil {
		db.Close()
		return nil, fmt.Errorf("保存失败: %w", err)
	}
	db.Close()
	remote.SetPublicEnabled(true)
	return a.V6Probe()
}

// V6Disable 解绑（关公网直连）：关掉后非内网来源一律 403。
func (a *App) V6Disable() (*V6State, error) {
	db, uid, _, err := a.v6Open()
	if err != nil {
		return nil, err
	}
	if err := v6Put(db, uid, "public.enabled", "0"); err != nil {
		db.Close()
		return nil, fmt.Errorf("保存失败: %w", err)
	}
	db.Close()
	remote.SetPublicEnabled(false)
	return a.V6Probe()
}

// V6DDNSIn 动态域名一次提交的全部字段（与网页端 ddns.save 同口径）。
type V6DDNSIn struct {
	Domain     string `json:"domain"`
	Sub        string `json:"sub"`
	Provider   string `json:"provider"`
	Token      string `json:"token"`
	ClearToken bool   `json:"clearToken"`
	Interval   int    `json:"interval"`
}

// v6KeyFormat 密钥格式提示（两种服务商的令牌长得完全不同，报错要说清）。
func v6KeyFormat(provider string) string {
	if provider == "aliyun" {
		return "AccessKeyID:AccessKeySecret 两段"
	}
	return "ID,Token 两段"
}

// V6SaveDDNS 保存动态域名配置。
//
// provider 为空 = 手动解析：只存域名，不碰令牌，界面会把当前该填的 AAAA 摆出来。
// provider 非空 = 自动解析：必须能拿到令牌（这次填的或上次存的），存完立刻真跑
// 一轮 —— 用户点「保存并解析」，就该当场看到 AAAA 改没改成功，而不是等 10 分钟。
//
// 域名全空时相当于清除配置（令牌留着，方便下次换域名接着用）。
func (a *App) V6SaveDDNS(in V6DDNSIn) (*V6State, error) {
	provider := strings.ToLower(strings.TrimSpace(in.Provider))
	switch provider {
	case "", "dnspod", "aliyun":
	default:
		return nil, fmt.Errorf("解析方式只支持 DNSPod / 阿里云；不想自动就选「手动解析」")
	}
	domain := strings.TrimSpace(in.Domain)
	sub := strings.TrimSpace(strings.TrimPrefix(in.Sub, "."))
	if domain != "" && !strings.Contains(domain, ".") {
		return nil, fmt.Errorf("主域名看着不对：%s（形如 example.com）", domain)
	}
	if provider == "" && strings.TrimSpace(in.Token) != "" {
		return nil, fmt.Errorf("手动解析不需要密钥；要自动改 AAAA，请先选解析方式")
	}

	db, uid, _, err := a.v6Open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	tokenSet := v6Get(db, uid, "ddns.token") != ""
	if provider != "" && !in.ClearToken && strings.TrimSpace(in.Token) == "" && !tokenSet {
		return nil, fmt.Errorf("选了自动解析就要填密钥（%s）", v6KeyFormat(provider))
	}

	interval := in.Interval
	if interval <= 0 {
		interval = 10
	}
	if interval < 5 {
		interval = 5 // 比 5 分钟更勤，DNS 服务商要限流
	}

	if err := v6Put(db, uid, "ddns.provider", provider); err != nil {
		return nil, fmt.Errorf("保存失败: %w", err)
	}
	if err := v6Put(db, uid, "ddns.domain", domain); err != nil {
		return nil, fmt.Errorf("保存失败: %w", err)
	}
	if err := v6Put(db, uid, "ddns.sub", sub); err != nil {
		return nil, fmt.Errorf("保存失败: %w", err)
	}
	if err := v6Put(db, uid, "ddns.interval", strconv.Itoa(interval)); err != nil {
		return nil, fmt.Errorf("保存失败: %w", err)
	}
	switch {
	case in.ClearToken:
		if err := v6Put(db, uid, "ddns.token", ""); err != nil {
			return nil, fmt.Errorf("保存失败: %w", err)
		}
	case strings.TrimSpace(in.Token) != "":
		sealed, err := credentials.Seal(strings.TrimSpace(in.Token))
		if err != nil {
			return nil, fmt.Errorf("密钥加密失败: %w", err)
		}
		if err := v6Put(db, uid, "ddns.token", sealed); err != nil {
			return nil, fmt.Errorf("保存失败: %w", err)
		}
	}

	// 配置动过就把「上次写入的地址」清掉：否则新域名/新令牌下第一轮会因
	// 「IP 没变，跳过」而永远写不进去，界面还显示旧的成功记录。
	if err := v6Put(db, uid, "ddns.last_ip", ""); err != nil {
		return nil, fmt.Errorf("保存失败: %w", err)
	}
	_ = v6Put(db, uid, "ddns.last_ok", "0")
	_ = v6Put(db, uid, "ddns.last_msg", "")

	remote.Kick() // 叫醒后台循环按新配置跑
	if provider != "" && domain != "" {
		// 同步跑一轮：这一步的成败就是「自动解析到底通不通」的答案，
		// 结果已经落在 ddns.last_* 里，随下面的 V6Probe 一起回给界面。
		if msg, ok := remote.UpdateOnce(db, uid); !ok {
			log.Writef("v6: ddns 立即解析未成功: %s", msg)
		}
	}
	return a.V6Probe()
}

// V6RunResult 「立即更新」的回执：本轮结果 + 刷新后的整套状态。
type V6RunResult struct {
	OK    bool     `json:"ok"`
	Msg   string   `json:"msg"`
	State *V6State `json:"state"`
}

// V6UpdateNow 立刻跑一轮自动解析（界面上的「立即更新」）。
// 失败不当异常抛：服务商的拒绝原因（令牌没权限、记录不存在）是给人看的结果，
// 由界面显示比弹个 error 更好读。
func (a *App) V6UpdateNow() (*V6RunResult, error) {
	db, uid, _, err := a.v6Open()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if v6Get(db, uid, "ddns.domain") == "" {
		return nil, fmt.Errorf("先填域名并保存")
	}
	if v6Get(db, uid, "ddns.provider") == "" {
		return nil, fmt.Errorf("当前是手动解析，没有可自动执行的动作")
	}
	msg, ok := remote.UpdateOnce(db, uid)
	st, err := a.V6Probe()
	if err != nil {
		return nil, err
	}
	return &V6RunResult{OK: ok, Msg: msg, State: st}, nil
}
