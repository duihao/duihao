// Package remote 远程访问：出门在外摸得到家里这台对号。
//
// 三条路线共用一个设置袋（remote_settings，迁移 018）：
//
//	① IPv6 直连   家宽开 IPv6 + 防火墙放行，自签证书全程加密 —— 免域名、免备案；
//	② frp 中继    frpc 进程连用户自己的 frps（下载器模式取包，对号只调进程），
//	              frpc↔frps 隧道自带 TLS；可视化配置生成 toml，不手写配置文件；
//	③ DDNS        腾讯 DNSPod / 阿里云，AAAA 记录跟着本机 IPv6 走；
//	              令牌进凭证保险箱，将来 ACME 自动签发也吃这一份令牌。
//
// 口径：
//   - 公网总开关默认**关**：关着时非局域网来源一律 403（guard 调 PublicEnabled）；
//   - 登录鉴权沿用既有 Basic + Bcrypt（guard 第二步本来就拦所有请求），
//     这里补的是「失败限速」——公网上不锁爆破等于没锁门；
//   - 令牌只进不出：接口只回 tokenSet 布尔，永不回明文（语音引擎同一套规矩）。
package remote

import (
	"database/sql"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
	"github.com/duihao/duihao/backend/duihao/webdavd/credentials"
	"github.com/duihao/duihao/backend/duihao/webdavd/sysinfo"
	"github.com/duihao/duihao/backend/duihao/webdavd/webui"
)

type App struct{}

func New() *App { return &App{} }

func (a *App) ID() string    { return "remote" }
func (a *App) Title() string { return "公网" }

func (a *App) Page(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	return webui.Render(w, webui.Page{App: a.ID(), Title: "公网", User: c.User, Writable: c.Writable, Role: c.Role, Perms: c.Perms})
}

func (a *App) API(c appkit.Context, w http.ResponseWriter, r *http.Request, action string) error {
	if c.DB == nil {
		return appkit.Errorf(http.StatusServiceUnavailable, "个人空间库没就绪，先启动网盘服务")
	}
	switch action {
	case "ping":
		appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "app": a.ID()})
	case "status":
		return status(c, w)
	case "public.save":
		return savePublic(c, w, r)
	case "ddns.save":
		return saveDDNS(c, w, r)
	case "ddns.test":
		return testDDNS(c, w, r)
	case "ddns.update":
		return updateNow(c, w)
	case "frpc.save":
		return saveFrp(c, w, r)
	case "frpc.start":
		return startFrpAPI(c, w)
	case "frpc.stop":
		return stopFrpAPI(w)
	case "comp.install":
		return installComp(w)
	case "comp.import":
		return importComp(c, w, r)
	default:
		return appkit.Errorf(http.StatusNotFound, "未知动作：%s", action)
	}
	return nil
}

// ---------- 键值袋 ----------

func getKV(db *sql.DB, uid, k string) string {
	var v string
	_ = db.QueryRow(`select v from remote_settings where uid=? and k=?`, uid, k).Scan(&v)
	return v
}

func putKV(db *sql.DB, uid, k, v string) error {
	_, err := db.Exec(`insert into remote_settings(uid,k,v,updated_at) values(?,?,?,?)
		on conflict(uid,k) do update set v=excluded.v, updated_at=excluded.updated_at`,
		uid, k, v, time.Now().Format(time.RFC3339))
	return err
}

// sealToken 令牌入库：密文落库，库里不存明文。
func sealToken(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	return credentials.Seal(plain)
}

// openToken 取回令牌明文（只给服务端调 DNS API 用，绝不回给页面）。
func openToken(sealed string) string {
	if sealed == "" {
		return ""
	}
	p, err := credentials.Open(sealed)
	if err != nil {
		Logf("remote: DNS 令牌解不开（换过机器/用户？）: %v", err)
		return ""
	}
	return p
}

// ---------- 状态 ----------

