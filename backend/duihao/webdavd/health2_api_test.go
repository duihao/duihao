package webdavd

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 健康 H2/H3/H4：指标趋势（报告 + 自测同义项归并）、日常自测、用药、就诊、
// 附件上传与路径防护、隐私开关、全量导出。
func TestHealthMoreAPI(t *testing.T) {
	shareRoot := t.TempDir()
	_, base, user, pass := startTestServer(t, func(c *Config) {
		c.InstanceDir = t.TempDir()
		c.Shares[0].Root = shareRoot
	})
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

	// 1) 两份化验：2025 写「谷丙转氨酶」，2026 写「ALT」，趋势应合成一条线
	if code, res := post("report.save", `{"memberId":"m-self","kind":"lab","reportAt":"2025-09-01",
		"metrics":[{"name":"谷丙转氨酶","value":42,"unit":"U/L","refLow":9,"refHigh":50}]}`); code != 200 {
		t.Fatalf("保存第一份应 200, got %d %v", code, res)
	}
	if code, res := post("report.save", `{"memberId":"m-self","kind":"lab","reportAt":"2026-09-18",
		"metrics":[{"name":"ALT","value":68,"unit":"U/L","refLow":9,"refHigh":50}]}`); code != 200 {
		t.Fatalf("保存第二份应 200, got %d %v", code, res)
	}

	// 2) 日常自测：体重 3 次、血压 2 次
	for i, w := range []string{"70.2", "69.4", "68.8"} {
		body := `{"memberId":"m-self","kind":"weight","measuredAt":"2026-0` + string(rune('5'+i)) +
			`-01T08:00","v1":` + w + `}`
		if code, res := post("vital.save", body); code != 200 {
			t.Fatalf("记录体重应 200, got %d %v", code, res)
		}
	}
	if code, res := post("vital.save", `{"memberId":"m-self","kind":"bp","measuredAt":"2026-08-01T08:10",
		"v1":128,"v2":82,"note":"早餐前"}`); code != 200 {
		t.Fatalf("记录血压应 200, got %d %v", code, res)
	}
	if code, res := post("vital.save", `{"memberId":"m-self","kind":"bp","measuredAt":"2026-08-02T08:10",
		"v1":146,"v2":94}`); code != 200 {
		t.Fatalf("记录第二次血压应 200, got %d %v", code, res)
	}

	// 2.1) 校验：血压缺舒张压 / 舒张压不低于收缩压 / 数值离谱
	if code, _ := post("vital.save", `{"memberId":"m-self","kind":"bp","v1":120}`); code != 400 {
		t.Fatalf("血压缺舒张压应 400, got %d", code)
	}
	if code, _ := post("vital.save", `{"memberId":"m-self","kind":"bp","v1":80,"v2":120}`); code != 400 {
		t.Fatalf("舒张压高于收缩压应 400, got %d", code)
	}
	if code, _ := post("vital.save", `{"memberId":"m-self","kind":"weight","v1":900}`); code != 400 {
		t.Fatalf("体重超范围应 400, got %d", code)
	}

	// 2.2) 列表：最近一条在前，血压显示 128/82，超出常见区间标 high
	code, res := post("vital.list", `{"memberId":"m-self"}`)
	if code != 200 {
		t.Fatalf("vital.list 应 200, got %d %v", code, res)
	}
	items, _ := res["items"].([]any)
	if len(items) != 5 {
		t.Fatalf("应有 5 条自测, got %d", len(items))
	}
	if it0, _ := items[0].(map[string]any); it0["display"] != "146/94" || it0["flag"] != "high" {
		t.Fatalf("最近一条应是 146/94 且标 high, got %v", items[0])
	}
	latest, _ := res["latest"].(map[string]any)
	if bp, _ := latest["bp"].(map[string]any); bp["display"] != "146/94" {
		t.Fatalf("latest.bp 应为 146/94, got %v", latest["bp"])
	}

	// 3) 趋势：ALT（两次）合成一条；体重、收缩压、舒张压各自一条
	code, res = post("trend.list", `{"memberId":"m-self"}`)
	if code != 200 {
		t.Fatalf("trend.list 应 200, got %d %v", code, res)
	}
	series, _ := res["series"].([]any)
	byName := map[string]map[string]any{}
	for _, s := range series {
		sm, _ := s.(map[string]any)
		byName[sm["name"].(string)] = sm
	}
	alt, ok := byName["ALT"]
	if !ok {
		t.Fatalf("趋势里应有 ALT（与谷丙转氨酶归并）, got %v", byName)
	}
	if num(alt["count"]) != 2 {
		t.Fatalf("ALT 应有 2 个点, got %v", alt["count"])
	}
	if flt(alt["min"]) != 42 || flt(alt["max"]) != 68 {
		t.Fatalf("ALT 范围应 42–68, got min=%v max=%v", alt["min"], alt["max"])
	}
	if w, ok := byName["体重"]; !ok || num(w["count"]) != 3 {
		t.Fatalf("体重应有 3 个点, got %v", byName["体重"])
	}
	if _, ok := byName["收缩压"]; !ok {
		t.Fatalf("血压应拆出收缩压, got %v", byName)
	}
	if _, ok := byName["舒张压"]; !ok {
		t.Fatalf("血压应拆出舒张压, got %v", byName)
	}
	// 参考带：ALT 来自报告 → 不是「常见范围」
	if alt["refCommon"] == true {
		t.Fatalf("报告指标不应标 refCommon, got %v", alt["refCommon"])
	}
	if lo := alt["refLow"]; lo == nil || flt(lo) != 9 {
		t.Fatalf("ALT 参考下限应 9, got %v", alt)
	}

	// 4) 用药：新增 → 在服计数 → 停用 → 删除
	code, res = post("med.save", `{"memberId":"m-self","name":"阿托伐他汀钙片","dose":"20mg · 1片",
		"freq":"每日一次 睡前","route":"口服","startAt":"2026-01-05"}`)
	if code != 200 {
		t.Fatalf("med.save 应 200, got %d %v", code, res)
	}
	medID, _ := res["id"].(string)
	code, res = post("med.list", `{"memberId":"m-self"}`)
	if code != 200 || num(res["active"]) != 1 {
		t.Fatalf("用药应有 1 条在服, got %d %v", code, res)
	}
	if code, res := post("med.save", `{"id":"`+medID+`","memberId":"m-self","name":"阿托伐他汀钙片","status":"stopped"}`); code != 200 {
		t.Fatalf("停用应 200, got %d %v", code, res)
	}
	if code, res := post("med.list", `{"memberId":"m-self","status":"active"}`); num(res["active"]) != 0 || code != 200 {
		t.Fatalf("停用后在服数应 0, got %v", res)
	}
	if code, _ := post("med.save", `{"memberId":"m-self","name":""}`); code != 400 {
		t.Fatalf("空药名应 400, got %d", code)
	}

	// 5) 就诊：新增 → 列表 → 校验 → 删除
	code, res = post("visit.save", `{"memberId":"m-self","visitAt":"2026-05-12","org":"市一院","dept":"心内科",
		"doctor":"张医生","reason":"复查血脂","diagnosis":"高脂血症","advice":"低脂饮食，三个月复查","cost":86.5}`)
	if code != 200 {
		t.Fatalf("visit.save 应 200, got %d %v", code, res)
	}
	visitID, _ := res["id"].(string)
	code, res = post("visit.list", `{"memberId":"m-self"}`)
	if code != 200 || num(res["total"]) != 1 {
		t.Fatalf("就诊应 1 条, got %d %v", code, res)
	}
	v0 := res["items"].([]any)[0].(map[string]any)
	if v0["diagnosis"] != "高脂血症" || flt(v0["cost"]) != 86.5 {
		t.Fatalf("就诊字段应回显, got %v", v0)
	}
	if code, _ := post("visit.save", `{"memberId":"m-self","visitAt":"2026/05/12","org":"市一院"}`); code != 400 {
		t.Fatalf("非法日期应 400, got %d", code)
	}
	if code, _ := post("visit.save", `{"memberId":"m-self","visitAt":"2026-05-12"}`); code != 400 {
		t.Fatalf("机构/原因/诊断全空应 400, got %d", code)
	}
	// 搜索命中
	if code, res := post("visit.list", `{"memberId":"m-self","q":"高脂"}`); code != 200 || num(res["total"]) != 1 {
		t.Fatalf("按诊断搜索应命中, got %d %v", code, res)
	}

	// 6) 附件：multipart 上传 → 落盘在 _appfiles/health/ → attach.save 挂到报告
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "体检单.jpg")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("fake-jpeg-bytes"))
	_ = mw.Close()

	req, err := http.NewRequest("POST", "http://"+base+"/-api/health/attach.upload", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.SetBasicAuth(user, pass)
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("附件上传应 200, got %d %s", resp.StatusCode, raw)
	}
	var up map[string]any
	_ = json.Unmarshal(raw, &up)
	attPath, _ := up["path"].(string)
	if !strings.HasPrefix(attPath, "/_appfiles/health/") || !strings.HasSuffix(attPath, "体检单.jpg") {
		t.Fatalf("附件路径应在 _appfiles/health/ 下, got %q", attPath)
	}
	if _, err := os.Stat(filepath.Join(shareRoot, filepath.FromSlash(attPath))); err != nil {
		t.Fatalf("附件应已落盘: %v", err)
	}

	// 6.1) 报告列表里拿一份报告，挂上附件
	_, res = post("report.list", `{"memberId":"m-self","pageSize":1}`)
	repID := res["items"].([]any)[0].(map[string]any)["id"].(string)
	if code, res := post("attach.save", `{"reportId":"`+repID+`","attachments":["`+attPath+`"]}`); code != 200 {
		t.Fatalf("attach.save 应 200, got %d %v", code, res)
	}
	if code, res := post("report.get", `{"id":"`+repID+`"}`); code != 200 {
		t.Fatalf("report.get 应 200, got %d %v", code, res)
	} else if rp, _ := res["report"].(map[string]any); len(rp["attachments"].([]any)) != 1 {
		t.Fatalf("报告应带 1 个附件, got %v", rp["attachments"])
	}
	// 6.2) 目录外的路径一律拒绝
	if code, _ := post("attach.save", `{"reportId":"`+repID+`","attachments":["/Documents/secret.pdf"]}`); code != 400 {
		t.Fatalf("非 _appfiles/health 路径应 400, got %d", code)
	}
	if code, _ := post("attach.delete", `{"path":"/Documents/secret.pdf"}`); code != 403 {
		t.Fatalf("删除目录外文件应 403, got %d", code)
	}
	// 6.3) 删除附件本体
	if code, res := post("attach.delete", `{"path":"`+attPath+`"}`); code != 200 {
		t.Fatalf("attach.delete 应 200, got %d %v", code, res)
	}
	if _, err := os.Stat(filepath.Join(shareRoot, filepath.FromSlash(attPath))); !os.IsNotExist(err) {
		t.Fatalf("附件文件应已删除, err=%v", err)
	}

	// 7) 隐私开关：默认关 → 开 → 落库 → 关
	if code, res := post("settings.get", `{}`); code != 200 || res["privacy"] != false {
		t.Fatalf("默认隐私应关, got %d %v", code, res)
	}
	if code, res := post("settings.save", `{"privacy":true}`); code != 200 || res["privacy"] != true {
		t.Fatalf("开启隐私应 200/true, got %d %v", code, res)
	}
	if code, res := post("settings.get", `{}`); code != 200 || res["privacy"] != true {
		t.Fatalf("隐私开关应持久化, got %d %v", code, res)
	}
	if code, _ := post("settings.save", `{"privacy":false}`); code != 200 {
		t.Fatalf("关闭隐私应 200, got %d", code)
	}

	// 8) 导出：各表条数对得上
	code, res = post("export", `{}`)
	if code != 200 {
		t.Fatalf("export 应 200, got %d %v", code, res)
	}
	counts, _ := res["counts"].(map[string]any)
	if num(counts["reports"]) != 2 || num(counts["vitals"]) != 5 ||
		num(counts["meds"]) != 1 || num(counts["visits"]) != 1 {
		t.Fatalf("导出计数不符: %v", counts)
	}
	if reps, _ := res["reports"].([]any); len(reps) != 2 {
		t.Fatalf("导出应含 2 份报告, got %d", len(reps))
	} else if m, _ := reps[0].(map[string]any)["metrics"].([]any); len(m) != 1 {
		t.Fatalf("导出的报告应带指标, got %v", reps[0])
	}

	// 9) 删除自测 / 用药 / 就诊
	if code, _ := post("vital.delete", `{"id":"nope"}`); code != 404 {
		t.Fatalf("删不存在的自测应 404, got %d", code)
	}
	if code, _ := post("med.delete", `{"id":"`+medID+`"}`); code != 200 {
		t.Fatalf("删用药应 200, got %d", code)
	}
	if code, _ := post("visit.delete", `{"id":"`+visitID+`"}`); code != 200 {
		t.Fatalf("删就诊应 200, got %d", code)
	}
}

// 只读账号：H2/H3/H4 的写接口一律 405，读接口照常。
func TestHealthMoreReadOnly(t *testing.T) {
	_, base, user, pass := startTestServer(t, func(c *Config) {
		c.InstanceDir = t.TempDir()
		c.Shares[0].ReadOnly = true
	})
	cli := newTestClient(t, false)
	for _, a := range []string{"vital.save", "vital.delete", "med.save", "med.delete",
		"visit.save", "visit.delete", "attach.save", "attach.delete", "attach.upload", "settings.save"} {
		r := doReq(t, cli, base, "POST", "/-api/health/"+a, user, pass, []byte("{}"))
		if r.StatusCode != 405 {
			t.Fatalf("只读账号 %s 应 405, got %d", a, r.StatusCode)
		}
		_ = r.Body.Close()
	}
	for _, a := range []string{"trend.list", "vital.list", "med.list", "visit.list", "settings.get", "export"} {
		r := doReq(t, cli, base, "POST", "/-api/health/"+a, user, pass, []byte("{}"))
		if r.StatusCode != 200 {
			t.Fatalf("只读账号 %s 应 200, got %d", a, r.StatusCode)
		}
		_ = r.Body.Close()
	}
}
