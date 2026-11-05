package instance

import (
	"testing"

	"github.com/duihao/duihao/backend/duihao/pluginc"
)

// 早期装配的实例没有落 name，List 必须在视图层按清单名回填，
// 否则前端下拉会渲染成 "undefined · nginx-1 · :80"。
func TestDisplayNameFallsBackToManifest(t *testing.T) {
	reg, err := pluginc.Load()
	if err != nil {
		t.Fatalf("load plugin registry: %v", err)
	}
	m := &Manager{plugins: reg}

	cases := map[string]string{
		"nginx":     "Nginx",
		"mysql":     "MySQL",
		"ftpserver": "FTPServer",
	}
	for plugin, want := range cases {
		if got := m.displayName(plugin); got != want {
			t.Errorf("displayName(%q) = %q, want %q", plugin, got, want)
		}
	}

	// 清单里没有的插件 id：退化为 id 本身，绝不返回空串。
	if got := m.displayName("no-such-plugin"); got != "no-such-plugin" {
		t.Errorf("unknown plugin displayName = %q, want plugin id", got)
	}

	// plugins 为 nil 时（测试/降级路径）不能 panic。
	empty := &Manager{}
	if got := empty.displayName("nginx"); got != "nginx" {
		t.Errorf("nil registry displayName = %q, want plugin id", got)
	}
}
