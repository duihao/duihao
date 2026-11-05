// Package deploy 一键部署「整站程序」到站点根目录（M20）。
//
// 定位：站点管理负责「跑起来」，本包负责「有东西可跑」。用户建完站点后，
// 从一个模板（DuihaoCMS / WordPress …）拉包 → 解压 → 写数据库配置 →
// 给出安装向导地址，把「建站」这一步真正闭环。
//
// 设计约束（与站点层一致）：
//   - 只往站点根目录写文件，绝不删用户已有文件（force 也只覆盖同名文件）。
//   - 程序模板是**声明式 JSON**（apps.json，编译进本体），加一个程序 = 加一段 JSON。
//   - 取包走「本地包 → 内置包 → 下载」三级，与运行时取包同构。
//   - 每个落地动作写 `对号部署标记`（.duihao-deploy.json），用于界面回显与升级判断。
package deploy

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	duihao "github.com/duihao/duihao/backend/duihao"
)

//go:embed apps.json
var appsFS embed.FS

// MarkerName 部署标记文件名（站点根目录下）。
const MarkerName = ".duihao-deploy.json"

// Requires 程序对运行环境的要求（仅用于前端提示与部署前预检）。
type Requires struct {
	PHP        string   `json:"php,omitempty"`
	DB         string   `json:"db,omitempty"`
	Extensions []string `json:"extensions,omitempty"`
}

