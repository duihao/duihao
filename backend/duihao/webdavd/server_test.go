package webdavd

import (
	"archive/zip"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 服务集成测试：起真实监听，走完整 HTTP 管线（auth → 路径守卫 → webdav）。
// 测试客户端必须 Proxy: nil —— Go http.Client 默认走环境代理会把 127.0.0.1
// 劫持到沙箱代理（工程已知坑）。
func newTestClient(t *testing.T, useTLS bool) *http.Client {
	transport := &http.Transport{Proxy: nil}
	if useTLS {
		// 自签证书：测试里不校验（真实客户端会提示一次，与站点 HTTPS 同语义）。
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}
}

func startTestServer(t *testing.T, mutate func(*Config)) (*Server, string, string, string) {
	t.Helper()
	root := t.TempDir()
	user, pass := "tester", "secret-pass"
	hash, err := HashPassword(pass)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Bind: "127.0.0.1",
		Port: 0,
		Shares: []Share{{
			ID: "sh-test", User: user, PassHash: hash,
			Name: "测试盘", Root: root, Enabled: true,
		}},
		MaxConns:  8,
		MaxBodyMB: 8,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	// Port 0 → 从 listener 回读实际端口。
	addr := srv.Addr()
	if addr == "" {
		t.Fatal("listener addr empty")
	}
	_, port, _ := net.SplitHostPort(addr)
	return srv, "127.0.0.1:" + port, user, pass
}

func doReq(t *testing.T, cli *http.Client, base, method, path, user, pass string, body []byte) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, fmt.Sprintf("http://%s%s", base, path), rdr)
	if err != nil {
		t.Fatal(err)
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestWebDAVE2E(t *testing.T) {
	_, base, user, pass := startTestServer(t, nil)
	cli := newTestClient(t, false)

	// 1) 无凭据 → 401
	if r := doReq(t, cli, base, "PROPFIND", "/", "", "", nil); r.StatusCode != 401 {
		t.Fatalf("无凭据应 401, got %d", r.StatusCode)
	}
	// 2) 错密码 → 401
	if r := doReq(t, cli, base, "PROPFIND", "/", user, "wrong", nil); r.StatusCode != 401 {
		t.Fatalf("错密码应 401, got %d", r.StatusCode)
	}
	// 3) 不存在的用户 → 401（且耗时应对齐，这里只验状态码）
	if r := doReq(t, cli, base, "PROPFIND", "/", "nobody", "x", nil); r.StatusCode != 401 {
		t.Fatalf("不存在用户应 401, got %d", r.StatusCode)
	}
	// 4) PUT 中文名
	body := []byte("对号网盘内容 hello")
	if r := doReq(t, cli, base, "PUT", "/中文文件.txt", user, pass, body); r.StatusCode != 201 {
		t.Fatalf("PUT 中文名应 201, got %d", r.StatusCode)
	}
	// 5) GET 回读
	r := doReq(t, cli, base, "GET", "/中文文件.txt", user, pass, nil)
	got, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 || !bytes.Equal(got, body) {
		t.Fatalf("GET 回读不符: %d %q", r.StatusCode, got)
	}
	// 6) PROPFIND 列表
	if r = doReq(t, cli, base, "PROPFIND", "/", user, pass, nil); r.StatusCode != 207 {
		t.Fatalf("PROPFIND 应 207, got %d", r.StatusCode)
	}
	// 6b) GET 目录 → 200 HTML 列表（浏览器只有 GET，回归「登录进去啥也没有」）
	r = doReq(t, cli, base, "GET", "/", user, pass, nil)
	page, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 {
		t.Fatalf("GET 目录应 200, got %d", r.StatusCode)
	}
	if !bytes.Contains(page, []byte("中文文件.txt")) {
		t.Fatalf("GET 目录列表应含中文文件名: %q", page)
	}
	// 7) 穿越路径 → 403
	if r = doReq(t, cli, base, "GET", "/../secret.txt", user, pass, nil); r.StatusCode != 403 {
		t.Fatalf("穿越应 403, got %d", r.StatusCode)
	}
	// 8) 保留名 → 403
	if r = doReq(t, cli, base, "PUT", "/CON", user, pass, []byte("x")); r.StatusCode != 403 {
		t.Fatalf("保留名应 403, got %d", r.StatusCode)
	}
	// 9) DELETE
	if r = doReq(t, cli, base, "DELETE", "/中文文件.txt", user, pass, nil); r.StatusCode != 204 {
		t.Fatalf("DELETE 应 204, got %d", r.StatusCode)
	}
}

func TestReadOnlyShare(t *testing.T) {
	_, base, user, pass := startTestServer(t, func(c *Config) {
		c.Shares[0].ReadOnly = true
	})
	cli := newTestClient(t, false)
	if r := doReq(t, cli, base, "PUT", "/x.txt", user, pass, []byte("x")); r.StatusCode != 405 {
		t.Fatalf("只读共享 PUT 应 405, got %d", r.StatusCode)
	}
	if r := doReq(t, cli, base, "PROPFIND", "/", user, pass, nil); r.StatusCode != 207 {
		t.Fatalf("只读共享 PROPFIND 应 207, got %d", r.StatusCode)
	}
}

func TestNoPlainHTTPOnLAN(t *testing.T) {
	hash, _ := HashPassword("x")
	_, err := New(Config{
		Bind: "0.0.0.0", Port: 19111,
		Shares: []Share{{ID: "s", User: "u", PassHash: hash, Root: t.TempDir(), Enabled: true}},
	})
	if err == nil {
		t.Fatal("0.0.0.0 + 无 TLS 应拒绝启动（v3.1 硬规则）")
	}
}

func TestShareRootExpansion(t *testing.T) {
	dir := t.TempDir()
	hash, _ := HashPassword("p")
	cfg := Config{
		Bind: "127.0.0.1", Port: 0, InstanceDir: dir,
		Shares: []Share{{ID: "s", User: "u", PassHash: hash, Root: "{dir}/dav", Enabled: true}},
	}
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	if _, err := os.Stat(filepath.Join(dir, "dav")); err != nil {
		t.Fatalf("{dir}/dav 未被创建: %v", err)
	}
}

func TestLoginSessionAndZip(t *testing.T) {
	_, base, user, pass := startTestServer(t, nil)
	cli := newTestClient(t, false)

	// 1) 未认证浏览器导航 → 登录页（200，而非 401 弹窗）
	req, _ := http.NewRequest("GET", "http://"+base+"/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	r, err := cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if r.StatusCode != 200 || !bytes.Contains(page, []byte("登 录")) {
		t.Fatalf("未认证浏览器导航应给登录页 200, got %d", r.StatusCode)
	}

	// 2) 非浏览器导航未认证 → 401 challenge（WebDAV 客户端语义不变）
	if r = doReq(t, cli, base, "GET", "/", "", "", nil); r.StatusCode != 401 {
		t.Fatalf("未认证 API GET 应 401, got %d", r.StatusCode)
	}

	// 3) 登录错密码 → 401 JSON
	if r = doReq(t, cli, base, "POST", "/-login", "", "",
		[]byte(`{"u":"tester","p":"wrong"}`)); r.StatusCode != 401 {
		t.Fatalf("登录错密码应 401, got %d", r.StatusCode)
	}

	// 4) 登录成功 → Set-Cookie
	r = doReq(t, cli, base, "POST", "/-login", "", "",
		[]byte(`{"u":"tester","p":"secret-pass"}`))
	if r.StatusCode != 200 {
		t.Fatalf("登录应 200, got %d", r.StatusCode)
	}
	var ck *http.Cookie
	for _, c := range r.Cookies() {
		if c.Name == sessCookie {
			ck = c
		}
	}
	if ck == nil || ck.Value == "" {
		t.Fatal("登录成功应签发会话 Cookie")
	}
	if !ck.HttpOnly {
		t.Fatal("会话 Cookie 必须 HttpOnly")
	}

	// 5) Cookie 访问目录 → 200 管理页
	req2, _ := http.NewRequest("GET", "http://"+base+"/", nil)
	req2.Header.Set("Accept", "text/html")
	req2.AddCookie(ck)
	r, err = cli.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(r.Body)
	_ = r.Body.Close()
	if r.StatusCode != 200 || !bytes.Contains(page, []byte("davdata")) {
		t.Fatalf("Cookie 访问应得管理页 200, got %d", r.StatusCode)
	}

	// 6) 伪造 Cookie → 拒绝
	req3, _ := http.NewRequest("GET", "http://"+base+"/", nil)
	req3.Header.Set("Accept", "text/html")
	req3.AddCookie(&http.Cookie{Name: sessCookie, Value: "fake.sig"})
	r, err = cli.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("伪造 Cookie 的浏览器导航应回登录页 200, got %d", r.StatusCode)
	}

	// 7) Basic Auth 与 Cookie 并存可用（WebDAV 客户端不受影响）
	if r = doReq(t, cli, base, "PROPFIND", "/", user, pass, nil); r.StatusCode != 207 {
		t.Fatalf("Basic PROPFIND 应 207, got %d", r.StatusCode)
	}

	// 8) 准备目录与文件，测 zip 打包
	if r = doReq(t, cli, base, "MKCOL", "/打包目录", user, pass, nil); r.StatusCode != 201 {
		t.Fatalf("MKCOL 应 201, got %d", r.StatusCode)
	}
	if r = doReq(t, cli, base, "PUT", "/打包目录/内文件.txt", user, pass, []byte("zip 内容")); r.StatusCode != 201 {
		t.Fatalf("PUT 应 201, got %d", r.StatusCode)
	}

	// 9) zip 下载（Basic 与 Cookie 各来一次）
	r = doReq(t, cli, base, "GET", "/打包目录/?zip=1", user, pass, nil)
	zipBody, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if r.StatusCode != 200 || len(zipBody) < 100 ||
		!bytes.Contains(zipBody, []byte("PK")) {
		t.Fatalf("zip 下载异常: %d len=%d", r.StatusCode, len(zipBody))
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBody), int64(len(zipBody)))
	if err != nil {
		t.Fatalf("zip 解析失败: %v", err)
	}
	found := false
	for _, f := range zr.File {
		if f.Name == "内文件.txt" {
			found = true
		}
	}
	if !found {
		t.Fatal("zip 应包含 内文件.txt")
	}

	// 10) 登出：清 Cookie；带假 Basic 的登出请求应 200（冲浏览器缓存的凭据）
	req4, _ := http.NewRequest("GET", "http://"+base+"/-logout", nil)
	req4.Header.Set("Authorization", "Basic bG9nb3V0OmxvZ291dA==")
	r, err = cli.Do(req4)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("登出应 200, got %d", r.StatusCode)
	}
	for _, c := range r.Cookies() {
		if c.Name == sessCookie && c.MaxAge != -1 {
			t.Fatal("登出应下发过期 Cookie")
		}
	}

	// 11) token 往返与防伪造
	tok := issueToken([]byte("key"), "tester")
	if u, err := verifyToken([]byte("key"), tok); err != nil || u != "tester" {
		t.Fatalf("token 往返失败: %v %q", err, u)
	}
	if _, err := verifyToken([]byte("other"), tok); err == nil {
		t.Fatal("密钥不同应校验失败")
	}
}
