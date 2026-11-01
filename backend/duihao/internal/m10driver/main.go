// m10driver：站点配置（M10，借鉴 phpStudy「站点」对话框）端到端验收。
//
// 真实 nginx + php 运行时（隔离 DUIHAO_DATA_ROOT），逐项开开关并用 HTTP 断言行为：
//
//	基本 → 启动 → 高级(日志/压缩/缓存/目录索引/首页) → 安全(隐藏文件/后缀/禁执行/跨站/白名单)
//	→ 错误页(生成 + 命中) → 伪静态 → 301 全站跳转 → 端口冲突拒绝 → 非法配置回滚 → 删除
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	duihao "github.com/ys-ll/uniterm/backend/duihao"
	"github.com/ys-ll/uniterm/backend/duihao/audit"
	"github.com/ys-ll/uniterm/backend/duihao/instance"
	"github.com/ys-ll/uniterm/backend/duihao/site"
)

var fails int

func ck(name string, ok bool, extra ...any) {
	if ok {
		fmt.Printf("  [PASS] %s\n", name)
		return
	}
	fails++
	fmt.Printf("  [FAIL] %s %v\n", name, extra)
}

// resp GET 一次并返回状态码/头/正文（不跟随跳转，便于断言 301）。
func resp(url string, gzip bool) (int, http.Header, string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, nil, "", err
	}
	if gzip {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	c := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	r, err := c.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r.StatusCode, r.Header, string(b), nil
}

// rawResp 不自动解压的 GET：用来断言 gzip 的 Content-Encoding 头。
func rawResp(url string) (int, http.Header, error) {
	c := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{Proxy: nil, DisableCompression: true},
	}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	r, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer r.Body.Close()
	_, _ = io.Copy(io.Discard, r.Body)
	return r.StatusCode, r.Header, nil
}