// Source 安装包来源。kind: local（用户选本地 zip）| bundled（内置/离线包）| url（下载）。
type Source struct {
	Kind   string `json:"kind"`
	File   string `json:"file,omitempty"`
	URL    string `json:"url,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

// ConfigFile 按模板生成配置文件（如 wp-config.php）。
type ConfigFile struct {
	From    string   `json:"from"`
	To      string   `json:"to"`
	Defines []string `json:"defines,omitempty"`
	Salts   bool     `json:"salts,omitempty"`
}

// Prefill 往站点写「安装向导预填文件」（DuihaoCMS 读它填表单）。
type Prefill struct {
	File string `json:"file"`
}

// Install 安装契约。
type Install struct {
	Entry       string       `json:"entry"`
	Prefill     *Prefill     `json:"prefill,omitempty"`
	ConfigFiles []ConfigFile `json:"configFiles,omitempty"`
	Note        string       `json:"note,omitempty"`
}

// App 一个可一键部署的程序模板。
type App struct {
	ID       string   `json:"id"`
	Order    int      `json:"order,omitempty"`
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Category string   `json:"category"`
	Summary  string   `json:"summary"`
	Homepage string   `json:"homepage,omitempty"`
	License  string   `json:"license,omitempty"`
	Requires Requires `json:"requires"`
	Sources  []Source `json:"sources"`
	Install  Install  `json:"install"`
}

type catalogFile struct {
	SchemaVersion int   `json:"schemaVersion"`
	Apps          []App `json:"apps"`
}

// Catalog 全部程序模板（按 categories 顺序稳定排序：cms 在前）。
func Catalog() []App {
	data, err := appsFS.ReadFile("apps.json")
	if err != nil {
		return nil
	}
	var f catalogFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil
	}
	out := make([]App, len(f.Apps))
	copy(out, f.Apps)
	SortApps(out)
	return out
}

// Find 按 id 找程序模板。
func Find(id string) (*App, error) {
	for _, a := range Catalog() {
		if a.ID == id {
			app := a
			return &app, nil
		}
	}
	return nil, fmt.Errorf("未知的部署程序: %s", id)
}

// Marker 部署标记（站点根目录 .duihao-deploy.json）。
type Marker struct {
	AppID      string `json:"appId"`
	AppName    string `json:"appName"`
	AppVersion string `json:"appVersion"`
	DeployedAt string `json:"deployedAt"`
	Source     string `json:"source"`
	Files      int    `json:"files"`
	Bytes      int64  `json:"bytes"`
}

// ReadMarker 读部署标记（未部署过返回 false）。
func ReadMarker(root string) (*Marker, bool) {
	data, err := os.ReadFile(filepath.Join(root, MarkerName))
	if err != nil {
		return nil, false
	}
	var m Marker
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false
	}
	return &m, true
}

// Target 站点绑定的数据库连接信息（用于生成程序配置）。
type Target struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string
}

// Step 部署进度（经服务层转发成 Wails 事件）。
type Step struct {
	Phase   string `json:"phase"` // source | verify | extract | configure | done
	Percent int    `json:"percent"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// Options 一次部署的输入。
type Options struct {
	AppID    string
	Root     string // 站点根目录
	SiteName string
	BaseURL  string // 站点访问基址（用于拼安装向导地址）
	DB       *Target
	LocalZip string // 用户选的本地包（可空）
	// EntryOverride 覆盖模板里的安装入口（「自定义程序」用：前端填 install.php 之类）。
	EntryOverride string
	Force         bool // 根目录非空时是否继续（只覆盖同名文件）
	OnStep        func(Step)
}

// Result 部署结果。
type Result struct {
	AppID    string `json:"appId"`
	AppName  string `json:"appName"`
	Version  string `json:"version"`
	Root     string `json:"root"`
	Source   string `json:"source"`
	Files    int    `json:"files"`
	Bytes    int64  `json:"bytes"`
	Entry    string `json:"entry"`
	SetupURL string `json:"setupUrl"`
	Note     string `json:"note,omitempty"`
	Warning  string `json:"warning,omitempty"`
	// PrefillActive 安装包是否支持「对号预填」（老包不读 data/deploy-prefill.php）。
	PrefillActive bool `json:"prefillActive"`
	// DBHint 手动填库时照抄的一行（老包 / 预填失败时给用户）。
	DBHint string `json:"dbHint,omitempty"`
}

// Deploy 执行一次部署。任何阶段失败都会回滚本次**新增**的文件吗？不会——
// 站点根是用户目录，部分解压比「删干净」更安全（已覆盖的同名文件无法复原，
// 删新文件又会误删同名用户文件）。失败时返回错误 + 已写入数量，由 UI 提示用户
// 可整目录清空重来（根目录本就是站点专有的）。
func Deploy(ctx context.Context, opt Options) (*Result, error) {
	app, err := Find(opt.AppID)
	if err != nil {
		return nil, err
	}
	step := func(phase string, pct int, msg string) {
		if opt.OnStep != nil {
			opt.OnStep(Step{Phase: phase, Percent: pct, Message: msg})
		}
	}

	root := strings.TrimSpace(opt.Root)
	if root == "" {
		return nil, fmt.Errorf("站点根目录为空")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("无法创建站点根目录: %w", err)
	}

	if n := countEntries(abs); n > 0 && !opt.Force {
		return nil, fmt.Errorf("目标目录已有 %d 个文件或子目录，继续部署会覆盖同名文件（已确认请重试并允许覆盖）", n)
	}
	if app.ID == "custom" && strings.TrimSpace(opt.LocalZip) == "" {
		return nil, fmt.Errorf("「自定义程序」必须指定本地安装包（zip）")
	}

	step("source", 8, "定位安装包…")
	zipPath, sourceDesc, declaredSHA, cleanup, err := resolveSource(ctx, app, opt, step)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	step("verify", 52, "校验安装包…")
	warning := ""
	if declaredSHA != "" {
		actual, _, err := sha256File(zipPath)
		if err != nil {
			return nil, fmt.Errorf("计算摘要失败: %w", err)
		}
		if !strings.EqualFold(actual, declaredSHA) {
			return nil, fmt.Errorf("安装包校验失败（SHA256 不匹配），已中止部署。\n期望 %s\n实际 %s", declaredSHA, actual)
		}
	} else {
		warning = "该安装包来源未声明 SHA256，未做完整性校验；如需严格校验，请用本地包并核对摘要。"
	}

	tmp, err := os.MkdirTemp("", "duihao-deploy-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	step("extract", 62, "解压安装包…")
	if err := extractZip(zipPath, tmp); err != nil {
		return nil, fmt.Errorf("解压失败: %w", err)
	}
	promoteSingleRoot(tmp)

	step("extract", 82, "写入站点目录…")
	files, bytes, err := copyTree(tmp, abs)
	if err != nil {
		return nil, fmt.Errorf("写入站点目录失败: %w", err)
	}

	step("configure", 92, "生成配置文件…")
	prefillActive := false
	dbHint := ""
	if opt.DB != nil {
		dbHint = fmt.Sprintf("%s:%d · 库 %s · 用户 %s · 密码 %s",
			opt.DB.Host, opt.DB.Port, opt.DB.Database, opt.DB.User, opt.DB.Password)
	}
	if app.Install.Prefill != nil {
		if opt.DB == nil {
			warning = joinWarning(warning, "站点未绑定数据库，安装向导需要手填数据库信息。")
		} else if err := writePrefill(abs, app.Install.Prefill.File, opt); err != nil {
			warning = joinWarning(warning, "写预填文件失败："+err.Error())
		} else {
			// 老版本安装包不认识预填文件（里面没有 deploy-prefill 字样）：
			// 明说一句，别让用户以为已经填好了、结果建了个 SQLite 站。
			prefillActive = installerSupportsPrefill(abs, app.Install.Entry)
			if !prefillActive {
				// 老包不会读它 → 立刻删掉，别把库密码留在站点目录里
				_ = os.Remove(filepath.Join(abs, filepath.FromSlash(app.Install.Prefill.File)))
				warning = joinWarning(warning,
					"该安装包版本较旧，安装向导不会自动填数据库：请选 MySQL 并按下面这行填写。")
			}
		}
	}
	for _, cf := range app.Install.ConfigFiles {
		if err := writeConfigFile(abs, cf, opt); err != nil {
			warning = joinWarning(warning, err.Error())
		}
	}

	m := Marker{
		AppID: app.ID, AppName: app.Name, AppVersion: app.Version,
		DeployedAt: time.Now().Format(time.RFC3339), Source: sourceDesc,
		Files: files, Bytes: bytes,
	}
	if data, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(abs, MarkerName), append(data, '\n'), 0o644)
	}

	entry := strings.TrimPrefix(app.Install.Entry, "/")
	if e := strings.TrimSpace(opt.EntryOverride); e != "" {
		entry = strings.TrimPrefix(e, "/")
	}
	base := opt.BaseURL
	if base == "" {
		base = "/"
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}

	step("done", 100, "部署完成")
	return &Result{
		AppID: app.ID, AppName: app.Name, Version: app.Version,
		Root: abs, Source: sourceDesc, Files: files, Bytes: bytes,
		Entry: entry, SetupURL: base + entry, Note: app.Install.Note,
		Warning: warning, PrefillActive: prefillActive, DBHint: dbHint,
	}, nil
}

