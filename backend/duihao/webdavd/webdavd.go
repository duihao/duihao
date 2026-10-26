// Package webdavd 内置 WebDAV 网盘（embedded 类插件，golang.org/x/net/webdav，BSD-3）。
//
// 定位（v3.1 §6.2）：把本机目录变成任意设备可挂载的盘——手机 ES 文件管理器、
// RaiDrive、Windows 资源管理器、浏览器、另一台对号都走标准 WebDAV。
//
// 模型：一个实例一个监听端口，多个共享；共享 = Basic Auth 凭据 + 根目录。
// URL 对所有共享相同（http://host:port/），哪个用户名登录就看到哪个根目录。
//
// 安全边界（v3.1 硬规则）：
//   - 默认只监听 127.0.0.1；开局域网（0.0.0.0）必须同时开 TLS（自签证书），
//     不提供「HTTP + 0.0.0.0」这一档；
//   - 路径两层防御：HTTP 层逐段校验原始 URL（见 pathsafe.go），FS 层再查一遍；
//   - 密码只存 bcrypt 哈希；账号不存在也跑一次 dummy bcrypt（时序对齐）；
//   - 并发连接上限 + 单请求体积上限（http.MaxBytesReader）；
//   - 磁盘水位：上传前剩余空间 < 500MB 记警告日志。
//
// 边界：本包只认「一份配置 + 若干共享」，不认实例/站点（那是上层的事）；
// 共享表由实例设置传入，本包不做持久化（落盘在 shares.go，由服务层调用）。
package webdavd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/net/webdav"
)

// Config 一次启动所需的全部参数（由实例设置 + 端口渲染而来）。
type Config struct {
	Bind    string // 127.0.0.1（默认）/ 0.0.0.0（须 TLS）
	Port    int
	TLS     bool   // 自签证书 HTTPS（0.0.0.0 时强制）
	CertDir string // 证书目录（实例下 certs/）；空则临时用内存证书

	// Shares 共享表（来自 webdavshares.json）。至少一个启用共享才允许启动。
	Shares []Share

	ReadOnly  bool // 全局只读上限（与共享只读 OR）
	MaxConns  int  // 并发连接上限
	MaxBodyMB int  // 单请求体积上限（MB）

	LogPath string

	// InstanceDir 实例数据目录（共享根目录里的 {dir} token 用它展开）。
	InstanceDir string
}

// Server 一个 WebDAV 服务实例（一实例一 Server）。
type Server struct {
	cfg Config

	mu       sync.Mutex
	httpSrv  *http.Server
	ln       net.Listener
	logFile  *os.File
	running  bool
	lastErr  string
	conns    int64  // 当前连接数（MaxConns 限制用）
	shareCnt int    // 最近一次构建的共享数（诊断）
	sessKey  []byte // 浏览器会话签名密钥（session.key）
}

// New 构造（不启动）。
func New(cfg Config) (*Server, error) {
	if cfg.Port < 0 || cfg.Port > 65535 {
		return nil, fmt.Errorf("端口 %d 非法", cfg.Port)
	}
	if cfg.Bind == "" {
		cfg.Bind = "127.0.0.1"
	}
	if cfg.Bind != "127.0.0.1" && cfg.Bind != "localhost" && !cfg.TLS {
		// v3.1 §6.2：不提供 HTTP + 0.0.0.0 档——局域网明文传密码不行。
		return nil, fmt.Errorf("局域网监听（%s）必须开启 TLS：WebDAV 用 Basic Auth，明文 HTTP 会把密码暴露给同网段任何人", cfg.Bind)
	}
	enabled := 0
	for i := range cfg.Shares {
		if err := cfg.Shares[i].Validate(); err != nil {
			return nil, fmt.Errorf("共享 %s: %w", cfg.Shares[i].User, err)
		}
		if cfg.Shares[i].Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return nil, fmt.Errorf("至少需要一个启用的共享（在「管理」里新建）")
	}
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = 32
	}
	if cfg.MaxBodyMB <= 0 {
		cfg.MaxBodyMB = 1024
	}
	sessKey, err := loadSessionKey(cfg.InstanceDir)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, sessKey: sessKey}, nil
}

// Start 绑定端口并开始服务（同步返回绑定错误，异步 serve）。
// 与 ftpd 同款节奏：先 Listen，成功后再交给 goroutine（M9 教训）。
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return fmt.Errorf("WebDAV 服务已在运行")
	}

	// 每个启用共享的根目录先建好：删掉目录后服务仍能启动，登录时不至于一片 500。
	for _, sh := range s.cfg.Shares {
		if !sh.Enabled {
			continue
		}
		r := expandRoot(sh.Root, s.cfg.InstanceDir)
		if r == "" {
			continue
		}
		if err := os.MkdirAll(r, 0o755); err != nil {
			return fmt.Errorf("共享 %s 的根目录不可用（%s）: %w", sh.User, r, err)
		}
	}

	addr := net.JoinHostPort(s.cfg.Bind, fmt.Sprint(s.cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败（端口被占用或无权限）: %w", addr, err)
	}

	lf, _ := openLog(s.cfg.LogPath)
	logger := slog.New(slog.NewTextHandler(logWriter(lf), &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler := &webdav.Handler{
		LockSystem: webdav.NewMemLS(),
		Logger:     func(r *http.Request, err error) { davLog(logger, r, err) },
	}

	var tlsCfg *tlsConfigBundle
	if s.cfg.TLS {
		tlsCfg, err = ensureSelfSigned(s.cfg.CertDir, "duihao-webdav")
		if err != nil {
			_ = ln.Close()
			_ = closeLog(lf)
			return fmt.Errorf("生成自签证书失败: %w", err)
		}
		ln = tls.NewListener(ln, tlsCfg.tlsConfig())
	}

	limit := &connLimiter{Listener: ln, max: int64(s.cfg.MaxConns)}
	srv := &http.Server{
		Handler:           s.guard(logger, handler),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       300 * time.Second,
	}

	s.httpSrv = srv
	s.ln = limit
	s.logFile = lf
	s.running = true
	s.shareCnt = len(s.cfg.Shares)
	scheme := "http"
	if s.cfg.TLS {
		scheme = "https"
	}
	logger.Info("WebDAV 已启动", "addr", addr, "scheme", scheme,
		"shares", s.shareCnt, "readonly", s.cfg.ReadOnly, "maxConns", s.cfg.MaxConns)

	go func() {
		err := srv.Serve(limit)
		s.mu.Lock()
		s.running = false
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			s.lastErr = err.Error()
			logger.Error("WebDAV 服务异常退出", "err", err)
		}
		s.mu.Unlock()
	}()
	return nil
}

// Stop 关闭监听与在途连接（幂等）。
func (s *Server) Stop() error {
	s.mu.Lock()
	srv, lf := s.httpSrv, s.logFile
	s.httpSrv, s.ln, s.logFile, s.running = nil, nil, nil, false
	s.mu.Unlock()

	var err error
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := srv.Shutdown(ctx); e != nil {
			_ = srv.Close()
			err = e
		}
	}
	_ = closeLog(lf)
	return err
}

