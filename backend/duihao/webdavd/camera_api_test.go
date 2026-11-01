package webdavd

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// 监控 HTTP 层：组件状态 / 品牌模板 / 新建相机（模板拼地址）/ 非法地址 / 列表 /
// 片段查询 / 路径穿越 / 删除 / 只读账号拦截。
// 注意：这些用例**不下载 ffmpeg**（80MB 不能进测试），所以 comp 停留在 missing，
// 录像是「存下配置但不启动」——正好验证「缺组件也能把相机配好」这条设计。
func TestCameraAPI(t *testing.T) {
	_, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = t.TempDir() })
	cli := newTestClient(t, false)

	post := func(action, body string) (int, map[string]any) {
		r := doReq(t, cli, base, "POST", "/-api/camera/"+action, user, pass, []byte(body))
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return r.StatusCode, m
	}
	get := func(path string) (int, map[string]any) {
		r := doReq(t, cli, base, "GET", path, user, pass, nil)
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return r.StatusCode, m
	}

	// 1) 组件状态：没装时 state=missing（装了也不该是别的）
	code, res := get("/-api/camera/comp.status")
	if code != 200 {
		t.Fatalf("comp.status 应 200, got %d %v", code, res)
	}
	if st, _ := res["state"].(string); st != "missing" && st != "ready" {
		t.Fatalf("组件状态异常: %v", res["state"])
	}
	if _, ok := res["size"].(float64); !ok {
		t.Fatalf("组件状态应带体积: %v", res)
	}

	// 2) 品牌模板
	code, res = get("/-api/camera/presets")
	if code != 200 {
		t.Fatalf("presets 应 200, got %d", code)
	}
	vendors, _ := res["vendors"].([]any)
	if len(vendors) < 5 {
		t.Fatalf("品牌模板太少: %d", len(vendors))
	}

	// 3) 空名字应 400
	if code, _ := post("cam.save", `{"name":"  "}`); code != 400 {
		t.Fatalf("空名字应 400, got %d", code)
	}

	// 4) 非 rtsp 地址应 400（页面把手填地址也纳管，避免 http 地址混进来）
	if code, _ := post("cam.save", `{"name":"x","urlMain":"http://192.168.1.9/snap.jpg"}`); code != 400 {
		t.Fatalf("http 地址应 400, got %d", code)
	}

	// 5) 用海康模板新建（只填 IP + 账号）
	body := `{"name":"门口","vendor":"hikvision","host":"192.168.1.64",
		"tpl":{"vendor":"hikvision","host":"192.168.1.64","user":"admin","pass":"Abc12345","channel":1},
		"record":1,"retainDays":7}`
	code, res = post("cam.save", body)
	if code != 200 || res["id"] == nil {
		t.Fatalf("cam.save 应 200, got %d %v", code, res)
	}
	id := res["id"].(string)

	// 6) 列表里地址应已按模板拼好（子码流 102）
	code, res = get("/-api/camera/list")
	if code != 200 {
		t.Fatalf("list 应 200, got %d", code)
	}
	items, _ := res["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("应有 1 台相机, got %d", len(items))
	}
	cam := items[0].(map[string]any)
	sub, _ := cam["urlSub"].(string)
	if !strings.HasSuffix(sub, "/Streaming/Channels/102") {
		t.Fatalf("子码流应按海康模板拼好: %q", sub)
	}
	if cam["vendor"] != "hikvision" || cam["retainDays"].(float64) != 7 {
		t.Fatalf("字段没存对: %v", cam)
	}
	// V2：密码进保险箱，地址里一个字都不能有；品牌推断出云台能力
	if strings.Contains(sub, "Abc12345") || strings.Contains(sub, "@") {
		t.Fatalf("地址里不该出现账号密码: %q", sub)
	}
	if cam["hasCred"] != true || cam["credUser"] != "admin" {
		t.Fatalf("凭证状态不对: hasCred=%v credUser=%v", cam["hasCred"], cam["credUser"])
	}
	if cam["ptz"] != "hik:1" {
		t.Fatalf("海康应自动带上海康云台: %v", cam["ptz"])
	}
	if cam["plan"] != "" {
		t.Fatalf("新相机默认全天（plan 空）: %v", cam["plan"])
	}
	if _, ok := res["comp"]; !ok {
		t.Fatal("list 应带组件状态")
	}
	if _, ok := res["disk"]; !ok {
		t.Fatal("list 应带磁盘信息")
	}

	// 6b) 录像计划：合法保存 / 非法时间被拒 / 保存后列表能读回
	code, res = post("plan.save", `{"id":"`+id+`","plan":"[{\"days\":[1,2,3,4,5],\"from\":\"08:00\",\"to\":\"18:00\"}]"}`)
	if code != 200 {
		t.Fatalf("plan.save 应 200, got %d %v", code, res)
	}
	if txt, _ := res["planText"].(string); !strings.Contains(txt, "08:00") {
		t.Fatalf("计划摘要不对: %v", res["planText"])
	}
	if code, _ := post("plan.save", `{"id":"`+id+`","plan":"[{\"from\":\"8点\",\"to\":\"18:00\"}]"}`); code != 400 {
		t.Fatalf("非法计划应 400, got %d", code)
	}
	if code, _ := post("plan.save", `{"id":"nope","plan":""}`); code != 404 {
		t.Fatalf("未知相机改计划应 404, got %d", code)
	}

	// 6c) 云台参数校验（不真连相机，避免测试依赖局域网）
	if code, _ := post("ptz.preset", `{"id":"`+id+`","act":"jump","n":1}`); code != 400 {
		t.Fatalf("非法 act 应 400, got %d", code)
	}

	// 6d) 直接贴带账号的地址：入库前也要剥干净
	code, res = post("cam.save", `{"name":"后门","urlMain":"rtsp://root:Sec%40123@192.168.1.65:554/cam/realmonitor?channel=1&subtype=1"}`)
	if code != 200 {
		t.Fatalf("带凭据地址应能存: %d %v", code, res)
	}
	id2 := res["id"].(string)
	_, res = get("/-api/camera/list")
	for _, it := range res["items"].([]any) {
		m := it.(map[string]any)
		if m["id"] != id2 {
			continue
		}
		if s, _ := m["urlMain"].(string); strings.Contains(s, "Sec") || strings.Contains(s, "@") {
			t.Fatalf("手贴地址里的凭据没剥掉: %q", s)
		}
		if m["credUser"] != "root" || m["hasCred"] != true {
			t.Fatalf("手贴地址的凭据没存对: %v %v", m["credUser"], m["hasCred"])
		}
	}
	_, _ = post("cam.delete", `{"ids":["`+id2+`"]}`)

	// 7) 片段查询：还没录像 → 空列表但不报错
	code, res = get("/-api/camera/seg.list?cam=" + id)
	if code != 200 {
		t.Fatalf("seg.list 应 200, got %d %v", code, res)
	}
	if items, _ := res["items"].([]any); len(items) != 0 {
		t.Fatalf("不该有片段: %d", len(items))
	}

	// 8) 路径穿越 / 非 mp4 一律 400
	if code, _ := get("/-api/camera/seg.file?cam=" + id + "&f=../../../settings.json"); code != 400 {
		t.Fatalf("穿越文件名应 400, got %d", code)
	}
	if code, _ := get("/-api/camera/seg.file?cam=" + id + "&f=nope.mp4"); code != 404 {
		t.Fatalf("不存在的片段应 404, got %d", code)
	}

	// 9) 未知相机 id
	if code, _ := post("rec.start", `{"id":"nope"}`); code != 404 {
		t.Fatalf("未知相机应 404, got %d", code)
	}

	// 10) 日志接口
	code, res = get("/-api/camera/log.list")
	if code != 200 {
		t.Fatalf("log.list 应 200, got %d", code)
	}
	if _, ok := res["items"].([]any); !ok {
		t.Fatalf("log.list 应返回 items: %v", res)
	}

	// 11) 删除
	code, res = post("cam.delete", `{"ids":["`+id+`"]}`)
	if code != 200 || res["n"].(float64) != 1 {
		t.Fatalf("delete 应删除 1 台, got %d %v", code, res)
	}
	if _, res = get("/-api/camera/list"); len(res["items"].([]any)) != 0 {
		t.Fatal("删完了列表还非空")
	}

	// 12) 未知接口
	if code, _ := post("nope", `{}`); code != 404 {
		t.Fatalf("未知接口应 404, got %d", code)
	}
}

