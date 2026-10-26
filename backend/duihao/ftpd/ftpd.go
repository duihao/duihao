// Package ftpd 内置 FTP 服务（embedded 类插件，MIT：github.com/fclairamb/ftpserverlib）。
//
// 为什么内置而不下载 FileZilla Server：后者是 GPLv2，随包分发有许可传染风险；
// ftpserverlib 是 MIT，可直接编译进对号本体，用户零安装。
//
// 边界：本包只认「一份配置 + 一个根目录」，不认实例/站点（那是上层的事）；
// 账号密码由实例设置传入，本包不做任何持久化。
package ftpd

import (
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	ftpserverlib "github.com/fclairamb/ftpserverlib"
	"github.com/spf13/afero"
	"golang.org/x/crypto/bcrypt"
)

// Config 一次启动所需的全部参数（由实例设置 + 端口渲染而来）。
type Config struct {
	Bind    string // 127.0.0.1 / 0.0.0.0
	Port    int    // 控制连接端口
	Root    string // 根目录（绝对路径）
	User    string
	Pass    string
	ReadOnly bool

	// Accounts 多账号表（来自 ftpaccounts.json）。非空时优先于 User/Pass。
	// 每个账号可覆盖根目录与只读位，ReadOnly 仍是全局上限（OR 关系）。
	Accounts []Account

	Welcome     string
	IdleTimeout int // 秒
	PasvStart   int
	PasvEnd     int
	PublicHost  string // PASV 回包里的公网 IP（NAT 后用）
	DisableActive bool
	DisableMLSD   bool

	LogPath string

	// InstanceDir 实例数据目录（账号根目录里的 {dir} token 用它展开）。
	InstanceDir string
}

// Server 一个 FTP 服务实例（一实例一 Server）。
type Server struct {
	cfg Config

	mu      sync.Mutex
	srv     *ftpserverlib.FtpServer
	ln      net.Listener
	logFile *os.File
	running bool
	lastErr string
	// accounts 热更新用副本（改账号不必重启服务，已连接的会话保留旧权限直到断开）。
	accounts []Account
}

// New 构造（不启动）。
func New(cfg Config) (*Server, error) {
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("端口 %d 非法", cfg.Port)
	}
	if strings.TrimSpace(cfg.User) == "" {
		return nil, fmt.Errorf("用户名不能为空")
	}
	if len(cfg.Accounts) == 0 {
		if cfg.Pass == "" {
			return nil, fmt.Errorf("密码不能为空——匿名 FTP 会把根目录暴露给同网段任何人")
		}
	} else {
		enabled := 0
		for i := range cfg.Accounts {
			if err := cfg.Accounts[i].Validate(); err != nil {
				return nil, fmt.Errorf("账号 %s: %w", cfg.Accounts[i].User, err)
			}
			if cfg.Accounts[i].Enabled {
				enabled++
			}
		}
		if enabled == 0 {
			return nil, fmt.Errorf("至少需要一个启用的 FTP 账号")
		}
	}
	if cfg.Bind == "" {
		cfg.Bind = "127.0.0.1"
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 900
	}
	return &Server{cfg: cfg, accounts: cfg.Accounts}, nil
}

// SetAccounts 热更新账号表：下一次登录即生效，不打断现有连接。
func (s *Server) SetAccounts(list []Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts = append([]Account(nil), list...)
}

// Accounts 当前账号表快照。
func (s *Server) Accounts() []Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Account(nil), s.accounts...)
}

