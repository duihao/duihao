package runtimepkg

import (
	"strings"
	"testing"
)

// TOFU 三条规则：首次登记 / 同摘要放行 / 摘要变化拒绝。
func TestPinTOFU(t *testing.T) {
	withDataRoot(t)
	spec := VersionSpec{PluginID: "pin-test", Version: "9.9"}
	if err := checkPin(spec, "aaa", "download"); err != nil {
		t.Fatalf("首次登记不该报错: %v", err)
	}
	if err := checkPin(spec, "aaa", "mirror"); err != nil {
		t.Fatalf("同摘要（换来源）应放行: %v", err)
	}
	err := checkPin(spec, "bbb", "download")
	if err == nil {
		t.Fatal("摘要变化必须拒绝")
	}
	if !strings.Contains(err.Error(), "指纹") {
		t.Errorf("错误信息应说明指纹不一致: %v", err)
	}
	// 空摘要（未算出/不适用）直接放行，不写记录
	if err := checkPin(VersionSpec{PluginID: "pin-empty", Version: "1"}, "", "download"); err != nil {
		t.Errorf("空摘要不该报错: %v", err)
	}
}
