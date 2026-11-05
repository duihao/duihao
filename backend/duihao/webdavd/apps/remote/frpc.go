package remote

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/duihao/duihao/backend/duihao"
	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
	"github.com/duihao/duihao/backend/duihao/webdavd/childproc"
	"github.com/duihao/duihao/backend/duihao/webdavd/compkit"
)

// frp 内网穿透：frpc 进程连用户自己的 frps。
//
// 口径：
//   - **可视化配置**：页面上填服务器 + 加隧道，对号生成 frpc.toml 落到
//     config/remote/frpc.toml —— 用户永远不手写配置文件；
//   - **对号死它必死**：childproc（CREATE_NO_WINDOW + Job Object）兜底，
//     不留孤儿进程占着远端端口；
//   - transport.tls.enable 默认开：frpc↔frps 隧道加密，对号本机可以纯 HTTP；
//   - localPort 默认指到本机网盘端口（webdavd 注入 SetLocalService）；
//   - 令牌与 DNS 令牌同一套规矩：密文入库、接口只回 tokenSet。

// frpServer frps 服务器。
type frpServer struct {
	Addr  string `json:"addr"`
	Port  int    `json:"port"`
	User  string `json:"user"`
	Token string `json:"-"` // 令牌不进这份 JSON（页面只看 tokenSet）
}

// frpProxy 一条隧道。
type frpProxy struct {
	Name         string `json:"name"`
	Type         string `json:"type"` // tcp | http | https
	LocalIP      string `json:"localIP"`
	LocalPort    int    `json:"localPort"`
	RemotePort   int    `json:"remotePort"`   // tcp 专用：frps 上开的对外端口
	CustomDomain string `json:"customDomain"` // http/https 专用：访问域名（逗号可多个）
}

// FrpConfig 可视化配置的完整模型（前端直接渲染这份 JSON）。
type FrpConfig struct {
	Enabled   bool       `json:"enabled"`
	Server    frpServer  `json:"server"`
	TLSEnable bool       `json:"tlsEnable"`
	Proxies   []frpProxy `json:"proxies"`
}

const frpcTomlName = "frpc.toml"
const frpcLogName = "frpc.log"

var (
	frpMu       sync.Mutex
	frpProc     *childproc.Proc
	frpStartErr string
	localPort   int // 本机网盘端口（webdavd 注入），给「自动指向网盘」用
)

// SetLocalService webdavd 启动时注入自己的端口，隧道默认往这指。
func SetLocalService(port int) {
	frpMu.Lock()
	localPort = port
	frpMu.Unlock()
}

// ---------- 配置存取 ----------

// loadFrp 读配置。令牌存独立键（密文），JSON 里不带。
func loadFrp(db *sql.DB, uid string) FrpConfig {
	cfg := defaultFrp()
	if raw := getKV(db, uid, "frpc.config"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			Logf("remote: frpc 配置解析失败（用默认）: %v", err)
			cfg = defaultFrp()
		}
	}
	if cfg.Server.Port <= 0 {
		cfg.Server.Port = 7000
	}
	if cfg.Proxies == nil {
		cfg.Proxies = []frpProxy{}
	}
	return cfg
}

func defaultFrp() FrpConfig {
	return FrpConfig{TLSEnable: true, Server: frpServer{Port: 7000}, Proxies: []frpProxy{}}
}

func frpToken(db *sql.DB, uid string) string {
	return openToken(getKV(db, uid, "frpc.token"))
}

// frpView 页面要的 frpc 状态块。
func frpView(c appkit.Context) map[string]any {
	cfg := loadFrp(c.DB, c.User)
	return map[string]any{
		"enabled":  cfg.Enabled,
		"server":   map[string]any{"addr": cfg.Server.Addr, "port": cfg.Server.Port, "user": cfg.Server.User, "tokenSet": frpToken(c.DB, c.User) != ""},
		"tls":      cfg.TLSEnable,
		"proxies":  cfg.Proxies,
		"running":  frpRunning(),
		"startErr": frpStartErrSnapshot(),
		"local":    localSnapshot(),
		"comp":     compStatus(),
	}
}

func frpStartErrSnapshot() string {
	frpMu.Lock()
	defer frpMu.Unlock()
	return frpStartErr
}

func localSnapshot() int {
	frpMu.Lock()
	defer frpMu.Unlock()
	return localPort
}

// ---------- TOML 生成（可视化配置 → frpc.toml） ----------

