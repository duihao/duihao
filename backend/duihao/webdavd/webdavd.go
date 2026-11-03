// Package webdavd 内置 WebDAV 网盘（embedded 类插件，golang.org/x/net/webdav，BSD-3）。
//
// 定位（v3.1 §6.2）：把本机目录变成任意设备可挂载的盘——手机 ES 文件管理器、
// RaiDrive、Windows 资源管理器、浏览器、另一台对号都走标准 WebDAV。
//
// 模型：一个实例一个监听端口，多个共享；共享 = Basic Auth 凭据 + 根目录。
// URL 对所有共享相同（http://host:port/），哪个用户名登录就看到哪个根目录。
//
// 安全边界（v3.1 硬规则 + M58 调整）：
//   - 默认只监听 127.0.0.1；开局域网/公网（0.0.0.0）后，是否加密由两项设置决定：
//     dav.tls（是否额外提供 https）与 dav.plain（是否放行明文 http，默认放行）。
//     TLS 开 + 明文允许 = 同端口双协议（见 tlsniff.go）；TLS 关 = 纯 http。
//     明文下 Basic Auth 的密码可被同链路嗅探，界面常驻提示、启动日志记警告；
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
	"database/sql"
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

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/apps/camera"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/apps/download"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/apps/health"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/apps/home"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/apps/ledger"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/apps/music"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/apps/notes"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/apps/remote"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/apps/users"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/apps/video"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/apps/voice"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/hubusers"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/store"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/webui"
)

// Config 一次启动所需的全部参数（由实例设置 + 端口渲染而来）。
type Config struct {
	Bind string // 127.0.0.1（默认）/ 0.0.0.0（局域网/公网可连）
	Port int
	TLS  bool // 自签证书 HTTPS
	// AllowPlain 允许明文 HTTP 连接（dav.plain，默认允许）。
	// TLS 开着时与 https 同端口并存（按 ClientHello 首字节分流）；
	// TLS 关着时整个服务就是纯 http。
	AllowPlain bool
	CertDir    string // 证书目录（实例下 certs/）；空则临时用内存证书

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

	statMu    sync.Mutex          // statCache 并发保护
	statCache map[string]*volStat // 共享根目录 → 存储统计（stats.go）

	// 个人空间（单端口多应用 + 单库）：库按需惰性打开，Stop 时关闭并允许重开。
	apps      *appkit.Registry
	hubOnce   sync.Once
	bootOnce  sync.Once // NAS 账号引导只跑一次
	hubStore  *store.Store
	hubErr    error

	voiceOnce sync.Once // 语音提醒调度器只起一次（Start/Stop 可重复调）
}

// New 构造（不启动）。
func New(cfg Config) (*Server, error) {
	if cfg.Port < 0 || cfg.Port > 65535 {
		return nil, fmt.Errorf("端口 %d 非法", cfg.Port)
	}
	if cfg.Bind == "" {
		cfg.Bind = "127.0.0.1"
	}
	// 明文口径交给 dav.plain（M58 用户定稿：局域网与公网都要能直接 http）。
	// 曾经这里是硬规则「对外监听必须 TLS」，现在不再拦——真正起作用的是
	// AllowPlain：TLS 开着 + 允许明文 = 同端口双协议；TLS 关 = 纯 http。
	// 风险由界面提示（明文下 Basic Auth 的密码可被同链路嗅探）与审计承担。
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
	// 个人空间：所有子应用共用一个端口、一套登录、一个 SQLite 库。
	// 顶栏顺序定稿：网盘 · 影视 · 音乐 · 下载 · 笔记 · 账本 · 健康 · 收藏夹 ·
	// 语音提醒 · 监控（10 项）。下载紧跟音乐：它是给影视/音乐供料的那一端
	// （落点即入库）。语音提醒是「本机替你说话」；监控最右，它是可选模块 ——
	// 组件没装时页面只显示引导卡。（2026-10-30 增加监控与下载）
	apps := appkit.New(home.New(), notes.New(), ledger.New(), video.New(), health.New(), music.New(),
		voice.New(), camera.New(), download.New(), remote.New(), users.New())
	navOrder := []string{"video", "music", "download", "notes", "ledger", "health", "home", "voice", "camera", "remote", "users"}

	// 下载完成 → 让影视/音乐重扫一次，实现「落点即入库」。
	// 用注入而不是让 apps/download 直接 import apps/video，保持 apps 之间单向依赖。
	download.Resync = func(c appkit.Context, dir string) {
		var err error
		switch dir {
		case "video":
			err = video.Rescan(c)
		case "music":
			err = music.Rescan(c)
		}
		if err != nil {
			slog.Warn("下载完成后重扫媒体库失败", "dir", dir, "err", err)
		}
	}
	byID := make(map[string]appkit.App, len(navOrder))
	for _, a := range apps.Apps() {
		byID[a.ID()] = a
	}
	nav := make([]webui.NavItem, 0, len(navOrder)+1)
	nav = append(nav, webui.NavItem{Path: "/", Label: "网盘"})
	placed := map[string]bool{}
	for _, id := range navOrder {
		if a, ok := byID[id]; ok {
			nav = append(nav, webui.NavItem{Path: "/-" + id, Label: a.Title()})
			placed[id] = true
		}
	}
	// 兜底：新增了应用却忘了加进 navOrder 时，仍能在顶栏出现。
	for _, a := range apps.Apps() {
		if !placed[a.ID()] {
			nav = append(nav, webui.NavItem{Path: "/-" + a.ID(), Label: a.Title()})
		}
	}
	webui.SetNav(nav)
	return &Server{cfg: cfg, sessKey: sessKey, statCache: map[string]*volStat{}, apps: apps}, nil
}

// hub 惰性打开单库（InstanceDir 不可用时返回 nil，页面接口统一 503）。
func (s *Server) hub() *store.Store {
	s.hubOnce.Do(func() {
		if s.cfg.InstanceDir == "" {
			s.hubErr = errors.New("实例目录为空，个人空间不可用")
			return
		}
		// 库文件放实例数据目录，绝不放共享根（同步客户端会拉走写入中的 SQLite）。
		st, err := store.Open(filepath.Join(s.cfg.InstanceDir, "hub.db"))
		if err != nil {
			s.hubErr = err
			return
		}
		s.hubStore = st
		s.bootstrapUsers(st)
	})
	return s.hubStore
}