func status(c appkit.Context, w http.ResponseWriter) error {
	db, uid := c.DB, c.User
	out := map[string]any{}

	addr := sysinfo.GlobalIPv6()
	addrs := sysinfo.GlobalIPv6All()
	if addrs == nil {
		addrs = []string{}
	}
	out["ipv6"] = map[string]any{
		"addr":  addr,
		"addrs": addrs, // 全部 GUA：运营商会同时下发接口地址 + 隐私扩展临时地址
		"out":   addr != "" && sysinfo.IPv6Outbound(),
	}
	out["public"] = map[string]any{"enabled": getKV(db, uid, "public.enabled") == "1"}
	out["local"] = localSnapshot()

	dn := map[string]any{
		"provider": getKV(db, uid, "ddns.provider"),
		"domain":   getKV(db, uid, "ddns.domain"),
		"sub":      getKV(db, uid, "ddns.sub"),
		"tokenSet": getKV(db, uid, "ddns.token") != "",
	}
	if n, err := strconv.Atoi(getKV(db, uid, "ddns.interval")); err == nil && n >= 5 {
		dn["interval"] = n
	} else {
		dn["interval"] = 10
	}
	dn["lastIP"] = getKV(db, uid, "ddns.last_ip")
	dn["lastOK"] = getKV(db, uid, "ddns.last_ok") == "1"
	dn["lastMsg"] = getKV(db, uid, "ddns.last_msg")
	dn["lastAt"] = getKV(db, uid, "ddns.last_at")
	out["ddns"] = dn

	out["frpc"] = frpView(c)
	appkit.JSON(w, http.StatusOK, out)
	return nil
}

// ---------- 公网总开关 ----------

func savePublic(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	_ = appkit.Decode(r, &in)
	v := "0"
	if in.Enabled {
		v = "1"
	}
	if err := putKV(c.DB, c.User, "public.enabled", v); err != nil {
		return appkit.Errorf(http.StatusInternalServerError, "保存失败：%v", err)
	}
	pubCacheStore(in.Enabled)
	Logf("remote: 公网访问开关 → %s（by %s）", v, c.User)
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": in.Enabled})
	return nil
}

// ---------- DDNS 设置 ----------

func saveDDNS(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Provider   string `json:"provider"`
		Domain     string `json:"domain"`
		Sub        string `json:"sub"`
		Token      string `json:"token"`
		ClearToken bool   `json:"clearToken"`
		Interval   int    `json:"interval"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "参数解析失败：%v", err)
	}
	provider := strings.ToLower(strings.TrimSpace(in.Provider))
	if provider != "" && provider != "dnspod" && provider != "aliyun" {
		return appkit.Errorf(http.StatusBadRequest, "DNS 服务商只支持 dnspod / aliyun")
	}
	domain := strings.TrimSpace(in.Domain)
	if domain != "" && !strings.Contains(domain, ".") {
		return appkit.Errorf(http.StatusBadRequest, "主域名看着不对：%s", domain)
	}
	sub := strings.TrimSpace(in.Sub)
	interval := in.Interval
	if interval <= 0 {
		interval = 10
	}
	if interval < 5 {
		return appkit.Errorf(http.StatusBadRequest, "更新间隔最小 5 分钟——比这更勤，DNS 服务商要限流了")
	}

	if provider != "" {
		if err := putKV(c.DB, c.User, "ddns.provider", provider); err != nil {
			return appkit.Errorf(http.StatusInternalServerError, "保存失败：%v", err)
		}
	}
	if in.Domain != "" || in.Sub != "" {
		if err := putKV(c.DB, c.User, "ddns.domain", domain); err != nil {
			return appkit.Errorf(http.StatusInternalServerError, "保存失败：%v", err)
		}
		if err := putKV(c.DB, c.User, "ddns.sub", sub); err != nil {
			return appkit.Errorf(http.StatusInternalServerError, "保存失败：%v", err)
		}
	}
	if err := putKV(c.DB, c.User, "ddns.interval", strconv.Itoa(interval)); err != nil {
		return appkit.Errorf(http.StatusInternalServerError, "保存失败：%v", err)
	}
	switch {
	case in.ClearToken:
		if err := putKV(c.DB, c.User, "ddns.token", ""); err != nil {
			return appkit.Errorf(http.StatusInternalServerError, "保存失败：%v", err)
		}
	case in.Token != "":
		sealed, err := sealToken(strings.TrimSpace(in.Token))
		if err != nil {
			return appkit.Errorf(http.StatusInternalServerError, "令牌加密失败：%v", err)
		}
		if err := putKV(c.DB, c.User, "ddns.token", sealed); err != nil {
			return appkit.Errorf(http.StatusInternalServerError, "保存失败：%v", err)
		}
	}
	ddnsKick() // 存完立刻按新配置跑一轮
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// testDDNS 用「表单里填的」令牌试一次真实更新——用户要能在保存前就知道
// 令牌对不对、权限够不够。测试会真的写一条 AAAA（值就是当前 IP）。
func testDDNS(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Provider string `json:"provider"`
		Domain   string `json:"domain"`
		Sub      string `json:"sub"`
		Token    string `json:"token"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "参数解析失败：%v", err)
	}
	token := strings.TrimSpace(in.Token)
	if token == "" { // 没填就用已存的
		token = openToken(getKV(c.DB, c.User, "ddns.token"))
	}
	ip := sysinfo.GlobalIPv6()
	if ip == "" {
		return appkit.Errorf(http.StatusBadRequest, "本机没有公网 IPv6 地址，先确认宽带和光猫开了 IPv6")
	}
	msg, err := ddnsUpdate(strings.ToLower(strings.TrimSpace(in.Provider)),
		strings.TrimSpace(in.Domain), strings.TrimSpace(in.Sub), token, ip)
	if err != nil {
		appkit.JSON(w, http.StatusOK, map[string]any{"ok": false, "msg": msg})
		return nil
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "msg": msg, "ip": ip})
	return nil
}

