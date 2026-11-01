//go:build windows

package licence

import "syscall"

// hideWindow：命令行采集不能闪黑窗（GUI 进程里起子进程默认会弹控制台窗）。
func hideWindow() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
