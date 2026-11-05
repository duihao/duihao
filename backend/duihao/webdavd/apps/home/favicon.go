package home

// favicon 图标代理：浏览器直接向外站取 favicon 在部分网络下会失败（被墙 / 防盗链 / 跨域策略），
// 改由服务端取一次、存进单库，之后从本机直出 —— 第二次进首页瞬间出图标，断网也能显示已缓存的。
//
// 取图顺序：https://host/favicon.ico → http://host/favicon.ico → 首页 <link rel="icon"> → DuckDuckGo 图标服务。
// 全失败则记负缓存（mime='none'），避免每次刷新都去外网超时拖慢首页。

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
)

const (
	maxIconBytes = 256 << 10
	iconFresh    = 30 * 24 * time.Hour // 正缓存
	iconStale    = 24 * time.Hour      // 负缓存
	// 同时向外的取图请求上限，避免一次刷出七十个书签就把网络打满
	maxIconFetch = 8
)

var (
	iconClient = &http.Client{Timeout: 6 * time.Second}
	iconSema   = make(chan struct{}, maxIconFetch)

	iconLinkRe = regexp.MustCompile(`(?is)<link\b[^>]*\brel\s*=\s*["']?[^"'>]*icon[^"'>]*["']?[^>]*>`)
	hrefRe     = regexp.MustCompile(`(?is)\bhref\s*=\s*["']([^"']+)["']`)
	hostRe     = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*(:[0-9]{1,5})?$`)
)

// serveFavicon GET /-api/home/favicon?host=example.com
func serveFavicon(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	host, err := normalizeHost(r.URL.Query().Get("host"))
	if err != nil {
		return appkit.Errorf(http.StatusBadRequest, "%s", err.Error())
	}

	if data, mime, ok := iconFromCache(c, host); ok {
		if mime == "none" {
			return appkit.Errorf(http.StatusNotFound, "没有取到图标")
		}
		writeIcon(w, data, mime)
		return nil
	}
	if isLocalHost(host) {
		return appkit.Errorf(http.StatusBadRequest, "不支持内网地址")
	}

	data, mime, ok := fetchIcon(host)
	if !ok {
		saveIcon(c, host, nil, "none")
		return appkit.Errorf(http.StatusNotFound, "没有取到图标")
	}
	saveIcon(c, host, data, mime)
	writeIcon(w, data, mime)
	return nil
}

func writeIcon(w http.ResponseWriter, data []byte, mime string) {
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// normalizeHost 接受 "example.com"、"example.com:8080" 或带协议的整条 URL。
func normalizeHost(raw string) (string, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "", errText("缺少 host")
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return "", errText("host 不合法")
		}
		raw = u.Host
	}
	raw = strings.TrimSuffix(raw, ".")
	if len(raw) > 253 || !hostRe.MatchString(raw) {
		return "", errText("host 不合法")
	}
	return raw, nil
}

// isLocalHost 挡住 localhost / *.local / 私网 IP 字面量：本机服务不该被网页借去探测内网。
func isLocalHost(host string) bool {
	h := host
	if i := strings.LastIndexByte(h, ':'); i > 0 {
		h = h[:i]
	}
	if h == "localhost" || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

func iconFromCache(c appkit.Context, host string) ([]byte, string, bool) {
	var data []byte
	var mime, at string
	err := c.DB.QueryRow("select data, mime, fetched_at from favicons where host=?", host).Scan(&data, &mime, &at)
	if err != nil {
		return nil, "", false
	}
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return nil, "", false
	}
	ttl := iconFresh
	if mime == "none" {
		ttl = iconStale
	}
	if time.Since(t) > ttl {
		return nil, "", false
	}
	return data, mime, true
}

func saveIcon(c appkit.Context, host string, data []byte, mime string) {
	if data == nil {
		data = []byte{}
	}
	_, _ = c.DB.Exec(`insert into favicons(host, mime, data, fetched_at) values(?,?,?,?)
		on conflict(host) do update set mime=excluded.mime, data=excluded.data, fetched_at=excluded.fetched_at`,
		host, mime, data, time.Now().Format(time.RFC3339))
}

func fetchIcon(host string) ([]byte, string, bool) {
	for _, u := range []string{"https://" + host + "/favicon.ico", "http://" + host + "/favicon.ico"} {
		if b, m, ok := getIcon(u); ok {
			return b, m, true
		}
	}
	if u, ok := iconFromHomepage(host); ok {
		if b, m, ok := getIcon(u); ok {
			return b, m, true
		}
	}
	if b, m, ok := getIcon("https://icons.duckduckgo.com/ip3/" + host + ".ico"); ok {
		return b, m, true
	}
	return nil, "", false
}

// getIcon 取一张图：状态码 200、内容是 image/*、体积合理才算数
// （很多站对 /favicon.ico 返回 200 + HTML 软 404，必须按内容判断）。
func getIcon(rawURL string) ([]byte, string, bool) {
	iconSema <- struct{}{}
	defer func() { <-iconSema }()

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) DuihaoNAS/1.0")
	req.Header.Set("Accept", "image/avif,image/webp,image/png,image/svg+xml,image/*;q=0.9,*/*;q=0.5")
	resp, err := iconClient.Do(req)
	if err != nil {
		return nil, "", false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", false
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxIconBytes))
	if err != nil || len(b) < 70 { // 空文件 / 1x1 占位图没有意义
		return nil, "", false
	}
	mime := http.DetectContentType(b)
	if !strings.HasPrefix(mime, "image/") {
		// ICO 常被嗅探成 application/octet-stream，认扩展名兜一下
		if strings.HasSuffix(strings.ToLower(strings.SplitN(rawURL, "?", 2)[0]), ".ico") &&
			strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "image/") {
			mime = strings.ToLower(strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0]))
		} else if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "image/") {
			mime = strings.ToLower(strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0]))
		} else {
			return nil, "", false
		}
	}
	return b, mime, true
}

// iconFromHomepage 抓首页 HTML 里的 <link rel="icon">，兼容相对路径。
func iconFromHomepage(host string) (string, bool) {
	iconSema <- struct{}{}
	defer func() { <-iconSema }()

	page := "https://" + host + "/"
	req, err := http.NewRequest(http.MethodGet, page, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) DuihaoNAS/1.0")
	resp, err := iconClient.Do(req)
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	html, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return "", false
	}
	base, _ := url.Parse(page)
	for _, tag := range iconLinkRe.FindAllString(string(html), -1) {
		m := hrefRe.FindStringSubmatch(tag)
		if len(m) < 2 {
			continue
		}
		href := strings.TrimSpace(m[1])
		if href == "" || strings.HasPrefix(strings.ToLower(href), "data:") {
			continue
		}
		ref, err := url.Parse(href)
		if err != nil {
			continue
		}
		u := base.ResolveReference(ref)
		if u.Scheme != "http" && u.Scheme != "https" {
			continue
		}
		return u.String(), true
	}
	return "", false
}
