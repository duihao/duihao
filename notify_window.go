package main

// 到点提醒卡片窗（语音提醒 V2）：屏幕右上角一张无边框卡片，写着「该喝水了」，
// 右边两个按钮「完成」「延迟」。
//
// 形态沿用 M18-C 命令面板窗：Frameless + 透明 + 前端 CSS 画圆角与阴影，
// 不进任务栏、不抢焦点（只在右上角出现，用户点它才响应）。
//
// 分工：调度器（webdavd 里的 apps/voice）**发通知**，这里负责开窗；
// 按钮动作走 App.NotifyAction binding 回到 Go —— 前端拿不到 uid/事件 id，
// 只能传 "done"/"snooze"，改库用的是调度器给的闭包（见 apps/voice/notify.go）。
//
// 同一时刻只留一张卡：新的到点提醒直接顶掉旧卡（提醒是"当下"的事，
// 两条提醒叠着反而都不重要了）。

import (
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/duihao/duihao/backend/duihao/webdavd/apps/voice"
	"github.com/duihao/duihao/backend/log"
)

const (
	// notifyWinW/H 卡片尺寸（DIP）。380 宽刚好放下「头像 + 两行正文 + 两个按钮」。
	notifyWinW = 380
	notifyWinH = 124
	// notifyMargin 距屏幕右上角的留白（DIP）。
	notifyMargin = 16
)

// notifyCtx 一张卡片在 Go 侧的全部状态。
type notifyCtx struct {
	win    *application.WebviewWindow
	ack    func(action string, minutes int) error
	snooze int
	title  string
	timer  *time.Timer
}

var (
	notifyWinMu sync.Mutex
	notifyCur   *notifyCtx
)

// initVoiceNotifier 把「到点弹卡片」接到语音提醒的调度器上。
// 在 ServiceStartup 里调一次：连不上（没注册）也能正常出声，弹窗只是增强。
func (a *App) initVoiceNotifier() {
	voice.SetNotifier(a.showVoiceNotice)
	log.Writef("voice: 到点提醒卡片已接入（右上角弹窗）")
}

