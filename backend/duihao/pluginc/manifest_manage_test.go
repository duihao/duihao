package pluginc

import (
	"strings"
	"testing"
)

// 清单 manage 段：只声明 panel（「管理」按钮直连哪个内置连接管理界面）。
func TestParseManagePanel(t *testing.T) {
	raw := `{"schemaVersion":2,"id":"demo","name":"Demo","kind":"embedded",
		"manage":{"panel":"mysql"}}`
	m, warns, err := parseManifest([]byte(raw))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	for _, w := range warns {
		if strings.Contains(w, "manage") {
			t.Fatalf("manage 被当作未知字段: %s", w)
		}
	}
	if m.Manage == nil || m.Manage.Panel != "mysql" {
		t.Fatalf("Manage 解析错误: %+v", m.Manage)
	}
}

// 内置清单：只有内置了连接管理界面的软件才带 manage，且 panel 取值固定。
func TestBuiltinManageDeclaration(t *testing.T) {
	want := map[string]string{"mysql": "mysql", "redis": "redis", "ftpserver": "ftp", "webdav": "webdav"}
	entries, err := builtinFS.ReadDir("embed/builtin")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := builtinFS.ReadFile("embed/builtin/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		m, _, err := parseManifest(data)
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		panel, ok := want[m.ID]
		if !ok {
			if m.Manage != nil {
				t.Fatalf("%s 不应声明 manage", m.ID)
			}
			continue
		}
		found++
		if m.Manage == nil || m.Manage.Panel != panel {
			t.Fatalf("%s 的 manage.panel 期望 %q，实际 %+v", m.ID, panel, m.Manage)
		}
	}
	if found != len(want) {
		t.Fatalf("内置清单应含 %d 个管理面，实际 %d", len(want), found)
	}
}