// bootstrapUsers NAS 账号引导：一个账号都没有时生成 admin + 随机密码。
// uid 固定 "admin"（2026-10-31 定稿：默认共享也叫 admin，两边同名对齐）；
// 明文密码进 hub_secrets 密文（连接管理页可查看），日志里只出现 uid。
func (s *Server) bootstrapUsers(st *store.Store) {
	s.bootOnce.Do(func() {
		uid, pass, created, err := hubusers.EnsureAdmin(st.DB(), "admin")
		if err != nil {
			slog.Warn("NAS 管理员账号引导失败", "err", err)
			return
		}
		if created {
			slog.Info("NAS 管理员账号已生成（密码见连接管理页）", "uid", uid)
		}
		_ = pass // 明文不落日志；连接管理页从 hub_secrets 取
	})
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
		// 同端口双协议：TLS 照旧；明文是否放行看 dav.plain（默认放行 → http
		// 与 https 共用这个端口，客户端爱用哪个用哪个）。
		ln = newDualListener(ln, tlsCfg.tlsConfig(), s.cfg.AllowPlain)
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
	logger.Info("WebDAV 已启动", "addr", addr, "scheme", scheme, "plain", s.cfg.AllowPlain,
		"shares", s.shareCnt, "readonly", s.cfg.ReadOnly, "maxConns", s.cfg.MaxConns)
	if s.cfg.AllowPlain && s.cfg.Bind != "127.0.0.1" && s.cfg.Bind != "localhost" {
		logger.Warn("允许明文 HTTP 且对外监听：Basic Auth 密码可被同链路嗅探，仅建议在可信网络使用",
			"addr", addr)
	}

	// 语音提醒：到点由本机出声，所以调度器跟着服务一起活（与页面无关）。
	// 第二个参数给网盘根：GPU 引擎预合成的 wav 要落在 _appfiles/voice 下。
	// 远程访问：DDNS 循环 + frpc 自动拉起；顺便把自己的端口给隧道默认落点。
	remote.SetLocalService(s.cfg.Port)
	remote.Start(func() *sql.DB {
		if st := s.hub(); st != nil {
			return st.DB()
		}
		return nil
	})

	s.voiceOnce.Do(func() {
		// 语音模块的报错必须进实例日志：GUI 进程的 stderr 没有落点，
		// 播报失败不接进来就等于静音且查无此错（真机踩过）。
		voice.Logf = func(format string, args ...any) {
			logger.Warn(fmt.Sprintf(format, args...))
		}
		voice.Start(func() *sql.DB {
			if st := s.hub(); st != nil {
				return st.DB()
			}
			return nil
		}, func() string {
			if len(s.cfg.Shares) == 0 {
				return ""
			}
			return expandRoot(s.cfg.Shares[0].Root, s.cfg.InstanceDir)
		})
	})

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
	// 个人空间库随服务停：释放文件句柄，并允许下次 Start 重新打开。
	remote.StopFrp()
	if s.hubStore != nil {
		_ = s.hubStore.Close()
		s.hubStore = nil
		s.hubOnce = sync.Once{}
		s.hubErr = nil
	}
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
		// 0) 内置窗口的窗口按钮（/-shell/<act>）：票即凭据，先于认证处理 ——
		//    它只碰窗口，不碰文件与账号，见 shell.go。
		if s.handleShell(w, r) {
			return
		}

		// 0) 保留端点：登录 / 登出（先于认证，登出要能冲掉缓存的 Basic 凭据）。
		switch {
		case r.URL.Path == "/-login" && r.Method == "POST":
			s.handleLogin(w, r)
			return
		case r.URL.Path == "/-q" && r.Method == "GET":
			s.handleQuick(w, r)
			return
		case r.URL.Path == "/-logout" && (r.Method == "GET" || r.Method == "HEAD"):
			s.handleLogout(w)
			return
		case reservedPath(r.URL.Path):
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 0.5) 公网总开关（远程访问）：关着时只放行局域网/本机来源。
		// frp 转发进来的流量在 frpc 那头出栈（对 127.0.0.1），不受影响；
		// IPv6 直连的公网访客在开关关着时一律 403。
		var hubDB *sql.DB
		if st := s.hub(); st != nil {
			hubDB = st.DB()
		}
		if !isPrivateClient(r) && !remote.PublicEnabled(hubDB) {
			logger.Warn("公网访问被拒（远程访问开关未开）", "remote", r.RemoteAddr, "path", r.URL.Path)
			http.Error(w, "远程访问未开启", http.StatusForbidden)
			return
		}

		// 0.7) 桌面端自动登录票（?_k=）：普通模式宫格点进 NAS 用。只认本机
		// 回环 + 60 秒短票；扫码与浏览器直接访问不带票，照常走账号密码。
		if r.Method == "GET" && r.URL.Query().Get("_k") != "" {
			if s.tryEntryKey(w, r) {
				return
			}
		}

		// 1) 路径安全：按原始 URL 逐段校验（M0 踩坑：不能 Clean 后再查）。
		if err := CheckRequestPath(r.URL.Path, r.URL.RawPath); err != nil {
			logger.Warn("拒绝危险路径", "remote", r.RemoteAddr, "path", r.URL.Path, "err", err)
			http.Error(w, "forbidden path", http.StatusForbidden)
			return
		}

		// 2) 认证：Basic（WebDAV 客户端）→ 会话 Cookie（浏览器登录页签发）。
		//    都没有时：浏览器导航给登录页，其余 401 challenge。
		//    返回的 uid 是账号键：共享账号名，或 hub: 前缀票解析出的 NAS 账号。
		sh, uid := s.authShare(r)
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

		// 2.5) NAS 账号身份：uid 有对应 hub_users 账号 → 带角色/权限。
		//      位置在文件层闸门之前：files 权限要在这里用。
		hubRole, hubPerms := "", map[string]string(nil)
		if st := s.hub(); st != nil {
			if u, _ := hubusers.Get(st.DB(), uid); u != nil && u.Enabled {
				hubRole, hubPerms = u.Role, u.Perms
			}
		}
		// 页面显示的用户名：NAS 账号显示 NAS 用户名；只有纯 WebDAV 共享
		// 会话（无 hub 账号）才显示共享账号名。
		dispUser := sh.User
		if hubRole != "" {
			dispUser = uid
		}

		// 2.6) NAS 账号的「网盘」模块权限（files）：写方法要 w。
		//      只管浏览器/票会话（/-* 之外的文件路径）；挂载客户端走共享
		//      凭据（Basic），根本不会有 hub 角色，不受影响。
		if hubRole != "" && !strings.HasPrefix(r.URL.Path, "/-") {
			switch r.Method {
			case "PUT", "MKCOL", "DELETE", "COPY", "MOVE", "PROPPATCH", "POST", "LOCK", "UNLOCK":
				if !hubusers.Can(hubRole, hubPerms, "files", "w") {
					logger.Warn("NAS 账号无网盘写权限", "user", uid, "method", r.Method, "path", r.URL.Path)
					http.Error(w, "没有「网盘」的修改权限", http.StatusForbidden)
					return
				}
			}
		}

		// 3) 单请求体积上限（PUT/PROPPATCH 等带体的方法）。
		if r.ContentLength > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, int64(s.cfg.MaxBodyMB)*1024*1024)
		}

		// 4) 只读共享：挡住所有写方法（LOCK 保留——只读盘不该拿锁）。
		//    个人空间接口（/-api/*）例外：读动作同样走 POST，由 appkit 按动作名
		//    判定写权限并回 405 中文原因（这里拦会把只读账号的读请求也挡掉）。
		if readonly && !strings.HasPrefix(r.URL.Path, "/-api/") {
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

		// 6.5) 递归搜索端点（需认证；按当前共享根向下走，见 search.go）。
		//      NAS 账号没有 files 读权限时同样拒（否则绕过页面直接搜）。
		if r.URL.Path == "/-search" && r.Method == "GET" {
			if hubRole != "" && !hubusers.Can(hubRole, hubPerms, "files", "r") {
				http.Error(w, "没有「网盘」的访问权限", http.StatusForbidden)
				return
			}
			s.handleSearch(w, r, root)
			return
		}

		// 6.6) 个人空间（单端口多应用）：静态资源 /-ui/*、页面 /-<app>、
		//      接口 /-api/<app>/<action>。账号键写进各表 uid 列；NAS 账号
		//      带角色/权限（2.5 已解析），共享会话（无对应账号）只能看文件。
		if webui.Static(w, r) {
			return
		}
		// 6.55) 主题：GET 查一次 / SSE 订阅变化（NAS 网页跟随桌面端外观设置）。
		//       放在 appkit 之前 —— 它不是某个应用的接口，也不该走模块权限。
		if r.URL.Path == "/-api/theme" && r.Method == "GET" {
			s.handleThemeAPI(w, r)
			return
		}
		if r.URL.Path == "/-api/theme/stream" && r.Method == "GET" {
			s.handleThemeStream(w, r)
			return
		}
		if s.apps != nil && strings.HasPrefix(r.URL.Path, "/-") {
			hctx := appkit.Context{User: uid, Root: root, Writable: !readonly, Role: hubRole, Perms: hubPerms}
			if st := s.hub(); st != nil {
				hctx.DB = st.DB()
			} else if s.hubErr != nil {
				logger.Warn("个人空间数据库不可用", "err", s.hubErr)
			}
			if hctx.Role == "" {
				// 没有 NAS 账号（或库没就绪）：文件照旧，应用页/API 拒入。
				appkit.Error(w, appkit.Errorf(http.StatusForbidden,
					"这个账号没有 NAS 网页账号，请用管理员账号登录，或让管理员在「用户」里开账号"))
				return
			}
			if s.apps.Handle(hctx, w, r) {
				return
			}
		}

		// 7) GET 目录 → 文件管理器页面。浏览器地址栏只会 GET，而 webdav.Handler
		//    对集合返回 405（用户看到的就是「登录进去啥也没有」）。
		//    文件下载仍走 webdav.ServeHTTP（带 Range/断点续传）。
		if r.Method == "GET" {
			// 7a) 目录打包 zip 下载（?zip=1）。
			if r.URL.Query().Get("zip") == "1" {
				if hubRole != "" && !hubusers.Can(hubRole, hubPerms, "files", "r") {
					writeNoPermPage(w, dispUser, hubRole, hubPerms)
					return
				}
				streamZip(w, root, r.URL.Path)
				return
			}
			if f, oerr := reqDav.FileSystem.OpenFile(r.Context(), r.URL.Path, os.O_RDONLY, 0); oerr == nil {
				st, serr := f.Stat()
				if serr == nil && st.IsDir() {
					// 7b) 目录 = 文件管理器页面：NAS 账号要 files 读权限
					//     （GET 文件深链不拦，笔记/收藏夹里的下载链接照常能点）。
					if hubRole != "" && !hubusers.Can(hubRole, hubPerms, "files", "r") {
						_ = f.Close()
						writeNoPermPage(w, dispUser, hubRole, hubPerms)
						return
					}
					subs, rerr := f.Readdir(-1)
					_ = f.Close()
					if rerr == nil {
						// 页面只读口径：共享只读之外，NAS 账号缺「网盘」写权限
						// 同样按只读渲染——重命名/删除/上传/新建按钮不生成。
						// （普通共享会话无 hub 角色，不走这个判断，照旧。）
						listRO := readonly || (hubRole != "" && !hubusers.Can(hubRole, hubPerms, "files", "w"))
						writeDirListing(w, r, dispUser, subs, listRO, s.statJSON(root), hubRole, hubPerms)
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

// writeDirListing 渲染浏览器文件管理器（消费级版式，借鉴易等 NAS：
// 左栏「存储空间 + 文件分类」、主区「工具栏 + 列表/缩略图双视图」；
// 上传 / 新建文件夹 / 重命名 / 删除 / 下载，支持拖拽上传与进度条）。
//
// 实现取舍：动态数据（当前路径 + 条目 + 存储统计）以 JSON 塞进页面，DOM 全部
// 由前端 JS 渲染——转义靠 createElement/textContent 天然安全，Go 侧不拼 HTML。
// 增删改直接调标准 WebDAV 方法（PUT/MKCOL/DELETE/MOVE），服务端零新增端点，
// 权限/路径校验/审计全部走既有 guard 管线；只读共享前端隐藏写按钮、后端照样拒绝。
// 文件分类是「当前目录内过滤」（扩展名分桶），全局分类索引后置（P3）。
func writeDirListing(w http.ResponseWriter, r *http.Request, user string, entries []os.FileInfo, readonly bool, stat map[string]any, hubRole string, hubPerms map[string]string) {
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
		"stat": stat,
	})

	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html lang=\"zh-CN\" data-theme=\"" + webui.ThemeAttr() + "\"><head><meta charset=\"utf-8\">")
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	// 标题带品牌：根目录显示「对号 NAS · 网盘」，子目录追加相对路径
	title := "对号 NAS · 网盘"
	if p := strings.Trim(r.URL.Path, "/"); p != "" {
		title += " · /" + p
	}
	b.WriteString(`<title>` + html.EscapeString(title) + `</title>`)
	// 账号块要读 body 上的 data-user / data-role（改密码弹窗用），acct.js 也在这里引
	b.WriteString(`<style>` + davCSS + `</style>` + acctScript + themeScript + winctlScript + `</head>` +
		`<body data-user="` + html.EscapeString(user) + `" data-role="` + html.EscapeString(hubRole) + `">`)
	// 顶层品牌条：与导航/笔记/账本/影视四个页面（layout.html 的 .hub-top）完全同一套结构，
	// 让网盘的「顶部」也落在最外层、横跨整窗，而不是缩进在主区 header 里。
	b.WriteString(`<header class="hub-top">` +
		`<a class="hub-brand" href="/-home"><b>对号</b><span>NAS</span></a>` +
		`<nav class="hub-nav">` + string(webui.NavFor("", hubRole, hubPerms)) + `</nav>` +
		string(webui.AcctFor(user, readonly, hubRole)) +
		`</header>`)
	b.WriteString(`<div class="shell">`)
	b.WriteString(`<aside class="side">` +
		`<div class="scard"><div class="sc-t">存储空间</div>` +
		`<div class="sc-big" id="sc-big">—<small id="sc-total"></small></div>` +
		`<div class="sc-bar"><i id="sc-fill"></i></div>` +
		`<div class="sc-sub" id="sc-sub">统计中…</div>` +
		`<div class="sc-sub" id="sc-sub2"></div></div>` +
		`<div class="sc-t">文件分类</div><nav class="cats" id="cats"></nav></aside>`)
	b.WriteString(`<main class="main"><header>` +
		`<div class="top"><div class="tools">` +
		`<button class="tb primary" id="btn-up">上传</button><button class="tb" id="btn-mk">新建文件夹</button>` +
		`<button class="tb danger" id="btn-batch" hidden></button></div>` +
		`<div class="views">` +
		`<input id="q" class="q" placeholder="搜索（含子目录）">` +
		`<button class="vw" id="v-list" title="列表视图"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M4 6h16M4 12h16M4 18h16"/></svg></button>` +
		`<button class="vw" id="v-grid" title="缩略图视图"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="4" y="4" width="7" height="7" rx="1"/><rect x="13" y="4" width="7" height="7" rx="1"/><rect x="4" y="13" width="7" height="7" rx="1"/><rect x="13" y="13" width="7" height="7" rx="1"/></svg></button></div></div>` +
		`<nav class="crumbs" id="crumbs"></nav>` +
		`<div class="chips" id="chips"></div>` +
		`<div class="prog" id="prog" hidden><div class="bar" id="bar"></div></div></header>`)
	b.WriteString(`<div class="thead" id="thead"><span class="nm th" data-k="n">名称<span class="arr" id="a-n"></span></span><span class="mt th" data-k="m" id="th-m">修改时间<span class="arr" id="a-m"></span></span><span class="sz th" data-k="s">大小<span class="arr" id="a-s"></span></span><span class="op"><input type="checkbox" class="cb" id="all" title="全选/全不选"></span></div>`)
	b.WriteString(`<div class="rows" id="rows"></div><div class="grid" id="grid" hidden></div><div class="empty" id="empty" hidden></div>`)
	b.WriteString(`<footer><span>对号 DUIHAO</span><span id="stat"></span></footer></main>`)
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

// writeNoPermPage 网盘（文件管理器）的「无权限」页：NAS 账号没有 files
// 读权限时不渲染文件列表、也不裸 403，而是给一个与 hub 顶栏一致的页面，
// 明确告诉用户缺什么、找谁开。顶栏导航照常可点（跳回有权限的模块）。
func writeNoPermPage(w http.ResponseWriter, user, role string, perms map[string]string) {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html><html lang=\"zh-CN\" data-theme=\"" + webui.ThemeAttr() + "\"><head><meta charset=\"utf-8\">")
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString(`<title>对号 NAS · 无权限</title>`)
	b.WriteString(`<style>` + davCSS + `</style>` + acctScript + themeScript + winctlScript + `</head>` +
		`<body data-user="` + html.EscapeString(user) + `" data-role="` + html.EscapeString(role) + `">`)
	b.WriteString(`<header class="hub-top">` +
		`<a class="hub-brand" href="/-home"><b>对号</b><span>NAS</span></a>` +
		`<nav class="hub-nav">` + string(webui.NavFor("", role, perms)) + `</nav>` +
		string(webui.AcctFor(user, false, role)) +
		`</header>`)
	b.WriteString(`<div class="noperm"><div class="np-card">` +
		`<div class="np-ico">🔒</div>` +
		`<div class="np-t">无权限</div>` +
		`<div class="np-s">你的账号没有「网盘」模块的访问权限，请联系管理员开通。</div>` +
		`<a class="np-back" href="/-home">返回个人空间</a>` +
		`</div></div></body></html>`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, b.String())
}
// acctScript 顶栏账号下拉的脚本（改密码弹窗）。davCSS 之外单独一份静态文件：
// 模板页（layout.html）也引它，两边共用同一份实现。
const acctScript = `<script src="/-ui/acct.js" defer></script>`

// themeScript NAS 页面的主题跟随（SSE 增量）。首帧的 data-theme 由服务端写死，
// 这个脚本只负责「已经打开的页面在应用里切主题时跟着变」。
const themeScript = `<script src="/-ui/theme.js" defer></script>`

// winctlScript 桌面窗口外壳（右上角三个按钮 + 顶栏拖动）。客户端开的模块窗会
// 在 URL 上带票（_dw=…），脚本据此自绘标题栏按钮；浏览器打开时它直接 return。
const winctlScript = `<script src="/-ui/winctl.js" defer></script>`

const davCSS = `/* 主题变量 —— 与桌面端 frontend/src/style.css 同名概念逐项对齐：
   --bg←--bg-base  --card←--bg-elevated  --tx←--text-primary
   --mut←--text-muted  --hov←--bg-hover  --ln←--border-subtle
   --acc←--accent（A 模式品牌玫红）  --danger←--error
   六档主题全支持（dark / light / system / deep-blue / duihao-red / duihao-light），
   以前只认明暗两档，深蓝/深红落到通用 dark 上，网页和 NAS 主窗就两副面孔。
   :root 兜底 = light（system 在浅色系统下直接继承），深色系逐个覆盖。 */
:root{--bg:#ffffff;--card:#f5f5f5;--tx:#000000;--mut:#5c5c5c;--ln:rgba(0,0,0,.08);--hov:#ebebeb;--acc:#ff4d8d;--danger:#d13438;--br:14px}
[data-theme="dark"]{--bg:#0f0f0f;--card:#1a1a1a;--tx:#ffffff;--mut:#9a9a9a;--ln:rgba(255,255,255,.14);--hov:#303030;--acc:#ff4d8d;--danger:#f87171}
[data-theme="deep-blue"]{--bg:#0b111e;--card:#121c30;--tx:#ffffff;--mut:#8fa1bd;--ln:rgba(255,255,255,.14);--hov:#243256;--acc:#ff4d8d;--danger:#f87171}
[data-theme="duihao-red"]{--bg:#170d13;--card:#20121a;--tx:#ffffff;--mut:#b08a9a;--ln:rgba(255,255,255,.14);--hov:#3a2233;--acc:#ff4d8d;--danger:#ff6b6b}
[data-theme="duihao-light"]{--bg:#fffaf9;--card:#fdf3f2;--tx:#200b10;--mut:#84636c;--ln:rgba(0,0,0,.08);--hov:#f9e9ea;--acc:#dd1553;--danger:#d13438}
@media (prefers-color-scheme:dark){[data-theme="system"]{--bg:#0f0f0f;--card:#1a1a1a;--tx:#ffffff;--mut:#9a9a9a;--ln:rgba(255,255,255,.14);--hov:#303030;--acc:#ff4d8d;--danger:#f87171}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--tx);font-family:system-ui,"Segoe UI","Microsoft YaHei",sans-serif;height:100vh;height:100dvh;overflow:hidden;display:flex;flex-direction:column}
/* 顶层品牌条：与 layout.html 的 .hub-top 同一套（品牌 + 五项导航 + 账号），横跨整窗 */
.hub-top{display:flex;align-items:center;gap:1.2rem;padding:.6rem 1.25rem;background:var(--card);border-bottom:1px solid var(--ln);position:sticky;top:0;z-index:11}
.hub-brand{display:flex;align-items:baseline;gap:.45rem;text-decoration:none;color:var(--tx);white-space:nowrap}
.hub-brand b{font-size:.95rem;font-weight:600;color:var(--acc)}
.hub-brand span{font-size:.8rem;color:var(--mut)}
.hub-nav{display:flex;gap:.2rem;flex:1;min-width:0;overflow-x:auto}
.hub-nav a{padding:.35rem .7rem;border-radius:8px;text-decoration:none;color:var(--tx);font-size:.85rem;white-space:nowrap}
.hub-nav a:hover{background:var(--hov)}
.hub-nav a.on{background:var(--acc);color:#fff}
.hub-user{display:flex;align-items:center;gap:.4rem;font-size:.75rem;color:var(--mut);white-space:nowrap}
/* 顶栏账号：用户名 + 下拉（修改密码 / 退出）。开合靠原生 <details>，弹窗样式在 acct.js */
.hub-acct{position:relative;margin-left:.9rem;flex:0 0 auto}
.hub-acct>summary{list-style:none;cursor:pointer;display:flex;align-items:center;gap:.35rem;padding:.2rem .5rem;border:1px solid transparent;border-radius:8px;color:var(--mut);white-space:nowrap}
.hub-acct>summary::-webkit-details-marker{display:none}
.hub-acct>summary::marker{content:""}
.hub-acct>summary:hover,.hub-acct[open]>summary{color:var(--tx);border-color:var(--ln);background:var(--hov)}
.hub-acct .ro{font-size:.64rem;line-height:1.5;padding:0 .28rem;margin-left:.3rem;border:1px solid currentColor;border-radius:5px;opacity:.85}
.hub-acct .ro[hidden]{display:none}
.hub-acct-caret{flex:0 0 auto;transition:transform .15s}
.hub-acct[open] .hub-acct-caret{transform:rotate(180deg)}
.hub-acct-menu{position:absolute;right:0;top:calc(100% + .35rem);min-width:8.4rem;z-index:40;display:flex;flex-direction:column;padding:.25rem;background:var(--card);border:1px solid var(--ln);border-radius:10px;box-shadow:0 12px 30px rgba(0,0,0,.22)}
.hub-acct-item{display:block;width:100%;text-align:left;padding:.42rem .6rem;border:0;background:none;border-radius:7px;font:inherit;font-size:.8rem;color:var(--tx);text-decoration:none;cursor:pointer;white-space:nowrap}
.hub-acct-item:hover{background:var(--hov);color:var(--tx)}
.hub-acct-item.danger{color:var(--danger)}
/* 整窗应用布局：顶栏固定 → 左栏固定 → 只有右侧主区的列表区滚动 */
.shell{display:flex;flex:1;min-height:0;overflow:hidden}
.side{flex:0 0 13.5rem;padding:1rem .85rem;display:flex;flex-direction:column;overflow-y:auto;min-height:0}
.main{flex:1;min-width:0;min-height:0;display:flex;flex-direction:column;overflow:hidden}
.main header{flex:0 0 auto}
.main header{padding:.9rem 1.25rem .8rem;border-bottom:1px solid var(--ln)}
.top{display:flex;justify-content:space-between;align-items:center;gap:.75rem;margin-bottom:.55rem;flex-wrap:wrap}
.scard{border:1px solid var(--ln);border-radius:10px;padding:.7rem .8rem;margin:.8rem 0 .3rem}
.sc-big{font-size:1.12rem;font-weight:600;margin-top:.1rem}
.sc-big small{font-size:.72rem;color:var(--mut);font-weight:400;margin-left:.2rem}
.sc-bar{height:4px;border-radius:99px;background:var(--ln);margin:.5rem 0 .35rem;overflow:hidden}
.sc-bar i{display:block;height:100%;width:0;background:var(--acc);border-radius:99px;transition:width .3s}
.sc-sub{font-size:.7rem;color:var(--mut);line-height:1.45;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.sc-t{font-size:.72rem;color:var(--mut);letter-spacing:.05em;margin:.7rem .2rem .35rem}
.cats{display:flex;flex-direction:column;gap:2px}
.cat{display:flex;align-items:center;gap:.5rem;padding:.42rem .55rem;border-radius:8px;font-size:.82rem;color:var(--tx);cursor:pointer;border:none;background:none;text-align:left;width:100%}
.cat:hover{background:var(--hov)}
.cat.on{background:var(--acc);color:#fff}
.cat .dot{width:9px;height:9px;border-radius:3px;flex:0 0 9px}
.cat .n{margin-left:auto;font-size:.72rem;color:var(--mut)}
.cat.on .n{color:rgba(255,255,255,.85)}
.views{display:flex;gap:.35rem;align-items:center}
.vw{border:1px solid var(--ln);background:var(--card);color:var(--mut);width:1.95rem;height:1.75rem;border-radius:8px;cursor:pointer;display:inline-flex;align-items:center;justify-content:center;padding:0}
.vw:hover{color:var(--acc);border-color:var(--acc)}
.vw.on{background:var(--acc);border-color:var(--acc);color:#fff}
.vw.on:hover{color:#fff}
.chips{display:none;gap:.4rem;overflow-x:auto;padding-top:.55rem}
.chip{flex:0 0 auto;border:1px solid var(--ln);border-radius:99px;padding:.25rem .7rem;font-size:.75rem;color:var(--tx);background:var(--card);cursor:pointer;white-space:nowrap}
.chip.on{background:var(--acc);border-color:var(--acc);color:#fff}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(7.2rem,1fr));gap:.6rem;padding:1rem 1.25rem;flex:1;min-height:0;overflow-y:auto;align-content:start}
.tile{display:flex;flex-direction:column;align-items:center;gap:.4rem;padding:.8rem .4rem .6rem;border-radius:10px;border:1px solid var(--ln);background:var(--card);text-decoration:none;color:var(--tx)}
.tile:hover{border-color:var(--acc)}
.tile .tic{width:2.7rem;height:2.7rem;display:flex;align-items:center;justify-content:center;border-radius:8px}
.tile .tic svg{width:27px;height:27px}
.tile .tnm{font-size:.78rem;max-width:100%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;text-align:center}
.tile .tacts{display:flex;gap:.15rem;opacity:0;transition:opacity .12s}
.tile:hover .tacts{opacity:1}
#rows[hidden],.grid[hidden]{display:none}
.rows{flex:1;min-height:0;overflow-y:auto}
.ro{margin-left:.2rem;padding:.1rem .45rem;border:1px solid var(--acc);color:var(--acc);border-radius:99px;font-size:.7rem}
.tools{display:flex;gap:.5rem;align-items:center;flex-wrap:wrap}
.q{border:1px solid var(--ln);background:var(--card);color:var(--tx);padding:.35rem .6rem;border-radius:8px;font-size:.8rem;outline:none;width:10rem}
.q:focus{border-color:var(--acc)}
.tb{border:1px solid var(--ln);background:var(--card);color:var(--tx);padding:.35rem .8rem;border-radius:8px;font-size:.8rem;cursor:pointer;white-space:nowrap}
.tb:hover{border-color:var(--acc);color:var(--acc)}
.tb.primary{background:var(--acc);border-color:var(--acc);color:#fff}
.tb.primary:hover{opacity:.88;color:#fff}
.tb.danger{border-color:var(--danger);color:var(--danger)}
.tb.danger:hover{background:var(--danger);color:#fff}
.crumbs{display:flex;flex-wrap:wrap;align-items:center;gap:.15rem;font-size:1rem;font-weight:600}
.crumbs a{color:var(--mut);text-decoration:none;font-weight:500;padding:.1rem .3rem;border-radius:6px;display:inline-flex;align-items:center;gap:.25rem}
.crumbs a:hover{color:var(--acc);background:var(--hov)}
.crumbs a.cur{color:var(--tx);font-weight:700}
.crumbs a.cur:hover{color:var(--tx);background:none}
.crumbs .sep{color:var(--mut);opacity:.5}
.prog{height:4px;border-radius:99px;background:var(--ln);margin-top:.7rem;overflow:hidden}
.bar{height:100%;width:0;background:var(--acc);transition:width .15s}
.thead{display:flex;flex:0 0 auto;gap:.75rem;padding:.5rem 1.5rem;font-size:.75rem;color:var(--mut);border-bottom:1px solid var(--ln);letter-spacing:.05em}
.thead .nm{flex:1;padding-left:2.75rem}.thead .sz{flex:0 0 5.5rem;text-align:right}.thead .op{flex:0 0 7rem;display:flex;justify-content:flex-end;align-items:center}.thead .mt{flex:0 0 9.5rem}
.th{cursor:pointer;user-select:none;display:inline-flex;align-items:center;gap:.2rem}
.th:hover{color:var(--acc)}
.th .arr{font-size:.7rem;min-width:.7em;color:var(--acc)}
.row{display:flex;align-items:center;gap:.75rem;padding:.55rem 1.5rem;text-decoration:none;color:var(--tx);border-bottom:1px solid var(--ln)}
.row:hover{background:var(--hov)}
.row.sel{background:rgba(240,36,126,.08)}
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
footer{flex:0 0 auto;padding:.9rem 1.5rem;color:var(--mut);font-size:.78rem;display:flex;justify-content:space-between;gap:1rem}
.drop{position:absolute;inset:0;display:none;align-items:center;justify-content:center;background:rgba(240,36,126,.12);border:2px dashed var(--acc);border-radius:var(--br);font-weight:600;color:var(--acc);z-index:5}
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
/* 无权限页（writeNoPermPage）：顶栏之下居中一张提示卡 */
.noperm{flex:1;display:flex;align-items:center;justify-content:center;padding:1rem}
.np-card{text-align:center;padding:2.6rem 3rem;border:1px solid var(--ln);border-radius:14px;background:var(--card)}
.np-ico{font-size:2.1rem}
.np-t{font-size:1.15rem;font-weight:600;margin:.55rem 0 .3rem}
.np-s{font-size:.85rem;color:var(--mut)}
.np-back{display:inline-block;margin-top:1.1rem;font-size:.85rem;color:var(--acc);text-decoration:none}
.np-back:hover{text-decoration:underline}
@media (max-width:46rem){
 .side{display:none}
 .chips{display:flex}
 /* 顶栏两行：与 webui/shared.css 末尾同一套 —— 手机上一行塞不下「品牌+账号+11 项导航」 */
 .hub-top{flex-wrap:wrap;gap:.5rem .6rem;padding:.5rem .8rem}
 .hub-brand{flex:0 0 auto}
 .hub-acct{order:1;margin-left:auto}
 .hub-nav{order:2;flex:0 1 auto;width:100%;min-width:0;gap:.15rem;padding:0;scrollbar-width:none}
 .hub-nav::-webkit-scrollbar{height:0}
 .hub-nav a{padding:.3rem .55rem;font-size:.8rem}
 .hub-acct>summary{padding:.18rem .4rem}
 .hub-user{font-size:.72rem}
 .grid{grid-template-columns:repeat(auto-fill,minmax(5.4rem,1fr))}
 .row .mt,.thead .mt{display:none}
 .row .sz,.thead .sz{flex-basis:4rem}
 .acts,.thead .op{flex-basis:6.2rem}
 .crumbs{font-size:.9rem}
 .q{flex:1 1 6rem;width:auto}
}`

// davJS 文件管理器交互：渲染条目 / 上传（XHR 进度 + 拖拽）/ 新建 / 重命名 / 删除。
// 写操作直接调标准 WebDAV 方法（PUT/MKCOL/DELETE/MOVE）；401 交给浏览器重新弹认证。
// 注意：本常量是 Go 原生字符串，JS 里的正则 \/ 原样透传（匹配斜杠必须转义）。
const davJS = `(function(){
var D=JSON.parse(document.getElementById('davdata').textContent);
var STAT=D.stat||null;
/* 文件分类（当前目录内按扩展名分桶；全局索引后置） */
var CATS=[{k:'all',t:'全部',c:''},{k:'doc',t:'文档',c:'#3b82f6'},{k:'img',t:'图片',c:'#0ea5e9'},{k:'vid',t:'视频',c:'#8b5cf6'},{k:'aud',t:'音频',c:'#10b981'},{k:'arch',t:'压缩包',c:'#f0247e'},{k:'other',t:'其它',c:'#64748b'}];
var catK='all';
var view=(function(){var q=new URLSearchParams(location.search).get('view');
 if(q==='grid'||q==='list')return q;return localStorage.getItem('dav-view')||'list'})();
function catOf(n){var k=kindOf(n);return (k==='code'||k==='file')?'other':k}
function gb(n){if(n==null||n<0)return null;var g=n/1073741824;if(g>=1024)return (g/1024).toFixed(2)+' TB';return g>=10?(Math.round(g*10)/10)+' GB':g.toFixed(1)+' GB'}
var $=function(id){return document.getElementById(id)};
var DIR='<svg width="18" height="18" viewBox="0 0 24 24"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z" fill="#f0247e" opacity=".9"/></svg>';
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
var I_ARCHIVE='<svg width="18" height="18" viewBox="0 0 24 24" fill="none"><rect x="4" y="3" width="16" height="18" rx="2" stroke="#f0247e" stroke-width="1.7"/><path d="M11 3v3h2V3M11 8v2h2V8M11 12v2h2v-2" stroke="#f0247e" stroke-width="1.7"/><path d="M10 17h4" stroke="#f0247e" stroke-width="1.7" stroke-linecap="round"/></svg>';
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
 var l=D.list.filter(function(e){
  /* 分类筛选只滤文件，子目录始终保留 —— 逐级点进去才能看到各分类的文件 */
  if(catK!=='all'&&!e.d&&catOf(e.n)!==catK)return false;
  return !q||e.n.toLowerCase().indexOf(q)>=0;
 });
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
 /* 搜索结果里 e.p = 所在目录；常规列表 e.p 不存在，仍显示修改时间 */
 a.appendChild(el('span','mt',e.p||e.m||''));
 a.appendChild(el('span','sz',e.d?'':sz(e.s)));
 if(e.p)a.title=(e.p==='/'?'':e.p)+'/'+e.n;
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
/* ── 左栏：存储卡 + 分类（桌面侧栏与移动端 chips 共用一套计数）── */
function renderSide(){
 var big=$('sc-big'),tot=$('sc-total'),fill=$('sc-fill'),sub=$('sc-sub'),sub2=$('sc-sub2');
 if(STAT&&STAT.total){
  /* 大数字 = 磁盘已用（总量-剩余），与进度条同源；网盘目录占用只作副行参考，
     两者混用会出现「已用 0.0 GB / 320 GB 但剩余 147 GB」的自相矛盾。 */
  var diskUsed=STAT.total-STAT.free;
  var pct=STAT.total>0?Math.min(100,Math.round(diskUsed/STAT.total*100)):0;
  big.firstChild.nodeValue=gb(diskUsed)||'—';
  tot.textContent='/ '+gb(STAT.total);
  fill.style.width=pct+'%';
  sub.textContent='剩余 '+gb(STAT.free)+' · 已用 '+pct+'%';
  sub2.textContent='网盘占用 '+(STAT.used>=0?gb(STAT.used):'统计中…');
 }else{
  big.firstChild.nodeValue='—';tot.textContent='';fill.style.width='0';sub.textContent='存储信息不可用';sub2.textContent='';
 }
 var counts={all:D.list.length,doc:0,img:0,vid:0,aud:0,arch:0,other:0};
 D.list.forEach(function(e){if(!e.d)counts[catOf(e.n)]++});
 var box=$('cats');box.innerHTML='';
 CATS.forEach(function(c){
  var b=el('button','cat'+(catK===c.k?' on':''));
  var d=el('span','dot');d.style.background=c.c||'var(--acc)';
  b.appendChild(d);b.appendChild(el('span',null,c.t));
  b.appendChild(el('span','n',String(counts[c.k]||0)));
  b.onclick=function(){catK=c.k;if(SEARCH){SEARCH=null;TRUNC=false;$('q').value=''}renderSide();render()};
  box.appendChild(b);
 });
 var chips=$('chips');chips.innerHTML='';
 CATS.forEach(function(c){
  var s=el('button','chip'+(catK===c.k?' on':''),c.t+' ('+(counts[c.k]||0)+')');
  s.onclick=function(){catK=c.k;if(SEARCH){SEARCH=null;TRUNC=false;$('q').value=''}renderSide();render()};
  chips.appendChild(s);
 });
}
function tile(e){
 var a=el('a','tile');a.href=e.h;
 if(e.p)a.title=(e.p==='/'?'':e.p)+'/'+e.n;
 var ic=el('span','tic');ic.innerHTML=iconFor(e.n,e.d);a.appendChild(ic);
 a.appendChild(el('span','tnm',e.n));
 var acts=el('span','tacts');
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
 crumbs();renderSide();
 var isGrid=view==='grid';
 $('rows').hidden=isGrid;$('grid').hidden=!isGrid;
 $('thead').style.display=isGrid?'none':'';
 /* 递归搜索结果接管列表（不再套分类/排序；.mt 列改显所在目录） */
 $('th-m').firstChild.nodeValue=SEARCH?'所在位置':'修改时间';
 var items=SEARCH||sortedList(),nd=0,nf=0;
 if(isGrid){var g=$('grid');g.innerHTML='';items.forEach(function(e){g.appendChild(tile(e));e.d?nd++:nf++})}
 else{var box=$('rows');box.innerHTML='';items.forEach(function(e){box.appendChild(row(e));e.d?nd++:nf++})}
 var emp=$('empty');
 if(items.length){emp.hidden=true}
 else{emp.hidden=false;emp.textContent=SEARCH?('没有匹配「'+$('q').value.trim()+'」的文件'):(catK!=='all')?'该分类下暂无文件':(D.w?'此目录为空，拖拽文件进来即可上传':'此目录为空')}
 $('stat').textContent=SEARCH
  ?(nd+' 个目录 · '+nf+' 个文件'+(TRUNC?' · 已达上限，请缩小关键词':''))
  :nd+' 个目录 · '+nf+' 个文件';
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
/* 表头点击排序 */
[].forEach.call(document.querySelectorAll('.th'),function(t){
 t.onclick=function(){var k=t.getAttribute('data-k');if(sk===k){sd=-sd}else{sk=k;sd=1}render()};
});
/* 搜索：空关键词 = 当前目录即时过滤；有关键词 = 防抖 300ms 后走 /-search 递归
   （文件名匹配、深度 10、上限 1000，见 search.go），结果里 .mt 列显示所在目录 */
var SEARCH=null,TRUNC=false,qTimer=null,qSeq=0;
$('q').oninput=function(){
 var self=this;
 clearTimeout(qTimer);
 qTimer=setTimeout(function(){doSearch(self.value)},300);
};
async function doSearch(raw){
 var q=raw.trim(),seq=++qSeq;
 if(!q){SEARCH=null;TRUNC=false;render();return}
 try{
  var r=await fetch('/-search?q='+encodeURIComponent(q)+'&dir='+encodeURIComponent(D.cur));
  if(gone(r))return;
  var j=await r.json();
  if(seq!==qSeq)return; // 过期响应丢弃（输入还在继续）
  SEARCH=j.items||[];TRUNC=!!j.truncated;
  render();
 }catch(e){toast('搜索失败',true)}
}
/* 全选 / 批量删除 */
$('all').onchange=function(){
 var v=this.checked;
 (SEARCH||sortedList()).forEach(function(e){if(D.w)sel[e.h]=v});
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
/* 视图切换（记忆到 localStorage） */
function setView(v){view=v;localStorage.setItem('dav-view',v);
 $('v-list').className='vw'+(v==='list'?' on':'');$('v-grid').className='vw'+(v==='grid'?' on':'');render()}
$('v-list').onclick=function(){setView('list')};
$('v-grid').onclick=function(){setView('grid')};
setView(view);
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
		AllowPlain:  get("dav.plain", "1") == "1",
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