func updateNow(c appkit.Context, w http.ResponseWriter) error {
	if msg, ok := ddnsRun(c.DB, c.User); !ok {
		return appkit.Errorf(http.StatusBadRequest, "%s", msg)
	} else {
		appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "msg": msg})
	}
	return nil
}

// ---------- DDNS 后台循环 ----------

var (
	ddnsMu      sync.Mutex
	ddnsKickCh  = make(chan struct{}, 1)
	ddnsLastRun time.Time
)

func ddnsKick() {
	select {
	case ddnsKickCh <- struct{}{}:
	default:
	}
}

// Start 由 webdavd 启动时调一次：起 DDNS 循环（frpc 的自动拉起也在里面）。
func Start(getDB func() *sql.DB) {
	go func() {
		time.Sleep(5 * time.Second) // 等库与目录就绪
		for {
			db := getDB()
			if db == nil {
				time.Sleep(15 * time.Second)
				continue
			}
			uid := firstUID(db)
			if uid != "" {
				_, _ = ddnsRun(db, uid)
				frpcAutoStart(db, uid)
			}
			interval := 10 * time.Minute
			if db != nil && uid != "" {
				if n, err := strconv.Atoi(getKV(db, uid, "ddns.interval")); err == nil && n >= 5 {
					interval = time.Duration(n) * time.Minute
				}
			}
			select {
			case <-time.After(interval):
			case <-ddnsKickCh:
			}
		}
	}()
}

// firstUID 库里第一个账号（个人空间单账号口径；多账号时 DDNS 跟第一个走）。
func firstUID(db *sql.DB) string {
	var uid string
	_ = db.QueryRow(`select uid from remote_settings limit 1`).Scan(&uid)
	return uid
}

// UpdateOnce 立刻跑一轮 DDNS 并把结果说人话 —— 桌面 IPv6 弹窗的「保存并解析」
// 与「立即更新」走这里。刻意只做转发：执行体只能有一个（ddnsRun），网页
// ddns.update 与桌面弹窗两条入口一旦各写一份，迟早对不上。
func UpdateOnce(db *sql.DB, uid string) (string, bool) { return ddnsRun(db, uid) }

