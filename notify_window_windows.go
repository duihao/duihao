//go:build windows

package main

import "unsafe"

// applyNotifyWindowOS 给提醒卡片套 Windows 专属样式：
//   - WS_EX_TOOLWINDOW + 去掉 WS_EX_APPWINDOW：不进任务栏、不出现在 Alt+Tab
//     （HiddenOnTaskbar 只管任务栏按钮，APPWINDOW 样式会让它再冒出来）；
//   - 去掉 DWM 的原生 1px 边框与系统圆角：否则浅色主题下圆角四周会描出白框。
//
// 只在窗口刚创建时套一次（每次 Show 都 SetWindowPos(FRAMECHANGED) 会瞬态失焦）。
func applyNotifyWindowOS(hwnd unsafe.Pointer) {
	if hwnd == nil {
		return
	}
	styleToolWindow(uintptr(hwnd))
	removeNativeWindowBorder(hwnd)
}
