package webdavd

// 主题跟随（M24）的守卫：桌面端改外观设置后，NAS 网页要立刻跟着明暗切换。
//
// 三件事各自容易被改坏：
//   ① 映射：前端的配色名（deep-blue / duihao-red / duihao-light）要收成
//      dark / light / system —— 网页这边只认明暗两档；
//   ② 首帧：<html data-theme> 由服务端渲染，打开就是对的（不然先白后黑闪）；
//   ③ 增量：SSE 端点能建连、能推当前值（已打开的页面靠它跟着变）。

import (
	"bufio"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestThemeMapping(t *testing.T) {
	cases := map[string]string{
		"dark":         "dark",
		"deep-blue":    "dark",
		"duihao-red":   "dark",
		"light":        "light",
		"duihao-light": "light",
		"system":       "system",
		"":             "system",
		"nonsense":     "system", // 脏值当跟随系统，比硬塞一个深色底安全
	}
	defer SetAppTheme("system")
	for in, want := range cases {
		SetAppTheme(in)
		if got := ThemeAttr(); got != want {
			t.Fatalf("SetAppTheme(%q) → ThemeAttr()=%q, want %q", in, got, want)
		}
	}
}

func TestThemeRendersIntoPages(t *testing.T) {
	instanceDir := t.TempDir()
	_, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = instanceDir })
	cli := newTestClient(t, false)
	defer SetAppTheme("system")

	for _, tc := range []struct{ theme, attr string }{
		{"dark", "dark"},
		{"light", "light"},
		{"duihao-red", "dark"},
	} {
		SetAppTheme(tc.theme)
		// 应用页（模板渲染）与文件管理页（Go 拼 HTML）两条路都要带上
		for _, path := range []string{"/-home", "/"} {
			r := doReq(t, cli, base, "GET", path, user, pass, nil)
			b, _ := io.ReadAll(r.Body)
			body := string(b)
			if r.StatusCode != http.StatusOK {
				t.Fatalf("%s 应 200, got %d", path, r.StatusCode)
			}
			want := `data-theme="` + tc.attr + `"`
			if !strings.Contains(body, want) {
				t.Fatalf("主题 %s 下 %s 缺少 %s", tc.theme, path, want)
			}
			// 已打开的页面靠这个脚本跟着变
			if !strings.Contains(body, "/-ui/theme.js") {
				t.Fatalf("%s 没引 theme.js（页面切主题不会跟着变）", path)
			}
		}
	}
}

func TestThemeAPIAndStream(t *testing.T) {
	instanceDir := t.TempDir()
	_, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = instanceDir })
	cli := newTestClient(t, false)
	defer SetAppTheme("system")

	SetAppTheme("deep-blue")
	r := doReq(t, cli, base, "GET", "/-api/theme", user, pass, nil)
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("/-api/theme 应 200, got %d", r.StatusCode)
	}
	if !strings.Contains(string(b), `"theme":"dark"`) {
		t.Fatalf("/-api/theme 返回值不对: %s", string(b))
	}

	// SSE：建连先收到当前值，改主题后再收到一条
	req, err := http.NewRequest("GET", "http://"+base+"/-api/theme/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(user, pass)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("SSE 建连失败: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("SSE Content-Type 应 text/event-stream, got %q", ct)
	}
	br := bufio.NewReader(resp.Body)
	// 首条消息（跳过 retry: / 注释行）
	first := ""
	for i := 0; i < 6 && first == ""; i++ {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("读取 SSE 失败: %v", err)
		}
		if strings.HasPrefix(line, "data: ") {
			first = line
		}
	}
	if !strings.Contains(first, `"theme":"dark"`) {
		t.Fatalf("SSE 首条应是当前主题 dark, got %q", first)
	}

	SetAppTheme("light")
	got := ""
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && got == "" {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("读取 SSE 增量失败: %v", err)
		}
		if strings.HasPrefix(line, "data: ") {
			got = line
		}
	}
	if !strings.Contains(got, `"theme":"light"`) {
		t.Fatalf("切到 light 后 SSE 应推 light, got %q", got)
	}
}

func TestThemeStaticServed(t *testing.T) {
	instanceDir := t.TempDir()
	_, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = instanceDir })
	cli := newTestClient(t, false)
	r := doReq(t, cli, base, "GET", "/-ui/theme.js", user, pass, nil)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("/-ui/theme.js 应 200, got %d（embed 列表漏了？）", r.StatusCode)
	}
}
