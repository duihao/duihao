package webdavd

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// 语音提醒 HTTP 层：新建（含合成）/ 列表 / 启停 / 试听 / 取音频 / 删除 / 参数校验。
// 合成走本机 SAPI，没有语音引擎的机器上会自动降级（save 返回 warn，不失败）。
func TestVoiceAPI(t *testing.T) {
	srv, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = t.TempDir() })
	_ = srv

	cli := newTestClient(t, false)
	num := func(v any) int { f, _ := v.(float64); return int(f) }
	post := func(action, body string) (int, map[string]any) {
		r := doReq(t, cli, base, "POST", "/-api/voice/"+action, user, pass, []byte(body))
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return r.StatusCode, m
	}

	// 1) 预置项
	code, res := post("presets", `{}`)
	if code != 200 {
		t.Fatalf("presets 应 200, got %d %v", code, res)
	}
	if vs, _ := res["voices"].([]any); len(vs) < 3 {
		t.Fatalf("音色预设太少: %v", res["voices"])
	}
	if ts, _ := res["tones"].([]any); len(ts) < 6 {
		t.Fatalf("语气预设太少: %v", res["tones"])
	}

	// 2) 空标题应 400
	if code, _ := post("save", `{"title":"  ","content":"x","period":"daily","atTime":"09:00"}`); code != 400 {
		t.Fatalf("空标题应 400, got %d", code)
	}

	// 3) 新建每日提醒（时间取明天早上，保证 nextAt 在未来）
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	body := `{"title":"喝水提醒","content":"该喝水了宝贝，别等渴了才想起来。","voice":"huihui-soft",
		"tone":"warm","times":2,"period":"daily","startDate":"` + tomorrow + `","atTime":"09:30",
		"action":"none","actionArg":""}`
	code, res = post("save", body)
	if code != 200 || res["id"] == nil {
		t.Fatalf("save 应 200, got %d %v", code, res)
	}
	id := res["id"].(string)
	nextAt, _ := res["nextAt"].(string)
	if nextAt == "" || !strings.HasSuffix(nextAt, "09:30") {
		t.Fatalf("nextAt 应为明天的 09:30, got %q", nextAt)
	}
	if _, hasWarn := res["warn"]; !hasWarn {
		audio, _ := res["audio"].(string)
		if audio == "" {
			t.Fatalf("合成成功却没有 audio: %v", res)
		}
		if ms := num(res["audioMs"]); ms < 500 {
			t.Fatalf("音频时长异常: %v", res["audioMs"])
		}
		// 取音频流
		r := doReq(t, cli, base, "GET", "/-api/voice/audio?f="+audio, user, pass, nil)
		if r.StatusCode != 200 {
			t.Fatalf("取音频应 200, got %d", r.StatusCode)
		}
		if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "audio/wav") {
			t.Fatalf("Content-Type 应为 audio/wav, got %q", ct)
		}
		b, _ := io.ReadAll(r.Body)
		if len(b) < 1000 {
			t.Fatalf("音频太小: %d 字节", len(b))
		}
		// 文件名穿越必须挡住
		if r := doReq(t, cli, base, "GET", "/-api/voice/audio?f=../hub.db", user, pass, nil); r.StatusCode != 400 {
			t.Fatalf("穿越文件名应 400, got %d", r.StatusCode)
		}
	}

	// 4) 列表与计数
	code, res = post("list", `{}`)
	if code != 200 {
		t.Fatalf("list 应 200, got %d %v", code, res)
	}
	items, _ := res["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("应有 1 条, got %d", len(items))
	}
	row := items[0].(map[string]any)
	if row["status"] != "pending" {
		t.Fatalf("状态应为 pending: %v", row)
	}
	counts, _ := res["counts"].(map[string]any)
	if num(counts["all"]) != 1 || num(counts["daily"]) != 1 {
		t.Fatalf("计数不对: %v", counts)
	}

	// 5) 按周期筛选
	if _, res = post("list", `{"period":"weekly"}`); len(res["items"].([]any)) != 0 {
		t.Fatalf("weekly 应为空: %v", res["items"])
	}

	// 6) 停用 → paused，且不再有下次时间推进
	code, res = post("update", `{"ids":["`+id+`"],"enabled":false}`)
	if code != 200 || num(res["n"]) != 1 {
		t.Fatalf("停用应 200 n=1, got %d %v", code, res)
	}
	_, res = post("list", `{}`)
	row = res["items"].([]any)[0].(map[string]any)
	if row["status"] != "paused" || row["enabled"] != false {
		t.Fatalf("停用后状态不对: %v", row)
	}

	// 7) 启用重算 nextAt
	code, res = post("update", `{"ids":["`+id+`"],"enabled":true}`)
	if code != 200 || num(res["n"]) != 1 {
		t.Fatalf("启用应 200 n=1, got %d %v", code, res)
	}

	// 8) 试听（不入库）
	code, res = post("preview", `{"voice":"huihui-std","tone":"cheer","content":"测试一下语气"}`)
	if code != 200 {
		t.Fatalf("preview 应 200, got %d %v", code, res)
	}
	if _, hasWarn := res["error"]; !hasWarn && res["audio"] != nil {
		if u, _ := res["url"].(string); !strings.HasPrefix(u, "/-api/voice/audio?f=") {
			t.Fatalf("试听 url 不对: %v", res)
		}
	}

	// 9) 触发流水（新库为空）
	code, res = post("log.list", `{}`)
	if code != 200 || len(res["items"].([]any)) != 0 {
		t.Fatalf("log.list 应为空: %d %v", code, res)
	}

	// 10) 删除
	code, res = post("delete", `{"ids":["`+id+`"]}`)
	if code != 200 || num(res["n"]) != 1 {
		t.Fatalf("删除应 200 n=1, got %d %v", code, res)
	}
	if _, res = post("list", `{}`); len(res["items"].([]any)) != 0 {
		t.Fatalf("删完应为空")
	}

	// 11) 未知动作
	if code, _ := post("nope", `{}`); code != 404 {
		t.Fatalf("未知动作应 404, got %d", code)
	}
}

// 只读账号不能新建（isWriteAction 必须认得 save/update/delete）。
func TestVoiceReadOnly(t *testing.T) {
	_, base, user, pass := startTestServer(t, func(c *Config) {
		c.InstanceDir = t.TempDir()
		c.ReadOnly = true
	})
	cli := newTestClient(t, false)
	r := doReq(t, cli, base, "POST", "/-api/voice/save", user, pass,
		[]byte(`{"title":"x","content":"y","period":"daily","atTime":"09:00"}`))
	if r.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("只读账号新建应 405, got %d", r.StatusCode)
	}
	// 读接口照常
	if r := doReq(t, cli, base, "POST", "/-api/voice/list", user, pass, []byte(`{}`)); r.StatusCode != 200 {
		t.Fatalf("只读账号应能 list, got %d", r.StatusCode)
	}
}
