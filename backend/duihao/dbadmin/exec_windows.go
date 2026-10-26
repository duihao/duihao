//go:build windows

package dbadmin

import (
	"os/exec"
	"syscall"
)

// hideWindow 后台调用 mysqldump/mysql 时不弹控制台黑窗。
// 对号是 windowsgui 子系统，任何控制台子进程不加这个标志都会闪一个窗口。
const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