// Start 绑定端口并开始接受连接（同步返回绑定错误，异步 serve）。
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return fmt.Errorf("FTP 服务已在运行")
	}

	root := filepath.Clean(s.cfg.Root)
	if root == "" || root == "." {
		return fmt.Errorf("根目录未配置")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("创建根目录失败: %w", err)
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return fmt.Errorf("根目录不可用: %s", root)
	}

	// 每个账号的根目录先建好：删掉目录后服务仍能启动，登录时不至于 550 到不可用路径。
	for _, acc := range s.accounts {
		if !acc.Enabled {
			continue
		}
		r := expandRoot(acc.Root, s.cfg.InstanceDir)
		if r == "" {
			continue
		}
		if err := os.MkdirAll(r, 0o755); err != nil {
			return fmt.Errorf("账号 %s 的根目录不可用（%s）: %w", acc.User, r, err)
		}
	}

	addr := net.JoinHostPort(s.cfg.Bind, fmt.Sprint(s.cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败（端口被占用或无权限）: %w", addr, err)
	}

	lf, logErr := openLog(s.cfg.LogPath)
	logger := slog.New(slog.NewTextHandler(logWriter(lf), &slog.HandlerOptions{Level: slog.LevelInfo}))
	_ = logErr

	settings := &ftpserverlib.Settings{
		Listener:                 ln,
		ListenAddr:               addr,
		Banner:                   firstNonEmpty(s.cfg.Welcome, "对号 FTP 服务"),
		PublicHost:               s.cfg.PublicHost,
		IdleTimeout:              s.cfg.IdleTimeout,
		ConnectionTimeout:        30,
		DefaultTransferType:      ftpserverlib.TransferTypeBinary,
		DisableActiveMode:        s.cfg.DisableActive,
		DisableMLSD:              s.cfg.DisableMLSD,
		ActiveTransferPortNon20:  true,
		TLSRequired:              ftpserverlib.ClearOrEncrypted,
	}
	if s.cfg.PasvStart > 0 && s.cfg.PasvEnd >= s.cfg.PasvStart {
		// 被动模式端口区间：不配的话客户端（FileZilla 默认走 PASV）连不上数据通道。
		settings.PassiveTransferPortRange = &ftpserverlib.PortRange{
			Start: s.cfg.PasvStart, End: s.cfg.PasvEnd,
		}
	}

	d := &driver{srv: s, cfg: s.cfg, root: root, settings: settings, logger: logger}
	srv := ftpserverlib.NewFtpServer(d)
	srv.Logger = logger
	// Listen 会调 GetSettings 并把 settings.Listener 装进 server（Serve 依赖它）。
	if err := srv.Listen(); err != nil {
		_ = ln.Close()
		if lf != nil {
			_ = lf.Close()
		}
		return fmt.Errorf("FTP 服务初始化失败: %w", err)
	}

	s.srv = srv
	s.ln = ln
	s.logFile = lf
	s.running = true
	logger.Info("FTP 已启动", "addr", addr, "root", root, "readonly", s.cfg.ReadOnly)

	go func() {
		err := srv.Serve()
		s.mu.Lock()
		s.running = false
		if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
			s.lastErr = err.Error()
			logger.Error("FTP 服务异常退出", "err", err)
		}
		s.mu.Unlock()
	}()
	return nil
}

// Stop 关闭监听（幂等）。
func (s *Server) Stop() error {
	s.mu.Lock()
	srv, lf := s.srv, s.logFile
	s.srv, s.ln, s.logFile, s.running = nil, nil, nil, false
	s.mu.Unlock()

	var err error
	if srv != nil {
		if e := srv.Stop(); e != nil && !errors.Is(e, net.ErrClosed) && !errors.Is(e, ftpserverlib.ErrNotListening) {
			err = e
		}
	}
	if lf != nil {
		_ = lf.Close()
	}
	return err
}

// Running 是否在监听。
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Port 当前控制端口。
func (s *Server) Port() int { return s.cfg.Port }

// Addr 实际监听地址（:0 分配时用于回读）。
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

// ─── ftpserverlib 驱动 ───

type driver struct {
	srv      *Server
	cfg      Config
	root     string
	settings *ftpserverlib.Settings
	logger   *slog.Logger
}

func (d *driver) GetSettings() (*ftpserverlib.Settings, error) { return d.settings, nil }

func (d *driver) ClientConnected(cc ftpserverlib.ClientContext) (string, error) {
	d.logger.Info("客户端接入", "remote", cc.RemoteAddr().String())
	return d.settings.Banner, nil
}

func (d *driver) ClientDisconnected(cc ftpserverlib.ClientContext) {
	d.logger.Info("客户端断开", "remote", cc.RemoteAddr().String())
}