// Running 是否在监听。
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Port 当前端口。
func (s *Server) Port() int { return s.cfg.Port }

// Addr 实际监听地址。
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// LastError 最近一次服务端异常（诊断用）。
func (s *Server) LastError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// ─── 请求管线：连接上限 → 路径安全 → Basic Auth → webdav ───

// guard 包一层：路径校验 + 保留端点 + 认证（Basic / 会话 Cookie / 登录页）
// + 每请求按凭据装配 webdav.Handler。
func (s *Server) guard(logger *slog.Logger, dav *webdav.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 0) 保留端点：登录 / 登出（先于认证，登出要能冲掉缓存的 Basic 凭据）。
		switch {
		case r.URL.Path == "/-login" && r.Method == "POST":
			s.handleLogin(w, r)
			return
		case r.URL.Path == "/-logout" && (r.Method == "GET" || r.Method == "HEAD"):
			s.handleLogout(w)
			return
		case reservedPath(r.URL.Path):
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 1) 路径安全：按原始 URL 逐段校验（M0 踩坑：不能 Clean 后再查）。
		if err := CheckRequestPath(r.URL.Path, r.URL.RawPath); err != nil {
			logger.Warn("拒绝危险路径", "remote", r.RemoteAddr, "path", r.URL.Path, "err", err)
			http.Error(w, "forbidden path", http.StatusForbidden)
			return
		}

		// 2) 认证：Basic（WebDAV 客户端）→ 会话 Cookie（浏览器登录页签发）。
		//    都没有时：浏览器导航给登录页，其余 401 challenge。
		sh := s.shareByBasic(r)
		if sh == nil {
			sh = s.shareByCookie(r)
		}
		if sh == nil {
			if isBrowserNav(r) {
				writeLoginPage(w, "")
				return
			}
			s.challenge(w)
			return
		}

		root := expandRoot(sh.Root, s.cfg.InstanceDir)
		readonly := sh.ReadOnly || s.cfg.ReadOnly

		// 3) 单请求体积上限（PUT/PROPPATCH 等带体的方法）。
		if r.ContentLength > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, int64(s.cfg.MaxBodyMB)*1024*1024)
		}

		// 4) 只读共享：挡住所有写方法（LOCK 保留——只读盘不该拿锁）。
		if readonly {
			switch r.Method {
			case "PUT", "MKCOL", "DELETE", "COPY", "MOVE", "PROPPATCH", "POST", "LOCK", "UNLOCK":
				logger.Warn("只读共享拒绝写操作", "user", sh.User, "method", r.Method, "path", r.URL.Path)
				w.Header().Set("Allow", "OPTIONS, GET, HEAD, PROPFIND, REPORT")
				http.Error(w, "share is read-only", http.StatusMethodNotAllowed)
				return
			}
		}

		// 5) 磁盘水位：上传前查剩余空间。
		if r.Method == "PUT" && r.ContentLength > 0 {
			if free, ferr := diskFreeMB(root); ferr == nil && free < 500 {
				logger.Warn("磁盘剩余空间不足", "root", root, "freeMB", free)
			}
		}

		// 6) 每请求装配 FS：凭据决定根目录（用户名 → 根目录）。
		reqDav := *dav
		reqDav.FileSystem = &guardedFS{Dir: webdav.Dir(root)}

		// 7) GET 目录 → 文件管理器页面。浏览器地址栏只会 GET，而 webdav.Handler
		//    对集合返回 405（用户看到的就是「登录进去啥也没有」）。
		//    文件下载仍走 webdav.ServeHTTP（带 Range/断点续传）。
		if r.Method == "GET" {
			// 7a) 目录打包 zip 下载（?zip=1）。
			if r.URL.Query().Get("zip") == "1" {
				streamZip(w, root, r.URL.Path)
				return
			}
			if f, oerr := reqDav.FileSystem.OpenFile(r.Context(), r.URL.Path, os.O_RDONLY, 0); oerr == nil {
				st, serr := f.Stat()
				if serr == nil && st.IsDir() {
					subs, rerr := f.Readdir(-1)
					_ = f.Close()
					if rerr == nil {
						writeDirListing(w, r, subs, readonly)
						return
					}
				} else {
					_ = f.Close()
				}
			}
		}

		reqDav.ServeHTTP(w, r)
	})
}

// challenge 401 认证挑战（WebDAV 客户端会带上凭据重试）。
func (s *Server) challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Duihao WebDAV", charset="UTF-8"`)
	http.Error(w, "authorization required", http.StatusUnauthorized)
}

