package webdavd

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// 下载 HTTP 层：列表 / 加任务（含非法地址）/ 暂停 / 继续 / 删除 / 设置 / 只读拦截。
// 与监控同一套前提：**测试里不下载 aria2**，所以组件停在 missing，
// 加任务会「存下记录 + 给 warn」，引擎相关动作返回 409 —— 正好验证这条设计。
func TestDownloadAPI(t *testing.T) {
	_, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = t.TempDir() })
	cli := newTestClient(t, false)

	post := func(action, body string) (int, map[string]any) {
		r := doReq(t, cli, base, "POST", "/-api/download/"+action, user, pass, []byte(body))
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return r.StatusCode, m
	}
	get := func(action string) (int, map[string]any) {
		r := doReq(t, cli, base, "POST", "/-api/download/"+action, user, pass, []byte("{}"))
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return r.StatusCode, m
	}

	// 1) 空列表：结构齐备
	code, res := get("list")
	if code != 200 {
		t.Fatalf("list 应 200, got %d %v", code, res)
	}
	items, _ := res["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("初始应为空队列, got %d", len(items))
	}
	comp, _ := res["comp"].(map[string]any)
	if st, _ := comp["state"].(string); st != "missing" && st != "ready" {
		t.Fatalf("组件状态异常: %v", comp["state"])
	}
	if _, ok := res["setting"].(map[string]any); !ok {
		t.Fatal("list 应带 setting")
	}
	if _, ok := res["disk"].(map[string]any); !ok {
		t.Fatal("list 应带 disk")
	}

	// 2) 本地路径/未知协议一律拒（下载器不该能读本机文件）
	for _, bad := range []string{"file:///c:/windows/win.ini", `{"urls":["C:\\\\x\\\\y.iso"]}`} {
		body := bad
		if bad[0] != '{' {
			body = `{"urls":["` + bad + `"]}`
		}
		if code, _ := post("add", body); code != 400 {
			t.Fatalf("%s 应 400, got %d", bad, code)
		}
	}

	// 3) 合法直链：任务要存下来（组件没装也不该失败，只能给 warn）
	code, res = post("add", `{"urls":["https://mirror.example.com/iso/ubuntu-24.04.iso"],"dir":"video"}`)
	if code != 200 {
		t.Fatalf("add 应 200, got %d %v", code, res)
	}
	if n, _ := res["n"].(float64); n != 1 {
		t.Fatalf("应加入 1 个任务, got %v", res["n"])
	}
	if _, ok := res["warn"].(string); !ok {
		t.Logf("提示：组件已装，本次没有 warn（%v）", res)
	}

	// 4) 列表里能看到：落点是 video、状态排队中、名字从 URL 猜出来
	_, res = get("list")
	items, _ = res["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("应有 1 条任务, got %d", len(items))
	}
	task, _ := items[0].(map[string]any)
	if task["dir"] != "video" {
		t.Errorf("落点应 video, got %v", task["dir"])
	}
	if task["status"] != "waiting" && task["status"] != "active" && task["status"] != "error" {
		t.Errorf("状态异常: %v", task["status"])
	}
	if task["kind"] != "http" {
		t.Errorf("类型应 http, got %v", task["kind"])
	}
	if name, _ := task["name"].(string); name != "ubuntu-24.04.iso" {
		t.Errorf("名字应从 URL 猜出，got %q", name)
	}
	id, _ := task["id"].(string)
	if id == "" {
		t.Fatal("任务应有 id")
	}

	// 5) 重复添加同一个地址应被挡（队列里已有未完成的同一个 url）
	if code, _ := post("add", `{"urls":["https://mirror.example.com/iso/ubuntu-24.04.iso"]}`); code != 400 {
		t.Fatalf("重复地址应 400, got %d", code)
	}

	// 6) 暂停：不需要引擎也能落状态
	code, res = post("pause", `{"id":"`+id+`"}`)
	if code != 200 {
		t.Fatalf("pause 应 200, got %d %v", code, res)
	}
	if res["state"] != "paused" {
		t.Errorf("应 paused, got %v", res["state"])
	}

	// 7) 继续：组件没装时明确告诉用户去装，而不是静默失败
	code, res = post("resume", `{"id":"`+id+`"}`)
	if code == 200 {
		t.Logf("组件已就绪，resume 直接成功（state=%v）", res["state"])
	} else if code != 409 {
		t.Fatalf("缺组件时 resume 应 409, got %d %v", code, res)
	}

	// 8) 设置：越界值要被夹回合法范围
	code, res = post("setting.save", `{"maxConc":99,"downLimit":-3,"upLimit":0,"btNet":false,"seed":false,"autoDir":"../etc"}`)
	if code != 200 {
		t.Fatalf("setting.save 应 200, got %d %v", code, res)
	}
	st, _ := res["setting"].(map[string]any)
	if st["maxConc"].(float64) != 3 {
		t.Errorf("并发越界应夹回 3, got %v", st["maxConc"])
	}
	if st["autoDir"] != "download" {
		t.Errorf("非法落点应回落 download, got %v", st["autoDir"])
	}
	if st["btNet"] != false {
		t.Errorf("btNet 应保存为 false, got %v", st["btNet"])
	}

	// 9) 删除：任务消失
	if code, res := post("remove", `{"id":"`+id+`"}`); code != 200 {
		t.Fatalf("remove 应 200, got %d %v", code, res)
	}
	_, res = get("list")
	if items, _ := res["items"].([]any); len(items) != 0 {
		t.Fatalf("删除后应为空, got %d", len(items))
	}

	// 10) 未知动作 404
	if code, _ := post("nope", `{}`); code != 404 {
		t.Fatalf("未知动作应 404, got %d", code)
	}
}

// 只读账号：页面能看，写动作一律拦住。
func TestDownloadReadOnly(t *testing.T) {
	_, base, user, pass := startTestServer(t, func(c *Config) {
		c.InstanceDir = t.TempDir()
		c.Shares[0].ReadOnly = true
	})
	cli := newTestClient(t, false)

	r := doReq(t, cli, base, "GET", "/-download", user, pass, nil)
	if r.StatusCode != 200 {
		t.Fatalf("只读账号仍应能看下载页, got %d", r.StatusCode)
	}
	_ = r.Body.Close()

	r = doReq(t, cli, base, "POST", "/-api/download/add", user, pass,
		[]byte(`{"urls":["https://x/y.iso"]}`))
	if r.StatusCode != 405 {
		t.Fatalf("只读账号加任务应 405, got %d", r.StatusCode)
	}
	_ = r.Body.Close()
}

// 页面资源齐备：脚本必须自己引，少一行就是白页。
func TestDownloadPageAssets(t *testing.T) {
	_, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = t.TempDir() })
	cli := newTestClient(t, false)

	r := doReq(t, cli, base, "GET", "/-download", user, pass, nil)
	raw, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	body := string(raw)

	for _, want := range []string{
		`data-app="download"`,
		`id="d-urls"`, `id="d-dir"`, `id="d-add"`, `id="d-list"`, `id="d-setup"`,
		`id="d-mask"`, `id="d-maxconc"`,
		`/-ui/apps/download.js`,
		"对号不提供搜索、索引或任何种子源",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("下载页缺少 %q", want)
		}
	}

	// 脚本本体也要能取到
	r = doReq(t, cli, base, "GET", "/-ui/apps/download.js", user, pass, nil)
	js, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if r.StatusCode != 200 || len(js) < 3000 {
		t.Fatalf("download.js 取不到或不完整: %d / %d 字节", r.StatusCode, len(js))
	}
}
