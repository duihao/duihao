package camera

import (
	"strings"
	"testing"
	"time"
)

// ---------- 凭证 ----------

func TestSplitAndJoinCred(t *testing.T) {
	cases := []struct {
		raw, clean, user, pass string
	}{
		{"rtsp://admin:hik12345@192.168.1.64:554/Streaming/Channels/102",
			"rtsp://192.168.1.64:554/Streaming/Channels/102", "admin", "hik12345"},
		{"rtsp://192.168.1.64:554/Streaming/Channels/102",
			"rtsp://192.168.1.64:554/Streaming/Channels/102", "", ""},
		{"", "", "", ""},
	}
	for _, c := range cases {
		clean, u, p := splitCred(c.raw)
		if clean != c.clean || u != c.user || p != c.pass {
			t.Fatalf("%q → clean=%q user=%q pass=%q", c.raw, clean, u, p)
		}
	}

	// 拼回去要能还原（密码里带 @ 也能过 url 编码）
	got := withCred("rtsp://192.168.1.64:554/x", "admin", "p@ss:word")
	_, u, p := splitCred(got)
	if u != "admin" || p != "p@ss:word" {
		t.Fatalf("拼回后解析不一致: %q → %q / %q", got, u, p)
	}
}

func TestMaskURLHidesPass(t *testing.T) {
	got := maskURL("rtsp://admin:hik12345@192.168.1.64:554/x")
	if strings.Contains(got, "hik12345") {
		t.Fatalf("脱敏后不该出现密码: %s", got)
	}
	if !strings.Contains(got, "admin") || !strings.Contains(got, "***") {
		t.Fatalf("应保留用户名并打码密码: %s", got)
	}
}

func TestCamPassRoundTrip(t *testing.T) {
	sealed, err := sealPass("hik12345")
	if err != nil {
		t.Fatal(err)
	}
	if sealed == "" || !strings.HasPrefix(sealed, "d1:") && !strings.HasPrefix(sealed, "a1:") {
		t.Fatalf("密文应带平台前缀: %q", sealed)
	}
	if strings.Contains(sealed, "hik12345") {
		t.Fatal("密文里不该有明文")
	}
	got, err := camPass(Cam{CredSeal: sealed})
	if err != nil || got != "hik12345" {
		t.Fatalf("解不回原文: %q %v", got, err)
	}

	// 空密码 = 不需要鉴权，不报错
	if s, err := sealPass("  "); s != "" || err != nil {
		t.Fatalf("空密码应得空密文: %q %v", s, err)
	}
	if p, err := camPass(Cam{}); p != "" || err != nil {
		t.Fatalf("无密文应得空密码: %q %v", p, err)
	}
}

func TestStreamURLPrefersSubAndCarriesCred(t *testing.T) {
	sealed, _ := sealPass("hik12345")
	cam := Cam{
		URLMain:  "rtsp://192.168.1.64:554/Streaming/Channels/101",
		URLSub:   "rtsp://192.168.1.64:554/Streaming/Channels/102",
		CredUser: "admin", CredSeal: sealed,
	}
	got := streamURL(cam, true)
	if !strings.Contains(got, "102") {
		t.Fatalf("应优先子码流: %s", got)
	}
	if !strings.Contains(got, "admin:hik12345@") {
		t.Fatalf("应带上凭据: %s", got)
	}
	if main := streamURL(cam, false); !strings.Contains(main, "101") {
		t.Fatalf("主码流不对: %s", main)
	}
	// 子码流为空时退回主码流
	cam.URLSub = ""
	if got := streamURL(cam, true); !strings.Contains(got, "101") {
		t.Fatalf("无子码流应退回主码流: %s", got)
	}
}

// ---------- 录像计划 ----------

func TestNormPlan(t *testing.T) {
	cases := []struct {
		in      string
		out     string
		wantErr bool
	}{
		{"", "", false},
		{"[]", "", false},
		{`[{"days":[3,1,3],"from":"08:00","to":"12:00"}]`,
			`[{"days":[1,3],"from":"08:00","to":"12:00"}]`, false},
		// 空段（from==to）被丢掉；全空则整条计划为空 = 全天
		{`[{"days":[],"from":"09:00","to":"09:00"}]`, "", false},
		{`[{"days":[],"from":"9:00","to":"12:00"}]`, "", true},
		{`[{"days":[],"from":"08:00","to":"25:00"}]`, "", true},
		{`not json`, "", true},
	}
	for _, c := range cases {
		got, err := normPlan(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("%q 应该报错，却得到 %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q 报错: %v", c.in, err)
		}
		if got != c.out {
			t.Fatalf("%q → %q，期望 %q", c.in, got, c.out)
		}
	}
}

func at(y, m, d, hh, mm int) time.Time {
	return time.Date(y, time.Month(m), d, hh, mm, 0, 0, time.Local)
}

