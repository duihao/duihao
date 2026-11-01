package webdavd

import (
	"encoding/json"
	"io"
	"testing"
)

// 健康 H1：成员播种、报告与指标录入、参考范围判高低、同义项归并后的「较上次」对比、
// 概览异常汇总、成员删除保护、只读账号写保护。
func TestHealthAPI(t *testing.T) {
	_, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = t.TempDir() })
	cli := newTestClient(t, false)

	num := func(v any) int { f, _ := v.(float64); return int(f) }
	flt := func(v any) float64 { f, _ := v.(float64); return f }
	post := func(action, body string) (int, map[string]any) {
		r := doReq(t, cli, base, "POST", "/-api/health/"+action, user, pass, []byte(body))
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return r.StatusCode, m
	}

	// 1) 成员：首次打开自动播种「本人」
	code, res := post("member.list", `{}`)
	if code != 200 {
		t.Fatalf("member.list 应 200, got %d %v", code, res)
	}
	ms, _ := res["members"].([]any)
	if len(ms) != 1 {
		t.Fatalf("应自动播种 1 个成员, got %v", ms)
	}
	if m0, _ := ms[0].(map[string]any); m0["id"] != "m-self" || m0["name"] != "本人" {
		t.Fatalf("默认成员应为 本人/m-self, got %v", ms[0])
	}

	// 2) 第一份化验单（2025-09-01）：ALT 42 正常、总胆固醇 5.0 正常、尿蛋白定性阴性
	code, res = post("report.save", `{"id":"","memberId":"m-self","kind":"lab","title":"",
		"org":"市一院","dept":"检验科","reportAt":"2025-09-01","summary":"大致正常","attachments":[],
		"metrics":[
			{"name":"谷丙转氨酶","value":42,"unit":"U/L","refLow":9,"refHigh":50},
			{"name":"总胆固醇","value":5.0,"unit":"mmol/L","refHigh":5.2},
			{"name":"尿蛋白","valueText":"阴性"}
		]}`)
	if code != 200 {
		t.Fatalf("report.save 应 200, got %d %v", code, res)
	}
	if num(res["total"]) != 3 || num(res["abnormal"]) != 0 {
		t.Fatalf("第一份应 3 项 0 异常, got %v", res)
	}
	r1, _ := res["id"].(string)
	if r1 == "" {
		t.Fatal("应返回报告 id")
	}

	// 3) 第二份体检（2026-09-18）：故意用缩写「ALT」+ 68，验证同义项归并后能对上上次
	code, res = post("report.save", `{"id":"","memberId":"m-self","kind":"checkup","title":"年度体检",
		"org":"市一院体检中心","dept":"","reportAt":"2026-09-18","summary":"","attachments":[],
		"metrics":[
			{"name":"ALT","value":68,"unit":"U/L","refLow":9,"refHigh":50},
			{"name":"总胆固醇","value":6.2,"unit":"mmol/L","refHigh":5.2},
			{"name":"血红蛋白","value":150,"unit":"g/L","refLow":130,"refHigh":175}
		]}`)
	if code != 200 {
		t.Fatalf("第二份保存应 200, got %d %v", code, res)
	}
	if num(res["abnormal"]) != 2 {
		t.Fatalf("第二份应 2 项异常（ALT、总胆固醇）, got %v", res)
	}
	r2, _ := res["id"].(string)

	// 4) 列表：最近一份在最前，异常数正确
	code, res = post("report.list", `{"memberId":"m-self"}`)
	if code != 200 || num(res["total"]) != 2 {
		t.Fatalf("列表应 2 份, got %d %v", code, res)
	}
	items, _ := res["items"].([]any)
	if it0, _ := items[0].(map[string]any); it0["id"] != r2 || num(it0["abnormal"]) != 2 || num(it0["total"]) != 3 {
		t.Fatalf("第一项应是第二份且 2/3 异常, got %v", items[0])
	}

	// 5) 详情：ALT 判高、且 prev 指回 2025-09-01 的 42（跨「ALT / 谷丙转氨酶」两种写法）
	code, res = post("report.get", `{"id":"`+r2+`"}`)
	if code != 200 {
		t.Fatalf("report.get 应 200, got %d %v", code, res)
	}
	metrics, _ := res["metrics"].([]any)
	if len(metrics) != 3 {
		t.Fatalf("应有 3 项指标, got %v", metrics)
	}
	alt, _ := metrics[0].(map[string]any)
	if alt["name"] != "ALT" || alt["flag"] != "high" || flt(alt["value"]) != 68 {
		t.Fatalf("ALT 应 high/68, got %v", alt)
	}
	prev, ok := alt["prev"].(map[string]any)
	if !ok {
		t.Fatalf("ALT 应带 prev（上次 42）, got %v", alt)
	}
	if flt(prev["value"]) != 42 || prev["reportAt"] != "2025-09-01" {
		t.Fatalf("prev 应为 2025-09-01 的 42, got %v", prev)
	}
	hgb, _ := metrics[2].(map[string]any)
	if hgb["flag"] != "" {
		t.Fatalf("血红蛋白在范围内不应判异常, got %v", hgb)
	}
	if _, has := hgb["prev"]; has {
		t.Fatalf("首次出现的指标不应有 prev, got %v", hgb)
	}

	// 6) 概览：总数 + 异常汇总
	code, res = post("overview", `{"memberId":"m-self"}`)
	if code != 200 || num(res["total"]) != 2 || num(res["abnormalTotal"]) != 2 {
		t.Fatalf("概览应为 2 份 / 2 项异常, got %d %v", code, res)
	}
	last, _ := res["last"].(map[string]any)
	if last["reportAt"] != "2026-09-18" {
		t.Fatalf("最近报告应为 2026-09-18, got %v", last)
	}

	// 7) 校验：日期格式、缺失结果值
	if code, _ := post("report.save", `{"memberId":"m-self","kind":"lab","reportAt":"2026/09/18"}`); code != 400 {
		t.Fatalf("非法日期应 400, got %d", code)
	}
	if code, _ := post("report.save", `{"memberId":"m-self","kind":"lab","reportAt":"2026-09-18",
		"metrics":[{"name":"血糖"}]}`); code != 400 {
		t.Fatalf("指标缺结果值应 400, got %d", code)
	}
	if code, _ := post("report.save", `{"memberId":"m-self","kind":"nope","reportAt":"2026-09-18"}`); code != 400 {
		t.Fatalf("未知报告类型应 400, got %d", code)
	}

	// 8) 成员：新增 → 挂报告 → 有报告时不许删 → 删报告后可删
	code, res = post("member.save", `{"name":"父亲","relation":"父亲","sex":"男","birth":"1958-03-02","height":172}`)
	if code != 200 {
		t.Fatalf("member.save 应 200, got %d %v", code, res)
	}
	dad, _ := res["id"].(string)
	code, res = post("report.save", `{"memberId":"`+dad+`","kind":"checkup","reportAt":"2026-05-01","metrics":[]}`)
	if code != 200 {
		t.Fatalf("给父亲建报告应 200, got %d %v", code, res)
	}
	dadRep, _ := res["id"].(string)
	if code, _ := post("member.delete", `{"id":"`+dad+`"}`); code != 400 {
		t.Fatalf("成员下有报告应拒绝删除, got %d", code)
	}
	if code, _ := post("report.delete", `{"id":"`+dadRep+`"}`); code != 200 {
		t.Fatalf("删报告应 200, got %d", code)
	}
	if code, _ := post("member.delete", `{"id":"`+dad+`"}`); code != 200 {
		t.Fatalf("空成员应可删, got %d", code)
	}
	if code, _ := post("member.delete", `{"id":"m-self"}`); code != 400 {
		t.Fatalf("「本人」不可删, got %d", code)
	}

	// 9) 搜索：能按指标名命中报告
	code, res = post("report.list", `{"memberId":"m-self","q":"血红蛋白"}`)
	if code != 200 || num(res["total"]) != 1 {
		t.Fatalf("按指标名搜索应命中 1 份, got %d %v", code, res)
	}

	// 10) 删除报告连带指标
	if code, _ := post("report.delete", `{"id":"`+r1+`"}`); code != 200 {
		t.Fatalf("删第一份应 200, got %d", code)
	}
	if code, res := post("report.get", `{"id":"`+r1+`"}`); code != 404 {
		t.Fatalf("已删报告应 404, got %d %v", code, res)
	}
	if code, res := post("overview", `{"memberId":"m-self"}`); num(res["abnormalTotal"]) != 2 || code != 200 {
		t.Fatalf("删掉旧报告后异常数仍应 2（新报告 2 项）, got %v", res)
	}
}

// 只读账号：健康写接口一律 405。
func TestHealthReadOnly(t *testing.T) {
	_, base, user, pass := startTestServer(t, func(c *Config) {
		c.InstanceDir = t.TempDir()
		c.Shares[0].ReadOnly = true
	})
	cli := newTestClient(t, false)
	for _, a := range []string{"report.save", "report.delete", "member.save", "member.delete"} {
		r := doReq(t, cli, base, "POST", "/-api/health/"+a, user, pass, []byte("{}"))
		if r.StatusCode != 405 {
			t.Fatalf("只读账号 %s 应 405, got %d", a, r.StatusCode)
		}
		_ = r.Body.Close()
	}
	if r := doReq(t, cli, base, "GET", "/-health", user, pass, nil); r.StatusCode != 200 {
		t.Fatalf("只读账号仍应能看健康页, got %d", r.StatusCode)
	}
}
