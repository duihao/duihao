package main

// 会员中心独立窗口（B8）。
//
// 形态照 manage_window.go：Frameless + 透明 + 前端 CSS 圆角，标题栏由前端
// 自绘（--wails-draggable 拖动区 + 最小化/关闭）。窗口内加载 /?w=member，
// 渲染 MembershipStandalone（会员状态 / 套餐 / 扫码支付 / 结果）。
//
// 为什么不复用 el-dialog：NAS 主窗只有 330 DIP 宽，520px 的对话框在竖窗里
// 几乎铺满，套餐卡和二维码挤成一列，观感很差（用户 2026-11-02 明确要求
// 「点击弹出独立会员中心窗口」）。独立窗有横向空间做左右两栏。
//
// 全局单例：会员中心同一时刻只需要一个，重复点只聚焦。

import (
	"fmt"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/duihao/duihao/backend/log"
)

// 会员中心窗几何：左栏会员权益（商品摘要，10 条左右）+ 右栏问候/套餐/支付
// 正好一屏放下，不滚动。最小宽 720 保两栏不塌（再窄按单列堆叠）。
const (
	memberWinW      = 880
	memberWinH      = 620
	memberWinMinW   = 720
	memberWinMinH   = 560
	memberWindowKey = "membership"
)

var (
	memberWinMu sync.Mutex
	memberWin   *application.WebviewWindow
)

// OpenMembershipWindow 打开（或聚焦）会员中心窗口，返回窗口 key。
func (a *App) OpenMembershipWindow() (string, error) {
	if a.app == nil {
		return "", fmt.Errorf("窗口管理器尚未就绪")
	}

	memberWinMu.Lock()
	if w := memberWin; w != nil {
		memberWinMu.Unlock()
		w.Show()
		w.Focus()
		return memberWindowKey, nil
	}
	memberWinMu.Unlock()

	w := a.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "对号 · 会员中心",
		Width:     memberWinW,
		Height:    memberWinH,
		MinWidth:  memberWinMinW,
		MinHeight: memberWinMinH,
		// 与主窗口/管理窗同款：边框、圆角、阴影全由前端 CSS 画。
		Frameless:        true,
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		Windows: application.WindowsWindow{
			DisableFramelessWindowDecorations: true,
		},
		CloseButtonState: application.ButtonHidden,
		URL:              "/?w=member",
	})

	memberWinMu.Lock()
	memberWin = w
	memberWinMu.Unlock()

	// 关窗即清理：下次点「会员中心」重新建，避免持有已销毁的窗口对象。
	w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		memberWinMu.Lock()
		if memberWin == w {
			memberWin = nil
		}
		memberWinMu.Unlock()
		log.Writef("member window closed")
	})

	w.Center()
	w.Show()
	w.Focus()
	log.Writef("member window opened")
	return memberWindowKey, nil
}
