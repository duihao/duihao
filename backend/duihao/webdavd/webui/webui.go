// Package webui 个人空间的页面外壳与静态资源（go:embed，无前端构建链）。
//
// 分工：Go 只负责「外壳 + 顶栏 + 模板注入」，交互都在 shell.js 与各应用脚本里；
// 资源走 /-ui/<file>，页面走 /-<app>；两者都在保留命名空间内，不与用户目录撞名。
package webui

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed layout.html shared.css shell.js acct.js theme.js apps/*.html apps/*.js
var files embed.FS

// NavItem 顶栏一项。
type NavItem struct {
	Path  string
	Label string
	On    bool
}

// Page 模板数据。
type Page struct {
	App      string // 当前应用 id（同时也是 apps/<app>.html 的名字）
	Title    string
	User     string
	Writable bool
	Role     string            // "admin" | "member"；空 = 老式共享会话（导航只留网盘）
	Perms    map[string]string // member 的模块权限
	Nav      []NavItem
	Acct     template.HTML // 右上角账号块（用户名 + 下拉），由 Render 填
	Theme    string        // <html data-theme>：dark / light / system，由 Render 填
}

var (
	navMu sync.RWMutex
	nav   []NavItem

	tmplOnce sync.Once
	tmpls    map[string]*template.Template
	tmplErr  error
)

// SetNav 由 server 启动时按注册表写入（首项是文件管理 "/"）。
func SetNav(items []NavItem) {
	navMu.Lock()
	nav = items
	navMu.Unlock()
}

// navAllowed 顶栏可见性：admin 全见；member 按读权限；无 NAS 账号的会话
// 只见文件管理（点进应用页也会被 appkit 拦成 403，入口干脆不渲染）。
func navAllowed(path, role string, perms map[string]string) bool {
	if path == "/" {
		// 网盘（文件管理器）= 授权模块 files：NAS 账号按读权限显隐；
		// 共享会话（role==""，扫码快传等）维持原样可进；老成员权限表缺
		// files 键时维持改动前的可看（与 hubusers.Can 口径一致）。
		if role == "" || role == "admin" {
			return true
		}
		p, ok := perms["files"]
		return (ok && strings.Contains(p, "r")) || !ok
	}
	if !strings.HasPrefix(path, "/-") {
		return false
	}
	app := strings.TrimPrefix(path, "/-")
	if role == "" {
		return false
	}
	if role == "admin" {
		return true
	}
	if app == "users" {
		return false
	}
	p, ok := perms[app]
	return ok && strings.Contains(p, "r")
}

func currentNav(app, role string, perms map[string]string) []NavItem {
	navMu.RLock()
	defer navMu.RUnlock()
	out := []NavItem{}
	for _, it := range nav {
		if !navAllowed(it.Path, role, perms) {
			continue
		}
		// app=="" 表示文件管理页（路径 "/"）；其余按 /-<app> 命中
		it.On = (it.Path == "/" && app == "") || it.Path == "/-"+app
		out = append(out, it)
	}
	return out
}

// NavFor 渲染顶栏链接片段：文件管理页（Go 拼的 HTML）与模板共用同一份导航，
// 免得两处各写一遍标签。app=="" 表示文件管理页。
func NavFor(app, role string, perms map[string]string) template.HTML {
	var b strings.Builder
	for _, it := range currentNav(app, role, perms) {
		b.WriteString(`<a class="hl`)
		if it.On {
			b.WriteString(` on`)
		}
		b.WriteString(`" href="` + it.Path + `">` + template.HTMLEscapeString(it.Label) + `</a>`)
	}
	return template.HTML(b.String())
}

// AcctFor 渲染顶栏右上角的「账号」块：用户名 + 下拉（修改密码 / 退出）。
//
// 为什么用原生 <details>：网盘文件管理页由 Go 拼 HTML、只加载 davJS，不加载
// shell.js；而这两类页面的顶栏必须长得一模一样。开合交给浏览器，就不用写两份 JS。
//
// readonly=true 时用户名后面挂「只读」小标（文件管理页会按当前目录权限动态开关
// #ro，所以这个 id 要留着）。role=="" 是纯 WebDAV 共享会话，没有 NAS 账号，
// 改不了密码 —— 菜单里就不出「修改密码」，只留退出。
func AcctFor(user string, readonly bool, role string) template.HTML {
	var b strings.Builder
	b.WriteString(`<details class="hub-acct"><summary class="hub-acct-btn">`)
	b.WriteString(`<span class="hub-user">` + template.HTMLEscapeString(user))
	b.WriteString(`<span id="ro" class="ro"`)
	if !readonly {
		b.WriteString(` hidden`)
	}
	b.WriteString(`>只读</span></span>`)
	b.WriteString(`<svg class="hub-acct-caret" width="10" height="10" viewBox="0 0 24 24" fill="none" ` +
		`stroke="currentColor" stroke-width="2.6" stroke-linecap="round"><path d="M6 9l6 6 6-6"/></svg>`)
	b.WriteString(`</summary><div class="hub-acct-menu">`)
	if role != "" {
		b.WriteString(`<button type="button" class="hub-acct-item" data-acct="passwd">修改密码</button>`)
	}
	b.WriteString(`<a class="hub-acct-item danger" href="/-logout">退出</a>`)
	b.WriteString(`</div></details>`)
	return template.HTML(b.String())
}

func load() {
	tmpls = map[string]*template.Template{}
	ents, err := files.ReadDir("apps")
	if err != nil {
		tmplErr = err
		return
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".html")
		t, err := template.ParseFS(files, "layout.html", path.Join("apps", e.Name()))
		if err != nil {
			tmplErr = fmt.Errorf("解析模板 %s 失败: %w", e.Name(), err)
			return
		}
		tmpls[name] = t
	}
}

// Render 渲染某个应用的完整页面。
func Render(w http.ResponseWriter, p Page) error {
	tmplOnce.Do(load)
	if tmplErr != nil {
		return tmplErr
	}
	t, ok := tmpls[p.App]
	if !ok {
		return fmt.Errorf("未注册的页面模板: %s", p.App)
	}
	p.Nav = currentNav(p.App, p.Role, p.Perms)
	p.Acct = AcctFor(p.User, !p.Writable, p.Role)
	p.Theme = ThemeAttr()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	return t.ExecuteTemplate(w, "layout.html", p)
}

// Static 处理 /-ui/<file>；不是该前缀返回 false（交回上层）。
func Static(w http.ResponseWriter, r *http.Request) bool {
	const prefix = "/-ui/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	name := strings.TrimPrefix(r.URL.Path, prefix)
	if name == "" || strings.Contains(name, "..") || strings.ContainsAny(name, `\:`) {
		http.NotFound(w, r)
		return true
	}
	b, err := files.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	switch filepath.Ext(name) {
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".js":
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case ".svg":
		w.Header().Set("Content-Type", "image/svg+xml")
	}
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
	return true
}