// Kick 叫醒后台循环：改完配置立刻按新值跑一轮，不用等下一个间隔。
func Kick() { ddnsKick() }

// ddnsRun 跑一轮 DDNS 更新。返回 (人话结果, 是否成功)。
func ddnsRun(db *sql.DB, uid string) (string, bool) {
	ddnsMu.Lock()
	defer ddnsMu.Unlock()
	ddnsLastRun = time.Now()

	provider := getKV(db, uid, "ddns.provider")
	domain := getKV(db, uid, "ddns.domain")
	if domain == "" {
		return "没配域名，跳过", false
	}
	token := openToken(getKV(db, uid, "ddns.token"))
	if token == "" {
		return "没配 DNS 令牌，跳过", false
	}
	ip := sysinfo.GlobalIPv6()
	if ip == "" {
		setDDNSResult(db, uid, false, "本机没有公网 IPv6 地址")
		return "本机没有公网 IPv6 地址", false
	}
	if getKV(db, uid, "ddns.last_ip") == ip && getKV(db, uid, "ddns.last_ok") == "1" {
		return "IP 没变，跳过", true // 不变不调 API，别去烦服务商
	}
	msg, err := ddnsUpdate(provider, domain, getKV(db, uid, "ddns.sub"), token, ip)
	ok := err == nil
	setDDNSResult(db, uid, ok, msg)
	if ok {
		_ = putKV(db, uid, "ddns.last_ip", ip)
		Logf("remote: DDNS 已更新 %s → %s", fullDomain(domain, getKV(db, uid, "ddns.sub")), ip)
	} else {
		Logf("remote: DDNS 更新失败: %s", msg)
	}
	return msg, ok
}

func setDDNSResult(db *sql.DB, uid string, ok bool, msg string) {
	v := "0"
	if ok {
		v = "1"
	}
	_ = putKV(db, uid, "ddns.last_ok", v)
	_ = putKV(db, uid, "ddns.last_msg", msg)
	_ = putKV(db, uid, "ddns.last_at", time.Now().Format("2006-01-02 15:04"))
}

func fullDomain(domain, sub string) string {
	if sub == "" || sub == "@" {
		return domain
	}
	return sub + "." + domain
}

// ---------- 公网开关给 guard 用（带缓存，别每个请求都查库） ----------

var (
	pubMu    sync.RWMutex
	pubOn    bool
	pubAt    time.Time
	pubKnown bool
)

func pubCacheStore(on bool) {
	pubMu.Lock()
	pubOn, pubAt, pubKnown = on, time.Now(), true
	pubMu.Unlock()
}

// SetPublicEnabled 由宿主（桌面 App 的「IPv6 直连」弹窗）直接改完库后调用，
// 让 guard 立刻看到新值。不调的话要等缓存过期（最多 30 秒）才生效 ——
// 用户点「开启」却发现还是 403，会以为是坏的。
func SetPublicEnabled(on bool) { pubCacheStore(on) }

// PublicEnabled 公网总开关当前值。guard 对每个请求都调，所以查一次库后缓存 30 秒。
func PublicEnabled(db *sql.DB) bool {
	pubMu.RLock()
	if pubKnown && time.Since(pubAt) < 30*time.Second {
		v := pubOn
		pubMu.RUnlock()
		return v
	}
	pubMu.RUnlock()
	pubMu.Lock()
	defer pubMu.Unlock()
	if pubKnown && time.Since(pubAt) < 30*time.Second {
		return pubOn
	}
	on := false
	if db != nil {
		var v string
		if err := db.QueryRow(`select v from remote_settings where k='public.enabled'
			order by updated_at desc limit 1`).Scan(&v); err == nil {
			on = v == "1"
		}
	}
	pubOn, pubAt, pubKnown = on, time.Now(), true
	return on
}

// ---------- 日志钩子（同 voice.Logf：GUI 进程 stderr 没落点） ----------

var Logf = func(format string, args ...any) {}

// remoteIP 取客户端 IP（strip 端口；frp 场景下是 frpc 或直连访客）。
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
