package voice

import (
	"strings"
	"testing"
	"time"
)

var cst = time.FixedZone("CST", 8*3600)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, cst)
	if err != nil {
		panic(err)
	}
	return t
}

// TestNextDue 七种周期口径（时间全部显式给，不依赖 now）。
func TestNextDue(t *testing.T) {
	base := at("2026-10-30 10:00") // 周五

	cases := []struct {
		name string
		ev   event
		from time.Time
		want string // 空 = 不再触发
	}{
		{"单次未到", event{Period: "once", StartDate: "2026-10-30", AtTime: "11:53"}, base, "2026-10-30 11:53"},
		{"单次已过", event{Period: "once", StartDate: "2026-10-30", AtTime: "09:00"}, base, ""},
		{"单次未来日", event{Period: "once", StartDate: "2026-11-01", AtTime: "09:00"}, base, "2026-11-01 09:00"},
		{"每日今天", event{Period: "daily", StartDate: "2026-01-01", AtTime: "11:53"}, base, "2026-10-30 11:53"},
		{"每日已过", event{Period: "daily", StartDate: "2026-01-01", AtTime: "09:00"}, base, "2026-10-31 09:00"},
		{"重复窗口内", event{Period: "repeat", StartDate: "2026-10-01", IntervalMin: 60, DayFrom: "09:00", DayTo: "18:00"}, base, "2026-10-30 11:00"},
		{"重复对齐", event{Period: "repeat", StartDate: "2026-10-01", IntervalMin: 60, DayFrom: "09:00", DayTo: "18:00"}, at("2026-10-30 10:30"), "2026-10-30 11:00"},
		{"重复窗口到尾", event{Period: "repeat", StartDate: "2026-10-01", IntervalMin: 60, DayFrom: "09:00", DayTo: "18:00"}, at("2026-10-30 18:00"), "2026-10-31 09:00"},
		{"重复半小时", event{Period: "repeat", StartDate: "2026-10-01", IntervalMin: 30, DayFrom: "09:00", DayTo: "12:00"}, at("2026-10-30 10:10"), "2026-10-30 10:30"},
		{"每周今天", event{Period: "weekly", StartDate: "2026-01-01", Weekdays: "1,3,5", AtTime: "11:00"}, base, "2026-10-30 11:00"},
		{"每周跳到下周一", event{Period: "weekly", StartDate: "2026-01-01", Weekdays: "1,3,5", AtTime: "11:00"}, at("2026-10-30 12:00"), "2026-11-02 11:00"},
		{"每周空则按起始日", event{Period: "weekly", StartDate: "2026-10-28", AtTime: "08:00"}, base, "2026-11-04 08:00"},
		{"每月同日", event{Period: "monthly", StartDate: "2026-01-31", AtTime: "09:00"}, base, "2026-10-31 09:00"},
		{"每月压月末", event{Period: "monthly", StartDate: "2026-01-31", AtTime: "09:00"}, at("2026-11-01 00:00"), "2026-11-30 09:00"},
		{"每季", event{Period: "quarterly", StartDate: "2026-01-15", AtTime: "09:00"}, base, "2027-01-15 09:00"},
		{"每年", event{Period: "yearly", StartDate: "2020-03-08", AtTime: "09:30"}, base, "2027-03-08 09:30"},
	}
	for _, c := range cases {
		got := nextDue(c.ev, c.from)
		gotStr := ""
		if !got.IsZero() {
			gotStr = got.Format("2006-01-02 15:04")
		}
		if gotStr != c.want {
			t.Errorf("%s: got %q want %q", c.name, gotStr, c.want)
		}
	}
}

func TestComposeText(t *testing.T) {
	cases := [][3]string{
		{"喝水", "该喝水了", "喝水。该喝水了"},
		{"", "只有内容", "只有内容"},
		{"只有标题", "", "只有标题"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := composeText(c[0], c[1]); got != c[2] {
			t.Errorf("composeText(%q,%q)=%q want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestNormWeekdays(t *testing.T) {
	cases := map[string]string{
		"5,1,3,1": "1,3,5",
		"0,8,x":   "",
		" 7 ":     "7",
	}
	for in, want := range cases {
		if got := normWeekdays(in); got != want {
			t.Errorf("normWeekdays(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSafeAudioName(t *testing.T) {
	ok := []string{"abc123.wav", "a-b.1.wav"}
	bad := []string{"", "../x.wav", "a/b.wav", `a\b.wav`, "abc.mp3", strings.Repeat("a", 70) + ".wav"}
	for _, s := range ok {
		if !safeAudioName(s) {
			t.Errorf("应允许 %q", s)
		}
	}
	for _, s := range bad {
		if safeAudioName(s) {
			t.Errorf("应拒绝 %q", s)
		}
	}
}

func TestResolveSpeech(t *testing.T) {
	lang, rate, vol, _ := resolveSpeech("huihui-slow", "urge")
	if lang != "zh-CN" {
		t.Errorf("lang=%q", lang)
	}
	if rate != -2 { // -5 + 3
		t.Errorf("rate=%d want -2", rate)
	}
	if vol != 100 {
		t.Errorf("vol=%d want 100", vol)
	}
	// 未知键回退默认
	lang2, rate2, vol2, _ := resolveSpeech("nope", "nope")
	if lang2 != "zh-CN" || rate2 != 0 || vol2 != 100 {
		t.Errorf("回退异常: %q %d %d", lang2, rate2, vol2)
	}
}

func TestAudioNameStable(t *testing.T) {
	a := audioName("sapi", "huihui-std", "normal", "喝水。该喝水了")
	b := audioName("sapi", "huihui-std", "normal", "喝水。该喝水了")
	c := audioName("sapi", "huihui-soft", "normal", "喝水。该喝水了")
	// 同一句话，只是换了引擎（GPU → 本机）：必须重新合成，不能放出旧音频
	d := audioName("http|http://gpu:9880|cosy|a|1.00", "huihui-std", "normal", "喝水。该喝水了")
	if a != b {
		t.Error("同样内容应得同样文件名")
	}
	if a == c {
		t.Error("不同音色应有不同文件名")
	}
	if a == d {
		t.Error("不同引擎应有不同文件名")
	}
	if !strings.HasSuffix(a, ".wav") {
		t.Error("应以 .wav 结尾")
	}
}
