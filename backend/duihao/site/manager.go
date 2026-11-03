// 站点管理器：CRUD、片段渲染、启停编排（不直接启进程）。
package site

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	duihao "github.com/ys-ll/uniterm/backend/duihao"
	"github.com/ys-ll/uniterm/backend/duihao/audit"
	"github.com/ys-ll/uniterm/backend/duihao/instance"
	"github.com/ys-ll/uniterm/backend/duihao/processd"
)

// Manager 站点管理器。
type Manager struct {
	mu       sync.Mutex
	reg      *Registry
	inst     *instance.Manager
	auditLog *audit.Logger
}

// NewManager 构建站点管理器（实例启停全部委托 instance.Manager）。
func NewManager(inst *instance.Manager, auditLog *audit.Logger) (*Manager, error) {
	reg, err := LoadRegistry()
	if err != nil {
		return nil, err
	}
	return &Manager{reg: reg, inst: inst, auditLog: auditLog}, nil
}

// DefaultSitePort 站点默认监听端口（浏览器 http://localhost 即默认打开 80）。
const DefaultSitePort = 80

// DefaultSiteTLSPort https 站点的默认监听端口。
const DefaultSiteTLSPort = 443

// EffectivePort 站点实际监听端口（未设置按 80）。
func (rec *Record) EffectivePort() int {
	if rec.Port > 0 {
		return rec.Port
	}
	return DefaultSitePort
}

// CreateOpts 新建站点入参。
type CreateOpts struct {
	Name     string
	Domains  []string
	Port     int    // 监听端口（0 → http 用 80 / https 用 443，被占用则顺延）
	Root     string
	Web      string // nginx 实例 id（必填）
	PHP      string // php 实例 id（静态站点留空）
	DB       string // 数据库实例 id（可空）
	Database string // 库名（可空）
	TLS      bool   // true = https（自签名证书，保存时自动签发）
	Options  Options
}

// View 站点视图（记录 + 引用实例活状态 + 访问地址）。
type View struct {
	Record
	WebLive    string `json:"webLive"`
	PHPLive    string `json:"phpLive,omitempty"`
	PHPPort    int    `json:"phpPort,omitempty"` // 绑定的 PHP 版本 fcgi 端口（0 = 静态站点）
	DBLive     string `json:"dbLive,omitempty"`
	WebPort    int    `json:"webPort"`  // nginx 实例自身的默认端口
	SitePort   int    `json:"sitePort"` // 站点监听端口（Record.Port 的有效值）
	URL        string `json:"url"`
	// PlainURL https 站点附带的明文地址（空 = 只有 https）。
	PlainURL   string `json:"plainUrl,omitempty"`
	HostsReady bool   `json:"hostsReady"`
	// Live 站点级状态：停用 → stopped；启用 → 跟随 web 实例（nginx 是共享的，
	// 光看 webLive 分不出「这个站点」是开是关）。
	Live string `json:"live"`
}

// siteURL 站点访问地址：走 localhost + 端口，不依赖 hosts。
func siteURL(port int, tls bool) string {
	scheme := "http"
	def := DefaultSitePort
	if tls {
		scheme, def = "https", DefaultSiteTLSPort
	}
	if port == def {
		return scheme + "://localhost/"
	}
	return fmt.Sprintf("%s://localhost:%d/", scheme, port)
}

// plainSiteURL https 站点的明文地址（PlainPort 为 0 时返回空串）。
func plainSiteURL(port int) string {
	if port <= 0 {
		return ""
	}
	if port == DefaultSitePort {
		return "http://localhost/"
	}
	return fmt.Sprintf("http://localhost:%d/", port)
}

// pickPlainPortLocked 给 https 站点挑一个明文 HTTP 端口（0 = 没挑到，不阻断建站）。
// 候选与 http 站点同一套（80-89 / 8080-8099），并把同实例下所有站点的主端口与
// 明文端口都算占用；selfID 传自己的 id，改配置时别把自己的端口算成别人的。
// 调用方需持锁。
func (m *Manager) pickPlainPortLocked(web string, webPort int, selfID string) int {
	taken := map[int]bool{webPort: true}
	for _, other := range m.reg.List() {
		if other.Web != web || other.ID == selfID {
			continue
		}
		taken[other.EffectivePort()] = true
		if other.PlainPort > 0 {
			taken[other.PlainPort] = true
		}
	}
	for p := DefaultSitePort; p < DefaultSitePort+10; p++ {
		if !taken[p] && probeFree(p) {
			return p
		}
	}
	for p := 8080; p <= 8099; p++ {
		if !taken[p] && probeFree(p) {
			return p
		}
	}
	return 0
}

// checkPortUsable 端口可用性预检。// Windows 下 bind <1024 需要管理员权限，非提权运行会直接报权限错误 ——
// 这里提前给出可读提示，而不是等 nginx 启动失败。
func checkPortUsable(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err == nil {
		ln.Close()
		return nil
	}
	if port < 1024 {
		return fmt.Errorf("端口 %d 已被占用，或需要管理员权限（Windows 下 1-1023 端口必须以管理员身份运行对号）：%v", port, err)
	}
	return fmt.Errorf("端口 %d 不可用：%v", port, err)
}