// shareByBasic Basic Auth：用户名找共享 → bcrypt 校验。
func (s *Server) shareByBasic(r *http.Request) *Share {
	user, pass, ok := r.BasicAuth()
	if !ok || user == "" {
		return nil
	}
	return s.shareByCreds(user, pass)
}

// shareByCookie 浏览器会话 Cookie → 用户名 → 启用的共享。
func (s *Server) shareByCookie(r *http.Request) *Share {
	c, err := r.Cookie(sessCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	user, err := verifyToken(s.sessKey, c.Value)
	if err != nil {
		return nil
	}
	snap := s.cfg.Shares
	for i := range snap {
		if snap[i].Enabled && snap[i].User == user {
			return &snap[i]
		}
	}
	return nil
}

// guardedFS 在 webdav.Dir 外再包一层逻辑路径校验（防御纵深）。
type guardedFS struct {
	webdav.Dir
}

func (g *guardedFS) check(name string) error {
	if err := CheckRelPath(name); err != nil {
		return webdav.ErrForbidden
	}
	return nil
}

func (g *guardedFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	if err := g.check(name); err != nil {
		return err
	}
	return g.Dir.Mkdir(ctx, name, perm)
}

func (g *guardedFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	if err := g.check(name); err != nil {
		return nil, err
	}
	return g.Dir.OpenFile(ctx, name, flag, perm)
}

func (g *guardedFS) RemoveAll(ctx context.Context, name string) error {
	if err := g.check(name); err != nil {
		return err
	}
	return g.Dir.RemoveAll(ctx, name)
}

func (g *guardedFS) Rename(ctx context.Context, oldName, newName string) error {
	if err := g.check(oldName); err != nil {
		return err
	}
	if err := g.check(newName); err != nil {
		return err
	}
	return g.Dir.Rename(ctx, oldName, newName)
}

func (g *guardedFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	if err := g.check(name); err != nil {
		return nil, err
	}
	return g.Dir.Stat(ctx, name)
}

// connLimiter 并发连接上限：满了直接拒（对 WebDAV 客户端表现为连接被重置，
// 比排队 hang 住更早暴露问题）。
type connLimiter struct {
	net.Listener
	max int64
	mu  sync.Mutex
	n   int64
}

func (l *connLimiter) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err // 真错误（监听已关）才上抛
		}
		l.mu.Lock()
		if l.n >= l.max {
			l.mu.Unlock()
			_ = c.Close() // 超限：丢弃这一条，继续收（返回 error 会停掉整个 Serve）
			continue
		}
		l.n++
		l.mu.Unlock()
		return &limitedConn{Conn: c, l: l}, nil
	}
}

type limitedConn struct {
	net.Conn
	l    *connLimiter
	once sync.Once
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		c.l.mu.Lock()
		c.l.n--
		c.l.mu.Unlock()
	})
	return err
}

// writeDirListing 渲染浏览器文件管理器（对标宝塔文件管理：上传 / 新建文件夹 /
// 重命名 / 删除 / 下载，支持拖拽上传与进度条）。
//
// 实现取舍：动态数据（当前路径 + 条目）以 JSON 塞进页面，DOM 全部由前端 JS
// 渲染——转义靠 createElement/textContent 天然安全，Go 侧不拼 HTML。
// 增删改直接调标准 WebDAV 方法（PUT/MKCOL/DELETE/MOVE），服务端零新增端点，
// 权限/路径校验/审计全部走既有 guard 管线；只读共享前端隐藏写按钮、后端照样拒绝。
func writeDirListing(w http.ResponseWriter, r *http.Request, entries []os.FileInfo, readonly bool) {
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i].IsDir(), entries[j].IsDir()
		if a != b {
			return a // 目录优先
		}
		return entries[i].Name() < entries[j].Name()
	})

	type ent struct {
		N string `json:"n"`
		D bool   `json:"d"`
		S int64  `json:"s"`
		M string `json:"m"`
		H string `json:"h"` // 已编码请求路径（浏览器 href / fetch 直接用）
	}
	list := make([]ent, 0, len(entries))
	for _, e := range entries {
		href, _ := url.Parse(r.URL.Path)
		href = href.JoinPath(e.Name())
		if e.IsDir() {
			href.Path += "/"
		}
		mod := ""
		if e.ModTime().Year() > 1980 {
			mod = e.ModTime().Format("2006-01-02 15:04")
		}
		list = append(list, ent{N: e.Name(), D: e.IsDir(), S: e.Size(), M: mod, H: href.EscapedPath()})
	}
	payload, _ := json.Marshal(map[string]any{
		"cur": r.URL.EscapedPath(), "w": !readonly, "list": list,
	})

	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">")
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString(`<title>` + html.EscapeString(r.URL.Path) + `</title>`)
	b.WriteString(`<style>` + davCSS + `</style></head><body><div class="card" id="card">`)
	b.WriteString(`<header><div class="top"><div class="brand"><b>对号</b> · WebDAV 网盘` +
		`<span id="ro" class="ro" hidden>只读</span></div><div class="tools">` +
		`<input id="q" class="q" placeholder="搜索当前目录">` +
		`<button class="tb danger" id="btn-batch" hidden></button>` +
		`<button class="tb primary" id="btn-up">上传</button><button class="tb" id="btn-mk">新建文件夹</button>` +
		`<button class="tb out" id="btn-out">退出</button></div></div>` +
		`<nav class="crumbs" id="crumbs"></nav>` +
		`<div class="prog" id="prog" hidden><div class="bar" id="bar"></div></div></header>`)
	b.WriteString(`<div class="thead"><span class="nm th" data-k="n">名称<span class="arr" id="a-n"></span></span><span class="mt th" data-k="m">修改时间<span class="arr" id="a-m"></span></span><span class="sz th" data-k="s">大小<span class="arr" id="a-s"></span></span><span class="op"><input type="checkbox" class="cb" id="all" title="全选/全不选"></span></div>`)
	b.WriteString(`<div id="rows"></div><div class="empty" id="empty" hidden></div>`)
	b.WriteString(`<footer><span>对号 DUIHAO</span><span id="stat"></span></footer>`)
	b.WriteString(`<div class="drop" id="drop">松开鼠标上传到当前目录</div>`)
	b.WriteString(`<input type="file" id="file" multiple hidden>`)
	b.WriteString(`<div class="mask" id="mask" hidden><div class="dlg" id="dlg"></div></div>`)
	b.WriteString(`<div id="toast"></div>`)
	b.WriteString(`<script type="application/json" id="davdata">` + string(payload) + `</script>`)
	b.WriteString(`<script>` + davJS + `</script>`)
	b.WriteString(`</div></body></html>`)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, b.String())
}

