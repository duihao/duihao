package main

// 管理界面独立窗口（M20 试点）：把「管理」面板从 el-dialog 分离成独立卡片窗。
//
// 形态与命令面板窗一致：Frameless + 透明 + 前端 CSS 圆角，标题栏由前端自绘
// （--wails-draggable 拖动区 + 最小化/关闭）。窗口内加载 /?w=manage&k=&i=&n=
// 渲染同一套管理面板（standalone 模式 = 不套 el-dialog 外壳）。
//
// 同实例只开一个：key = panel + ":" + instanceID，重复点只聚焦不重建。

import (
	"fmt"
	"net/url"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/duihao/duihao/backend/log"
)

// manageWinSize 管理窗默认尺寸（够放下四个面板最宽的 MySQL 表格）。
const (
	manageWinW = 960
	manageWinH = 680
)

var (
	manageWinsMu sync.Mutex
	manageWins   = map[string]*application.WebviewWindow{}
)

// OpenManageWindow 打开（或聚焦）某实例的管理窗口，返回窗口 key。
// panel: mysql / redis / ftp / webdav；instanceName 仅用于标题与前端展示。
func (a *App) OpenManageWindow(panel, instanceID, instanceName string) (string, error) {
	if a.app == nil {
		return "", fmt.Errorf("窗口管理器尚未就绪")
	}
	if panel == "" || instanceID == "" {
		return "", fmt.Errorf("缺少面板类型或实例 ID")
	}
	key := panel + ":" + instanceID

	manageWinsMu.Lock()
	if w := manageWins[key]; w != nil {
		manageWinsMu.Unlock()
		w.Show()
		w.Focus()
		return key, nil
	}
	manageWinsMu.Unlock()

	title := instanceName
	if title == "" {
		title = instanceID
	}
	u := fmt.Sprintf("/?w=manage&k=%s&i=%s&n=%s",
		url.QueryEscape(panel), url.QueryEscape(instanceID), url.QueryEscape(instanceName))

	w := a.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:    "对号 · " + title,
		Width:    manageWinW,
		Height:   manageWinH,
		MinWidth: 720,
		MinHeight: 480,
		// 与主窗口同款：边框/圆角/阴影全由前端 CSS 画，原生装饰一律不要。
		Frameless:        true,
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		Windows: application.WindowsWindow{
			DisableFramelessWindowDecorations: true,
		},
		CloseButtonState: application.ButtonHidden,
		// 管理面要拖文件进网盘浏览对话框，和主窗口一致允许 OS 拖放
		EnableFileDrop: true,
		URL:            u,
	})

	manageWinsMu.Lock()
	manageWins[key] = w
	manageWinsMu.Unlock()

	// 关窗即清理：下次点「管理」重新建，避免持有已销毁的窗口对象。
	w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		manageWinsMu.Lock()
		if manageWins[key] == w {
			delete(manageWins, key)
		}
		manageWinsMu.Unlock()
		log.Writef("M20: manage window closed key=%s", key)
	})

	w.Center()
	w.Show()
	w.Focus()
	log.Writef("M20: manage window opened key=%s panel=%s inst=%s", key, panel, instanceID)
	return key, nil
}

// CloseManageWindow 关闭指定管理窗口（前端「退出」或宿主切换视图时用）。
func (a *App) CloseManageWindow(key string) {
	manageWinsMu.Lock()
	w := manageWins[key]
	delete(manageWins, key)
	manageWinsMu.Unlock()
	if w != nil {
		w.Close()
	}
}

// ManageWindowCount 当前打开的管理窗口数（诊断/测试用）。
func (a *App) ManageWindowCount() int {
	manageWinsMu.Lock()
	defer manageWinsMu.Unlock()
	return len(manageWins)
}
