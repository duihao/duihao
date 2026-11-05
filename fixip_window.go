package main

// 「固定内网地址」弹窗（桌面独立窗）。
//
// 形态照 v6_window.go / member_window.go：Frameless + 透明 + 前端 CSS 圆角，
// 标题栏自绘。窗口内加载 /?w=fixip，渲染 FixIpStandalone。
//
// 为什么不塞进 NAS 主窗的设置下拉：那个窗只有 330 DIP 宽，配置卡六行
// （网卡/IP/掩码/网关/DNS/当前方式）+ 两段说明 + 三步提示，竖窗里会挤成一团，
// 而且这是一次性操作，盖在首屏上做完就关，独立窗更合适。
//
// 全局单例：同一时刻只需要一个，重复点只聚焦。

import (
	"fmt"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/duihao/duihao/backend/log"
)

const (
	fixIpWinW      = 520
	fixIpWinH      = 672
	fixIpWinMinW   = 460
	fixIpWinMinH   = 560
	fixIpWindowKey = "fixip"
)

var (
	fixIpWinMu sync.Mutex
	fixIpWin   *application.WebviewWindow
)

// fixIpWindowGeometry 在 520×640 基础上按主屏工作区收口（小屏上别盖住任务栏）。
func fixIpWindowGeometry(w3app *application.App) (w, h, minW, minH int) {
	w, h, minW, minH = fixIpWinW, fixIpWinH, fixIpWinMinW, fixIpWinMinH
	if w3app == nil || w3app.Screen == nil {
		return
	}
	sc := w3app.Screen.GetPrimary()
	if sc == nil {
		return
	}
	wa := sc.WorkArea
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

// OpenFixIpWindow 打开（或聚焦）「固定内网地址」窗口，返回窗口 key。
func (a *App) OpenFixIpWindow() (string, error) {
	if a.app == nil {
		return "", fmt.Errorf("窗口管理器尚未就绪")
	}

	fixIpWinMu.Lock()
	if w := fixIpWin; w != nil {
		fixIpWinMu.Unlock()
		w.Show()
		w.Focus()
		return fixIpWindowKey, nil
	}
	fixIpWinMu.Unlock()

	winW, winH, winMinW, winMinH := fixIpWindowGeometry(a.app)

	w := a.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "对号 · 固定内网地址",
		Width:     winW,
		Height:    winH,
		MinWidth:  winMinW,
		MinHeight: winMinH,
		Frameless: true,
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		Windows: application.WindowsWindow{
			DisableFramelessWindowDecorations: true,
		},
		CloseButtonState: application.ButtonHidden,
		URL:              "/?w=fixip",
	})

	fixIpWinMu.Lock()
	fixIpWin = w
	fixIpWinMu.Unlock()

	w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		fixIpWinMu.Lock()
		if fixIpWin == w {
			fixIpWin = nil
		}
		fixIpWinMu.Unlock()
		log.Writef("fixip window closed")
	})

	w.Center()
	w.Show()
	w.Focus()
	log.Writef("fixip window opened")
	return fixIpWindowKey, nil
}
