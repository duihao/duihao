// 站点级配置项（借鉴 phpStudy「网站」对话框的 6 个 Tab）。
//
// 铁律 1：这里全是**站点层**的声明式配置——只影响 nginx 片段渲染，
// 不启动任何进程、不携带可执行逻辑。手工改片段会在下次渲染时被覆盖。
package site

import (
	"strings"
)

// Options 站点可配置项。零值 = 对号默认行为（与 phpStudy 各开关默认 OFF 一致）。
type Options struct {
	// —— 高级配置 ——
	AccessLog    bool   `json:"accessLog,omitempty"`    // 独立访问日志（logs/sites/<id>.access.log）
	ErrorLog     bool   `json:"errorLog,omitempty"`     // 独立错误日志（默认跟随 nginx 主配置）
	Gzip         bool   `json:"gzip,omitempty"`         // 页面压缩
	CacheExpires bool   `json:"cacheExpires,omitempty"` // 静态资源缓存（expires 30d）
	AutoIndex    bool   `json:"autoIndex,omitempty"`    // 目录索引（目录列表）
	Index        string `json:"index,omitempty"`        // 网站首页，空格分隔（默认 index.html index.htm index.php）

	// —— 安全配置 ——
	DenyHidden   bool   `json:"denyHidden,omitempty"`   // 禁止访问 . 开头的文件/目录
	DenyFiles    string `json:"denyFiles,omitempty"`    // 禁止下载的后缀，逗号/空格分隔；留空关
	DenyExecDirs string `json:"denyExecDirs,omitempty"` // 禁止执行 PHP 的目录，逗号分隔
	CrossIsolate bool   `json:"crossIsolate,omitempty"` // 跨站隔离（X-Frame-Options + nosniff）
	AllowIPs     string `json:"allowIPs,omitempty"`     // 允许 IP/网段，逗号分隔（填了即白名单）
	DenyIPs      string `json:"denyIPs,omitempty"`      // 拒绝 IP/网段，逗号分隔

	// —— 错误页面 ——
	ErrorPages string `json:"errorPages,omitempty"` // 每行一条：404 /404.html

	// —— 伪静态 ——
	Rewrite string `json:"rewrite,omitempty"` // 原样写进 location / 的 rewrite 规则

	// —— 其他 ——
	// Production 生产环境：PHP 错误只写日志、不输出到页面（display_errors=Off）。
	// 没有 PHP 的纯静态站点该标记不产生 nginx 指令，只在卡片上做标识。
	Production bool   `json:"production,omitempty"`
	RedirectTo string `json:"redirectTo,omitempty"` // 301 全站跳转目标（如 https://www.x.com）
	ExpireAt   string `json:"expireAt,omitempty"`   // 到期日期（仅展示提醒，不生效）
	Remark     string `json:"remark,omitempty"`     // 备注
}

// DefaultIndex 未指定网站首页时的默认值。
const DefaultIndex = "index.html index.htm index.php"

// IndexList 首页文件列表（空白分隔，已规范化）。
func (o Options) IndexList() []string {
	return splitTokens(o.Index)
}

// ErrPageLines 解析错误页配置，返回 "errPage 404 /404.html" 形式的多行串。
// 非法行（状态码不是 3 位数字）直接丢弃——宁可不生效，也不写坏 nginx 配置。
func (o Options) ErrPageLines() []string {
	var out []string
	for _, line := range strings.Split(o.ErrorPages, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		code := f[0]
		if len(code) != 3 || !allDigits(code) {
			continue
		}
		out = append(out, "error_page "+strings.Join(f, " ")+";")
	}
	return out
}

// ErrCodes 从错误页配置里取状态码（生成静态页时用）。
func (o Options) ErrCodes() []string {
	var out []string
	for _, line := range strings.Split(o.ErrorPages, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) >= 2 && len(f[0]) == 3 && allDigits(f[0]) {
			out = append(out, f[0])
		}
	}
	return out
}

// denyFileExts 禁止下载的后缀（去掉点号）。
func (o Options) denyFileExts() []string {
	var out []string
	for _, t := range splitTokens(o.DenyFiles) {
		t = strings.TrimPrefix(strings.ToLower(t), ".")
		if t != "" && isSafeToken(t) {
			out = append(out, t)
		}
	}
	return out
}

// execDenyDirs 禁止执行 PHP 的目录名（去掉斜杠）。
func (o Options) execDenyDirs() []string {
	var out []string
	for _, t := range splitTokens(o.DenyExecDirs) {
		t = strings.Trim(t, "/\\")
		if t != "" && isSafeToken(t) {
			out = append(out, t)
		}
	}
	return out
}

// ipRules allow/deny 列表（顺序：先 allow 再 deny all）。
func (o Options) ipRules() []string {
	allow := splitTokens(o.AllowIPs)
	deny := splitTokens(o.DenyIPs)
	var out []string
	for _, ip := range allow {
		if isSafeToken(ip) {
			out = append(out, "allow "+ip+";")
		}
	}
	for _, ip := range deny {
		if isSafeToken(ip) {
			out = append(out, "deny "+ip+";")
		}
	}
	// 只配了白名单：其余全拒（否则白名单没有意义）。
	if len(allow) > 0 {
		out = append(out, "deny all;")
	}
	return out
}

// splitTokens 逗号/空格/换行/分号 分隔的 token 列表。
func splitTokens(s string) []string {
	f := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ';'
	})
	out := make([]string, 0, len(f))
	for _, x := range f {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// isSafeToken 只允许「字母数字 . : - _ / * 和 []」——防止把用户输入注入成 nginx 指令。
// 这一层比模板转义更直接：不合法的 token 直接丢弃，配置里绝不出现任意文本。
func isSafeToken(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == ':', r == '-', r == '_', r == '/', r == '*', r == '[', r == ']':
		default:
			return false
		}
	}
	return true
}

// rewriteLines 伪静态规则：逐行 trim 后原样返回（用户写的 nginx 规则，不做改写）。
// 只挡掉能把 server 块提前闭合的花括号，避免半截配置把整个 nginx 弄挂。
func (o Options) rewriteLines() []string {
	var out []string
	for _, line := range strings.Split(o.Rewrite, "\n") {
		l := strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		if strings.ContainsAny(l, "{}") {
			// 含花括号的行（如 location 块）在这里不安全：伪静态只接受单行指令。
			continue
		}
		out = append(out, "    "+l)
	}
	return out
}
