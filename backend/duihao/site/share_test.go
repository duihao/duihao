package site

import "testing"

// 站点片段命名：启用 = .conf（被 nginx 的 sites/*.conf 加载），
// 停用 = .conf.off（文件留着但不再加载）。这是「站点开关不影响共享服务」的落点，
// 写错后缀就会出现「停用了站点却还在响应」或「启用了却 404」。
func TestFragName(t *testing.T) {
	if got := fragName("site-1", false); got != "sites/site-1.conf" {
		t.Fatalf("启用片段 = %q", got)
	}
	if got := fragName("site-1", true); got != "sites/site-1.conf.off" {
		t.Fatalf("停用片段 = %q", got)
	}
	// 后缀必须是 .off：否则 include sites/*.conf 还会把它加载进去
	if fragName("x", true)[len("sites/x.conf"):] != ".off" {
		t.Fatalf("停用后缀不对: %q", fragName("x", true))
	}
}

// 共享实例的引用计数：停一个站点不能把别的启用站点还在用的 php/nginx 停掉。
func TestReferencedByEnabled(t *testing.T) {
	recs := []*Record{
		{ID: "site-1", Web: "nginx-1", PHP: "php-1", DB: "mysql-1"},
		{ID: "site-2", Web: "nginx-1", PHP: "php-2"},
		{ID: "site-3", Web: "nginx-1", PHP: "php-1", Disabled: true},
	}
	cases := []struct {
		inst    string
		exclude string
		want    bool
	}{
		{"nginx-1", "site-1", true},  // site-2 还在用
		{"php-1", "site-2", true},    // site-1 在用（site-3 已停用不算）
		{"php-1", "site-1", false},   // 只有停用的 site-3 引用 → 可回收
		{"php-1", "site-1", false},   // 同上（重复断言保持表驱动可读）
		{"php-2", "", true},          // 没排除任何站点时 site-2 在用
		{"mysql-1", "site-1", false}, // 仅 site-1 引用 → 停它后可回收
		{"", "", false},              // 空 id 永不命中
	}
	for i, c := range cases {
		if got := referencedByEnabled(recs, c.inst, c.exclude); got != c.want {
			t.Fatalf("case %d: inst=%s exclude=%s got=%v want=%v", i, c.inst, c.exclude, got, c.want)
		}
	}
	// 全部停用 → 谁都不引用
	allOff := []*Record{{ID: "s1", PHP: "php-1", Disabled: true}}
	if referencedByEnabled(allOff, "php-1", "") {
		t.Fatal("全停用时不应判为被引用")
	}
	// nil 记录不应 panic
	if referencedByEnabled([]*Record{nil}, "php-1", "") {
		t.Fatal("nil 记录不应命中")
	}
}
