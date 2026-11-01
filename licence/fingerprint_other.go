//go:build !windows

package licence

import (
	"errors"
	"os"
	"runtime"
	"strings"
)

// otherParts：非 Windows 兜底 —— machine-id（/etc/machine-id 或 IOPlatformUUID）。
// 桌面版当前只发 Windows；这份实现保证 darwin/linux 开发机可跑通流程。
func platformParts() ([3]string, error) {
	id := machineID()
	if id == "" {
		return [3]string{}, errors.New("设备信息采集失败")
	}
	h := partHash(id)
	return [3]string{h, h, h}, nil
}

func machineID() string {
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if b, err := os.ReadFile(p); err == nil {
			if v := trimSpace(string(b)); v != "" {
				return v
			}
		}
	}
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(h)
}

func deviceName() string {
	h, _ := os.Hostname()
	if trimSpace(h) == "" {
		return runtime.GOOS
	}
	return trimSpace(h)
}

func deviceOS() string { return runtime.GOOS + "/" + runtime.GOARCH }

