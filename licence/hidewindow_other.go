//go:build !windows

package licence

import "syscall"

// 非 Windows：无 HideWindow 概念，返回 nil。
func hideWindow() *syscall.SysProcAttr { return nil }
