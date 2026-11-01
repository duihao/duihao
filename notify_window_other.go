//go:build !windows

package main

import "unsafe"

// applyNotifyWindowOS 非 Windows 平台没有要补的原生样式：窗口本来就吃
// Frameless + AlwaysOnTop + HiddenOnTaskbar 这几项，落位走 SetPosition。
func applyNotifyWindowOS(hwnd unsafe.Pointer) {}
