package main

// NAS 应用窗（普通模式宫格 → 影视/音乐/笔记…）：用内置 webview 打开个人空间
// 的应用页，不再跳系统浏览器。
//
// 形态与 M20 管理窗不同：网页自己有顶栏和完整布局，**保留原生标题栏**最稳
//（frameless 的拖动要前端配合，hub 页面没有 --wails-draggable 区）。
//
// 同一地址只开一个窗：key = 页面 URL，重复点只调到前台，不无限开新窗。
// 关窗即清理记录（manage_window.go 同款），下次点重新建。

import (
	"fmt"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/ys-ll/uniterm/backend/log"
)

// NAS 应用窗标题**固定**为「对号 NAS」。
//
// 不做「对号 · 影视」这种按落点页命名：窗内顶栏可以在应用之间跳转，标题写死
// 某个应用名后，一切都还在那个名字下（用户 2026-10-30 明确要求统一叫 NAS）。
// 网页自己的 <title> 仍是「影视 · 对号 NAS」，那只影响浏览器标签页。
const nasWindowTitle = "对号 NAS"

// NAS 应用窗几何。个人空间的宽屏布局断点是 CSS 1120px（影视/健康/音乐在
// ≤1120 会把侧栏拆成上下堆叠的窄屏/pad 形态），所以**最小宽度必须越过 1120**，
// 否则窗口一缩就掉进 pad 排版。1160 = 1120 + 滚动条/边框余量。
const (
	nasWindowWidth     = 1320
	nasWindowHeight    = 840
	nasWindowMinWidth  = 1160
	nasWindowMinHeight = 640
)

var (
	appWinsMu sync.Mutex
	appWins   = map[string]*application.WebviewWindow{}
)

// nasWindowGeometry 按主屏工作区收敛应用窗尺寸：默认 1320×840，放不下就收，
// 但**最小宽度**只在屏幕本身比 1160 还窄时才降（否则窗口比屏幕还宽，用户连
// 拖动都费劲）。ok=false 表示屏幕信息还拿不到（Run 之前），用默认值即可。
func nasWindowGeometry(w3app *application.App) (w, h, minW, minH int, ok bool) {
	w, h, minW, minH = nasWindowWidth, nasWindowHeight, nasWindowMinWidth, nasWindowMinHeight
	if w3app == nil || w3app.Screen == nil {
		return w, h, minW, minH, false
	}
	sc := w3app.Screen.GetPrimary()
	if sc == nil {
		return w, h, minW, minH, false
	}
	wa := sc.WorkArea // DIP，与窗口尺寸同单位
	if wa.Width <= 0 || wa.Height <= 0 {
		return w, h, minW, minH, false
	}
	if maxW := wa.Width - 40; w > maxW && maxW > 0 {
		w = maxW
	}
	if maxH := wa.Height - 60; h > maxH && maxH > 0 {
		h = maxH
	}
	if minW > w {
		minW = w
	}
	if minH > h {
		minH = h
	}
	return w, h, minW, minH, true
}

// OpenAppWindow 打开（或聚焦）一个 NAS 应用窗，返回窗口 key。
//
// title 参数保留只为兼容已生成的绑定签名，**不再用于窗口标题**（见
// nasWindowTitle）；窗内可自由切换应用，标题始终是「对号 NAS」。
func (a *App) OpenAppWindow(title, rawURL string) (string, error) {
	if a.app == nil {
		return "", fmt.Errorf("窗口管理器尚未就绪")
	}
	if rawURL == "" {
		return "", fmt.Errorf("地址为空（网盘服务没启动？）")
	}
	key := rawURL

	appWinsMu.Lock()
	if w := appWins[key]; w != nil {
		appWinsMu.Unlock()
		w.Show()
		w.Focus()
		return key, nil
	}
	appWinsMu.Unlock()

	w0, h0, minW, minH, _ := nasWindowGeometry(a.app)
	w := a.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: nasWindowTitle,
		Width: w0, Height: h0,
		MinWidth: minW, MinHeight: minH,
		// 网页有自己的顶栏（可滚动、可交互），原生标题栏负责拖动/缩放/关闭
		URL: rawURL,
		// 监控/语音页面要拖文件上传，与主窗口一致允许 OS 拖放
		EnableFileDrop: true,
	})

	appWinsMu.Lock()
	appWins[key] = w
	appWinsMu.Unlock()

	w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		appWinsMu.Lock()
		if appWins[key] == w {
			delete(appWins, key)
		}
		appWinsMu.Unlock()
		log.Writef("app window closed key=%s title=%s", key, title)
	})

	w.Center()
	w.Show()
	return key, nil
}