// probeFree 静默探测端口可 bind（不产生错误信息，供自动选端口用）。
func probeFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// pickSitePortLocked 自动选站点端口：http 从 80 起顺延到 89，再退到 8080-8099；
// https 从 443 起顺延到 452，再退到 8443-8459。
// 跳过 nginx 实例自身端口与同实例下已被站点占用的端口（主端口与明文端口都算，
// 调用方需持锁）。
func (m *Manager) pickSitePortLocked(web string, webPort int, tls bool) (int, error) {
	taken := map[int]bool{webPort: true}
	for _, other := range m.reg.List() {
		if other.Web == web {
			taken[other.EffectivePort()] = true
			if other.PlainPort > 0 {
				taken[other.PlainPort] = true
			}
		}
	}
	var cands []int
	if tls {
		for p := DefaultSiteTLSPort; p < DefaultSiteTLSPort+10; p++ {
			cands = append(cands, p)
		}
		for p := 8443; p <= 8459; p++ {
			cands = append(cands, p)
		}
	} else {
		for p := DefaultSitePort; p < DefaultSitePort+10; p++ {
			cands = append(cands, p)
		}
		for p := 8080; p <= 8099; p++ {
			cands = append(cands, p)
		}
	}
	for _, p := range cands {
		if taken[p] {
			continue
		}
		if probeFree(p) {
			return p, nil
		}
	}
	if tls {
		return 0, fmt.Errorf("找不到可用端口（443-452 / 8443-8459 均被占用），请先释放端口或手动指定")
	}
	return 0, fmt.Errorf("找不到可用端口（80-89 / 8080-8099 均被占用），请先释放端口或手动指定")
}

