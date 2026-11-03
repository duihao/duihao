package main

// IPv6 直连独立窗口（桌面弹窗）。
//
// 形态照 member_window.go：Frameless + 透明 + 前端 CSS 圆角，标题栏自绘。
// 窗口内加载 /?w=v6，渲染 V6Standalone（检测 → 激活 → 地址/域名/解绑）。
//
// 为什么不塞进 NAS 主窗的设置下拉：那个下拉只有 330 DIP 宽，三条 IPv6 地址
// 加上复制/打开按钮，横着根本放不下；而且这是一组「向导式」界面，盖在首屏上
// 会把宫格全遮掉。独立窗还能被用户拖到副屏，边看边在手机上调防火墙。
//
// 全局单例：同一时刻只需要一个，重复点只聚焦。

import (
	"fmt"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/ys-ll/uniterm/backend/log"
)

// 弹窗几何：默认 640×480（用户定稿：弹窗就该小，一开别占半屏）。
// 这个尺寸下折叠态（域名卡 + 地址卡 + 折叠的域名设置 + 解绑）刚好一屏，
// 展开域名设置必然超出 —— 内容区自己滚，滚动条出现即可，不用把窗口撑高。
// 最小也是 640×480：再小地址行会被挤断；最大不限，用户想摊开也能拖大。
const (
	v6WinW      = 640
	v6WinH      = 480
	v6WinMinW   = 640
	v6WinMinH   = 480
	v6WindowKey = "v6"
)

var (
	v6WinMu sync.Mutex
	v6Win   *application.WebviewWindow
)

// v6WindowGeometry 在 640×480 基础上按主屏工作区收口：极小的屏（1024×600）
// 上连 560 高都盖任务栏，宽高都不超工作区，最小尺寸同步下调 —— 内容区本来
// 就能滚，窗口矮一点只是多滚两下，不会裁内容。
func v6WindowGeometry(w3app *application.App) (w, h, minW, minH int) {
	w, h, minW, minH = v6WinW, v6WinH, v6WinMinW, v6WinMinH
	if w3app == nil || w3app.Screen == nil {
		return
	}
	sc := w3app.Screen.GetPrimary()
	if sc == nil {
		return
	}
	wa := sc.WorkArea // DIP，与窗口尺寸同单位
	if wa.Width <= 0 || wa.Height <= 0 {
		return
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
	return
}

// OpenV6Window 打开（或聚焦）IPv6 直连窗口，返回窗口 key。
func (a *App) OpenV6Window() (string, error) {
	if a.app == nil {
		return "", fmt.Errorf("窗口管理器尚未就绪")
	}

	v6WinMu.Lock()
	if w := v6Win; w != nil {
		v6WinMu.Unlock()
		w.Show()
		w.Focus()
		return v6WindowKey, nil
	}
	v6WinMu.Unlock()

	winW, winH, winMinW, winMinH := v6WindowGeometry(a.app)

	w := a.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "对号 · IPv6 直连",
		Width:     winW,
		Height:    winH,
		MinWidth:  winMinW,
		MinHeight: winMinH,
		Frameless: true,
		// 与主窗口/会员窗同款：边框、圆角、阴影全由前端 CSS 画。
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		Windows: application.WindowsWindow{
			DisableFramelessWindowDecorations: true,
		},
		CloseButtonState: application.ButtonHidden,
		URL:              "/?w=v6",
	})

	v6WinMu.Lock()
	v6Win = w
	v6WinMu.Unlock()

	// 关窗即清理：下次点「IPv6 远程访问」重新建。
	w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		v6WinMu.Lock()
		if v6Win == w {
			v6Win = nil
		}
		v6WinMu.Unlock()
		log.Writef("v6 window closed")
	})

	w.Center()
	w.Show()
	w.Focus()
	log.Writef("v6 window opened")
	return v6WindowKey, nil
}
