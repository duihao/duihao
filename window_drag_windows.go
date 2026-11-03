//go:build windows

package main

// Frameless 窗口的「按住顶栏拖动」。
//
// 做法：先 ReleaseCapture 让浏览器放手，再给窗口自己发一条
// WM_NCLBUTTONDOWN + HTCAPTION —— 等于告诉 Windows「用户刚刚按住了标题栏」。
// 之后整段拖动由系统的模态循环接管：贴边吸附、双击最大化、拖到另一块屏 /
// 拖到屏幕顶端触发贴靠，全都是原生行为，比在 JS 里算坐标发 SetPosition 稳。
//
// 两个必须注意的点（2026-11-02 实测踩过）：
//  1. 必须回到 UI 线程执行。ReleaseCapture 作用于「调用线程的鼠标捕获」，在
//     HTTP handler 的 goroutine 里调它，WebView2 那边仍抓着 capture，拖动被
//     立刻掐断（请求回来了、窗口却纹丝不动）。
//  2. SendMessage 会一直阻塞到用户松手，所以不能同步等（InvokeAsync）。

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

const (
	wmNCLButtonDown = 0x00A1 // WM_NCLBUTTONDOWN
	htCaption       = 2      // HTCAPTION
)

func dragWindow(w *application.WebviewWindow) {
	hwnd := uintptr(w.NativeWindow())
	if hwnd == 0 {
		return
	}
	application.InvokeAsync(func() {
		user32 := windows.NewLazySystemDLL("user32.dll")
		user32.NewProc("ReleaseCapture").Call()
		user32.NewProc("SendMessageW").Call(hwnd, wmNCLButtonDown, htCaption, 0)
	})
}