// AuthUser 校验账号密码；失败返回错误 → 客户端收到 530。
//
// 多账号流程：先按用户名精确匹配（只对匹配项算 bcrypt，避免 N 个账号各算一遍
// 拖慢登录），没匹配到也跑一次 dummy bcrypt，让「用户名是否存在」不体现在响应时间上。
// 密码用 bcrypt 常量时间比较；账号表为空时回退设置里的单账号（旧版兼容）。
func (d *driver) AuthUser(cc ftpserverlib.ClientContext, user, pass string) (ftpserverlib.ClientDriver, error) {
	accs := d.srv.Accounts()

	var matched *Account
	for i := range accs {
		if !accs[i].Enabled {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(user), []byte(accs[i].User)) == 1 {
			matched = &accs[i]
			break
		}
	}

	if len(accs) == 0 {
		okUser := subtle.ConstantTimeCompare([]byte(user), []byte(d.cfg.User)) == 1
		okPass := subtle.ConstantTimeCompare([]byte(pass), []byte(d.cfg.Pass)) == 1
		if !okUser || !okPass {
			d.logger.Warn("登录失败", "remote", cc.RemoteAddr().String(), "user", user)
			return nil, fmt.Errorf("账号或密码错误")
		}
		d.logger.Info("登录成功", "user", user, "readonly", d.cfg.ReadOnly, "legacy", true)
		return d.clientFor(d.root, d.cfg.ReadOnly)
	}

	if matched == nil {
		// 时序对齐：真实账号校验一次 bcrypt 约 50ms，这里也花掉同样的时间。
		_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(pass))
		d.logger.Warn("登录失败（账号不存在）", "remote", cc.RemoteAddr().String(), "user", user)
		return nil, fmt.Errorf("账号或密码错误")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(matched.PassHash), []byte(pass)); err != nil {
		d.logger.Warn("登录失败（密码错误）", "remote", cc.RemoteAddr().String(), "user", user)
		return nil, fmt.Errorf("账号或密码错误")
	}

	root := d.root
	if r := expandRoot(matched.Root, d.cfg.InstanceDir); r != "" {
		root = r
	}
	readonly := matched.ReadOnly || d.cfg.ReadOnly
	d.logger.Info("登录成功", "user", user, "root", root, "readonly", readonly)
	return d.clientFor(root, readonly)
}

// clientFor 按根目录与只读位构造客户端文件系统（BasePathFs 把访问锁在根内）。
func (d *driver) clientFor(root string, readonly bool) (ftpserverlib.ClientDriver, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("根目录不可用: %w", err)
	}
	base := afero.NewBasePathFs(afero.NewOsFs(), root)
	var fs afero.Fs = base
	if readonly {
		fs = afero.NewReadOnlyFs(base)
	}
	return &clientDriver{Fs: fs}, nil
}

// dummyHash 一个真实的 bcrypt 哈希，仅用于「账号不存在」时把耗时对齐到真校验。
var dummyHash = func() string {
	h, err := bcrypt.GenerateFromPassword([]byte("duihao-timing-equalizer"), bcrypt.DefaultCost)
	if err != nil {
		return "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
	}
	return string(h)
}()

// GetTLSConfig 不启用 TLS（v1 只服务本机/局域网，默认绑 127.0.0.1）。
func (d *driver) GetTLSConfig() (*tls.Config, error) { return nil, nil }

// clientDriver 客户端文件系统（afero.Fs 即满足 ClientDriver）。
type clientDriver struct {
	afero.Fs
}

// ─── 小工具 ───

func openLog(path string) (*os.File, error) {
	if path == "" {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
}

func logWriter(f *os.File) io.Writer {
	if f == nil {
		return io.Discard
	}
	return f
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

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
	root := filepath.FromSlash(strings.ReplaceAll(get("ftp.root", ""), "{dir}", dir))
	if root == "" {
		root = filepath.Join(dir, "www")
	}
	// 账号表优先；没有账号表时用设置里的单账号（旧安装升级上来的形态）。
	accounts, accErr := LoadAccounts(dir)
	if accErr != nil {
		return nil, accErr
	}
	pass := values["ftp.password"]
	cfg := Config{
		Bind:          get("ftp.bind", "127.0.0.1"),
		Port:          ports["ftp"],
		Root:          root,
		User:          get("ftp.user", "duihao"),
		Pass:          pass,
		ReadOnly:      get("ftp.readonly", "0") == "1",
		Accounts:      accounts,
		InstanceDir:   dir,
		Welcome:       get("ftp.welcome", "对号 FTP 服务"),
		IdleTimeout:   atoi("ftp.idle", 900),
		PasvStart:     atoi("ftp.pasv_start", 2130),
		PasvEnd:       atoi("ftp.pasv_end", 2190),
		PublicHost:    get("ftp.public_host", ""),
		DisableActive: get("ftp.disable_active", "1") == "1",
		DisableMLSD:   get("ftp.disable_mlsd", "0") == "1",
		LogPath:       firstNonEmpty(logPath, filepath.Join(dir, "logs", "ftpserver.log")),
	}
	return New(cfg)
}