func main() {
	if err := duihao.EnsureDirs(); err != nil {
		fmt.Println("ENSURE-ERR:", err)
		os.Exit(1)
	}
	settings, err := duihao.LoadSettings()
	if err != nil {
		settings = duihao.DefaultSettings()
	}
	auditLog := audit.New(duihao.DirLogs())
	mgr, err := instance.NewManager(settings, auditLog)
	if err != nil {
		fmt.Println("MGR-ERR:", err)
		os.Exit(2)
	}
	sites, err := site.NewManager(mgr, auditLog)
	if err != nil {
		fmt.Println("SITE-ERR:", err)
		os.Exit(3)
	}

	fmt.Println("== 1) 实例")
	web, err := mgr.Create("nginx", "1.15.11", "", nil)
	if err != nil {
		fmt.Println("NGINX-ERR:", err)
		os.Exit(4)
	}
	php, err := mgr.Create("php", "8.2.29", "", nil)
	if err != nil {
		fmt.Println("PHP-ERR:", err)
		os.Exit(5)
	}
	wport := web.Ports["http"]
	fmt.Printf("  nginx=%s:%d php=%s:%d\n", web.ID, wport, php.ID, php.Ports["fcgi"])

	root := filepath.Join(duihao.DataRoot(), "www", "m10")
	must(os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	must(os.MkdirAll(filepath.Join(root, "uploads"), 0o755))
	// >1KB 便于验证 gzip（gzip_min_length 1k）
	big := strings.Repeat("对号站点配置验收 padding line.\n", 80)
	must(os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html><title>m10</title><h1>M10-HOME</h1>\n"+big), 0o644))
	must(os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("sub-file"), 0o644))
	must(os.WriteFile(filepath.Join(root, "app.css"), []byte("body{color:#333}"), 0o644))
	must(os.WriteFile(filepath.Join(root, ".secret"), []byte("hidden"), 0o644))
	must(os.WriteFile(filepath.Join(root, "dump.sql"), []byte("select 1"), 0o644))
	must(os.WriteFile(filepath.Join(root, "uploads", "shell.php"), []byte("<?php echo 'pwn';"), 0o644))
	must(os.WriteFile(filepath.Join(root, "index.php"), []byte("<?php echo 'M10-PHP';"), 0o644))

	fmt.Println("== 2) 创建站点（默认配置）")
	port := 18080
	rec, err := sites.Create(site.CreateOpts{
		Domains: []string{"m10.test", "www.m10.test"}, Port: port, Root: root,
		Web: web.ID, PHP: php.ID,
	})
	if err != nil {
		fmt.Println("CREATE-ERR:", err)
		os.Exit(6)
	}
	if err := sites.Start(rec.ID); err != nil {
		fmt.Println("START-ERR:", err)
		os.Exit(7)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	code, _, body, err := resp(base+"/", false)
	ck("默认配置首页 200", err == nil && code == 200 && strings.Contains(body, "M10-HOME"), code, err)
	code, _, body, _ = resp(base+"/index.php", false)
	ck("PHP 转发可用", code == 200 && strings.Contains(body, "M10-PHP"), code, body)

	fmt.Println("== 3) 高级配置：压缩 / 缓存 / 目录索引 / 首页 + 日志")
	opts := site.Options{
		AccessLog: true, ErrorLog: true, Gzip: true, CacheExpires: true, AutoIndex: true,
		Index:      "index.html index.php",
		CrossIsolate: true,
	}
	if err := sites.Update(rec.ID, site.UpdateOpts{
		Domains: []string{"m10.test", "www.m10.test"}, Port: port, Root: root,
		PHP: php.ID, Options: opts,
	}); err != nil {
		ck("Update 应用成功", false, err)
	} else {
		ck("Update 应用成功", true)
	}
	var gzCode int
	var gzEnc string
	ok := poll(4000, func() bool {
		c, h, err := rawResp(base + "/")
		gzCode, gzEnc = c, h.Get("Content-Encoding")
		return err == nil && c == 200 && gzEnc == "gzip"
	})
	ck("压缩：响应带 Content-Encoding: gzip", ok, gzCode, gzEnc)
	frag := readFragment(mgr, web.ID, rec.ID)
	ck("片段含 gzip on", strings.Contains(frag, "gzip on;"))
	ck("片段含 autoindex on", strings.Contains(frag, "autoindex on;"))
	ck("片段含 expires 30d", strings.Contains(frag, "expires 30d;"))
	ck("片段含独立访问日志", strings.Contains(frag, rec.ID+".access.log"))
	ck("片段含 X-Frame-Options", strings.Contains(frag, "X-Frame-Options SAMEORIGIN"))

	var subCode int
	var subBody string
	ok = poll(4000, func() bool {
		c, _, b, err := resp(base+"/sub/", false)
		subCode, subBody = c, b
		return err == nil && c == 200 && strings.Contains(b, "a.txt")
	})
	ck("目录索引开启 → 列表 200 且含文件链接", ok, subCode, subBody)
	logPath := filepath.Join(mgr.InstanceDirPath(web.ID), "logs", "sites", rec.ID+".access.log")
	ck("访问日志已落盘", poll(3000, func() bool {
		st, err := os.Stat(logPath)
		return err == nil && st.Size() > 0
	}), logPath)

	fmt.Println("== 4) 安全配置：隐藏文件 / 敏感后缀 / 禁执行目录 / 跨站头")
	opts.DenyHidden = true
	opts.DenyFiles = "sql, bak"
	opts.DenyExecDirs = "uploads"
	applyUpd(sites, rec.ID, port, root, php.ID, opts)
	code, _, _, _ = resp(base+"/.secret", false)
	ck("隐藏文件被拒 403", code == 403, code)
	code, _, _, _ = resp(base+"/dump.sql", false)
	ck("敏感后缀被拒 403", code == 403, code)
	code, _, body, _ = resp(base+"/uploads/shell.php", false)
	ck("禁执行目录内 php 被拒 403", code == 403 && !strings.Contains(body, "pwn"), code, body)
	code, xhdr, _, _ := resp(base+"/", false)
	ck("跨站隔离响应头存在", xhdr.Get("X-Frame-Options") == "SAMEORIGIN", xhdr.Get("X-Frame-Options"))
	code, _, _, _ = resp(base+"/app.css", false)
	ck("静态资源仍可访问", code == 200, code)

	fmt.Println("== 5) 切换为静态站点（PHP 站点下 404 会被 index.php 接管，错误页永不触发）")
	applyUpd(sites, rec.ID, port, root, "", opts)
	code, _, body, _ = resp(base+"/no-such-page", false)
	ck("静态站点未命中 → 404", code == 404, code)
	ck("静态化后片段不再含 fastcgi", !strings.Contains(readFragment(mgr, web.ID, rec.ID), "fastcgi_pass"))

	fmt.Println("== 6) 错误页面：生成 + 命中")
	opts.ErrorPages = "404 /404.html\n403 /403.html\n500 /500.html"
	applyUpd(sites, rec.ID, port, root, "", opts)
	made, err := sites.CreateErrorPages(rec.ID)
	ck("生成错误页文件 3 个", err == nil && len(made) == 3, len(made), err)
	code, _, body, _ = resp(base+"/no-such-page", false)
	ck("404 命中自定义错误页", code == 404 && strings.Contains(body, "由 对号 DUIHAO 生成"), code)
	code, _, body, _ = resp(base+"/.secret", false)
	ck("403 命中自定义错误页", code == 403 && strings.Contains(body, "禁止访问"), code)

	fmt.Println("== 7) 伪静态")
	opts.Rewrite = "try_files $uri $uri/ /index.html;"
	applyUpd(sites, rec.ID, port, root, "", opts)
	code, _, body, _ = resp(base+"/pretty/url/here", false)
	ck("伪静态回落到 index.html", code == 200 && strings.Contains(body, "M10-HOME"), code)

	fmt.Println("== 8) 301 全站跳转")
	opts.RedirectTo = "http://target.example"
	applyUpd(sites, rec.ID, port, root, "", opts)
	code, thdr, _, _ := resp(base+"/any/path", false)
	loc := thdr.Get("Location")
	ck("301 跳转生效", code == 301 && strings.Contains(loc, "target.example"), code, loc)
	opts.RedirectTo = ""
	applyUpd(sites, rec.ID, port, root, "", opts)

	fmt.Println("== 9) IP 白名单")
	opts.AllowIPs = "10.0.0.0/8"
	applyUpd(sites, rec.ID, port, root, "", opts)
	code, _, _, _ = resp(base+"/", false)
	ck("不在白名单 → 403", code == 403, code)
	opts.AllowIPs = "127.0.0.1"
	applyUpd(sites, rec.ID, port, root, "", opts)
	code, _, _, _ = resp(base+"/", false)
	ck("在白名单 → 200", code == 200, code)
	opts.AllowIPs = ""
	applyUpd(sites, rec.ID, port, root, "", opts)

	fmt.Println("== 10) 校验与回滚")
	if err := sites.Update(rec.ID, site.UpdateOpts{
		Domains: []string{"m10.test"}, Port: wport, Root: root, PHP: php.ID,
	}); err == nil {
		ck("撞 nginx 实例端口被拒", false)
	} else {
		ck("撞 nginx 实例端口被拒", true)
	}
	// 非法 allow 地址：nginx -t 会拒绝 → Update 应报错并回滚
	bad := site.Options{AllowIPs: "300.300.300.300"}
	errBefore := sites.Update(rec.ID, site.UpdateOpts{
		Domains: []string{"m10.test"}, Port: port, Root: root, PHP: php.ID, Options: bad,
	})
	ck("非法 allow 地址被 nginx 拒绝", errBefore != nil, errBefore)
	code, _, body, _ = resp(base+"/", false)
	ck("回滚后站点仍可用", code == 200 && strings.Contains(body, "M10-HOME"), code)
	frag = readFragment(mgr, web.ID, rec.ID)
	ck("回滚后片段不含非法地址", !strings.Contains(frag, "300.300.300.300"))

	if os.Getenv("KEEP") != "" {
		fmt.Println("KEEP=1 → 保留现场，不停止不删除")
		fmt.Printf("现场: %s\n", mgr.InstanceDirPath(web.ID))
		return
	}
	fmt.Println("== 11) 收尾：停止 + 删除（根目录保留）")
	_ = sites.Stop(rec.ID)
	if err := sites.Delete(rec.ID); err != nil {
		ck("删除站点", false, err)
	} else {
		ck("删除站点", true)
	}
	if _, err := os.Stat(filepath.Join(root, "index.html")); err == nil {
		ck("删除站点后根目录文件保留", true)
	} else {
		ck("删除站点后根目录文件保留", false, err)
	}

	if fails > 0 {
		fmt.Printf("M10-FAILED fails=%d\n", fails)
		os.Exit(10)
	}
	fmt.Println("M10-DONE all pass")
}

// poll 轮询到条件成立（最多 timeoutMs）。用于"配置已生效"类断言：
// nginx reload 是异步的，旧 worker 可能还在服务，刷新一次就好——断言应容忍这个窗口。
func poll(timeoutMs int, fn func() bool) bool {
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for {
		if fn() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// readBody 取正文（断言用，失败返回空串）。
func readBody(url string) string {
	_, _, body, err := resp(url, false)
	if err != nil {
		return ""
	}
	return body
}

// applyUpd Update 并断言成功（失败直接打印）。
func applyUpd(s *site.Manager, id string, port int, root, phpID string, o site.Options) {
	if err := s.Update(id, site.UpdateOpts{
		Domains: []string{"m10.test", "www.m10.test"}, Port: port, Root: root, PHP: phpID, Options: o,
	}); err != nil {
		fmt.Println("  UPDATE-ERR:", err)
		fails++
	}
	// nginx reload 是异步的：新 worker 起来需要时间，旧 worker 仍可能在服务。
	// 这里等 1s 再断言，用于区分「配置写错」与「reload 还没生效」。
	time.Sleep(time.Second)
}

// readFragment 读站点渲染出的 nginx 片段。
func readFragment(mgr *instance.Manager, webID, siteID string) string {
	b, err := mgr.ReadInstanceFile(webID, "sites/"+siteID+".conf")
	if err != nil {
		return ""
	}
	return string(b)
}

func must(err error) {
	if err != nil {
		fmt.Println("FS-ERR:", err)
		os.Exit(8)
	}
}