// 只读账号：cam.save 走写动作白名单，必须被拦。
func TestCameraReadOnly(t *testing.T) {
	_, base, user, pass := startTestServer(t, func(c *Config) {
		c.InstanceDir = t.TempDir()
		c.Shares[0].ReadOnly = true
	})
	cli := newTestClient(t, false)
	r := doReq(t, cli, base, "POST", "/-api/camera/cam.save", user, pass,
		[]byte(`{"name":"x","urlMain":"rtsp://h/x"}`))
	if r.StatusCode != 405 {
		t.Fatalf("只读账号应 405, got %d", r.StatusCode)
	}
	// 读接口照常
	r = doReq(t, cli, base, "GET", "/-api/camera/list", user, pass, nil)
	if r.StatusCode != 200 {
		t.Fatalf("只读账号读接口应 200, got %d", r.StatusCode)
	}
}

// 界面回归：页面必须自己引 camera.js（S4 老坑），且顶栏有「监控」。
func TestCameraPageAssets(t *testing.T) {
	_, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = t.TempDir() })
	cli := newTestClient(t, false)
	r := doReq(t, cli, base, "GET", "/-camera", user, pass, nil)
	raw, _ := io.ReadAll(r.Body)
	page := string(raw)
	for _, want := range []string{`id="c-cams"`, "/-ui/apps/camera.js", "监控"} {
		if !strings.Contains(page, want) {
			t.Fatalf("页面缺 %q", want)
		}
	}
	if r.StatusCode != 200 {
		t.Fatalf("页面应 200, got %d", r.StatusCode)
	}
}