// davCSS 文件管理器页面样式（浅色 / 深色跟随系统）。
const davCSS = `:root{--bg:#f4f5f7;--card:#fff;--tx:#24292f;--mut:#8a919c;--ln:#eceef1;--hov:#f6f8fa;--acc:#d97706;--danger:#dc2626;--br:14px}
@media (prefers-color-scheme:dark){:root{--bg:#16181c;--card:#1f2329;--tx:#e6e8eb;--mut:#7d8590;--ln:#2d333b;--hov:#262c34;--acc:#f59e0b;--danger:#f87171}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--tx);font-family:system-ui,"Segoe UI","Microsoft YaHei",sans-serif;display:flex;justify-content:center;padding:2.5rem 1rem;min-height:100vh}
.card{background:var(--card);width:100%;max-width:56rem;border-radius:var(--br);box-shadow:0 1px 3px rgba(0,0,0,.08),0 12px 32px rgba(0,0,0,.06);overflow:hidden;height:fit-content;position:relative}
header{padding:1.1rem 1.5rem 1rem;border-bottom:1px solid var(--ln)}
.top{display:flex;justify-content:space-between;align-items:center;gap:.75rem;margin-bottom:.55rem;flex-wrap:wrap}
.brand{font-size:.8rem;color:var(--mut);letter-spacing:.02em;white-space:nowrap}
.brand b{color:var(--tx);font-weight:600}
.ro{margin-left:.5rem;padding:.1rem .45rem;border:1px solid var(--acc);color:var(--acc);border-radius:99px;font-size:.7rem}
.tools{display:flex;gap:.5rem;align-items:center;flex-wrap:wrap}
.q{border:1px solid var(--ln);background:var(--card);color:var(--tx);padding:.35rem .6rem;border-radius:8px;font-size:.8rem;outline:none;width:10rem}
.q:focus{border-color:var(--acc)}
.tb{border:1px solid var(--ln);background:var(--card);color:var(--tx);padding:.35rem .8rem;border-radius:8px;font-size:.8rem;cursor:pointer;white-space:nowrap}
.tb:hover{border-color:var(--acc);color:var(--acc)}
.tb.primary{background:var(--acc);border-color:var(--acc);color:#fff}
.tb.primary:hover{opacity:.88;color:#fff}
.tb.danger{border-color:var(--danger);color:var(--danger)}
.tb.danger:hover{background:var(--danger);color:#fff}
.tb.out:hover{border-color:var(--danger);color:var(--danger)}
.crumbs{display:flex;flex-wrap:wrap;align-items:center;gap:.15rem;font-size:1rem;font-weight:600}
.crumbs a{color:var(--mut);text-decoration:none;font-weight:500;padding:.1rem .3rem;border-radius:6px;display:inline-flex;align-items:center;gap:.25rem}
.crumbs a:hover{color:var(--acc);background:var(--hov)}
.crumbs a.cur{color:var(--tx);font-weight:700}
.crumbs a.cur:hover{color:var(--tx);background:none}
.crumbs .sep{color:var(--mut);opacity:.5}
.prog{height:4px;border-radius:99px;background:var(--ln);margin-top:.7rem;overflow:hidden}
.bar{height:100%;width:0;background:var(--acc);transition:width .15s}
.thead{display:flex;gap:.75rem;padding:.5rem 1.5rem;font-size:.75rem;color:var(--mut);border-bottom:1px solid var(--ln);letter-spacing:.05em}
.thead .nm{flex:1}.thead .sz{flex:0 0 5.5rem;text-align:right}.thead .op{flex:0 0 7rem;display:flex;justify-content:flex-end;align-items:center}.thead .mt{flex:0 0 9.5rem}
.th{cursor:pointer;user-select:none;display:inline-flex;align-items:center;gap:.2rem}
.th:hover{color:var(--acc)}
.th .arr{font-size:.7rem;min-width:.7em;color:var(--acc)}
.row{display:flex;align-items:center;gap:.75rem;padding:.55rem 1.5rem;text-decoration:none;color:var(--tx);border-bottom:1px solid var(--ln)}
.row:hover{background:var(--hov)}
.row.sel{background:rgba(217,119,6,.08)}
.row .ck{flex:0 0 1rem;display:flex;align-items:center}
.row .ic{flex:0 0 1.25rem;display:flex;align-items:center}
.row .nm{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:.92rem}
.row.dir .nm{font-weight:500}
.row .mt{flex:0 0 9.5rem;color:var(--mut);font-size:.8rem;font-variant-numeric:tabular-nums}
.row .sz{flex:0 0 5.5rem;text-align:right;color:var(--mut);font-size:.8rem;font-variant-numeric:tabular-nums}
.acts{flex:0 0 7rem;display:flex;gap:.1rem;justify-content:flex-end}
.ab{border:none;background:transparent;color:var(--mut);cursor:pointer;padding:.25rem .3rem;border-radius:6px;display:inline-flex;align-items:center}
.ab:hover{background:var(--hov);color:var(--acc)}
.ab.danger:hover{color:var(--danger)}
.cb{width:15px;height:15px;accent-color:var(--acc);cursor:pointer}
.empty{padding:3rem 1.5rem;text-align:center;color:var(--mut);font-size:.9rem}
footer{padding:.9rem 1.5rem;color:var(--mut);font-size:.78rem;display:flex;justify-content:space-between;gap:1rem}
.drop{position:absolute;inset:0;display:none;align-items:center;justify-content:center;background:rgba(217,119,6,.12);border:2px dashed var(--acc);border-radius:var(--br);font-weight:600;color:var(--acc);z-index:5}
.drop.on{display:flex}
.mask{position:absolute;inset:0;background:rgba(0,0,0,.38);display:flex;align-items:center;justify-content:center;z-index:8}
.mask[hidden]{display:none}
.pv{position:fixed;inset:0;background:rgba(0,0,0,.78);display:flex;flex-direction:column;align-items:center;justify-content:center;z-index:20;gap:.8rem}
.pv img{max-width:92vw;max-height:78vh;border-radius:8px;box-shadow:0 12px 40px rgba(0,0,0,.5)}
.pv video{max-width:92vw;max-height:78vh;border-radius:8px;background:#000}
.pv audio{width:min(30rem,88vw)}
.pv .cap{color:#fff;font-size:.85rem;max-width:88vw;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.pv .x{position:absolute;top:.8rem;right:1.2rem;background:none;border:none;color:#fff;font-size:2rem;line-height:1;cursor:pointer;opacity:.85}
.pv .x:hover{opacity:1}
.dlg{background:var(--card);border-radius:12px;box-shadow:0 8px 28px rgba(0,0,0,.22);padding:1.1rem 1.2rem;width:min(22rem,86%);text-align:left}
.dlg h3{margin:0 0 .55rem;font-size:.95rem;font-weight:600}
.dlg p{margin:0 0 1rem;font-size:.85rem;color:var(--tx);word-break:break-all;white-space:pre-wrap}
.dlg input{width:100%;border:1px solid var(--ln);background:var(--card);color:var(--tx);padding:.5rem .7rem;border-radius:8px;font-size:.88rem;margin-bottom:1rem;outline:none}
.dlg input:focus{border-color:var(--acc)}
.dlg .btns{display:flex;gap:.5rem;justify-content:flex-end}
.dlg .btns button{border:1px solid var(--ln);background:var(--card);color:var(--tx);padding:.42rem 1.1rem;border-radius:8px;font-size:.82rem;cursor:pointer}
.dlg .btns button.ok{background:var(--acc);border-color:var(--acc);color:#fff}
.dlg .btns button.ok:hover{opacity:.9;color:#fff}
.dlg .btns button.ok.danger{background:var(--danger);border-color:var(--danger)}
.dlg .btns button.cancel:hover{border-color:var(--mut)}
#toast{position:fixed;left:50%;bottom:2rem;transform:translateX(-50%);background:#1f2329;color:#fff;padding:.55rem 1rem;border-radius:8px;font-size:.85rem;opacity:0;transition:opacity .2s;pointer-events:none;max-width:80vw;z-index:9}
#toast.err{background:var(--danger)}
#toast.on{opacity:.95}
@media (max-width:34rem){body{padding:1rem .5rem}.row .mt,.thead .mt{display:none}.row .sz,.thead .sz{flex-basis:4rem}.acts,.thead .op{flex-basis:6.2rem}.crumbs{font-size:.9rem}.q{flex:1 1 7rem;width:auto}}
`

