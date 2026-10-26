//go:build windows

package site

import (
	"os/exec"
	"syscall"
)

// hideWindow GUI 宿主下不弹控制台黑窗（certutil 是控制台程序）。
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