// waitPortListen 等端口开始监听（nginx reload 是异步的：信号发出后
// 主进程重建监听 socket 需要几十~几百毫秒，立即请求会 connection refused）。
func waitPortListen(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("端口 %d 在 %v 内未开始监听（nginx reload 可能失败，请看该实例日志）", port, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

var domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// normalizeDomains 校验并规范化域名（小写、去空、去 scheme/端口）。
func normalizeDomains(in []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		d = strings.TrimPrefix(strings.TrimPrefix(d, "http://"), "https://")
		if i := strings.Index(d, "/"); i >= 0 {
			d = d[:i]
		}
		if i := strings.Index(d, ":"); i >= 0 {
			d = d[:i]
		}
		if len(d) > 253 || !domainRe.MatchString(d) {
			return nil, fmt.Errorf("域名非法: %q（只允许字母、数字、点和连字符）", d)
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out, nil
}

// Create 新建站点：校验引用实例 → 建根目录 → 渲染片段 → reload → 落盘。
func (m *Manager) Create(opts CreateOpts) (*Record, error) {
	if opts.Web == "" {
		return nil, fmt.Errorf("必须选择一个 Web 服务器实例（nginx）")
	}
	webRec := m.inst.GetRecord(opts.Web)
	if webRec == nil {
		return nil, fmt.Errorf("Web 实例不存在: %s", opts.Web)
	}
	webPort, ok := webRec.Ports["http"]
	if !ok || webPort == 0 {
		return nil, fmt.Errorf("实例 %s 没有 http 端口（不是 Web 服务器？）", opts.Web)
	}

	phpPort := 0
	if opts.PHP != "" {
		phpRec := m.inst.GetRecord(opts.PHP)
		if phpRec == nil {
			return nil, fmt.Errorf("PHP 实例不存在: %s", opts.PHP)
		}
		phpPort = phpRec.Ports["fcgi"]
		if phpPort == 0 {
			return nil, fmt.Errorf("实例 %s 没有 fcgi 端口（不是 PHP？）", opts.PHP)
		}
	}
	if opts.DB != "" && m.inst.GetRecord(opts.DB) == nil {
		return nil, fmt.Errorf("数据库实例不存在: %s", opts.DB)
	}

	root := strings.TrimSpace(opts.Root)
	if root == "" {
		return nil, fmt.Errorf("站点根目录不能为空")
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("站点根目录必须是绝对路径: %s", root)
	}
	root = filepath.Clean(root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("创建根目录失败: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	port := opts.Port
	if port == 0 {
		// 未指定端口：优先 80；被占用或无权限（<1024 需管理员）时自动顺延，
		// 不打断建站流程——建好后可随时在卡片上改端口。
		p, err := m.pickSitePortLocked(opts.Web, webPort, opts.TLS)
		if err != nil {
			return nil, err
		}
		port = p
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("端口 %d 非法（1-65535）", port)
	}
	if port == webPort {
		return nil, fmt.Errorf("端口 %d 是 nginx 实例自身的默认端口，请换一个（例如 %d）", port, DefaultSitePort)
	}	// 同一 nginx 实例下端口必须唯一：站点靠端口区分，重复会互相覆盖。
	for _, other := range m.reg.List() {
		if other.Web == opts.Web && other.EffectivePort() == port {
			return nil, fmt.Errorf("端口 %d 已被站点 %s 占用（同一 nginx 实例下每个站点端口必须不同）", port, other.ID)
		}
	}
	if err := checkPortUsable(port); err != nil {
		return nil, err
	}

	id := m.uniqueIDLocked()
	domains, err := normalizeDomains(opts.Domains)
	if err != nil {
		return nil, err
	}
	if len(domains) == 0 {
		domains = []string{id + ".test"}
	}
	tlsMode := TLSNone
	if opts.TLS {
		tlsMode = TLSSelfSigned
	}
	rec := &Record{
		ID: id, Name: firstNonEmpty(opts.Name, domains[0]),
		Domains: domains, Port: port, Root: root,
		Web: opts.Web, PHP: opts.PHP, DB: opts.DB, Database: opts.Database,
		TLSMode: tlsMode,
		Options: opts.Options,
	}
	if rec.TLS() {
		// 证书必须先落盘：nginx 加载片段时找不到证书文件会直接拒载。
		if _, _, err := ensureCert(m.inst.InstanceDirPath(rec.Web), rec.ID, domains); err != nil {
			return nil, err
		}
		// 同时开一个明文端口：自签证书对挂载盘/老客户端不友好，http 直接可访问
		// 同一份站点根。挑不到就留 0（只有 https），不打断建站。
		rec.PlainPort = m.pickPlainPortLocked(rec.Web, webPort, rec.ID)
	}

	// 片段写入 web 实例 sites/ 目录（受控写入 + 越界拒绝）。
	if err := m.writeFragment(rec); err != nil {
		return nil, fmt.Errorf("写入 nginx 片段失败: %w", err)
	}
	if err := m.inst.Reload(rec.Web); err != nil {
		// 片段已落盘但 nginx 拒绝加载：回滚片段，避免留下坏配置。
		_ = m.dropFragment(rec)
		return nil, fmt.Errorf("nginx 重载失败（站点未创建）: %w", err)
	}
	if err := m.reg.Add(rec); err != nil {
		_ = m.dropFragment(rec)
		return nil, err
	}
	if m.auditLog != nil {
		m.auditLog.Ok("site", "create", rec.ID, map[string]any{
			"domains": rec.Domains, "port": rec.EffectivePort(), "root": rec.Root,
			"web": rec.Web, "php": rec.PHP, "db": rec.DB, "tls": rec.TLSMode,
		})
	}
	return rec, nil
}

// SetPort 修改站点端口：校验 → 重渲染片段 → reload → 落盘。
func (m *Manager) SetPort(id string, port int) error {
	rec := m.reg.Get(id)
	if rec == nil {
		return fmt.Errorf("站点不存在: %s", id)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("端口 %d 非法（1-65535）", port)
	}
	webRec := m.inst.GetRecord(rec.Web)
	if webRec == nil {
		return fmt.Errorf("Web 实例不存在: %s", rec.Web)
	}
	if port == webRec.Ports["http"] {
		return fmt.Errorf("端口 %d 是 nginx 实例自身的默认端口，请换一个", port)
	}
	for _, other := range m.reg.List() {
		if other.ID != id && other.Web == rec.Web && (other.EffectivePort() == port || other.PlainPort == port) {
			return fmt.Errorf("端口 %d 已被站点 %s 占用", port, other.ID)
		}
	}
	if port != rec.EffectivePort() {
		if err := checkPortUsable(port); err != nil {
			return err
		}
	}

	old := rec.EffectivePort()
	updated := *rec
	updated.Port = port
	// 新主端口撞上自己的明文端口时重挑一个，不然同一个 server 块里出现
	// 两个 listen 同端口，nginx 会以 duplicate listen 拒载。
	if updated.PlainPort == port {
		m.mu.Lock()
		updated.PlainPort = m.pickPlainPortLocked(rec.Web, webRec.Ports["http"], rec.ID)
		m.mu.Unlock()
	}
	if err := m.writeFragment(&updated); err != nil {
		return fmt.Errorf("写入 nginx 片段失败: %w", err)
	}
	if err := m.inst.Reload(rec.Web); err != nil {
		// 回滚到旧端口配置
		_ = m.writeFragment(rec)
		return fmt.Errorf("nginx 重载失败（端口未修改）: %w", err)
	}
	// reload 是异步的：实例在跑时等新端口真的起来，否则界面显示改了但打不开。
	if m.inst.Status(rec.Web) == processd.StateRunning {
		if err := waitPortListen(port, 3*time.Second); err != nil {
			_ = m.writeFragment(rec)
			_ = m.inst.Reload(rec.Web)
			return err
		}
	}
	if err := m.reg.Update(&updated); err != nil {
		return err
	}
	if m.auditLog != nil {
		m.auditLog.Ok("site", "set-port", id, map[string]any{"from": old, "to": port})
	}
	return nil
}

// Delete 删除站点：删片段 → reload → 移除记录。
// 根目录是用户目录，绝不动（数据安全铁律）。
func (m *Manager) Delete(id string) error {
	rec := m.reg.Get(id)
	if rec == nil {
		return fmt.Errorf("站点不存在: %s", id)
	}
	if err := m.dropFragment(rec); err != nil {
		return fmt.Errorf("删除 nginx 片段失败: %w", err)
	}
	if m.inst.GetRecord(rec.Web) != nil {
		if err := m.inst.Reload(rec.Web); err != nil {
			return fmt.Errorf("nginx 重载失败（片段已删除）: %w", err)
		}
	}
	if err := m.reg.Remove(id); err != nil {
		return err
	}
	if m.auditLog != nil {
		m.auditLog.Ok("site", "delete", id, map[string]any{"root": rec.Root, "rootKept": true})
	}
	return nil
}

// Start 启用站点：按 db → php → web 拉起它绑定的实例（已在跑则跳过），
// 然后把片段挂回 sites/<id>.conf 并 reload。
//
// 这里刻意「不要求用户先去运行时面板启动 PHP」：站点绑了哪个版本，启动站点就
// 拉起哪个版本的 php-cgi。PHP 清单声明 multiRun=true，所以 A 站用 8.2、B 站用
// 8.3 时两个版本的 php-cgi 可以并存（各自独立 fcgi 端口，片段各指各的）。
func (m *Manager) Start(id string) error {
	// 整体串行：并发启动多个站点时，共享实例（nginx / 同版本 php）的
	// 片段写入、reload、回收判断不能交错。实例层 Start 已幂等，这里
	// 主要保护「判断 + 写片段 + 记账」这个组合不被并发的 Stop 撕开。
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.reg.Get(id)
	if rec == nil {
		return fmt.Errorf("站点不存在: %s", id)
	}
	// 实例被删后记录里的 id 还在：直接说清是哪个实例、怎么修，
	// 比让用户对着「实例不存在」猜要好。
	if rec.Web == "" || m.inst.GetRecord(rec.Web) == nil {
		return fmt.Errorf("站点绑定的 Web 实例不存在: %s（请在站点配置里重新选择 Nginx）", rec.Web)
	}
	if rec.PHP != "" && m.inst.GetRecord(rec.PHP) == nil {
		return fmt.Errorf("站点绑定的 PHP 实例不存在: %s（请在站点配置里重新选择 PHP 版本）", rec.PHP)
	}
	if rec.DB != "" && m.inst.GetRecord(rec.DB) == nil {
		return fmt.Errorf("站点绑定的数据库实例不存在: %s（请在站点配置里重新选择）", rec.DB)
	}

	// 端口预检：rec.Disabled=true 表示这个站点刚才是停着的，它的片段没被
	// nginx include，所以此刻这个端口上**不该有我们的监听**。有人在听 =
	// 别的程序（phpstudy、另一个 web 服务器）占着。
	// 不预检的话：waitPortListen 会拨通「别人」的端口，一路判成功，界面显示
	// 运行中、浏览器却打不开——比直接报错难查得多。
	if rec.Disabled {
		if blocked, who := portBlocked(rec.EffectivePort()); blocked {
			if m.auditLog != nil {
				m.auditLog.Fail("site", "start", id, map[string]any{"port": rec.EffectivePort(), "occupier": who})
			}
			return fmt.Errorf("端口 %d 已被 %s 占用（可点「换端口」自动让开）", rec.EffectivePort(), who)
		}
	}

	// 先后端（db → php），起不来就到此为止：nginx 拉起来只会让所有 PHP 页面
	// 502，界面却显示「运行中」，比直接报错更难查。
	var errs []string
	for _, iid := range []string{rec.DB, rec.PHP} {
		if iid == "" || m.inst.Status(iid) == processd.StateRunning {
			continue
		}
		if err := m.inst.Start(iid); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", iid, err))
		}
	}
	if len(errs) > 0 {
		if m.auditLog != nil {
			m.auditLog.Fail("site", "start", id, map[string]any{"errors": errs})
		}
		return fmt.Errorf("后端实例启动失败，站点未启用 — %s", strings.Join(errs, "；"))
	}

	// 入口：启用片段 + reload。nginx 未运行则顺带拉起（站点启用即可访问）。
	updated := *rec
	updated.Disabled = false
	if err := m.writeFragment(&updated); err != nil {
		return fmt.Errorf("写入 nginx 片段失败: %w", err)
	}
	if err := m.inst.Reload(rec.Web); err != nil {
		// 片段挂上但 nginx 拒载：回滚到调用前的启停状态，避免留下坏配置。
		_ = m.writeFragment(rec)
		_ = m.inst.Reload(rec.Web)
		return fmt.Errorf("nginx 重载失败（站点未启用）: %w", err)
	}
	if m.inst.Status(rec.Web) != processd.StateRunning {
		if err := m.inst.Start(rec.Web); err != nil {
			if m.auditLog != nil {
				m.auditLog.Fail("site", "start", id, map[string]any{"web": err.Error()})
			}
			// nginx 起不来的头号原因就是端口被别的程序占了（phpstudy / 另一个
			// nginx）。只说「Web 实例启动失败」用户没法修；这里补一句占用者，
			// 前端据此弹出「一键换端口」。
			port := rec.EffectivePort()
			hint := ""
			if blocked, who := portBlocked(port); blocked {
				hint = fmt.Sprintf("；端口 %d 已被 %s 占用（可点「换端口」自动让开）", port, who)
			}
			return fmt.Errorf("Web 实例启动失败 — %s: %v%s", rec.Web, err, hint)
		}
	}
	if err := m.reg.Update(&updated); err != nil {
		return err
	}
	// nginx 进程起来 ≠ 站点端口已监听（worker 建 listen socket 有几百毫秒延迟）。
	// 不等的话：用户点「启动」后立刻打开浏览器会 connection refused，
	// 界面显示运行中但页面打不开——比报错更让人困惑。
	_ = waitPortListen(rec.EffectivePort(), 5*time.Second)
	if m.auditLog != nil {
		m.auditLog.Ok("site", "start", id, map[string]any{"php": rec.PHP, "web": rec.Web})
	}
	return nil
}

// Stop 停用站点：把片段改名为 <id>.conf.off 并 reload，站点立即 404/断连，
// 但共享的 nginx / php / mysql **不动** —— 同机其他站点照常跑。
//
// 只有当没有任何「启用中」的站点再引用某个实例时，才回收它（免得一堆
// php-cgi / mysqld 停在那儿白占内存）。这正是 phpStudy 的分层：
// 服务是服务，站点是站点，站点开关只影响自己。
func (m *Manager) Stop(id string) error {
	// 与 Start 互斥：否则并发「启动 B / 停用 A」时，A 的回收判断可能
	// 看不到 B 还没写完的状态，误停共享实例。
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.reg.Get(id)
	if rec == nil {
		return fmt.Errorf("站点不存在: %s", id)
	}
	updated := *rec
	updated.Disabled = true
	if m.inst.GetRecord(rec.Web) != nil {
		// 片段改名留档（<id>.conf.off）：站点配置还在，便于对照排查，
		// 只是不再被 nginx include。删除站点才真正清掉文件。
		if err := m.writeFragment(&updated); err != nil {
			return fmt.Errorf("停用站点失败: %w", err)
		}
		if err := m.inst.Reload(rec.Web); err != nil {
			return fmt.Errorf("nginx 重载失败（站点已停用）: %w", err)
		}
	}
	if err := m.reg.Update(&updated); err != nil {
		return err
	}
	recycled := m.recycleIfUnused(&updated)
	if m.auditLog != nil {
		m.auditLog.Ok("site", "stop", id, map[string]any{"recycled": recycled})
	}
	return nil
}

// recycleIfUnused 站点停用后回收没人用的实例：web 按实例、php/db 按引用。
// 返回被停掉的实例 id（审计/排错用）。
func (m *Manager) recycleIfUnused(rec *Record) []string {
	var stopped []string
	for _, iid := range []string{rec.PHP, rec.DB, rec.Web} {
		if iid == "" || m.inst.Status(iid) != processd.StateRunning {
			continue
		}
		if m.usedByEnabledSite(iid, rec.ID) {
			continue
		}
		if err := m.inst.Stop(iid); err == nil {
			stopped = append(stopped, iid)
		}
	}
	return stopped
}

// usedByEnabledSite 除了 exclude 站点，还有没有「启用中」的站点在用这个实例。
// 用启用状态而不是「nginx 在跑」判断：nginx 是共享的，靠它判会把所有站点都算成在跑。
func (m *Manager) usedByEnabledSite(instID, exclude string) bool {
	return referencedByEnabled(m.reg.List(), instID, exclude)
}

// referencedByEnabled 纯函数版引用检查（便于单测）：recs 里是否存在除 exclude
// 之外、未停用、且 Web/PHP/DB 引用了 instID 的站点。
func referencedByEnabled(recs []*Record, instID, exclude string) bool {
	if instID == "" {
		return false
	}
	for _, other := range recs {
		if other == nil || other.ID == exclude || other.Disabled {
			continue
		}
		if other.Web == instID || other.PHP == instID || other.DB == instID {
			return true
		}
	}
	return false
}

// fragName 站点片段相对路径：启用 = sites/<id>.conf（被 nginx include），
// 停用 = sites/<id>.conf.off（文件留着便于对照排查，但不再加载）。
func fragName(id string, disabled bool) string {
	if disabled {
		return "sites/" + id + ".conf.off"
	}
	return "sites/" + id + ".conf"
}

// writeFragment 按站点当前启停状态渲染片段，并清掉另一后缀的残留文件。
func (m *Manager) writeFragment(rec *Record) error {
	body := renderVHost(rec, phpPortOf(m.inst, rec.PHP), m.siteLogDir(rec.Web), m.inst.InstanceDirPath(rec.Web))
	if err := m.inst.WriteInstanceFile(rec.Web, fragName(rec.ID, rec.Disabled), body); err != nil {
		return err
	}
	_ = m.inst.RemoveInstanceFile(rec.Web, fragName(rec.ID, !rec.Disabled))
	return nil
}

// dropFragment 删除该站点的两种片段文件（停用/删除站点用）。
func (m *Manager) dropFragment(rec *Record) error {
	if m.inst.GetRecord(rec.Web) == nil {
		return nil // Web 实例已被删除：没有片段可清
	}
	if err := m.inst.RemoveInstanceFile(rec.Web, fragName(rec.ID, false)); err != nil {
		return err
	}
	if err := m.inst.RemoveInstanceFile(rec.Web, fragName(rec.ID, true)); err != nil {
		return err
	}
	return nil
}

// Rebuild 重新渲染片段（域名 / 根目录 / 栈变更后）。
func (m *Manager) Rebuild(id string) error {
	rec := m.reg.Get(id)
	if rec == nil {
		return fmt.Errorf("站点不存在: %s", id)
	}
	if m.inst.GetRecord(rec.Web) == nil {
		return fmt.Errorf("Web 实例不存在: %s", rec.Web)
	}
	if err := m.writeFragment(rec); err != nil {
		return err
	}
	return m.inst.Reload(rec.Web)
}

// List 站点视图列表。
func (m *Manager) List() []View {
	recs := m.reg.List()
	out := make([]View, 0, len(recs))
	for _, rec := range recs {
		v := View{Record: *rec}
		v.SitePort = rec.EffectivePort()
		if webRec := m.inst.GetRecord(rec.Web); webRec != nil {
			v.WebPort = webRec.Ports["http"]
			v.WebLive = m.inst.Status(rec.Web)
		}
		if rec.PHP != "" {
			v.PHPLive = m.inst.Status(rec.PHP)
			v.PHPPort = phpPortOf(m.inst, rec.PHP)
		}
		if rec.DB != "" {
			v.DBLive = m.inst.Status(rec.DB)
		}
		v.Live = v.WebLive
		if rec.Disabled {
			v.Live = processd.StateStopped
		}
		v.URL = siteURL(v.SitePort, rec.TLS())
		v.PlainURL = plainSiteURL(rec.PlainPort)
		v.HostsReady = HostsReady(rec.Domains)
		out = append(out, v)
	}
	return out
}

// ViewOf 单站点视图。
func (m *Manager) ViewOf(id string) (*View, error) {
	for _, v := range m.List() {
		if v.ID == id {
			return &v, nil
		}
	}
	return nil, fmt.Errorf("站点不存在: %s", id)
}

// ReferencesInstance 引用了该实例的站点（删除实例前的引用检查）。
func (m *Manager) ReferencesInstance(instanceID string) []string {
	return m.reg.ReferencesInstance(instanceID)
}

// PortInUseBy 返回占用该端口的站点 id（无则空串）。
// 实例层改端口前调用：nginx 实例端口若撞上已有站点端口，启动会 emerg。
func (m *Manager) PortInUseBy(instanceID string, port int) string {
	for _, s := range m.reg.List() {
		if s.Web == instanceID && (s.EffectivePort() == port || s.PlainPort == port) {
			return s.ID
		}
	}
	return ""
}

// Logs 站点日志 = web 实例进程日志尾部。
func (m *Manager) Logs(id string, tail int) (string, error) {
	rec := m.reg.Get(id)
	if rec == nil {
		return "", fmt.Errorf("站点不存在: %s", id)
	}
	return m.inst.Logs(rec.Web, tail)
}

// siteLogDir 站点独立日志目录：<web 实例目录>/logs/sites。
// 放在 web 实例下（而不是站点根目录）——站点根是用户目录，对号不往里拉屎。
func (m *Manager) siteLogDir(web string) string {
	dir := filepath.Join(m.inst.InstanceDirPath(web), "logs", "sites")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return dir
}

func (m *Manager) uniqueIDLocked() string {
	for i := 1; ; i++ {
		cand := fmt.Sprintf("site-%d", i)
		if m.reg.Get(cand) == nil {
			return cand
		}
	}
}

// renderVHost 渲染 nginx server 块：**每个站点独占自己的 listen 端口**
// （默认 80），因此 http://localhost:<端口> 即可直接访问，不需要改 hosts。
// 域名（server_name）保留为可选绑定，一个站点可绑多个域名。
// phpPort=0 时渲染为静态站点（无 fastcgi）。
//
// logDir 是该 web 实例的 sites 日志目录（instances/<web>/logs/sites）；
// 空则不写独立日志（跟随 nginx 主配置）。
// instDir 是该 web 实例的数据目录（证书放 <instDir>/certs，https 站点用）。
//
// 所有可选段都来自 Options，且**每个开关单独成段**——任意组合都必须被
// nginx -t 接受（M10 验收覆盖了全开/全关/单项三组）。
func renderVHost(rec *Record, phpPort int, logDir, instDir string) string {
	port := rec.EffectivePort()
	o := rec.Options
	var b strings.Builder

	fmt.Fprintf(&b, "# 对号生成 · 站点 %s · 端口 %d · 手工修改会在下次渲染时被覆盖\n", rec.ID, port)
	b.WriteString("server {\n")
	if rec.TLS() {
		// 证书由 site/tls.go 在渲染前生成/复用；换域名会重新签发（SAN 不匹配会被浏览器拒）
		cert, key := CertPaths(instDir, rec.ID)
		fmt.Fprintf(&b, "    listen %d ssl;\n", port)
		fmt.Fprintf(&b, "    ssl_certificate \"%s\";\n", slash(cert))
		fmt.Fprintf(&b, "    ssl_certificate_key \"%s\";\n", slash(key))
		// 同 server 块的明文端口：同一份 root/location，http 直接可访问。
		// nginx 的 ssl 是 listen 的修饰符（按端口生效），混用是合法的。
		if pp := rec.PlainPort; pp > 0 && pp != port {
			fmt.Fprintf(&b, "    # 明文端口（自签证书不便时直接用 http，同一份站点）\n    listen %d;\n", pp)
		}
	} else {
		fmt.Fprintf(&b, "    listen %d;\n", port)
	}
	fmt.Fprintf(&b, "    server_name %s;\n", strings.Join(rec.Domains, " "))
	fmt.Fprintf(&b, "    root \"%s\";\n", strings.ReplaceAll(rec.Root, `\`, "/"))
	b.WriteString("    charset utf-8;\n")

	index := DefaultIndex
	if l := o.IndexList(); len(l) > 0 {
		index = strings.Join(l, " ")
	}
	fmt.Fprintf(&b, "    index %s;\n\n", index)

	// —— 日志 ——
	if logDir != "" && (o.AccessLog || o.ErrorLog) {
		b.WriteString("    # M10 日志：可在「站点 → 配置 → 高级配置」关闭\n")
		if o.AccessLog {
			fmt.Fprintf(&b, "    access_log \"%s/%s.access.log\";\n", slash(logDir), rec.ID)
		}
		if o.ErrorLog {
			fmt.Fprintf(&b, "    error_log \"%s/%s.error.log\" warn;\n", slash(logDir), rec.ID)
		}
		b.WriteString("\n")
	}

	// —— 301 全站跳转（最高优先级：命中即返回）——
	if t := strings.TrimSpace(o.RedirectTo); t != "" && isSafeToken(strings.TrimSuffix(t, "/")) {
		fmt.Fprintf(&b, "    # 全站跳转\n    return 301 %s$request_uri;\n\n", strings.TrimRight(t, "/"))
	}

	// —— 访问控制（IP 白/黑名单）——
	if rules := o.ipRules(); len(rules) > 0 {
		b.WriteString("    # 访问控制\n")
		for _, r := range rules {
			b.WriteString("    " + r + "\n")
		}
		b.WriteString("\n")
	}

	// —— 跨站隔离 ——
	if o.CrossIsolate {
		b.WriteString("    # 跨站隔离\n")
		b.WriteString("    add_header X-Frame-Options SAMEORIGIN always;\n")
		b.WriteString("    add_header X-Content-Type-Options nosniff always;\n\n")
	}

	// —— 错误页 ——
	if lines := o.ErrPageLines(); len(lines) > 0 {
		b.WriteString("    # 错误页面\n")
		for _, l := range lines {
			b.WriteString("    " + l + "\n")
		}
		b.WriteString("\n")
	}

	// —— 禁止目录执行 PHP（先于通用 .php 规则，靠 nginx 正则顺序生效）——
	if dirs := o.execDenyDirs(); len(dirs) > 0 {
		alt := strings.Join(dirs, "|")
		fmt.Fprintf(&b, "    # 禁止这些目录执行 PHP\n    location ~* ^/(%s)/.*\\.(php|php5|phtml)$ {\n        deny all;\n    }\n\n", alt)
	}

	// —— 禁止访问隐藏文件 ——
	if o.DenyHidden {
		b.WriteString("    # 禁止访问 . 开头的文件/目录\n    location ~ /\\. {\n        deny all;\n    }\n\n")
	}

	// —— 禁止下载指定后缀 ——
	if exts := o.denyFileExts(); len(exts) > 0 {
		fmt.Fprintf(&b, "    # 禁止下载这些后缀\n    location ~* \\.(%s)$ {\n        deny all;\n    }\n\n", strings.Join(exts, "|"))
	}

	// —— 主 location ——
	rw := o.rewriteLines()
	rwHasTryFiles := false
	for _, l := range rw {
		if strings.Contains(l, "try_files") {
			rwHasTryFiles = true
		}
	}
	b.WriteString("    location / {\n")
	// 伪静态自带 try_files 时不再写默认的：同一 location 里重复 = nginx emerg。
	// 开目录索引时也不写：try_files 命中目录后会走 index 阶段，找不到 index 文件
	// 直接 403（autoindex 没机会接管）——「目录列表」和「前端路由回落」语义互斥，
	// 同时开时以目录索引为准（与 phpStudy 一致）。
	if !rwHasTryFiles && !o.AutoIndex {
		if phpPort > 0 {
			b.WriteString("        try_files $uri $uri/ /index.php?$query_string;\n")
		} else {
			b.WriteString("        try_files $uri $uri/ =404;\n")
		}
	}
	if o.AutoIndex {
		b.WriteString("        autoindex on;\n        autoindex_exact_size off;\n")
	}
	if len(rw) > 0 {
		b.WriteString("\n        # 伪静态（用户规则）\n")
		for _, l := range rw {
			b.WriteString(l + "\n")
		}
	}
	b.WriteString("    }\n\n")

	// —— 静态资源缓存 ——
	if o.CacheExpires {
		b.WriteString("    # 静态资源缓存\n")
		b.WriteString("    location ~* \\.(js|css|png|jpe?g|gif|svg|ico|woff2?|ttf|eot)$ {\n")
		b.WriteString("        expires 30d;\n        access_log off;\n        try_files $uri =404;\n    }\n\n")
	}

	// —— 页面压缩 ——
	if o.Gzip {
		b.WriteString("    # 页面压缩\n")
		b.WriteString("    gzip on;\n    gzip_min_length 1k;\n    gzip_comp_level 4;\n")
		b.WriteString("    gzip_types text/plain text/css application/json application/javascript text/xml application/xml image/svg+xml;\n\n")
	}

	// —— PHP 转发 ——
	if phpPort > 0 {
		fmt.Fprintf(&b, "    location ~ \\.php$ {\n        fastcgi_pass 127.0.0.1:%d;\n", phpPort)
		b.WriteString(`        fastcgi_index index.php;
        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
        fastcgi_param SCRIPT_NAME $fastcgi_script_name;
        fastcgi_param QUERY_STRING $query_string;
        fastcgi_param REQUEST_METHOD $request_method;
        fastcgi_param CONTENT_TYPE $content_type;
        fastcgi_param CONTENT_LENGTH $content_length;
        fastcgi_param REQUEST_URI $request_uri;
        fastcgi_param DOCUMENT_URI $document_uri;
        fastcgi_param DOCUMENT_ROOT $document_root;
        fastcgi_param SERVER_PROTOCOL $server_protocol;
        fastcgi_param GATEWAY_INTERFACE CGI/1.1;
        fastcgi_param SERVER_SOFTWARE nginx;
        fastcgi_param REMOTE_ADDR $remote_addr;
        fastcgi_param REMOTE_PORT $remote_port;
        fastcgi_param SERVER_ADDR $server_addr;
        fastcgi_param SERVER_PORT $server_port;
        fastcgi_param SERVER_NAME $server_name;
        fastcgi_param REDIRECT_STATUS 200;
`)
		// 生产环境：错误只进日志，不回显到页面（调试信息泄露是最常见的低级事故）。
		if o.Production {
			b.WriteString("        # 生产环境：PHP 错误只写日志，不输出到页面\n")
			b.WriteString("        fastcgi_param PHP_ADMIN_VALUE \"display_errors=Off\";\n")
		}
		b.WriteString("    }\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// slash 统一成正斜杠（nginx 配置里反斜杠会被当转义符）。
func slash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// UpdateOpts 修改站点入参（对应「站点 → 配置」对话框）。
// Web 实例不在可改范围：换 Web 要迁片段目录，属"新建站点"的语义。
type UpdateOpts struct {
	Name     string
	Domains  []string
	Port     int
	Root     string
	PHP      string
	DB       string
	Database string
	TLS      bool
	Options  Options
}

// Update 修改站点配置：校验 → 渲染新片段 → reload → 等端口 → 落盘。
// 任何一步失败都回滚到旧片段并恢复旧记录（运行中的站点不会因改错配置而失联）。
func (m *Manager) Update(id string, opts UpdateOpts) error {
	if err := m.applyUpdate(id, opts); err != nil {
		return err
	}
	return nil
}

func (m *Manager) applyUpdate(id string, opts UpdateOpts) error {
	rec := m.reg.Get(id)
	if rec == nil {
		return fmt.Errorf("站点不存在: %s", id)
	}
	webRec := m.inst.GetRecord(rec.Web)
	if webRec == nil {
		return fmt.Errorf("Web 实例不存在: %s", rec.Web)
	}
	webPort := webRec.Ports["http"]

	// —— 校验 ——
	root := strings.TrimSpace(opts.Root)
	if root == "" {
		return fmt.Errorf("站点根目录不能为空")
	}
	if !filepath.IsAbs(root) {
		return fmt.Errorf("站点根目录必须是绝对路径: %s", root)
	}
	root = filepath.Clean(root)

	port := opts.Port
	if port == 0 {
		port = DefaultSitePort
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("端口 %d 非法（1-65535）", port)
	}
	if port == webPort {
		return fmt.Errorf("端口 %d 是 nginx 实例自身的默认端口，请换一个", port)
	}
	for _, other := range m.reg.List() {
		if other.ID != id && other.Web == rec.Web && other.EffectivePort() == port {
			return fmt.Errorf("端口 %d 已被站点 %s 占用（同一 nginx 实例下每个站点端口必须不同）", port, other.ID)
		}
	}
	if port != rec.EffectivePort() {
		// 只在端口真的变了时探测，否则会被自己占着的旧端口判为"被占用"。
		if err := checkPortUsable(port); err != nil {
			return err
		}
	}

	domains, err := normalizeDomains(opts.Domains)
	if err != nil {
		return err
	}
	if len(domains) == 0 {
		domains = []string{rec.ID + ".test"}
	}

	phpPort := 0
	if opts.PHP != "" {
		phpRec := m.inst.GetRecord(opts.PHP)
		if phpRec == nil {
			return fmt.Errorf("PHP 实例不存在: %s", opts.PHP)
		}
		phpPort = phpRec.Ports["fcgi"]
		if phpPort == 0 {
			return fmt.Errorf("实例 %s 没有 fcgi 端口（不是 PHP？）", opts.PHP)
		}
	}
	if opts.DB != "" && m.inst.GetRecord(opts.DB) == nil {
		return fmt.Errorf("数据库实例不存在: %s", opts.DB)
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("创建根目录失败: %w", err)
	}

	// —— 渲染并应用（失败回滚）——
	updated := *rec
	updated.Name = firstNonEmpty(strings.TrimSpace(opts.Name), domains[0])
	updated.Domains = domains
	updated.Port = port
	updated.Root = root
	updated.PHP = opts.PHP
	updated.DB = opts.DB
	updated.Database = opts.Database
	updated.Options = opts.Options
	updated.TLSMode = TLSNone
	if opts.TLS {
		updated.TLSMode = TLSSelfSigned
	}
	if updated.TLS() {
		// 域名可能变了：ensureCert 内部比对 SAN，不一致就重签。
		if _, _, err := ensureCert(m.inst.InstanceDirPath(rec.Web), rec.ID, domains); err != nil {
			return err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// https 站点要有一个明文端口（切换回 http 就清掉，别留着占端口）。
	if updated.TLS() {
		if updated.PlainPort <= 0 || updated.PlainPort == port {
			updated.PlainPort = m.pickPlainPortLocked(rec.Web, webPort, rec.ID)
		}
	} else {
		updated.PlainPort = 0
	}

	if err := m.writeFragment(&updated); err != nil {
		return fmt.Errorf("写入 nginx 片段失败: %w", err)
	}
	rollback := func() {
		_ = m.writeFragment(rec)
		_ = m.inst.Reload(rec.Web)
	}
	if err := m.inst.Reload(rec.Web); err != nil {
		rollback()
		return fmt.Errorf("nginx 拒绝新配置（改动已回滚）: %w", err)
	}
	if port != rec.EffectivePort() && m.inst.Status(rec.Web) == processd.StateRunning && !updated.Disabled {
		if err := waitPortListen(port, 3*time.Second); err != nil {
			rollback()
			return err
		}
	}
	if err := m.reg.Update(&updated); err != nil {
		rollback()
		return err
	}
	// 站点在跑又换了 PHP 版本：新版本的 php-cgi 立刻拉起来（否则页面马上 502），
	// 老版本没人用了就顺手回收——这样切版本不需要用户去运行时面板点任何按钮。
	if !updated.Disabled && m.inst.Status(rec.Web) == processd.StateRunning {
		if updated.PHP != "" && m.inst.Status(updated.PHP) != processd.StateRunning {
			if err := m.inst.Start(updated.PHP); err != nil && m.auditLog != nil {
				m.auditLog.Fail("site", "start-php", id, map[string]any{"php": updated.PHP, "err": err.Error()})
			}
		}
		if rec.PHP != "" && rec.PHP != updated.PHP && m.inst.Status(rec.PHP) == processd.StateRunning &&
			!m.usedByEnabledSite(rec.PHP, "") {
			_ = m.inst.Stop(rec.PHP)
		}
	}
	if m.auditLog != nil {
		m.auditLog.Ok("site", "update", id, map[string]any{
			"domains": updated.Domains, "port": port, "root": root,
			"php": updated.PHP, "db": updated.DB, "tls": updated.TLSMode,
			"options": updated.Options,
		})
	}
	return nil
}

// phpPortOf 查 PHP 实例的 fcgi 端口（实例不存在/未分配返回 0）。
func phpPortOf(inst *instance.Manager, phpID string) int {
	if phpID == "" {
		return 0
	}
	if rec := inst.GetRecord(phpID); rec != nil {
		return rec.Ports["fcgi"]
	}
	return 0
}

// OpenRoot 在文件管理器中打开站点根目录。
func (m *Manager) OpenRoot(id string) error {
	rec := m.reg.Get(id)
	if rec == nil {
		return fmt.Errorf("站点不存在: %s", id)
	}
	if _, err := os.Stat(rec.Root); err != nil {
		return fmt.Errorf("根目录不存在: %s", rec.Root)
	}
	return openPath(rec.Root)
}

// CreateErrorPages 按「错误页面」配置在根目录生成静态错误页。
// 已存在的文件不覆盖——用户可能改过样式。
func (m *Manager) CreateErrorPages(id string) ([]string, error) {
	rec := m.reg.Get(id)
	if rec == nil {
		return nil, fmt.Errorf("站点不存在: %s", id)
	}
	var made []string
	for _, line := range strings.Split(rec.Options.ErrorPages, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) < 2 || len(f[0]) != 3 || !allDigits(f[0]) {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(f[1], "/"), `\`)
		if rel == "" || strings.Contains(rel, "..") {
			continue
		}
		abs := filepath.Join(rec.Root, filepath.FromSlash(rel))
		if !strings.HasPrefix(filepath.Clean(abs), filepath.Clean(rec.Root)) {
			continue // 越界拒绝
		}
		if _, err := os.Stat(abs); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return made, err
		}
		page := errorPageHTML(f[0])
		if err := os.WriteFile(abs, []byte(page), 0o644); err != nil {
			return made, err
		}
		made = append(made, abs)
	}
	if m.auditLog != nil {
		m.auditLog.Ok("site", "create-error-pages", id, map[string]any{"files": made})
	}
	return made, nil
}

// errorPageHTML 生成简洁错误页（不引外部资源，离线可用）。
func errorPageHTML(code string) string {
	text := map[string]string{
		"400": "请求无效", "403": "禁止访问", "404": "页面不存在",
		"500": "服务器内部错误", "501": "服务未实现", "502": "网关错误",
		"503": "服务暂不可用", "504": "网关超时", "505": "HTTP 版本不支持",
		"506": "内容协商错误", "507": "存储空间不足", "510": "扩展未实现",
	}[code]
	if text == "" {
		text = "请求处理失败"
	}
	return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + code + ` · ` + text + `</title>
<style>
  html,body{height:100%;margin:0}
  body{display:flex;align-items:center;justify-content:center;
       font:15px/1.6 -apple-system,"Segoe UI","Microsoft YaHei",sans-serif;
       color:#3c3c43;background:#f7f7f8}
  .box{text-align:center;padding:2rem}
  .code{font-size:4.5rem;font-weight:200;letter-spacing:.1em;color:#c4c4cc;margin:0}
  .text{font-size:1.0625rem;margin:.5rem 0 0}
  .hint{font-size:.8125rem;color:#8e8e93;margin-top:.75rem}
</style>
</head>
<body>
  <div class="box">
    <p class="code">` + code + `</p>
    <p class="text">` + text + `</p>
    <p class="hint">由 对号 DUIHAO 生成的错误页，可在站点根目录自行替换</p>
  </div>
</body>
</html>
`
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// DirSiteFragments 站点片段目录名（相对 web 实例数据目录）。
const DirSiteFragments = "sites"

// RootDir 站点根（用户目录默认值提示用）。
func RootDir() string { return filepath.Join(duihao.DataRoot(), "www") }