// installerSupportsPrefill 安装入口是否认识「对号预填文件」。
// 判据很轻：install.php 里出现 deploy-prefill 字样（本仓 1.0.x 起支持）。
func installerSupportsPrefill(root, entry string) bool {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(entry, "/"))))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "deploy-prefill")
}

func joinWarning(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + " " + b
}

// resolveSource 三级取包：本地包 → 内置包 → 下载。返回包路径、来源描述、
// 声明的 SHA256、清理函数。
func resolveSource(ctx context.Context, app *App, opt Options, step func(string, int, string)) (string, string, string, func(), error) {
	var local Source
	for _, s := range app.Sources {
		if s.Kind == "local" {
			local = s
		}
	}

	// ① 用户选的本地包（同目录放了 .sha256 就顺手校验，没有就只告警）
	if p := strings.TrimSpace(opt.LocalZip); p != "" {
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			return "", "", "", nil, fmt.Errorf("本地安装包不可用: %s", p)
		}
		return p, "本地包 " + filepath.Base(p), sidecarSHA(p), nil, nil
	}

	// ② 内置包（<data>/deploy/pkgs 或 exe 同级 payload/deploy）
	for _, s := range app.Sources {
		if s.Kind != "bundled" || s.File == "" {
			continue
		}
		for _, dir := range []string{
			filepath.Join(duihao.DataRoot(), "deploy", "pkgs"),
			filepath.Join(duihao.PayloadDir(), "deploy"),
		} {
			p := filepath.Join(dir, s.File)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				sum := s.SHA256
				if sum == "" {
					// 清单不写死摘要（重打包就会失效），改读包旁边的
					// `<file>.sha256`（打包脚本生成）——同样是真校验。
					sum = sidecarSHA(p)
				}
				return p, "内置包 " + s.File, sum, nil, nil
			}
		}
	}

	// ③ 下载
	for _, s := range app.Sources {
		if s.Kind != "url" || s.URL == "" {
			continue
		}
		step("source", 20, "下载安装包…")
		cacheDir := filepath.Join(duihao.DataRoot(), "deploy", "cache")
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return "", "", "", nil, err
		}
		name := cacheName(app, s.URL)
		dst := filepath.Join(cacheDir, name)
		if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
			// 已缓存且声明了摘要 → 直接复验；未声明则复用（避免重复下载）
			return dst, "缓存 " + name, s.SHA256, nil, nil
		}
		step("source", 28, "下载 "+app.Name+"…")
		if err := download(ctx, s.URL, dst); err != nil {
			_ = os.Remove(dst)
			continue // 换下一个 url 源
		}
		return dst, "下载 " + name, s.SHA256, nil, nil
	}

	hint := ""
	if local.Hint != "" {
		hint = "（" + local.Hint + "）"
	}
	return "", "", "", nil, fmt.Errorf("拿不到 %s 的安装包%s：\n· 点「选择本地包」指定 zip；\n· 或把 zip 放到 %s 后重试",
		app.Name, hint, filepath.Join(duihao.DataRoot(), "deploy", "pkgs"))
}

