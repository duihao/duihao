// m13 多版本支持：{setifver:} 版本门控 token + verAtLeast 数值比较单测。
package instance

import "testing"

func TestVerAtLeast(t *testing.T) {
	cases := []struct {
		v, min string
		want   bool
	}{
		{"8.0.40", "8.0.30", true},
		{"8.4.4", "8.0.30", true},
		{"5.7.44", "8.0.30", false},
		{"8.0.29", "8.0.30", false},
		{"8.0.30", "8.0.30", true},
		{"8.0", "8.0.0", true},      // 缺段按 0
		{"10", "8.0.30", true},      // 段数少但高位更大
		{"5.7", "5.6", true},
		{"v8.0.40", "8.0.30", true}, // v 前缀容忍
		{"", "8.0.30", false},       // 空版本不越门
		{"abc", "8.0.30", false},    // 非数字段按 0，无害
	}
	for _, c := range cases {
		if got := verAtLeast(c.v, c.min); got != c.want {
			t.Errorf("verAtLeast(%q,%q)=%v want %v", c.v, c.min, got, c.want)
		}
	}
}

func TestApplyTokensSetifver(t *testing.T) {
	// 注意：与既有 {setif:} 语义一致 —— token 自身补一个 \n，模板里 token
	// 之后的字面 \n 会留下空行（ini 文件无害，mysql/redis 模板既有行为）。
	render := func(ver string) string {
		v := Vars{
			ID: "i", Dir: "d", RuntimeDir: "r", Bin: "b", Version: ver,
			Set: map[string]string{"mysql.innodb_redo_log_capacity": "256"},
		}
		return applyTokens("head\n{setifver:8.0.30:innodb_redo_log_capacity={set:mysql.innodb_redo_log_capacity}M}\ntail\n", v)
	}
	if got := render("8.0.40"); got != "head\ninnodb_redo_log_capacity=256M\n\ntail\n" {
		t.Errorf("8.0.40 应保留行: %q", got)
	}
	if got := render("5.7.44"); got != "head\n\ntail\n" {
		t.Errorf("5.7.44 应删行: %q", got)
	}
	// 与 {setif:} 相邻共存：setif 不误吞 setifver（"{setif:" 不匹配 "{setifver:"）
	v := Vars{ID: "i", Dir: "d", RuntimeDir: "r", Bin: "b", Version: "5.7.44",
		Set: map[string]string{"k": "1"}}
	got := applyTokens("{setif:k:aaa}\n{setifver:8.0.30:bbb}\nccc\n", v)
	if got != "aaa\n\n\nccc\n" {
		t.Errorf("setif/setifver 共存: %q", got)
	}
}
