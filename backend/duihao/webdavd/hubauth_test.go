package webdavd

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/hubusers"
)

// NAS 多用户端到端：引导 admin → admin 进应用页 → 建成员 → 成员按权限进出
// → 登录页先查 NAS 账号 → ?_k= 直登票（本机回环）。
func TestHubMultiUser(t *testing.T) {
	var srv *Server
	var base string
	{
		root := t.TempDir()
		user, pass := "tester", "secret-pass"
		hash, err := HashPassword(pass)
		if err != nil {
			t.Fatal(err)
		}
		srv, err = New(Config{
			Bind: "127.0.0.1", Port: 0, InstanceDir: t.TempDir(),
			Shares: []Share{{ID: "sh", User: user, PassHash: hash, Root: root, Enabled: true}},
			MaxConns: 8, MaxBodyMB: 8,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = srv.Stop() })
		base = "http://" + srv.Addr() // 直连 cli.Do 用；doReq 另拼前缀的地方传 baseHost
	}
	cli := newTestClient(t, false)

	// 0) 引导的 admin 固定叫 "admin"（随机密码）；测试用与共享同名的管理员
	//    账号走 Basic 凭据（既中共享又中 NAS 账号）。
	if st := srv.hub(); st != nil {
		if err := hubusers.Create(st.DB(), "tester", "secret-pass", hubusers.RoleAdmin, nil); err != nil {
			t.Fatal(err)
		}
	}

	// 1) admin 进应用页：Basic 直进收藏夹首页。
	r := doReq(t, cli, srv.Addr(), "GET", "/-home", "tester", "secret-pass", nil)
	page, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 {
		t.Fatalf("admin 应能进 /-home, got %d %s", r.StatusCode, page)
	}
	if !strings.Contains(string(page), `data-role="admin"`) {
		t.Fatalf("页面应带 data-role=admin: %s", page)
	}

	// 2) 建成员：notes 只读。成员能进 notes、进不了 ledger、写不了 notes。
	{
		if err := hubusers.Create(srv.hub().DB(), "xiaomei", "pass66",
			hubusers.RoleMember, map[string]string{"notes": "r"}); err != nil {
			t.Fatalf("建成员: %v", err)
		}
	}

	// 成员登录（登录页先查 NAS 账号）→ 拿 Cookie。
	req, _ := http.NewRequest("POST", base+"/-login",
		strings.NewReader(`{"u":"xiaomei","p":"pass66"}`))
	req.Header.Set("Content-Type", "application/json")
	lr, err := cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, lr.Body)
	lr.Body.Close()
	if lr.StatusCode != 200 {
		t.Fatalf("成员登录应成功, got %d", lr.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range lr.Cookies() {
		if c.Name == sessCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("登录应签会话 Cookie")
	}

	getWithCookie := func(path string) *http.Response {
		req, _ := http.NewRequest("GET", base+path, nil)
		req.AddCookie(cookie)
		r, err := cli.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Body.Close() })
		return r
	}
	if r = getWithCookie("/-notes"); r.StatusCode != 200 {
		t.Fatalf("成员应能进 notes, got %d", r.StatusCode)
	}
	if r = getWithCookie("/-ledger"); r.StatusCode != 403 {
		t.Fatalf("成员无权限应 403, got %d", r.StatusCode)
	}
	if r = getWithCookie("/-users"); r.StatusCode != 403 {
		t.Fatalf("成员应进不了用户管理, got %d", r.StatusCode)
	}
	// 只读成员的写动作 → 403（权限收口在 appkit）。
	{
		req, _ := http.NewRequest("POST", base+"/-api/notes/cat.save",
			strings.NewReader(`{"name":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		r, err := cli.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		if r.StatusCode != 403 {
			t.Fatalf("只读成员写动作应 403, got %d", r.StatusCode)
		}
	}

	// 3) ?_k= 直登票：本机回环 GET 带票 → 302 种 Cookie。
	// 不跟随重定向（newTestClient 没有 CookieJar，跟着走会丢 Cookie 变 401）。
	noredir := &http.Client{Transport: cli.Transport, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	tok, err := IssueEntryToken(srv.cfg.InstanceDir, "tester", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest("GET", base+"/-home?_k="+tok, nil)
	krr, err := noredir.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, krr.Body)
	krr.Body.Close()
	if krr.StatusCode != http.StatusFound || !strings.HasPrefix(krr.Header.Get("Location"), "/-home") {
		t.Fatalf("直登票应 302 到干净地址, got %d %q", krr.StatusCode, krr.Header.Get("Location"))
	}
	hasCookie := false
	for _, c := range krr.Cookies() {
		if c.Name == sessCookie && c.Value != "" {
			hasCookie = true
		}
	}
	if !hasCookie {
		t.Fatal("直登票应种会话 Cookie")
	}

	// 4) 坏票：不拦请求，剥掉参数重走（302 到登录页路径）。
	req, _ = http.NewRequest("GET", base+"/-home?_k=bad-token", nil)
	br, err := noredir.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, br.Body)
	br.Body.Close()
	if br.StatusCode != http.StatusFound || !strings.Contains(br.Header.Get("Location"), "/-home") {
		t.Fatalf("坏票应剥参数重走, got %d %q", br.StatusCode, br.Header.Get("Location"))
	}

	// 5) 网盘（文件管理器）按 files 权限分档：
	//    admin/读写 → 正常页；只读 → 页面 w:false（编辑/删除按钮不生成）；
	//    显式无权限（files:""）→ 无权限页；搜索/写方法照样拒。
	{
		if err := hubusers.Create(srv.hub().DB(), "readman", "pass77",
			hubusers.RoleMember, map[string]string{"files": "r"}); err != nil {
			t.Fatalf("建只读成员: %v", err)
		}
		if err := hubusers.Create(srv.hub().DB(), "nofs", "pass88",
			hubusers.RoleMember, map[string]string{"files": "", "notes": "rw"}); err != nil {
			t.Fatalf("建无网盘成员: %v", err)
		}
		login := func(u, p string) *http.Cookie {
			req, _ := http.NewRequest("POST", base+"/-login",
				strings.NewReader(`{"u":"`+u+`","p":"`+p+`"}`))
			req.Header.Set("Content-Type", "application/json")
			r, err := cli.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
			if r.StatusCode != 200 {
				t.Fatalf("%s 登录应成功, got %d", u, r.StatusCode)
			}
			for _, c := range r.Cookies() {
				if c.Name == sessCookie {
					return c
				}
			}
			t.Fatalf("%s 登录未签会话 Cookie", u)
			return nil
		}
		get := func(c *http.Cookie, path string) (int, string) {
			req, _ := http.NewRequest("GET", base+path, nil)
			req.AddCookie(c)
			r, err := cli.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(r.Body)
			r.Body.Close()
			return r.StatusCode, string(body)
		}
		// admin：正常文件页，可写。
		ac := login("tester", "secret-pass")
		if code, body := get(ac, "/"); code != 200 || !strings.Contains(body, `"w":true`) {
			t.Fatalf("admin 网盘页应可写, got %d", code)
		}
		// 只读成员：页面 200 但 w:false；顶栏显示 NAS 用户名而不是共享账号名。
		rc := login("readman", "pass77")
		code, body := get(rc, "/")
		if code != 200 || !strings.Contains(body, `"w":false`) {
			t.Fatalf("只读成员网盘页应 w:false, got %d", code)
		}
		if !strings.Contains(body, ">readman<") || strings.Contains(body, ">tester<") {
			t.Fatalf("顶栏应显示 NAS 用户名 readman: %s", body[:400])
		}
		// 只读成员写方法 → 403（文件层闸门）。
		if req, _ := http.NewRequest("DELETE", base+"/x.txt", nil); true {
			req.AddCookie(rc)
			r, err := cli.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
			if r.StatusCode != 403 {
				t.Fatalf("只读成员删除应 403, got %d", r.StatusCode)
			}
		}
		// 显式无权限成员：网盘页 = 无权限页；zip/搜索/写方法全拒。
		nc := login("nofs", "pass88")
		for _, path := range []string{"/", "/?zip=1"} {
			code, body := get(nc, path)
			if code != 200 || !strings.Contains(body, "无权限") || !strings.Contains(body, "np-card") {
				t.Fatalf("无权限成员 %s 应出无权限页, got %d", path, code)
			}
		}
		if code, _ := get(nc, "/-search?q=a"); code != 403 {
			t.Fatalf("无权限成员搜索应 403, got %d", code)
		}
		if req, _ := http.NewRequest("DELETE", base+"/x.txt", nil); true {
			req.AddCookie(nc)
			r, err := cli.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, r.Body)
			r.Body.Close()
			if r.StatusCode != 403 {
				t.Fatalf("无权限成员删除应 403, got %d", r.StatusCode)
			}
		}
	}
}