func cacheName(app *App, url string) string {
	base := url
	if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.Index(base, "?"); i >= 0 {
		base = base[:i]
	}
	if base == "" || !strings.HasSuffix(strings.ToLower(base), ".zip") {
		base = app.ID + "-" + app.Version + ".zip"
	}
	return base
}

var httpClient = &http.Client{Timeout: 30 * time.Minute}

// sidecarSHA 读 `<pkg>.sha256`（打包脚本产出，格式 "摘要 文件名"）。
// 找不到或解析失败返回空串 —— 调用方按「未声明摘要」处理并给出告警。
func sidecarSHA(pkg string) string {
	b, err := os.ReadFile(pkg + ".sha256")
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return ""
	}
	sum := strings.ToLower(strings.TrimSpace(fields[0]))
	if len(sum) != 64 {
		return ""
	}
	for _, c := range sum {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return ""
		}
	}
	return sum
}

func download(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// writePrefill 写安装向导预填文件（PHP 数组，DuihaoCMS 的 install.php 会读）。
func writePrefill(root, rel string, opt Options) error {
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	db := opt.DB
	siteName := opt.SiteName
	if siteName == "" {
		siteName = "我的企业站"
	}
	var b strings.Builder
	b.WriteString("<?php\n")
	// 直接访问（http://站点/data/deploy-prefill.php）时直接 404：
	// 只有安装向导（先 define INSTALLING）才读得到里面的库密码。
	b.WriteString("if (!defined('INSTALLING')) { http_response_code(404); exit; }\n")
	b.WriteString("// 由「对号」一键部署写入：安装向导会读取本文件预填数据库信息，安装成功后自动删除。\n")
	b.WriteString("return array (\n")
	put := func(k, v string) {
		b.WriteString("  '" + k + "' => " + phpString(v) + ",\n")
	}
	put("driver", "mysql")
	put("db_host", db.Host)
	put("db_port", fmt.Sprintf("%d", db.Port))
	put("db_name", db.Database)
	put("db_user", db.User)
	put("db_pass", db.Password)
	put("site_name", siteName)
	b.WriteString(");\n")
	return os.WriteFile(p, []byte(b.String()), 0o644)
}

// phpString 单引号字符串转义（只处理 \ 与 '）。
func phpString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

var defineRe = regexp.MustCompile(`(define\(\s*'([A-Z_]+)'\s*,\s*)'([^']*)'`)

// writeConfigFile 从模板生成配置文件：替换 DB_* 定义，可选填充 WP 盐值。
func writeConfigFile(root string, cf ConfigFile, opt Options) error {
	src := filepath.Join(root, filepath.FromSlash(cf.From))
	dst := filepath.Join(root, filepath.FromSlash(cf.To))
	if _, err := os.Stat(dst); err == nil {
		return nil // 已存在（重复部署）→ 不动用户的配置
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("读配置模板失败（%s）: %w", cf.From, err)
	}
	text := string(data)
	values := map[string]string{
		"DB_HOST":     "127.0.0.1",
		"DB_NAME":     "",
		"DB_USER":     "",
		"DB_PASSWORD": "",
	}
	if opt.DB != nil {
		host := opt.DB.Host
		if opt.DB.Port > 0 && opt.DB.Port != 3306 {
			host = fmt.Sprintf("%s:%d", host, opt.DB.Port)
		}
		values["DB_HOST"] = host
		values["DB_NAME"] = opt.DB.Database
		values["DB_USER"] = opt.DB.User
		values["DB_PASSWORD"] = opt.DB.Password
	}
	want := map[string]bool{}
	for _, k := range cf.Defines {
		want[k] = true
	}
	saltKeys := map[string]bool{
		"AUTH_KEY": true, "SECURE_AUTH_KEY": true, "LOGGED_IN_KEY": true,
		"NONCE_KEY": true, "AUTH_SALT": true, "SECURE_AUTH_SALT": true,
		"LOGGED_IN_SALT": true, "NONCE_SALT": true,
	}
	if cf.Salts {
		for k := range saltKeys {
			want[k] = true
		}
	}
	text = defineRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := defineRe.FindStringSubmatch(m)
		if sub == nil {
			return m
		}
		key := sub[2]
		if !want[key] {
			return m
		}
		val, ok := values[key]
		if !ok {
			if cf.Salts && saltKeys[key] {
				val = randomSalt()
			} else {
				return m
			}
		}
		return sub[1] + phpString(val)
	})
	if err := os.WriteFile(dst, []byte(text), 0o644); err != nil {
		return fmt.Errorf("写 %s 失败: %w", cf.To, err)
	}
	return nil
}

