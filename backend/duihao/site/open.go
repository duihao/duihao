// 站点层的"打开目录"实现（与 service.openPath 同逻辑，
// 站点层不依赖 service 包，避免反向依赖）。
package site

import (
	"os/exec"
	"runtime"
)

func openPath(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}