func TestInPlan(t *testing.T) {
	// 2026-10-30 是周五（weekday=5）
	fri := at(2026, 10, 30, 0, 0)
	if fri.Weekday() != time.Friday {
		t.Fatalf("基准日期算错了: %v", fri.Weekday())
	}

	// 空计划 = 全天
	if !inPlan(nil, at(2026, 10, 30, 3, 0)) {
		t.Fatal("空计划应视为全天")
	}

	workday := parsePlan(`[{"days":[1,2,3,4,5],"from":"08:00","to":"18:00"}]`)
	if !inPlan(workday, at(2026, 10, 30, 8, 0)) {
		t.Fatal("周五 08:00 应在计划内（左闭）")
	}
	if inPlan(workday, at(2026, 10, 30, 18, 0)) {
		t.Fatal("18:00 应是右开，不在计划内")
	}
	if inPlan(workday, at(2026, 10, 31, 10, 0)) {
		t.Fatal("周六不该命中工作日计划")
	}

	// 跨夜：周五 22:00 → 次日 02:00
	night := parsePlan(`[{"days":[5],"from":"22:00","to":"02:00"}]`)
	if !inPlan(night, at(2026, 10, 30, 23, 0)) {
		t.Fatal("周五 23:00 应在跨夜段内")
	}
	if !inPlan(night, at(2026, 10, 31, 1, 30)) {
		t.Fatal("周六 01:30 属于周五那一段，应该命中")
	}
	if inPlan(night, at(2026, 10, 31, 3, 0)) {
		t.Fatal("周六 03:00 已超出跨夜段")
	}

	// 每天（days 为空）
	daily := parsePlan(`[{"days":[],"from":"00:00","to":"00:00"}]`)
	if inPlan(daily, at(2026, 10, 30, 12, 0)) {
		t.Fatal("00:00-00:00 是空段，不该当成全天")
	}
}

func TestPlanSummary(t *testing.T) {
	if got := planSummary(""); got != "全天" {
		t.Fatalf("空计划摘要: %q", got)
	}
	got := planSummary(`[{"days":[1,3,5],"from":"08:00","to":"12:00"}]`)
	if !strings.Contains(got, "周一") || !strings.Contains(got, "08:00–12:00") {
		t.Fatalf("摘要不对: %q", got)
	}
}

// ---------- 云台 ----------

// RFC 2617 的官方例子，用来钉死 response 算法。
func TestDigestResponseRFC2617(t *testing.T) {
	got := digestResponse("Mufasa", "Circle Of Life", "testrealm@host.com",
		"dcd98b7102dd2f0e8b11d0f600bfb0c093", "00000001", "0a4f113b",
		"GET", "/dir/index.html", "auth")
	if got != "6629fae49393a05397450978507c4ef1" {
		t.Fatalf("response 算错: %s", got)
	}
}

func TestParseDigestHeader(t *testing.T) {
	h := `Digest realm="IP Camera(D6A6)", qop="auth", nonce="5f3a1c", opaque="x,y"`
	kv := parseDigestHeader(h)
	if kv["realm"] != "IP Camera(D6A6)" {
		t.Fatalf("realm 解析错（引号内的逗号/括号）: %q", kv["realm"])
	}
	if kv["qop"] != "auth" || kv["nonce"] != "5f3a1c" || kv["opaque"] != "x,y" {
		t.Fatalf("字段解析错: %+v", kv)
	}
}

func TestDigestAuthHeaderShape(t *testing.T) {
	auth := digestAuth(`Digest realm="cam", nonce="abc", qop="auth"`,
		"PUT", "http://192.168.1.64/ISAPI/PTZCtrl/channels/1/continuous", "admin", "hik12345")
	for _, want := range []string{"Digest ", `username="admin"`, `realm="cam"`, "qop=auth", "nc=00000001", "cnonce="} {
		if !strings.Contains(auth, want) {
			t.Fatalf("Authorization 缺 %q: %s", want, auth)
		}
	}
	if strings.Contains(auth, "hik12345") {
		t.Fatal("Authorization 里不该出现明文密码")
	}
}

func TestPtzKind(t *testing.T) {
	if k, ch := ptzKind(Cam{Vendor: "hikvision"}); k != "hik" || ch != 1 {
		t.Fatalf("海康应推断为 hik:1，得到 %s:%d", k, ch)
	}
	if k, ch := ptzKind(Cam{Vendor: "dahua"}); k != "dahua" || ch != 1 {
		t.Fatalf("大华应推断为 dahua:1，得到 %s:%d", k, ch)
	}
	if k, ch := ptzKind(Cam{Vendor: "hikvision", Ptz: "hik:3"}); k != "hik" || ch != 3 {
		t.Fatalf("显式通道号应生效，得到 %s:%d", k, ch)
	}
	if k, _ := ptzKind(Cam{Vendor: "tplink"}); k != "" {
		t.Fatalf("TP-Link 没有云台，应返回空，得到 %q", k)
	}
}

func TestPtzBase(t *testing.T) {
	if got := ptzBase(Cam{Host: "192.168.1.64"}); got != "http://192.168.1.64" {
		t.Fatalf("host 拼错: %s", got)
	}
	// host 为空时从 rtsp 地址里取主机名
	if got := ptzBase(Cam{URLMain: "rtsp://192.168.1.64:554/x"}); got != "http://192.168.1.64:554" {
		t.Fatalf("回落取主机名失败: %s", got)
	}
}