// davJS 文件管理器交互：渲染条目 / 上传（XHR 进度 + 拖拽）/ 新建 / 重命名 / 删除。
// 写操作直接调标准 WebDAV 方法（PUT/MKCOL/DELETE/MOVE）；401 交给浏览器重新弹认证。
// 注意：本常量是 Go 原生字符串，JS 里的正则 \/ 原样透传（匹配斜杠必须转义）。
const davJS = `(function(){
var D=JSON.parse(document.getElementById('davdata').textContent);
var $=function(id){return document.getElementById(id)};
var DIR='<svg width="18" height="18" viewBox="0 0 24 24"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z" fill="#d97706" opacity=".9"/></svg>';
var FILE='<svg width="18" height="18" viewBox="0 0 24 24" fill="none"><path d="M6 3h8l4 4v13a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1Z" stroke="#8a919c" stroke-width="1.6" stroke-linejoin="round"/><path d="M14 3v4h4" stroke="#8a919c" stroke-width="1.6" stroke-linejoin="round"/></svg>';
var HOME='<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round"><path d="M3 11l9-8 9 8"/><path d="M5 10v10h5v-6h4v6h5V10"/></svg>';
var I_DL='<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 4v11"/><path d="M8 11l4 4 4-4"/><path d="M5 20h14"/></svg>';
var I_RN='<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 20l4.5-1L20 7.5a2.1 2.1 0 0 0-3-3L5.5 16 4 20Z"/></svg>';
var I_DE='<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h16"/><path d="M9 7V4h6v3"/><path d="M6 7l1 13h10l1-13"/></svg>';
var I_ZP='<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12"/><path d="M8 11l4 4 4-4"/><path d="M4 5h3M4 9h3M4 13h3"/><rect x="13" y="3" width="7" height="7" rx="1.5"/></svg>';
/* 按扩展名分型的文件图标（颜色区分大类） */
var I_IMG='<svg width="18" height="18" viewBox="0 0 24 24" fill="none"><rect x="3" y="4" width="18" height="16" rx="2" stroke="#0ea5e9" stroke-width="1.7"/><circle cx="9" cy="10" r="1.8" fill="#0ea5e9"/><path d="M5 19l5.5-5.5L14 17l3-3 3 3.4" stroke="#0ea5e9" stroke-width="1.7" stroke-linejoin="round"/></svg>';
var I_VID='<svg width="18" height="18" viewBox="0 0 24 24" fill="none"><rect x="3" y="5" width="18" height="14" rx="2" stroke="#8b5cf6" stroke-width="1.7"/><path d="M10.5 9.5l4.5 2.5-4.5 2.5v-5Z" fill="#8b5cf6"/></svg>';
var I_AUD='<svg width="18" height="18" viewBox="0 0 24 24" fill="none"><path d="M9 18V6l10-2v11" stroke="#10b981" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/><circle cx="6.8" cy="18" r="2.4" fill="#10b981"/><circle cx="16.8" cy="15" r="2.4" fill="#10b981"/></svg>';
var I_ARCHIVE='<svg width="18" height="18" viewBox="0 0 24 24" fill="none"><rect x="4" y="3" width="16" height="18" rx="2" stroke="#f59e0b" stroke-width="1.7"/><path d="M11 3v3h2V3M11 8v2h2V8M11 12v2h2v-2" stroke="#f59e0b" stroke-width="1.7"/><path d="M10 17h4" stroke="#f59e0b" stroke-width="1.7" stroke-linecap="round"/></svg>';
var I_DOC='<svg width="18" height="18" viewBox="0 0 24 24" fill="none"><path d="M6 3h8l4 4v13a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1Z" stroke="#3b82f6" stroke-width="1.6" stroke-linejoin="round"/><path d="M14 3v4h4M9 12h6M9 15.5h6M9 19h4" stroke="#3b82f6" stroke-width="1.6" stroke-linecap="round"/></svg>';
var I_CODE='<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="#64748b" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M8.5 7L4 12l4.5 5M15.5 7L20 12l-4.5 5"/></svg>';
function el(t,c,x){var e=document.createElement(t);if(c)e.className=c;if(x!=null)e.textContent=x;return e}
function sz(n){if(n<1024)return n+' B';var u=['KB','MB','GB','TB'],i=-1;do{n/=1024;i++}while(n>=1024&&i<3);return n.toFixed(1)+' '+u[i]}
function toast(m,err){var t=$('toast');t.textContent=m;t.className=err?'err on':'on';clearTimeout(t._h);t._h=setTimeout(function(){t.className=err?'err':''},2600)}
function joinP(name){var base=D.cur.replace(/\/+$/g,'');return (base?base:'')+'/'+encodeURIComponent(name)}
function exists(url){return fetch(url,{method:'PROPFIND',headers:{'Depth':'0'}}).then(function(r){return r.status===207})}
/* ── 模态：替代原生 prompt/confirm ── */
function modal(o){return new Promise(function(res){
 var mask=$('mask'),dlg=$('dlg');dlg.innerHTML='';
 dlg.appendChild(el('h3',null,o.title));
 if(o.msg)dlg.appendChild(el('p',null,o.msg));
 var inp=null;
 if(o.input){inp=document.createElement('input');inp.value=o.def||'';dlg.appendChild(inp)}
 var btns=el('div','btns');
 var done=function(v){mask.hidden=true;document.removeEventListener('keydown',key,true);res(v)};
 var key=function(e){
  if(e.key==='Escape'){e.stopPropagation();e.preventDefault();done(null)}
  else if(e.key==='Enter'&&o.input){e.preventDefault();done(inp.value.trim())}
 };
 var bc=el('button','cancel','取消');bc.onclick=function(){done(null)};
 var ok=el('button','ok'+(o.danger?' danger':''),o.okText||'确定');
 ok.onclick=function(){done(o.input?(inp.value.trim()||null):true)};
 btns.appendChild(bc);btns.appendChild(ok);dlg.appendChild(btns);
 mask.hidden=false;
 document.addEventListener('keydown',key,true);
 if(inp){inp.focus();inp.select()}else{ok.focus()}
})}
function askText(title,def){return modal({title:title,input:true,def:def})}
function askConfirm(title,danger){return modal({title:title,danger:!!danger}).then(function(v){return v===true})}
/* ── 401 → 回登录页（Cookie 过期或凭据被冲掉时服务端渲染登录页） ── */
function gone(r){if(r.status===401){location.reload();return true}return false}
/* ── 文件类型图标 ── */
var EXTMAP={img:'jpg jpeg png gif webp bmp svg ico heic',vid:'mp4 mkv avi mov wmv flv webm m4v',aud:'mp3 wav flac aac ogg m4a wma',arch:'zip rar 7z tar gz bz2 xz iso',doc:'doc docx pdf txt md xls xlsx ppt pptx csv rtf',code:'js ts go php html css json py java c cpp h sh bat ps1'};
function kindOf(n){var i=n.lastIndexOf('.'),e=i<0?'':n.slice(i+1).toLowerCase();
 for(var k in EXTMAP){if(EXTMAP[k].split(' ').indexOf(e)>=0)return k}
 return 'file'}
function iconFor(n,isDir){
 if(isDir)return DIR;
 switch(kindOf(n)){case 'img':return I_IMG;case 'vid':return I_VID;case 'aud':return I_AUD;
 case 'arch':return I_ARCHIVE;case 'doc':return I_DOC;case 'code':return I_CODE;default:return FILE}
}
function crumbs(){
 var c=$('crumbs');c.innerHTML='';
 var parts=D.cur.replace(/^\/+|\/+$/g,'').split('/').filter(Boolean);
 function homeLink(isCur){var a=el('a',isCur?'cur':null);a.href='/';a.innerHTML=HOME;var t=el('span',null,'网盘');a.appendChild(t);return a}
 if(!parts.length){c.appendChild(homeLink(true));return}
 c.appendChild(homeLink(false));
 var acc='';
 parts.forEach(function(p,i){
  c.appendChild(el('span','sep','/'));acc+='/'+p;
  var l=el('a',i===parts.length-1?'cur':null,decodeURIComponent(p));l.href=acc+'/';c.appendChild(l);
 });
}
/* 排序：sk/sd 为当前键与方向；目录永远在前 */
var sk='n',sd=1;
function sortedList(){
 var q=$('q').value.trim().toLowerCase();
 var l=D.list.filter(function(e){return !q||e.n.toLowerCase().indexOf(q)>=0});
 l.sort(function(a,b){
  if(a.d!==b.d)return a.d?-1:1;
  if(sk==='s')return (a.s-b.s)*sd;
  var x=sk==='m'?(a.m||''):a.n.toLowerCase(),y=sk==='m'?(b.m||''):b.n.toLowerCase();
  return x<y?-sd:x>y?sd:0;
 });
 return l;
}
function drawArrows(){
 $('a-n').textContent=sk==='n'?(sd>0?'↑':'↓'):'';
 $('a-m').textContent=sk==='m'?(sd>0?'↑':'↓'):'';
 $('a-s').textContent=sk==='s'?(sd>0?'↑':'↓'):'';
}
var sel={};
function updBatch(){
 var n=0;for(var k in sel)if(sel[k])n++;
 var b=$('btn-batch');
 if(n){b.hidden=false;b.textContent='删除所选('+n+')'}else{b.hidden=true}
}
function row(e){
 var a=el('a','row'+(e.d?' dir':'')+(sel[e.h]?' sel':''));a.href=e.h;
 var ck=el('span','ck');
 if(D.w){var cb=document.createElement('input');cb.type='checkbox';cb.className='cb';cb.checked=!!sel[e.h];
  cb.onclick=function(ev){ev.stopPropagation()};cb.onchange=function(){sel[e.h]=this.checked;updBatch()};
  ck.appendChild(cb)}
 a.appendChild(ck);
 var ic=el('span','ic');ic.innerHTML=iconFor(e.n,e.d);a.appendChild(ic);
 a.appendChild(el('span','nm',e.n));
 a.appendChild(el('span','mt',e.m||''));
 a.appendChild(el('span','sz',e.d?'':sz(e.s)));
 var acts=el('span','acts');
 if(e.d){var zp=el('button','ab');zp.title='打包下载 (zip)';zp.innerHTML=I_ZP;
  zp.onclick=function(ev){ev.preventDefault();var t=document.createElement('a');t.href=e.h+'?zip=1';document.body.appendChild(t);t.click();t.remove()};
  acts.appendChild(zp)}
 else{var dl=el('button','ab');dl.title='下载';dl.innerHTML=I_DL;
  dl.onclick=function(ev){ev.preventDefault();var t=document.createElement('a');t.href=e.h;t.download=e.n;document.body.appendChild(t);t.click();t.remove()};
  acts.appendChild(dl);
  var k=kindOf(e.n);
  if(k==='img'||k==='vid'||k==='aud'){a.onclick=function(ev){ev.preventDefault();preview(e)}}}
 if(D.w){
  var rn=el('button','ab');rn.title='重命名';rn.innerHTML=I_RN;rn.onclick=function(ev){ev.preventDefault();rename(e)};acts.appendChild(rn);
  var de=el('button','ab danger');de.title='删除';de.innerHTML=I_DE;de.onclick=function(ev){ev.preventDefault();del(e)};acts.appendChild(de);
 }
 a.appendChild(acts);return a;
}
function render(){
 crumbs();
 var box=$('rows');box.innerHTML='';
 var nd=0,nf=0;
 sortedList().forEach(function(e){box.appendChild(row(e));e.d?nd++:nf++});
 var emp=$('empty');
 if(D.list.length){emp.hidden=true}
 else{emp.hidden=false;emp.textContent=D.w?'此目录为空，拖拽文件进来即可上传':'此目录为空'}
 $('stat').textContent=nd+' 个目录 · '+nf+' 个文件';
 drawArrows();
 if(!D.w){$('ro').hidden=false;$('btn-up').hidden=true;$('btn-mk').hidden=true;$('btn-batch').hidden=true;$('all').hidden=true}
 updBatch();
}
/* ── 增删改 ── */
async function rename(e){
 var nv=await askText('重命名「'+e.n+'」',e.n);
 if(!nv||nv===e.n)return;
 var r=await fetch(e.h,{method:'MOVE',headers:{'Destination':joinP(nv)+(e.d?'/':''),'Overwrite':'F'}});
 if(gone(r))return;
 if(r.status===412){toast('「'+nv+'」已存在，请换个名字',true);return}
 if(r.ok||r.status===201||r.status===204){location.reload()}else{toast('操作失败（'+r.status+'）',true)}
}
async function del(e){
 var ok=await askConfirm('删除「'+e.n+'」？'+(e.d?'\n目录内所有内容将一并删除。':''),true);
 if(!ok)return;
 var r=await fetch(e.h,{method:'DELETE'});
 if(gone(r))return;
 if(r.ok||r.status===204){location.reload()}else{toast('删除失败（'+r.status+'）',true)}
}
/* ── 媒体预览层：图片 / 视频 / 音频（点行弹出，Esc 或点空白关闭）── */
function preview(e){
 var pv=document.createElement('div');pv.className='pv';
 var close=function(){document.body.removeChild(pv);document.removeEventListener('keydown',key,true)};
 var key=function(ev){if(ev.key==='Escape'){ev.preventDefault();close()}};
 var x=el('button','x','\u00d7');x.onclick=close;
 var k=kindOf(e.n),m=null;
 if(k==='img'){m=document.createElement('img');m.src=e.h;m.alt=e.n}
 else if(k==='vid'){m=document.createElement('video');m.src=e.h;m.controls=true;m.autoplay=true}
 else{m=document.createElement('audio');m.src=e.h;m.controls=true;m.autoplay=true}
 pv.appendChild(m);
 pv.appendChild(el('div','cap',decodeURIComponent(e.n)));
 pv.appendChild(x);
 pv.onclick=function(ev){if(ev.target===pv)close()};
 document.body.appendChild(pv);
 document.addEventListener('keydown',key,true);
}
$('btn-mk').onclick=async function(){
 var nv=await askText('新文件夹名称','');
 if(!nv)return;
 var r=await fetch(joinP(nv)+'/',{method:'MKCOL'});
 if(gone(r))return;
 if(r.status===405){toast('同名文件或文件夹已存在',true);return}
 if(r.status===201){location.reload()}else{toast('创建失败（'+r.status+'）',true)}
};
$('btn-out').onclick=function(){location.href='/-logout'};
/* 表头点击排序 */
[].forEach.call(document.querySelectorAll('.th'),function(t){
 t.onclick=function(){var k=t.getAttribute('data-k');if(sk===k){sd=-sd}else{sk=k;sd=1}render()};
});
/* 搜索 */
$('q').oninput=function(){render()};
/* 全选 / 批量删除 */
$('all').onchange=function(){
 var v=this.checked;
 sortedList().forEach(function(e){if(D.w)sel[e.h]=v});
 render();
};
$('btn-batch').onclick=async function(){
 var n=0;for(var k in sel)if(sel[k])n++;
 if(!n)return;
 var ok=await askConfirm('删除所选 '+n+' 项？目录内容将一并删除。',true);
 if(!ok)return;
 var hs=Object.keys(sel).filter(function(k){return sel[k]});
 for(var i=0;i<hs.length;i++){
  var r=await fetch(hs[i],{method:'DELETE'});
  if(gone(r))return;
 }
 sel={};
 toast('已删除 '+hs.length+' 项');
 location.reload();
};
/* ── 上传：XHR 进度 + 覆盖确认 ── */
var prog=$('prog'),bar=$('bar');
async function uploadSeq(files){
 var i=0;
 while(i<files.length){
  var f=files[i++],url=joinP(f.name);
  var r=await fetch(url,{method:'PROPFIND',headers:{'Depth':'0'}});
  if(gone(r))return;
  if(r.status===207){
   var ov=await askConfirm('「'+f.name+'」已存在，覆盖它吗？（取消则跳过该文件）');
   if(!ov)continue;
  }
  var done=await new Promise(function(res){
   var xhr=new XMLHttpRequest();xhr.open('PUT',url);
   xhr.upload.onprogress=function(ev){if(ev.lengthComputable){prog.hidden=false;bar.style.width=Math.round(ev.loaded/ev.total*100)+'%'}};
   xhr.onload=function(){res(xhr.status)};
   xhr.onerror=function(){res(0)};
   xhr.send(f);
  });
  prog.hidden=true;bar.style.width='0';
  if(done===201||done===204){continue}
  if(done===413){toast('「'+f.name+'」超过大小上限',true);return}
  if(done===0){toast('「'+f.name+'」上传失败（网络错误）',true);return}
  if(done===401){location.reload();return}
  toast('「'+f.name+'」上传失败（'+done+'）',true);
  if(i<files.length){var goOn=await askConfirm('继续上传剩余文件吗？');if(!goOn)return}
 }
 toast('上传完成');
 location.reload();
}
$('btn-up').onclick=function(){$('file').click()};
$('file').onchange=function(){if(this.files.length)uploadSeq([].slice.call(this.files));this.value=''};
var drop=$('drop'),dragN=0;
window.addEventListener('dragover',function(ev){ev.preventDefault()});
window.addEventListener('dragenter',function(ev){ev.preventDefault();if(D.w){dragN++;drop.classList.add('on')}});
window.addEventListener('dragleave',function(){if(--dragN<=0){dragN=0;drop.classList.remove('on')}});
window.addEventListener('drop',function(ev){ev.preventDefault();dragN=0;drop.classList.remove('on');
 if(!D.w)return;var fs=[].slice.call(ev.dataTransfer.files);if(fs.length)uploadSeq(fs)});
render();
})();`