// showVoiceNotice 由 apps/voice 的调度器回调（到点触发）。可能在任意 goroutine 上。
func (a *App) showVoiceNotice(n voice.Notice) {
	if a == nil || a.app == nil {
		return
	}
	// 顶掉上一张（含它的自动关闭定时器）
	a.closeNotifyWindow()

	title := n.Title
	if title == "" {
		title = "提醒"
	}
	snooze := n.SnoozeMin
	if snooze <= 0 {
		snooze = voice.SnoozeDefault
	}
	u := fmt.Sprintf("/?w=notify&t=%s&c=%s&sn=%d&ac=%d",
		url.QueryEscape(title), url.QueryEscape(n.Content), snooze, voice.NoticeAutoClose)

	win := a.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "对号 · " + title,
		Width:  notifyWinW,
		Height: notifyWinH,
		// 先隐藏：等 HWND 就绪后先摆到右上角再显示，避免先在屏幕中间闪一下。
		Hidden:        true,
		Frameless:     true,
		AlwaysOnTop:   true,
		DisableResize: true,
		// 圆角/阴影全由前端 CSS 画。
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		Windows: application.WindowsWindow{
			DisableFramelessWindowDecorations: true,
			HiddenOnTaskbar:                   true,
		},
		CloseButtonState: application.ButtonHidden,
		URL:              u,
	})

	ctx := &notifyCtx{win: win, ack: n.Ack, snooze: snooze, title: title}
	notifyWinMu.Lock()
	notifyCur = ctx
	notifyWinMu.Unlock()

	// 关窗（用户点关闭 / 系统回收）即清理，别持有已销毁的对象。
	win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		notifyWinMu.Lock()
		if notifyCur == ctx {
			notifyCur = nil
		}
		notifyWinMu.Unlock()
		if ctx.timer != nil {
			ctx.timer.Stop()
		}
	})

	// 自动收起：声音已经念过，卡片只是「我看见了」的确认入口。
	ctx.timer = time.AfterFunc(time.Duration(voice.NoticeAutoClose)*time.Second, func() {
		a.closeNotifyCtx(ctx, "timeout")
	})

	go func() {
		// HWND 就绪后再改样式 / 落位，否则 SetWindowPos 打空。
		for i := 0; i < 200; i++ {
			if hwnd := win.NativeWindow(); hwnd != nil {
				applyNotifyWindowOS(hwnd)
				a.placeNotifyWindow(win)
				win.Show()
				log.Writef("voice: 提醒卡片已弹出 title=%s", title)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		// 拿不到 HWND 也别把提醒憋回去：直接显示（位置由 wails 决定）。
		win.Show()
		log.Writef("voice: 提醒卡片已弹出（未拿到 HWND，位置不保证）title=%s", title)
	}()
}

// placeNotifyWindow 贴主屏工作区右上角。位置走原生物理像素（同 M21 首屏落位，
// wails 的 SetPosition 经 DIP 管线换算后会有偏移），非 Windows 上
// setWindowPosPhysical 是空实现，退回 SetPosition。
func (a *App) placeNotifyWindow(w *application.WebviewWindow) {
	x, y, scale, ok := notifyWindowPos(a.app)
	if !ok {
		log.Writef("voice: 拿不到屏幕信息，提醒卡片位置交给系统（x=%d y=%d）", x, y)
		return
	}
	w.SetPosition(x, y)
	if hwnd := w.NativeWindow(); hwnd != nil {
		setWindowPosPhysical(hwnd, int(float64(x)*scale+0.5), int(float64(y)*scale+0.5))
	}
}

// notifyWindowPos 返回卡片左上角的 DIP 坐标与主屏缩放。
func notifyWindowPos(w3app *application.App) (x, y int, scale float64, ok bool) {
	scale = 1
	if w3app == nil || w3app.Screen == nil {
		return 0, 0, scale, false
	}
	sc := w3app.Screen.GetPrimary()
	if sc == nil {
		return 0, 0, scale, false
	}
	wa := sc.WorkArea
	if wa.Width <= 0 || wa.Height <= 0 {
		return 0, 0, scale, false
	}
	if sc.PhysicalWorkArea.Width > 0 {
		scale = float64(sc.PhysicalWorkArea.Width) / float64(wa.Width)
	}
	x = wa.X + wa.Width - notifyWinW - notifyMargin
	y = wa.Y + notifyMargin
	return x, y, scale, true
}

// NotifyAction 卡片按钮的回调入口（前端 NotifyStandalone.vue 调这个）。
//
//	done   —— 记一条「已处理」流水，收起卡片
//	snooze —— 推迟 SnoozeDefault 分钟再提醒一次，收起卡片
//	close  —— 只收起卡片（右上角 ✕ / 点空白）
//
// 前端只传动作名：具体改哪条事件由调度器给的闭包决定，页面拿不到也改不了别的事件。
func (a *App) NotifyAction(kind string) (string, error) {
	notifyWinMu.Lock()
	ctx := notifyCur
	notifyWinMu.Unlock()
	if ctx == nil {
		return "closed", nil
	}
	switch kind {
	case "done", "snooze":
		// ack 为 nil = 页面上的「看看卡片」预览卡：点按钮只收起，不改任何数据。
		if ctx.ack != nil {
			minutes := 0
			if kind == "snooze" {
				minutes = ctx.snooze
			}
			if err := ctx.ack(kind, minutes); err != nil {
				return "", err
			}
		}
	case "close":
	default:
		return "", fmt.Errorf("未知操作: %s", kind)
	}
	a.closeNotifyCtx(ctx, kind)
	return "ok", nil
}

// closeNotifyWindow 收起当前卡片（无线程要求）。
func (a *App) closeNotifyWindow() {
	notifyWinMu.Lock()
	ctx := notifyCur
	notifyWinMu.Unlock()
	if ctx != nil {
		a.closeNotifyCtx(ctx, "replace")
	}
}

// closeNotifyCtx 只在 ctx 仍是「当前那张卡」时才真的关 —— 否则会把后来的
// 新提醒连坐关掉（旧卡的定时器到点时正好来了新卡就是这么发生的）。
func (a *App) closeNotifyCtx(ctx *notifyCtx, reason string) {
	notifyWinMu.Lock()
	if notifyCur != ctx {
		notifyWinMu.Unlock()
		return
	}
	notifyCur = nil
	notifyWinMu.Unlock()

	if ctx.timer != nil {
		ctx.timer.Stop()
	}
	if ctx.win != nil {
		ctx.win.Close()
	}
	log.Writef("voice: 提醒卡片收起 reason=%s title=%s", reason, ctx.title)
}

// NotifyWindowOpen 当前是否有提醒卡片（诊断 / 单测用）。
func (a *App) NotifyWindowOpen() bool {
	notifyWinMu.Lock()
	defer notifyWinMu.Unlock()
	return notifyCur != nil
}
