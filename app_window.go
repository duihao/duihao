package main

// NAS 应用窗（普通模式宫格 → 影视/音乐/笔记…）：用内置 webview 打开个人空间
// 的应用页，不再跳系统浏览器。
//
// 形态：**Frameless（无原生标题栏）**，最小化/最大化/关闭由网页在顶栏右端
// 自绘，拖动也由网页交给系统处理（见下面的窗口按钮桥）。网页自己有顶栏，再叠
// 一条原生标题栏既重复又吃高度，用户 2026-11-02 最终定稿就是这一版。
//
// 同一地址只开一个窗：key = 页面 URL，重复点只调到前台，不无限开新窗。
// 关窗即清理记录（manage_window.go 同款），下次点重新建。

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/duihao/duihao/backend/duihao/webdavd"
	"github.com/duihao/duihao/backend/log"
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

// nasCaptionTheme 与「隐藏标题文字」那一版一起退休了：窗口现在没有原生标题栏，
// 三个按钮由网页画（webui/winctl.js），拖动走 /-shell/drag。

// ── 窗口按钮桥 ──
//
// 模块窗是 Frameless，页面加载的是真实网页（http://127.0.0.1:<port>/-notes），
// 不是 Wails 的 assetserver 页面，拿不到 window.wails —— 网页既不能调
// Window.Close()，也不能用 --wails-draggable 拖窗口。所以「网页画按钮 → 回本机
// 请 Go 执行」，通道是 webdavd 的 /-shell/<act>（见 webdavd/shell.go）。
//
// 每窗一张 32 位十六进制随机票：开窗时追加到 URL（_dw=…），网页存进
// sessionStorage，窗内用顶栏导航跳模块后仍在；关窗即作废。用浏览器打开同一
// 页面没有票，winctl.js 直接空转，一个像素都不变。
var (
	shellWinsMu sync.Mutex
	shellWins   = map[string]*application.WebviewWindow{}
)

// newShellToken 生成窗口票（16 字节 → 32 个十六进制字符）。
func newShellToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// withShellToken 把票拼进 URL（保留原有查询串）。
func withShellToken(rawURL, token string) string {
	if token == "" {
		return rawURL
	}
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	return rawURL + sep + "_dw=" + token
}

// shellDispatch 按票找到窗口并执行动作。ok=false 表示票无效（网页不发第二遍）。
// 第二个返回值是执行**之后**的最大化状态，网页拿它决定右上角那颗按钮画
// 「最大化」还是「向下还原」—— 前端自己翻转迟早和真实状态走散（窗口已经
// 最大化时切到另一个应用，新页面根本不知道）。
func shellDispatch(token, act string) (ok, maximised bool) {
	shellWinsMu.Lock()
	w := shellWins[token]
	shellWinsMu.Unlock()
	if w == nil {
		return false, false
	}
	switch act {
	case "min":
		w.Minimise()
	case "max":
		w.ToggleMaximise()
	case "state": // 只查询
	case "close":
		w.Close()
		return true, false // 窗已销毁，别再问它状态
	case "drag":
		dragWindow(w)
	default:
		return false, false
	}
	return true, w.IsMaximised()
}

func init() { webdavd.SetShell(shellDispatch) }

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
	key := rawURL // 去重键不含票：同一地址永远只开一个窗

	appWinsMu.Lock()
	if w := appWins[key]; w != nil {
		appWinsMu.Unlock()
		w.Show()
		w.Focus()
		return key, nil
	}
	appWinsMu.Unlock()

	w0, h0, minW, minH, _ := nasWindowGeometry(a.app)
	token := newShellToken()
	w := a.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: nasWindowTitle,
		Width: w0, Height: h0,
		MinWidth: minW, MinHeight: minH,
		// 无原生标题栏：顶栏就是标题栏，三个按钮由 winctl.js 画在右上角。
		Frameless: true,
		// 透明窗体：与面板窗、NAS 主窗同款。原生 DWM 圆角只有 8px（Win11 固定
		// 四档：默认/直角/8px/4px），而主窗的圆角是前端 CSS 画的 12px —— 不透明
		// 窗口做不到 12px，两窗并排一眼就能看出「弹出窗的角更小」（用户 2026-11-02
		// 要求两者一致）。这里把底色与圆角也交给网页，winctl.js 负责画。
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		Windows: application.WindowsWindow{
			// 去掉 wails 注入的原生装饰层（半透明底 + 1px 边线 + 右上角 ✕）
			DisableFramelessWindowDecorations: true,
		},
		CloseButtonState: application.ButtonHidden,
		// EnableFileDrop：监控/语音页面要拖文件上传，与主窗口一致允许 OS 拖放
		URL:            withShellToken(rawURL, token),
		EnableFileDrop: true,
	})

	// DWM 的圆角偏好虽然被透明窗体压掉大半，但 Windows 11 还会在窗口外圈留
	// 1px 描边 —— 与主窗 `removeNativeWindowBorder` 同一处理（DONOTROUND）。
	// 等 WindowShow：那时原生窗口一定已经建好，WebView2 也完成首帧导航。
	if runtime.GOOS == "windows" {
		w.OnWindowEvent(events.Windows.WindowShow, func(*application.WindowEvent) {
			removeNativeWindowBorder(w.NativeWindow())
		})
	}

	appWinsMu.Lock()
	appWins[key] = w
	appWinsMu.Unlock()

	if token != "" {
		shellWinsMu.Lock()
		shellWins[token] = w
		shellWinsMu.Unlock()
	}

	w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		appWinsMu.Lock()
		if appWins[key] == w {
			delete(appWins, key)
		}
		appWinsMu.Unlock()
		if token != "" {
			shellWinsMu.Lock()
			if shellWins[token] == w {
				delete(shellWins, token)
			}
			shellWinsMu.Unlock()
		}
		log.Writef("app window closed key=%s title=%s", key, title)
	})

	w.Center()
	w.Show()
	return key, nil
}