// genTOML 生成 frpc 配置（frp ≥0.52 的 TOML 格式）。
// 校验与生成一体：结构不对直接报人话错误，页面在保存时就能拦住。
func genTOML(cfg FrpConfig, token string) (string, error) {
	if strings.TrimSpace(cfg.Server.Addr) == "" {
		return "", fmt.Errorf("服务器地址不能为空（frps 的 IP 或域名）")
	}
	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		return "", fmt.Errorf("服务器端口要在 1~65535 之间")
	}
	if len(cfg.Proxies) == 0 {
		return "", fmt.Errorf("至少加一条隧道，不然连上来什么也转发不了")
	}
	seen := map[string]bool{}
	for i, p := range cfg.Proxies {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			return "", fmt.Errorf("第 %d 条隧道没起名字", i+1)
		}
		if seen[name] {
			return "", fmt.Errorf("隧道名字「%s」重复了，frp 不允许", name)
		}
		seen[name] = true
		switch p.Type {
		case "tcp":
			if p.RemotePort <= 0 || p.RemotePort > 65535 {
				return "", fmt.Errorf("隧道「%s」要填 frps 上的对外端口", name)
			}
		case "http", "https":
			if strings.TrimSpace(p.CustomDomain) == "" {
				return "", fmt.Errorf("隧道「%s」是 %s 类型，要填访问域名", name, p.Type)
			}
		default:
			return "", fmt.Errorf("隧道「%s」类型只支持 tcp / http / https", name)
		}
		if p.LocalPort <= 0 || p.LocalPort > 65535 {
			return "", fmt.Errorf("隧道「%s」的本地端口不对", name)
		}
	}

	var b strings.Builder
	b.WriteString("# 由对号「远程」页生成 —— 手改会被下次保存覆盖\n")
	fmt.Fprintf(&b, "serverAddr = %q\n", strings.TrimSpace(cfg.Server.Addr))
	fmt.Fprintf(&b, "serverPort = %d\n", cfg.Server.Port)
	if cfg.Server.User != "" {
		fmt.Fprintf(&b, "user = %q\n", cfg.Server.User)
	}
	if token != "" {
		fmt.Fprintf(&b, "auth.token = %q\n", token)
	}
	fmt.Fprintf(&b, "transport.tls.enable = %v\n", cfg.TLSEnable)
	fmt.Fprintf(&b, "transport.heartbeatInterval = 30\n")
	fmt.Fprintf(&b, "transport.heartbeatTimeout = 90\n\n")

	localIP := func(p frpProxy) string {
		if strings.TrimSpace(p.LocalIP) != "" {
			return strings.TrimSpace(p.LocalIP)
		}
		return "127.0.0.1"
	}
	for _, p := range cfg.Proxies {
		b.WriteString("[[proxies]]\n")
		fmt.Fprintf(&b, "name = %q\n", strings.TrimSpace(p.Name))
		fmt.Fprintf(&b, "type = %q\n", p.Type)
		fmt.Fprintf(&b, "localIP = %q\n", localIP(p))
		fmt.Fprintf(&b, "localPort = %d\n", p.LocalPort)
		switch p.Type {
		case "tcp":
			fmt.Fprintf(&b, "remotePort = %d\n", p.RemotePort)
		case "http", "https":
			var domains []string
			for _, d := range strings.Split(p.CustomDomain, ",") {
				if d = strings.TrimSpace(d); d != "" {
					domains = append(domains, d)
				}
			}
			b.WriteString("customDomains = [")
			for i, d := range domains {
				if i > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(&b, "%q", d)
			}
			b.WriteString("]\n")
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

// ---------- 组件（下载器模式取包） ----------

func componentSpec() compkit.Spec {
	return compkit.Spec{
		Tag:     "remote",
		ID:      "frp",
		Name:    "frpc",
		Version: "0.61.2",
		Bin:     "frpc.exe",
		Size:    13 << 20,
		Note:    "内网穿透客户端：把家里的对号挂到你自己的 frp 服务器上（Apache-2.0）",
		URLs: []string{
			"https://github.com/fatedier/frp/releases/download/v0.61.2/frp_0.61.2_windows_amd64.zip",
			"https://ghproxy.net/https://github.com/fatedier/frp/releases/download/v0.61.2/frp_0.61.2_windows_amd64.zip",
			"https://gh-proxy.com/https://github.com/fatedier/frp/releases/download/v0.61.2/frp_0.61.2_windows_amd64.zip",
		},
	}
}

func compStatus() compkit.Status { return compkit.StatusOf(componentSpec()) }

func installComp(w http.ResponseWriter) error {
	if err := compkit.Install(componentSpec()); err != nil {
		return appkit.Errorf(http.StatusInternalServerError, "frpc 安装失败：%v", err)
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "state": compStatus().State})
	return nil
}

func importComp(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Zip string `json:"zip"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "参数解析失败：%v", err)
	}
	dir, err := compkit.Import(componentSpec(), in.Zip)
	if err != nil {
		return appkit.Errorf(http.StatusInternalServerError, "frpc 离线导入失败：%v", err)
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "dir": dir})
	return nil
}

// ---------- 进程管理 ----------

func frpRunning() bool {
	frpMu.Lock()
	defer frpMu.Unlock()
	return frpProc != nil && frpProc.Running()
}

func stopFrpProc() {
	frpMu.Lock()
	p := frpProc
	frpProc = nil
	frpMu.Unlock()
	if p != nil {
		p.Stop()
		Logf("remote: frpc 已停止")
	}
}

// StopFrp 服务停了 frpc 也跟着停（Job Object 是兜底，这里主动停是为了干净退出）。
func StopFrp() { stopFrpProc() }

// frpcDir 配置与日志落点：config/remote/（跟站点等配置同一棵树，进备份）。
func frpcDir() string { return filepath.Join(duihao.DirConfig(), "remote") }

// startFrp 校验配置 → 生成 toml → 拉进程。
func startFrp(db *sql.DB, uid string) error {
	frpMu.Lock()
	defer frpMu.Unlock()
	if frpProc != nil && frpProc.Running() {
		return nil // 已在跑
	}
	cfg := loadFrp(db, uid)
	token := frpToken(db, uid)
	if strings.TrimSpace(cfg.Server.Addr) == "" {
		return fmt.Errorf("还没填 frps 服务器地址")
	}
	toml, err := genTOML(cfg, token)
	if err != nil {
		return err
	}
	bin := compkit.Bin(componentSpec())
	if bin == "" {
		return fmt.Errorf("frpc 组件还没装（远程页 → 安装）")
	}
	if err := os.MkdirAll(frpcDir(), 0o755); err != nil {
		return fmt.Errorf("建配置目录失败: %w", err)
	}
	tomlPath := filepath.Join(frpcDir(), frpcTomlName)
	if err := os.WriteFile(tomlPath, []byte(toml), 0o600); err != nil {
		return fmt.Errorf("写配置失败: %w", err)
	}
	logPath := filepath.Join(frpcDir(), frpcLogName)
	proc, _, err := childproc.Start(bin, []string{"-c", tomlPath}, childproc.Options{LogPath: logPath})
	if err != nil {
		frpStartErr = err.Error()
		return fmt.Errorf("frpc 启动失败: %w", err)
	}
	frpProc = proc
	frpStartErr = ""
	Logf("remote: frpc 已启动（pid=%d, server=%s:%d, tls=%v, 隧道 %d 条）",
		proc.Pid(), cfg.Server.Addr, cfg.Server.Port, cfg.TLSEnable, len(cfg.Proxies))
	return nil
}

// frpcAutoStart 服务起来时，开关开着就自动拉。
func frpcAutoStart(db *sql.DB, uid string) {
	cfg := loadFrp(db, uid)
	if !cfg.Enabled || strings.TrimSpace(cfg.Server.Addr) == "" {
		return
	}
	if err := startFrp(db, uid); err != nil {
		frpMu.Lock()
		frpStartErr = err.Error()
		frpMu.Unlock()
		Logf("remote: frpc 自动启动失败: %v", err)
	}
}

// ---------- API 动作 ----------

// saveFrp 保存可视化配置。保存即校验（genTOML 的人话错误直接回给页面）；
// 开关开着就顺手拉起，关着就停掉 —— 用户不用再点第二次。
func saveFrp(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Enabled    bool       `json:"enabled"`
		Server     frpServer  `json:"server"`
		TLSEnable  bool       `json:"tlsEnable"`
		Proxies    []frpProxy `json:"proxies"`
		Token      string     `json:"token"`
		ClearToken bool       `json:"clearToken"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "参数解析失败：%v", err)
	}
	if in.Server.Port <= 0 {
		in.Server.Port = 7000
	}
	tok := strings.TrimSpace(in.Token)
	if tok == "" && !in.ClearToken {
		tok = frpToken(c.DB, c.User) // 没重填 = 沿用已存令牌
	}
	cfg := FrpConfig{
		Enabled: in.Enabled,
		Server: frpServer{
			Addr: strings.TrimSpace(in.Server.Addr),
			Port: in.Server.Port,
			User: strings.TrimSpace(in.Server.User),
		},
		TLSEnable: in.TLSEnable,
		Proxies:   in.Proxies,
	}
	if cfg.Proxies == nil {
		cfg.Proxies = []frpProxy{}
	}
	if _, err := genTOML(cfg, tok); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "%s", err.Error())
	}
	raw, _ := json.Marshal(cfg)
	if err := putKV(c.DB, c.User, "frpc.config", string(raw)); err != nil {
		return appkit.Errorf(http.StatusInternalServerError, "保存失败：%v", err)
	}
	switch {
	case in.ClearToken:
		_ = putKV(c.DB, c.User, "frpc.token", "")
	case tok != "" && strings.TrimSpace(in.Token) != "":
		sealed, err := sealToken(strings.TrimSpace(in.Token))
		if err != nil {
			return appkit.Errorf(http.StatusInternalServerError, "令牌加密失败：%v", err)
		}
		_ = putKV(c.DB, c.User, "frpc.token", sealed)
	}
	msg := "已保存"
	if in.Enabled {
		if err := startFrp(c.DB, c.User); err != nil {
			return appkit.Errorf(http.StatusBadRequest, "已保存，但启动失败：%v", err)
		}
		msg = "已保存并启动"
	} else {
		stopFrpProc()
	}
	Logf("remote: frpc 配置已保存（enabled=%v, 隧道 %d 条）", in.Enabled, len(cfg.Proxies))
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "msg": msg, "running": frpRunning()})
	return nil
}

func startFrpAPI(c appkit.Context, w http.ResponseWriter) error {
	if err := startFrp(c.DB, c.User); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "%v", err)
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "running": true})
	return nil
}

func stopFrpAPI(w http.ResponseWriter) error {
	stopFrpProc()
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "running": false})
	return nil
}
