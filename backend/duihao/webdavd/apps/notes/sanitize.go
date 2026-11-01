package notes

import (
	"strings"

	"golang.org/x/net/html"
)

// 笔记正文存 HTML（富文本），入库前必须过一遍白名单清洗：
//   - 只保留排版类标签，script/style/iframe 连同内容一起丢掉；
//   - 属性白名单：a[href]（http/https/mailto）、img/video/audio[src]
//     （本站绝对路径 /xxx 或 http/https；src 指向网盘 _appfiles 里的附件）；
//   - 落库前把未闭合的标签补齐，避免一篇坏 HTML 把整页结构带歪。
//
// 历史数据是纯文本（Markdown 风格），不含 '<'，直接原样保留——
// 卡片和编辑器都按「无标签 = 纯文本」处理，不需要迁移改写。
var noteTags = map[string]bool{
	"p": true, "br": true, "div": true, "span": true,
	"h1": true, "h2": true, "h3": true, "h4": true,
	"strong": true, "b": true, "em": true, "i": true, "u": true, "s": true, "strike": true,
	"ul": true, "ol": true, "li": true,
	"blockquote": true, "pre": true, "code": true,
	"a": true, "hr": true,
	"img": true, "video": true, "audio": true,
}

var voidTags = map[string]bool{"br": true, "hr": true, "img": true}

// 媒体标签允许出现的属性（值还需过 safeSrc / 安全字符检查）。
var mediaAttrs = map[string]map[string]bool{
	"img":   {"src": true, "alt": true, "width": true, "height": true},
	"video": {"src": true, "controls": true, "width": true, "preload": true},
	"audio": {"src": true, "controls": true, "preload": true},
}

// 这些标签的内容整段丢弃（不是只丢标签）。
var rawTextTags = map[string]bool{"script": true, "style": true, "iframe": true, "object": true}

// Sanitize 清洗笔记正文 HTML。
func Sanitize(s string) string {
	if s == "" {
		return ""
	}
	if !strings.ContainsAny(s, "<>") {
		return s // 纯文本：原样保留
	}
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	stack := make([]string, 0, 8)
	skip := ""
	for {
		switch z.Next() {
		case html.ErrorToken:
			for i := len(stack) - 1; i >= 0; i-- {
				b.WriteString("</" + stack[i] + ">")
			}
			return b.String()

		case html.TextToken:
			if skip != "" {
				continue
			}
			b.WriteString(html.EscapeString(string(z.Text())))

		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			tag := strings.ToLower(string(name))
			href := ""
			attrs := make([][2]string, 0, 4) // 已通过白名单的属性
			if hasAttr {
				for {
					k, v, more := z.TagAttr()
					key := strings.ToLower(string(k))
					val := string(v)
					if tag == "a" && key == "href" {
						href = val
					}
					if allow := mediaAttrs[tag]; allow != nil && allow[key] {
						if key == "src" {
							if safeSrc(val) {
								attrs = append(attrs, [2]string{"src", val})
							}
						} else if safePlain(val) {
							attrs = append(attrs, [2]string{key, val})
						}
					}
					if !more {
						break
					}
				}
			}
			if skip != "" {
				continue
			}
			if rawTextTags[tag] {
				skip = tag
				continue
			}
			if !noteTags[tag] {
				continue // 丢掉标签本身，保留里面的文字
			}
			if voidTags[tag] {
				b.WriteString("<" + tag + writeAttrs(tag, attrs) + ">")
				continue
			}
			b.WriteString("<" + tag + writeAttrs(tag, attrs))
			if tag == "a" && safeLinkHref(href) {
				b.WriteString(` href="` + html.EscapeString(href) + `" target="_blank" rel="noopener noreferrer"`)
			}
			b.WriteString(">")
			stack = append(stack, tag)

		case html.EndTagToken:
			name, _ := z.TagName()
			tag := strings.ToLower(string(name))
			if skip != "" {
				if tag == skip {
					skip = ""
				}
				continue
			}
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i] == tag {
					for j := len(stack) - 1; j >= i; j-- {
						b.WriteString("</" + stack[j] + ">")
					}
					stack = stack[:i]
					break
				}
			}
		}
	}
}

func safeHref(h string) bool {
	l := strings.ToLower(strings.TrimSpace(h))
	if l == "" {
		return false
	}
	for _, p := range []string{"http://", "https://", "mailto:"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

// safeSrc 媒体 src 白名单：本站绝对路径（/xxx，// 开头的协议相对地址不算）、
// http/https 外链。data: 一律不收 —— 附件都走网盘 _appfiles，正文里不塞 base64。
func safeSrc(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "/") && !strings.HasPrefix(t, "//") {
		return true
	}
	return safeHref(t)
}

// safeLinkHref 链接白名单：外链（http/https/mailto）+ 本站路径（插进正文的附件下载链）。
func safeLinkHref(h string) bool {
	t := strings.TrimSpace(h)
	if strings.HasPrefix(t, "/") && !strings.HasPrefix(t, "//") {
		return true
	}
	return safeHref(h)
}

// safePlain 普通属性值（alt/width 等）：不放换行与引号，防止把属性写出界。
func safePlain(s string) bool {
	if len(s) > 200 {
		return false
	}
	return !strings.ContainsAny(s, "\"'<>`\n\r")
}

// writeAttrs 把白名单内的媒体属性序列化回标签（布尔属性 controls 只写名字）。
func writeAttrs(tag string, attrs [][2]string) string {
	var b strings.Builder
	seen := map[string]bool{}
	for _, a := range attrs {
		if seen[a[0]] {
			continue
		}
		seen[a[0]] = true
		if a[0] == "controls" {
			b.WriteString(" controls")
			continue
		}
		b.WriteString(` ` + a[0] + `="` + html.EscapeString(a[1]) + `"`)
	}
	return b.String()
}

// PlainText 去掉标签取纯文本（标题推导、摘要、搜索预览都用它）。
// 块级标签转换行，连续空白压成单个空格。
func PlainText(s string) string {
	if s == "" {
		return ""
	}
	if !strings.ContainsAny(s, "<>") {
		return s
	}
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	skip := ""
	for {
		switch z.Next() {
		case html.ErrorToken:
			return b.String()
		case html.TextToken:
			if skip != "" {
				continue
			}
			b.WriteString(string(z.Text()))
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			tag := strings.ToLower(string(name))
			if rawTextTags[tag] {
				skip = tag
				continue
			}
			if tag == "br" || tag == "p" || tag == "div" || tag == "li" ||
				tag == "blockquote" || tag == "pre" || (len(tag) == 2 && tag[0] == 'h') {
				b.WriteString("\n")
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			tag := strings.ToLower(string(name))
			if skip != "" && tag == skip {
				skip = ""
			}
		}
	}
}

// collapse 把多行/多空格压成单行（摘要用）。
func collapse(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\u00a0", " ")), " ")
}

// snippet 摘要：压成单行后截断 n 个字符。
func snippet(s string, n int) string {
	r := []rune(collapse(PlainText(s)))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

// firstLine 取纯文本里第一个非空行（且去掉 Markdown 的 # 前缀）。
func firstLine(s string) string {
	for _, line := range strings.Split(PlainText(s), "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if line != "" {
			return line
		}
	}
	return ""
}
