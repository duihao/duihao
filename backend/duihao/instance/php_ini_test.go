// php.ini 模板的扩展清单回归：清单里声明了多少个扩展开关，模板里就必须
// 有一一对应的 extension 行，且版本门控（imap 只在 <8.4 出现）必须生效。
//
// 为什么值得单独测：模板是内联字符串 + token，两边（settings 的 key 与
// 模板的 {setif:...}）靠人工保持一致，漏一个用户就会「开关点了没反应」。
package instance

import (
	"strings"
	"testing"

	"github.com/ys-ll/uniterm/backend/duihao/pluginc"
)

func phpManifest(t *testing.T) *pluginc.Manifest {
	t.Helper()
	reg, err := pluginc.Load()
	if err != nil {
		t.Fatalf("加载内置清单: %v", err)
	}
	m, ok := reg.Get("php")
	if !ok {
		t.Fatalf("缺少内置 php 清单")
	}
	return m
}

func phpIniTemplate(t *testing.T, m *pluginc.Manifest) string {
	t.Helper()
	for _, a := range m.Lifecycle.Install {
		if a.Action == "write" && strings.HasSuffix(a.Dst, "php.ini") {
			return a.From
		}
	}
	t.Fatalf("php 清单里没有写 php.ini 的动作")
	return ""
}

// 全部扩展开关 → 渲染后的 ini 必须包含对应行（opcache 是 zend_extension）
func TestPhpIniRendersAllExtensions(t *testing.T) {
	m := phpManifest(t)
	tpl := phpIniTemplate(t, m)

	// 把所有扩展开关打开
	set := map[string]string{}
	var keys []string
	for _, g := range m.Settings.Groups {
		for _, f := range g.Fields {
			if strings.HasPrefix(f.Key, "php.ext_") {
				set[f.Key] = "1"
				keys = append(keys, f.Key)
			}
		}
	}
	if len(keys) < 30 {
		t.Fatalf("扩展开关只有 %d 个，明显偏少", len(keys))
	}

	ini := applyTokens(tpl, Vars{Dir: `C:\data\instances\php1`, Set: set, Version: "8.3.14", Ports: map[string]int{}})

	for _, k := range keys {
		name := strings.TrimPrefix(k, "php.ext_")
		if name == "opcache" {
			if !strings.Contains(ini, "zend_extension=php_opcache.dll") {
				t.Errorf("opcache 应写成 zend_extension")
			}
			continue
		}
		if !strings.Contains(ini, "extension="+name+"\n") {
			t.Errorf("%s 已声明但模板没有渲染 extension=%s", k, name)
		}
	}
}

// 关掉时一行都不该留下（避免「开关点了没反应」的反向问题）
func TestPhpIniExtensionsOff(t *testing.T) {
	m := phpManifest(t)
	tpl := phpIniTemplate(t, m)

	set := map[string]string{}
	for _, g := range m.Settings.Groups {
		for _, f := range g.Fields {
			if strings.HasPrefix(f.Key, "php.ext_") {
				set[f.Key] = "0"
			}
		}
	}
	ini := applyTokens(tpl, Vars{Dir: `C:\data\instances\php1`, Set: set, Version: "8.3.14", Ports: map[string]int{}})
	if strings.Contains(ini, "extension=") {
		t.Errorf("全部关闭后仍有 extension 行:\n%s", ini)
	}
}

// imap：PHP 8.4 起官方 Windows 包不再带 php_imap.dll，模板必须按版本门控。
func TestPhpIniImapVersionGate(t *testing.T) {
	m := phpManifest(t)
	tpl := phpIniTemplate(t, m)

	base := map[string]string{
		"php.ext_imap":   "1",
		"php.ext_curlim": "",
	}
	cases := []struct {
		version string
		want    bool
	}{
		{"8.2.29", true},
		{"8.3.14", true},
		{"8.4.4", false},
		{"8.10.1", false}, // 逐段数值比较，不是字符串比较
		{"", true},        // 老实例记录缺版本：按低版本处理，别丢扩展
	}
	for _, c := range cases {
		ini := applyTokens(tpl, Vars{Dir: `C:\d`, Set: base, Version: c.version, Ports: map[string]int{}})
		has := strings.Contains(ini, "extension=imap")
		if has != c.want {
			t.Errorf("版本 %q: imap 出现=%v，期望 %v", c.version, has, c.want)
		}
	}
}
