//go:build windows

package voice

import (
	"os/exec"
	"syscall"
)

// openTarget 交给系统默认程序打开：网址走浏览器，路径走资源管理器。
// 用 cmd /c start 是因为它不需要任何额外依赖；黑框靠 HideWindow 藏掉。
func openTarget(arg string) error {
	cmd := exec.Command("cmd", "/c", "start", "", arg)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}