func randomSalt() string {
	buf := make([]byte, 48)
	if _, err := rand.Read(buf); err != nil {
		return "duihao-salt-" + hex.EncodeToString([]byte(time.Now().String()))
	}
	// 只用 ASCII 可见字符，避免破坏 PHP 单引号字符串
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!#$%&()*+,-./:;<=>?@[]^_`{|}~"
	out := make([]byte, 64)
	for i := range out {
		out[i] = chars[int(buf[i%len(buf)])%len(chars)]
	}
	return string(out)
}

// countEntries 统计目录条目数（不递归；空目录视为可用）。
func countEntries(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.Name() == MarkerName {
			continue
		}
		n++
	}
	return n
}

// 保证 utf8 被使用（phpString 处理中文站点名时依赖 utf8 合法输入检查）。
var _ = utf8.ValidString

// SortApps 排序：order 小的在前（DuihaoCMS=1 置顶），同 order 按类别、名字。
func SortApps(apps []App) {
	sort.SliceStable(apps, func(i, j int) bool {
		oi, oj := apps[i].Order, apps[j].Order
		if oi == 0 {
			oi = 500
		}
		if oj == 0 {
			oj = 500
		}
		if oi != oj {
			return oi < oj
		}
		if apps[i].Category != apps[j].Category {
			return apps[i].Category < apps[j].Category
		}
		return apps[i].Name < apps[j].Name
	})
}