// ─── 日志与小工具 ───

// ─── 日志与小工具 ───

func davLog(logger *slog.Logger, r *http.Request, err error) {
	if err != nil {
		logger.Info("dav", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr, "err", err)
		return
	}
	// PROPFIND 轮询太吵，只记写操作与降级错误。
	switch r.Method {
	case "PROPFIND", "OPTIONS", "HEAD", "GET":
		return
	}
	logger.Info("dav", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
}

func openLog(path string) (*os.File, error) {
	if path == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}

func closeLog(f *os.File) error {
	if f == nil {
		return nil
	}
	return f.Close()
}

func logWriter(f *os.File) io.Writer {
	if f == nil {
		return io.Discard
	}
	return f
}

// dummyHash 一个真实的 bcrypt 哈希，仅用于「用户不存在」时把耗时对齐到真校验。
var dummyHash = func() string {
	h, err := bcrypt.GenerateFromPassword([]byte("duihao-timing-equalizer"), bcrypt.DefaultCost)
	if err != nil {
		return "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
	}
	return string(h)
}()

// FromValues 按实例设置构建服务（main 注入的 EmbeddedFactory 直接用它）。
// values 是「清单默认值 + 用户 override」合并后的表；{dir} token 在此展开。
func FromValues(dir, logPath string, ports map[string]int, values map[string]string) (*Server, error) {
	get := func(k, def string) string {
		if v, ok := values[k]; ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return def
	}
	atoi := func(k string, def int) int {
		n := 0
		if _, err := fmt.Sscanf(get(k, ""), "%d", &n); err != nil {
			return def
		}
		return n
	}
	shares, err := LoadShares(dir)
	if err != nil {
		return nil, err
	}
	certDir := filepath.Join(dir, "certs")
	return New(Config{
		Bind:        get("dav.bind", "127.0.0.1"),
		Port:        ports["dav"],
		TLS:         get("dav.tls", "0") == "1",
		CertDir:     certDir,
		Shares:      shares,
		ReadOnly:    get("dav.readonly", "0") == "1",
		MaxConns:    atoi("dav.maxconn", 32),
		MaxBodyMB:   atoi("dav.maxbody_mb", 1024),
		LogPath:     firstNonEmpty2(logPath, filepath.Join(dir, "logs", "webdav.log")),
		InstanceDir: dir,
	})
}

func firstNonEmpty2(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
