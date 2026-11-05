package main

import (
	"embed"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
	duihao "github.com/duihao/duihao/backend/duihao"
	"github.com/duihao/duihao/backend/duihao/audit"
	"github.com/duihao/duihao/backend/duihao/editionpkg"
	"github.com/duihao/duihao/backend/duihao/ftpd"
	"github.com/duihao/duihao/backend/duihao/instance"
	plugincenter "github.com/duihao/duihao/backend/duihao/service"
	"github.com/duihao/duihao/backend/duihao/site"
	"github.com/duihao/duihao/backend/duihao/webdavd"
	"github.com/duihao/duihao/backend/log"
	"github.com/duihao/duihao/backend/session"
	"github.com/duihao/duihao/backend/store"
)

var Version = "dev"

// Build metadata, injected at build time via -ldflags "-X main.BuildCommit=...
// -X main.BuildDate=..." (see Taskfile BUILD_COMMIT / BUILD_DATE). Both stay
// empty in `wails dev` / bare `go build` builds; the About page only shows the
// suffix when BuildCommit is non-empty so dev builds just display "dev".
var (
	BuildCommit = ""
	BuildDate   = ""
)

// devBuild is true for `wails dev` (Version == "dev"); false for production
// builds where `-ldflags '-X main.Version=...'` sets a real version string.
// Used to gate update downloads (app.go) and to pick the dev WebView2
// user-data folder (webview2DataPath below).
var devBuild = Version == "dev"

// webview2DataPath returns the fixed WebView2 user-data folder:
// %LOCALAPPDATA%\Duihao\webview2, with a "-dev" suffix on dev builds. The
// suffix keeps dev and release instances from contending for the same
// folder — a WebView2 user-data folder only tolerates a single browser
// process tree, and dev/release deliberately use distinct single-instance
// lock IDs (see singleinstance_dev.go) precisely so they can run side by
// side. The fixed folder replaces the old per-PID Temp dir, which was needed
// when multiple instances could run concurrently and accumulated one ~11MB
// dir per launch with no cleanup.
func webview2DataPath() string {
	name := "webview2"
	if devBuild {
		name += "-dev"
	}
	// %LOCALAPPDATA% unset (unexpected, but avoids a bogus relative path).
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return filepath.Join(os.TempDir(), "Duihao-"+name)
	}
	return filepath.Join(local, "Duihao", name)
}

// sweepLegacyWebview2Dirs removes per-PID WebView2 Temp dirs
// (uniTerm-webview2-<pid>) left behind by versions that created one per
// launch without ever cleaning them up. Every dir matching the legacy
// pattern is stale by definition — current versions keep their WebView2
// user-data folder fixed and outside Temp — so there is no stale-age check
// (unlike sweepStaleExtEditDirs): a dir newer than the sweep cannot belong
// to a current build. If a legacy build is somehow still running, its locked
// files fail to delete harmlessly. Runs async at startup.
func sweepLegacyWebview2Dirs() {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		matched, _ := filepath.Match("uniTerm-webview2-*", name)
		if !e.IsDir() || !matched {
			continue
		}
		// Defensive: never touch this process's own folder (only reachable
		// when %LOCALAPPDATA% is unset and the fallback lands in Temp).
		if filepath.Join(os.TempDir(), name) == webview2DataPath() {
			continue
		}
		_ = os.RemoveAll(filepath.Join(os.TempDir(), name))
	}
}

//go:embed all:frontend/dist
var assets embed.FS

// appIconTrayPNG is the tray icon used outside Windows' exact-size set
// (macOS/Linux, and Windows as the 64px fallback): the app logo scaled down
// proportionally, nothing redrawn. Keeping it a plain scale means the tray
// icon, the window title-bar logo (/favicon.png) and the exe icon all show
// the same artwork — an earlier version thickened the glyph at small sizes
// and the tray ended up looking like a different app. 64px, derived by
// scripts/gen_tray_icons.py.
//
//go:embed build/appicon_tray.png
var appIconTrayPNG []byte

// appIconTraySet is the Windows tray icon set: one PNG per OS small-icon
// size (16/20/24/28/32/36/40/48/64 — i.e. 16 DIP at 100/125/150/175/200/
// 225/250/300/400%), each produced by proportionally scaling the logo to
// that exact pixel grid (scripts/gen_tray_icons.py) so Windows never has to
// resample it. The 28 and 36 entries matter: at 175% DPI Windows asks for
// 28px, which used to be missing and fell back to a downsampled 64px image —
// visibly softer than the icons around it.
// trayIconPNG in app_windows.go picks the entry matching SM_CXSMICON.
//
//go:embed build/tray
var appIconTraySet embed.FS

// appIconTrayTemplatePNG is the macOS variant: the logo as a template image
// (black plate, glyph knocked out) so macOS can recolour it for light and
// dark menu bars. Maintained separately from the Windows set — macOS builds
// are not produced yet, so the file is carried as-is.
//
//go:embed build/appicon_tray_template.png
var appIconTrayTemplatePNG []byte

// uniqueInstanceID 返回本次运行使用的单实例锁名。
//
// 同一份内核会被打成多个发行版（core 的 duihao.exe、网盘版 duihao-drive.exe），
// 而这几个发行版允许装在同一台机器上。锁名若写死成同一个，先启动的那个会把后
// 启动的「吃掉」——第二个进程只是通知第一个窗口获得焦点，然后自己 exit 0，用户
// 看到的现象是「双击网盘版，屏幕上却跳出标准版」。所以在 lock key 后面补上 exe
// 名：同名 exe（正常升级、误双击两次）仍然互斥，不同发行版可以并存。
// 开发版/发布版的区分仍由 singleInstanceID 自身承担（见 singleinstance_*.go）。
func uniqueInstanceID() string {
	base := strings.ToLower(filepath.Base(os.Args[0]))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "" || base == "duihao" {
		return singleInstanceID
	}
	return singleInstanceID + "." + base
}

func main() {
	// Administrator-shell broker mode: an elevated copy of Duihao launched
	// via the "runas" verb relays ConPTY I/O for admin local terminals (see
	// backend/session/local_admin_windows.go). Must run before anything else
	// so the broker never creates a window, webview or app store. It also
	// runs before the single-instance lock, which keeps the broker process
	// exempt — only GUI instances compete for the lock.
	if session.RunLocalPtyBroker(os.Args) {
		return
	}

	// Single-instance relaunch: RelaunchApp/autotest set relaunchPending and
	// quit; the successor is spawned after Run() returns, once the lock is
	// released.

	// Capture top-level panics
	defer func() {
		if r := recover(); r != nil {
			_ = log.Init()
			log.Writef("FATAL PANIC: %v\n%s", r, string(debug.Stack()))
			log.Close()
			os.Exit(1)
		}
	}()

	if err := log.Init(); err != nil {
		println("Failed to init log:", err.Error())
	}
	defer log.Close()

	// Fixed folder (not per-PID): the single-instance lock guarantees only one
	// GUI process owns the WebView2 user-data folder; dev and release builds
	// each get their own folder so they can still run side by side. See
	// webview2DataPath above.
	webviewDataPath := webview2DataPath()
	os.MkdirAll(webviewDataPath, 0700)

	app := NewApp(webviewDataPath)

	// Read persisted window geometry and theme before creating the window —
	// both are fixed at creation in v3 (services start before the window
	// exists, so ServiceStartup can't position it). Race the loads against a
	// short deadline so a slow disk doesn't delay first paint; on timeout the
	// defaults (1200x800 centered, dark background) are used instead.
	//
	// Geometry comes from localState.json. The theme comes from settings.json,
	// whose store normally starts later in ServiceStartup, so it is loaded
	// directly here via the data-dir bootstrap (loadSavedSettings). Both loads
	// run in parallel against the same deadline.
	systemTitleBar := false
	winW, winH := 1200, 800 // fallback before any saved geometry is applied
	savedX, savedY := 0, 0
	savedMaxed := false
	var savedSettings store.AppSettings

	deadline := time.After(100 * time.Millisecond)

	settingsCh := make(chan store.AppSettings, 1)
	go func() {
		settingsCh <- loadSavedSettings()
	}()

	if configDir, err := os.UserConfigDir(); err == nil {
		ls := store.NewLocalStateStore(filepath.Join(configDir, "Duihao"))
		done := make(chan store.LocalState, 1)
		go func() {
			if state, err := ls.Load(); err == nil {
				done <- state
				return
			}
			done <- store.LocalState{}
		}()
		select {
		case state := <-done:
			systemTitleBar = state.SystemTitleBar
			if state.WindowWidth > 0 && state.WindowHeight > 0 {
				winW, winH = state.WindowWidth, state.WindowHeight
			}
			savedX, savedY = state.WindowX, state.WindowY
			savedMaxed = state.WindowMaximised
		case <-deadline:
			// Slow disk — paint the defaults. The goroutine continues to load
			// in the background; its result is discarded because the window
			// geometry options are fixed at startup.
		}
	}

	select {
	case savedSettings = <-settingsCh:
	case <-deadline:
		// Too slow — defaults are used below (tray language, hotkey binding).
	}

	// Restore a saved position only when one actually exists; otherwise keep v3's
	// default (centered) so a fresh install doesn't land at (0,0).
	startPos := application.WindowCentered
	if savedX != 0 || savedY != 0 {
		startPos = application.WindowXY
	}
	startState := application.WindowStateNormal
	if savedMaxed {
		startState = application.WindowStateMaximised
	}

	// M23：主窗口恒为「IT 工具」窗口（侧栏 + 标签页 + 终端那套），几何按上次
	// 记忆恢复；NAS 是独立主窗（下方 nasWin），不再靠主窗口换身份实现。
	minWinW, minWinH, maxWinW, maxWinH := 500, 450, 0, 0

	// Clean external-edit scratch dirs left behind by previous runs that
	// exited without cleanup (crash / force-kill). Old dirs only: a
	// concurrently running instance keeps its own. Async: it only ever
	// touches dirs older than the stale age, which no new session can
	// collide with (fresh PID + fresh session ID), so startup never waits
	// on it.
	go sweepStaleExtEditDirs()

	// Remove per-PID WebView2 Temp dirs left behind by pre-fixed-folder
	// versions (every launch created one and nothing ever deleted it). All
	// dirs matching the legacy pattern are stale by definition — the fixed
	// folder lives outside Temp. If an old-version instance is somehow still
	// running, its locked files fail to delete harmlessly; retry next launch.
	// Async and self-healing: after the first successful pass the Temp glob
	// matches nothing, so it costs nothing.
	go sweepLegacyWebview2Dirs()

	// Declared before application.New so the single-instance callback (which
	// runs after startup) can capture it.
	var window *application.WebviewWindow

	w3app := application.New(application.Options{
		Name:       "Duihao",
		Assets:     application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		OnShutdown: app.shutdown,
		// Single-instance: a second GUI launch acquires the OS lock, notifies
		// this instance (which shows/focuses the existing window) and exits.
		// This prevents two GUI processes from racing on the shared stores in
		// the user's data directory (settings.json, connections, credentials).
		// The admin-shell broker process never reaches application.New, so it
		// is exempt. RelaunchApp sets relaunchPending and quits first; the
		// successor is spawned after Run() returns, with the lock released.
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: uniqueInstanceID(),
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				// M23：第二次启动先把 NAS 主窗亮出来（它才是应用的脸）
				if app.nasWindow() != nil {
					app.ShowNasWindow()
					return
				}
				showMainWindow(window)
			},
		},
		// WebviewUserDataPath is a Windows-only path for WebView2 user data; it is
		// harmless (ignored) on other platforms.
		Windows: application.WindowsOptions{
			WebviewUserDataPath: webviewDataPath,
			// WebView2 runtimes 152.x/153.x call LogonUser(current local
			// account, invalid password) once per environment creation at
			// startup — a Chromium regression in the autofill-AI-wallet
			// feature that emits a 4625 failed-logon event per launch and,
			// on machines with an account lockout policy, can lock the
			// user out of Windows entirely. Disabling that feature stops
			// the logon attempt. Must go through DisabledFeatures so it
			// merges into Wails' single --disable-features switch:
			// Chromium honours only the LAST occurrence, so a second
			// switch in AdditionalBrowserArgs would silently re-enable
			// Wails' disabled SmartScreen protection.
			DisabledFeatures: []string{"AutofillAiWalletPrivatePasses"},
		},
		// Fixed program name so the window's X11 WM_CLASS stays "duihao" —
		// the StartupWMClass in the .desktop file matches it under X11/XWayland.
		// NOTE: on native Wayland this does NOT control the taskbar association:
		// the XDG app_id comes from the GtkApplication id, which Wails derives
		// as "org.wails." + lowercase Options.Name → "org.wails.duihao". GNOME
		// matches Wayland windows to launchers by desktop-file filename, so the
		// installed .desktop must be named org.wails.duihao.desktop (see
		// build/linux/Taskfile.yml generate:dotdesktop).
		Linux: application.LinuxOptions{
			ProgramName: "duihao",
		},
	})

	// On macOS, install the standard App + Edit menus. This must run AFTER
	// application.New(): menu roles dereference the global app instance and
	// panic with a nil pointer otherwise (macOS-only — NewAppMenu returns nil
	// on other platforms, which masked the crash). The Edit menu is what
	// routes the native Cmd+C/V/X/A/Z shortcuts to the first responder — every
	// WKWebView text field (input/textarea/contenteditable) relies on it. An
	// empty menu here used to suppress Wails' defaults but also killed those
	// shortcuts app-wide, forcing per-component JS reimplementations. The menu
	// lives in the top system menu bar, so it doesn't affect the frameless
	// window. On Linux (GTK) a non-nil Menu creates an empty GtkMenuBar that
	// shows as a thin white line in the frameless window, so leave it nil
	// there. See issue #291.
	var appMenu *application.Menu
	if runtime.GOOS == "darwin" {
		appMenu = application.NewMenu()
		appMenu.AddRole(application.AppMenu)
		appMenu.AddRole(application.EditMenu)
	}

	if appMenu != nil {
		w3app.Menu.SetApplicationMenu(appMenu)
	}

	// M19/M6: 窗口标题随发行版品牌（core = 「对号 IT工具」，专有版 = editions.json 的 name）。
	// 放在窗口创建时设定，比前端 Window.SetTitle 更可靠（任务栏/Alt+Tab 立刻正确）。
	winTitle := "对号 IT工具"
	if c, err := editionpkg.Current(); err == nil && c != nil && c.Name != "" {
		winTitle = c.Name
	}

	winOpts := application.WebviewWindowOptions{
		Title:           winTitle,
		Width:           winW,
		Height:          winH,
		X:               savedX,
		Y:               savedY,
		InitialPosition: startPos,
		StartState:      startState,
		MinWidth:        minWinW,
		MinHeight:       minWinH,
		MaxWidth:        maxWinW,
		MaxHeight:       maxWinH,
		// 用户定稿（M18-C）：启动只显示独立面板窗，主窗口常驻后台（webview
		// 照常加载，供面板动作随时唤起）。wails 只在 options.Hidden==false
		// 时才于 navigationCompleted 自动 Show，所以这里保持隐藏即可。
		// 例外：DUIHAO_SHOW_MAIN=1 时直接显示（GUI 自动化验收用，不影响正式行为）。
		Hidden:    os.Getenv("DUIHAO_SHOW_MAIN") != "1",
		Frameless: !systemTitleBar,
		// 透明窗体：原生背景与边全部去掉（含启动首帧的底色块），窗口底色
		// 与圆角完全由前端 CSS 决定。
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		EnableFileDrop:   true,
		// Open the WebView2 inspector when the window is first shown. Wails
		// disables browser accelerator keys on Windows (F12 / Ctrl+Shift+I
		// never reach us), and the app's custom context menus hide the
		// default "Inspect" entry — without this there is NO way to open
		// DevTools, even in dev builds. Inert in production: the wails
		// runtime only honors it when built without the `production` tag.
		// (Deliberately NOT a window KeyBinding for F12: that would be
		// window-global and could swallow F12 from TUI apps.)
		OpenInspectorOnStartup: true,
	}
	// 与面板窗/悬浮球同款去装饰：wails 会给 frameless 窗口注入一层原生
	// 装饰（半透明背景层 + 边框线 + 右上角 ✕），透明窗体下表现为「背景和
	// 边去不掉」。仅 frameless（自绘标题栏）时套；用户开启系统标题栏时
	// 保持原生外观。
	if !systemTitleBar {
		winOpts.Windows = application.WindowsWindow{DisableFramelessWindowDecorations: true}
		winOpts.CloseButtonState = application.ButtonHidden
	}
	window = w3app.Window.NewWithOptions(additionalWindowOptions(winOpts))

	// Window chrome is fully CSS-drawn now (transparent window + body
	// border-radius). The native DWM rounded-corner preference also draws a
	// 1px border line around the window — that native edge is removed here
	// (DONOTROUND); see removeNativeWindowBorder. WindowShow reliably fires
	// after WebView2 navigation completes, when the native window exists.
	// Idempotent; a no-op on pre-Win11 and other OSes.
	if runtime.GOOS == "windows" {
		window.OnWindowEvent(events.Windows.WindowShow, func(*application.WindowEvent) {
			removeNativeWindowBorder(window.NativeWindow())
		})
	}

	// 最大化/全屏 → 直角（贴边不留圆角），还原 → 恢复 CSS 圆角。窗口事件只
	// 在 Go 侧分发不自动推 JS，所以这里以 app:win-shape 广播，main.ts 收到后
	// 切 html.win-square 类，style.css 据此把 body/#app 的圆角归零。
	if runtime.GOOS == "windows" {
		winShape := func(square bool) func(*application.WindowEvent) {
			return func(*application.WindowEvent) { app.emit("app:win-shape", square) }
		}
		window.OnWindowEvent(events.Windows.WindowMaximise, winShape(true))
		window.OnWindowEvent(events.Windows.WindowUnMaximise, winShape(false))
		window.OnWindowEvent(events.Windows.WindowFullscreen, winShape(true))
		window.OnWindowEvent(events.Windows.WindowUnFullscreen, winShape(false))
	}

	// Wails v3 delivers OS file drops to Go-side window-event listeners rather
	// than (as v2 did) auto-forwarding them to the frontend. Re-emit the dropped
	// absolute paths under the original v2 event name so `Events.On(...)` pickers
	// (FileSidebar, SFTP tab) keep receiving them for path-based upload.
	window.OnWindowEvent(events.Common.WindowFilesDropped, func(event *application.WindowEvent) {
		filenames := event.Context().DroppedFiles()
		if len(filenames) == 0 {
			return
		}
		x, y, elementID := 0, 0, ""
		if details := event.Context().DropTargetDetails(); details != nil {
			x, y = details.X, details.Y
			elementID = details.ElementID
		}
		w3app.Event.Emit("common:WindowFilesDropped", map[string]any{
			"x":         x,
			"y":         y,
			"elementId": elementID,
			"filenames": filenames,
		})
	})

	// Wire the bound App back to the runtime before registering it as a service:
	// emit() / window operations route through these references.
	app.app = w3app
	app.window = window
	w3app.RegisterService(application.NewService(app))

	// 对号环境工具（M3）与站点管理（M4）：共享同一实例管理器
	// （两个 processd 管理器会各自持锁、互相看不见对方启的进程）。
	if err := duihao.EnsureDirs(); err != nil {
		log.Writef("duihao: ensure dirs failed: %v", err)
	}
	dhSettings, err := duihao.LoadSettings()
	if err != nil {
		log.Writef("duihao: load settings failed, using defaults: %v", err)
		dhSettings = duihao.DefaultSettings()
	}
	dhAudit := audit.New(duihao.DirLogs())
	dhMgr, err := instance.NewManager(dhSettings, dhAudit)
	if err != nil {
		log.Writef("duihao: instance manager init failed: %v", err)
	}
	// IPv6 直连弹窗（?w=v6）要读网盘实例的 hub.db 与 dav 端口。
	app.dhMgr = dhMgr
	var siteMgr *site.Manager
	if dhMgr != nil {
		// 内置服务（embedded 类）：编译进本体的实现由 main 注入，清单只声明数据。
		dhMgr.RegisterEmbedded("ftpserver", func(cfg instance.EmbeddedConfig) (instance.EmbeddedServer, error) {
			return ftpd.FromValues(cfg.Dir, cfg.LogPath, cfg.Ports, cfg.Values)
		})
		// M5 本地网盘：WebDAV 服务同样是 embedded 类（x/net/webdav，BSD-3）。
		dhMgr.RegisterEmbedded("webdav", func(cfg instance.EmbeddedConfig) (instance.EmbeddedServer, error) {
			return webdavd.FromValues(cfg.Dir, cfg.LogPath, cfg.Ports, cfg.Values)
		})
		if siteMgr, err = site.NewManager(dhMgr, dhAudit); err != nil {
			log.Writef("duihao: site manager init failed: %v", err)
		}
	}
	// 实例端口迁移到清单首选端口（nginx 默认 80）前，先问站点层该端口是否已被
	// 某个站点监听 —— instance 包不认识站点，只拿一个布尔回来。
	if dhMgr != nil && siteMgr != nil {
		dhMgr.SetPortGuard(func(instanceID string, port int) bool {
			return siteMgr.PortInUseBy(instanceID, port) != ""
		})
	}
	pcSvc := plugincenter.NewPluginCenterService(dhMgr)
	if siteMgr != nil {
		pcSvc.SetSiteManager(siteMgr)
	}
	w3app.RegisterService(application.NewService(pcSvc))
	w3app.RegisterService(application.NewService(plugincenter.NewSiteService(siteMgr)))

	// 打开软件默认自动启动 NAS（用户要求）：webdav 是 embedded 实例，listener
	// 起在本进程内，早启没有副作用。异步跑，别让证书生成/端口绑定拖慢首帧；
	// 还没建实例（首次运行、用户没点过「我的 NAS」）就静默跳过，交给首屏引导。
	// 走 StartInstance 而不是 dhMgr.Start：它顺带重渲染引用了该实例的站点片段，
	// 端口被自愈换掉时 nginx 不会还指着旧端口。
	if dhMgr != nil {
		go func() {
			for _, v := range dhMgr.List() {
				if v.Plugin != "webdav" {
					continue
				}
				if err := pcSvc.StartInstance(v.ID); err != nil {
					log.Writef("duihao: NAS 自动启动失败(%s): %v", v.ID, err)
				} else {
					log.Writef("duihao: NAS 已自动启动(%s)", v.ID)
				}
				break // 单实例口径：只起第一个
			}
		}()
	}

	// M11 运行时管理面：MySQL 库管理 / Redis 键浏览 / FTP 账号。
	// 三者都只依赖实例管理器（端口、目录、设置），不碰站点层；
	// FTPService 额外拿站点管理器仅用于「从站点选择根目录」下拉。
	if dhMgr != nil {
		ftpSvc := plugincenter.NewFTPService(dhMgr, dhAudit)
		if siteMgr != nil {
			ftpSvc.SetSiteManager(siteMgr)
		}
		mysqlSvc := plugincenter.NewMySQLService(dhMgr, dhAudit)
		w3app.RegisterService(application.NewService(mysqlSvc))
		redisSvc := plugincenter.NewRedisService(dhMgr, dhAudit)
		w3app.RegisterService(application.NewService(redisSvc))
		w3app.RegisterService(application.NewService(ftpSvc))
		// M5 本地网盘：共享管理（与 FTP 账号管理同构）。
		davSvc := plugincenter.NewWebDavService(dhMgr, dhAudit)
		davSvc.SetEventEmitter(func(name string, data any) { w3app.Event.Emit(name, data) })
		if siteMgr != nil {
			davSvc.SetSiteManager(siteMgr)
		}
		w3app.RegisterService(application.NewService(davSvc))
		// M20 一键部署：站点建好后装程序（DuihaoCMS / WordPress …）。
		// 需要 MySQL 服务提供「该库的账号密码」，所以和站点管理器一起注入。
		if siteMgr != nil {
			deploySvc := plugincenter.NewDeployService(siteMgr, mysqlSvc, dhAudit,
				func(name string, data any) { w3app.Event.Emit(name, data) })
			w3app.RegisterService(application.NewService(deploySvc))
		}

	}

	// System notification service: app_<os>.go notifyMCPApproval posts MCP
	// approval prompts through it when the window is unfocused. Windows needs
	// the service's Startup to run so the toast AUMID/registry side is set up;
	// macOS needs it for UNUserNotificationCenter authorization handling.
	//
	// Not registered on Android: the wails notifications service has no
	// Android backend, so on GOOS=android it compiles the linux D-Bus
	// implementation whose Startup fails (there is no session bus) and aborts
	// w3app.Run before platformRun signals the app ready — leaving the
	// WebView stuck on a blank wails.localhost error page. notifyMCPApproval
	// already handles a nil notifier.
	var mcpNotifier *notifications.NotificationService
	if runtime.GOOS != "android" {
		mcpNotifier = notifications.New()
		w3app.RegisterService(application.NewService(mcpNotifier))
	}
	app.notifier = mcpNotifier

	// M23 NAS 主窗：把原来的「普通模式」从「主窗口的一种身份」升格成独立窗口
	// （/?w=nas）。竖版 330（宽度锁死）× 780，贴主屏右侧、垂直居中，默认打开；
	// 原来那个主窗口退居「IT 工具」窗口，常驻后台（Hidden），按需唤起。
	//
	// 标题就是「对号」——与 IT 工具窗同名（用户定稿：主窗标题保持「对号」，
	// 不写成「对号 · NAS」）。两窗同名不影响功能：findMainWindow 靠
	// app.nasHwnd 把这个小窗排掉，剩下的才是 IT 工具窗（面板/悬浮菜单等辅助窗
	// 标题都带「 · 」，本来就不在候选里）。
	//
	// 先建 Hidden、等原生窗口与 Screen 都就绪再摆位 + Show：创建期 Screen 还没
	// 填充，直接显示会先在屏幕中央闪一下再跳到右边。
	var nasWin *application.WebviewWindow
	if runtime.GOOS != "android" {
		nasWin = w3app.Window.NewWithOptions(application.WebviewWindowOptions{
			Title:     "对号",
			Width:     LifeWindowWidth,
			Height:    LifeWindowHeight,
			MinWidth:  LifeWindowWidth,
			MinHeight: LifeWindowHeight,
			MaxWidth:  LifeWindowWidth,
			MaxHeight: LifeWindowHeight,
			// 宽高全固定：连原生 resize 边框都关掉，拖边缘不变形
			DisableResize: true,
			Hidden:        true,
			Frameless:     true,
			// 与主窗口同款：圆角/描边/阴影全由前端 CSS 画，原生背景透明
			BackgroundType:   application.BackgroundTypeTransparent,
			BackgroundColour: application.NewRGBA(0, 0, 0, 0),
			Windows: application.WindowsWindow{
				DisableFramelessWindowDecorations: true,
			},
			CloseButtonState: application.ButtonHidden,
			// NAS 各模块有拖文件上传（监控/音乐/网盘），与主窗口一致放行
			EnableFileDrop: true,
			URL:            "/?w=nas",
		})
		app.nasWin = nasWin
		// 记下原生句柄：两窗都叫「对号」，findMainWindow 靠它把 NAS 窗排掉。
		if h := nasWin.NativeWindow(); h != nil {
			app.nasHwnd = uintptr(h)
		}
		// 主窗常驻：关闭按钮不销毁窗口，只隐藏（退出走托盘 / 悬浮菜单）。
		//
		// **必须用 RegisterHook 而不是 OnWindowEvent**：wails v3 建窗时给
		// WindowClosing 挂了内置监听，它无条件 markAsDestroyed + 原生 close +
		// 从 WindowManager 移除。监听器（OnWindowEvent）是在独立 goroutine 里
		// 并发跑的，里面再 Cancel 已经晚了 —— 窗口照样被销毁，于是
		// IsVisible() 恒 false、Show() 落到 isDestroyed 分支（InvokeSync(w.Run)
		// 对已销毁窗口是空操作），表现就是「关掉 NAS 主窗后托盘 / 悬浮菜单 /
		// 热键都唤不回来」。hook 在监听器之前同步执行，Cancel 才是有效的。
		nasWin.RegisterHook(events.Common.WindowClosing, func(ev *application.WindowEvent) {
			ev.Cancel()
			nasWin.Hide()
		})
		if runtime.GOOS == "windows" {
			nasWin.OnWindowEvent(events.Windows.WindowShow, func(*application.WindowEvent) {
				removeNativeWindowBorder(nasWin.NativeWindow())
			})
		}
		// 启动显示：原生窗口 + Screen 就绪后先摆位再 Show，避免中央闪一下。
		go func() {
			for i := 0; i < 300; i++ {
				if nasWin.NativeWindow() != nil && app.app != nil && app.app.Screen != nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if os.Getenv("DUIHAO_UPDATE_AUTOTEST") == "1" {
				return
			}
			app.placeNasWindow(nasWin)
			if h := nasWin.NativeWindow(); h != nil {
				app.nasHwnd = uintptr(h)
			}
			nasWin.Show()
			log.Writef("M23: nas window shown")
			app.nudgeNasPlacement(nasWin)
		}()
	}

	// 桌面临时悬浮图标（uTools 桌面小球形态）：48×48 圆形无边框、置顶、不入
	// 任务栏的小窗，前端 /?w=float 画品牌圆钮，点击调 App.PaletteToggle，
	// 按住可拖拽。初始 Hidden：WS_EX_TOOLWINDOW 必须在任务栏按钮生成前生效，
	// 否则任务栏会残留「悬浮」图标（先样式化再 Hide/Show 刷新一次）。
	// 显示时机挂在面板窗首次显示上（主窗口启动不再显示）——见下方 paletteWin
	// 的 WindowShow 钩子；右键隐藏后不复活。
	var floatWin *application.WebviewWindow
	var floatShown bool
	if runtime.GOOS != "android" {
		floatWin = w3app.Window.NewWithOptions(application.WebviewWindowOptions{
			Title:  "对号 · 悬浮",
			Width:  72,
			Height: 78,
			// 不设会被系统 SM_CXMINTRACK（≈136px）钳成 135 宽的透明大窗：
			// wails 只在 MinWidth/MinHeight>0 时接管 WM_GETMINMAXINFO。
			// 72×78 = 40px 球体 + 两侧各 4px 白边（外径 48）+ 阴影余量
			//（横向 12×2、纵向 上10/下20）。环与阴影都画在窗口内部，画不出界；
			// 留白不够会被窗口边界硬裁成一道直线。前端 BALL_W/BALL_H 保持同步。
			MinWidth:  72,
			MinHeight: 78,
			// 禁掉缩放：frameless 窗口仍带 WS_THICKFRAME，透明区域整块都能
			// 被鼠标抓到拖大（实测被误拉成 474×161，球还只占左上 44px，
			// 菜单锚到窗口底边就显得离球很远）。
			DisableResize: true,
			Frameless:     true,
			AlwaysOnTop:   true,
			Hidden:        true,
			// 透明窗口：圆形 logo 四角透出桌面而不是色块
			BackgroundType:   application.BackgroundTypeTransparent,
			BackgroundColour: application.NewRGB(247, 25, 99),
			// 同样禁掉 wails 注入的原生装饰（小窗右上角会冒出 ✕）；
			// HiddenOnTaskbar 是 wails 官方开关：不给窗口置 WS_EX_APPWINDOW
			// （带 APPWINDOW 时会在任务栏留按钮，事后补 TOOLWINDOW 也压不住）。
			Windows: application.WindowsWindow{
				DisableFramelessWindowDecorations: true,
				HiddenOnTaskbar:                   true,
			},
			// 小窗不显示原生关闭按钮
			CloseButtonState: application.ButtonHidden,
			URL:              "/?w=float",
		})
		app.floatWin = floatWin
		// WS_EX_TOOLWINDOW 必须在首次 Show 之前生效，否则任务栏会闪出
		// 「悬浮」按钮 —— 原生窗口一创建（webview 加载完即存在，早于显示）
		// 就轮询套样式，显示时已带 TOOLWINDOW，任务栏全程无按钮。
		go func() {
			for i := 0; i < 200; i++ {
				if hwnd := floatWin.NativeWindow(); hwnd != nil {
					styleToolWindow(uintptr(hwnd))
					// 创建尺寸可能被 SM_CXMINTRACK（≈136）钳大，显示前掰回
					// 72×78（只动尺寸；位置归前端记忆/恢复）。
					forceWindowSize(uintptr(hwnd), 72, 78)
					log.Writef("M18: floatWin pre-show styled (try %d)", i)
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			log.Writef("M18: floatWin pre-show style timed out")
		}()
		if runtime.GOOS == "windows" {
			floatWin.OnWindowEvent(events.Windows.WindowShow, func(*application.WindowEvent) {
				if hwnd := uintptr(floatWin.NativeWindow()); hwnd != 0 {
					log.Writef("M18: floatWin WindowShow fired")
					// 兜底再套一次（均幂等）；不摆位置 —— 悬浮球位置由前端
					// 记忆/恢复（FloatIcon onMounted），Go 摆放会盖掉用户的
					// 拖拽落点。
					styleToolWindow(hwnd)
					forceWindowSize(hwnd, 72, 78)
				}
			})
		}
		// 兜底：面板窗首显钩子万一没触发（事件时序），3 秒后强制显示悬浮球。
		go func() {
			time.Sleep(3 * time.Second)
			if !floatShown && floatWin != nil {
				floatShown = true
				log.Writef("M18: floatWin fallback show")
				floatWin.Show()
			}
		}()
		// M23：面板窗不再开机自显，悬浮球改成跟着 NAS 主窗首显一起出现
		//（球是「主窗 / IT 工具 / 官网」的入口，不能等人按了 Alt+Space 才出来）。
		if nasWin != nil {
			var ballOnce sync.Once
			nasWin.OnWindowEvent(events.Common.WindowShow, func(*application.WindowEvent) {
				ballOnce.Do(func() {
					if !floatShown {
						floatShown = true
						floatWin.Show()
						log.Writef("M23: floatWin shown with nas window")
					}
				})
			})
		}
	}

	// 悬浮球右键菜单窗：uTools 同款圆角卡片（148×136，深/浅色跟随主题）。
	// 为什么要独立窗口：悬浮球窗只有 44px，HTML 菜单画不出界；原生
	// TrackPopupMenu 是系统灰菜单，做不出圆角卡片观感。独立透明窗 + 自绘
	// 卡片既贴球又能有阴影/圆角。
	var floatMenuWin *application.WebviewWindow
	if runtime.GOOS != "android" {
		floatMenuWin = w3app.Window.NewWithOptions(application.WebviewWindowOptions{
			Title: "对号 · 菜单",
			// 68×165 卡片 + 四周 14px 透明留白：box-shadow 画在卡片上，
			// 没有留白会被窗口边界直接裁掉，看不出柔和投影。
			// 4 项（主窗 / IT工具 / 设置 / 官网）+ 1 条分隔 = 165。
			Width:  96,
			Height: 193,
			// 同样要显式 Min 尺寸，否则被 SM_CXMINTRACK 钳成 135 宽
			MinWidth:      96,
			MinHeight:     193,
			DisableResize: true,
			Frameless:     true,
			AlwaysOnTop:   true,
			Hidden:        true,
			// 透明窗体：圆角卡片外的区域要透出桌面（含投影落点）
			BackgroundType:   application.BackgroundTypeTransparent,
			BackgroundColour: application.NewRGBA(0, 0, 0, 0),
			Windows: application.WindowsWindow{
				DisableFramelessWindowDecorations: true,
				HiddenOnTaskbar:                   true,
			},
			CloseButtonState: application.ButtonHidden,
			URL:              "/?w=floatmenu",
		})
		app.floatMenuWin = floatMenuWin
		go func() {
			for i := 0; i < 200; i++ {
				if hwnd := floatMenuWin.NativeWindow(); hwnd != nil {
					styleToolWindow(uintptr(hwnd))
					forceWindowSize(uintptr(hwnd), 96, 193)
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
		}()
		if runtime.GOOS == "windows" {
			// 每次显示都重新套一次尺寸（系统可能在显示时按最小跟踪尺寸掰宽）
			floatMenuWin.OnWindowEvent(events.Windows.WindowShow, func(*application.WindowEvent) {
				if hwnd := uintptr(floatMenuWin.NativeWindow()); hwnd != 0 {
					styleToolWindow(hwnd)
					removeNativeWindowBorder(unsafe.Pointer(hwnd))
					forceWindowSize(hwnd, 96, 193)
				}
			})
		}
	}

	// M18-C 独立悬浮命令面板：无边框置顶窗，前端 /?w=palette 只挂 CommandPalette。
	// M23 起它不再是「启动即显示的主入口」——主入口换成 NAS 主窗（上面那个），
	// 面板改成纯热键面：Alt+Space / 悬浮球 / 托盘唤起。启动两个窗口同时弹出来
	// 只会互相盖住，也没必要。
	// Alt+Space / 悬浮球切换它；导航/连接/创建动作经 App.PaletteNavigate/
	// PaletteConnect/PaletteAction 打开 IT 工具窗口。
	var paletteWin *application.WebviewWindow
	if runtime.GOOS != "android" {
		paletteWin = w3app.Window.NewWithOptions(application.WebviewWindowOptions{
			Title:  "对号 · 面板",
			Width:  640,
			Height: 480,
			// 启动不显示，只等热键/悬浮球唤起。
			Hidden:      true,
			Frameless:   true,
			AlwaysOnTop: true,
			// 透明窗体：面板圆角由前端 CSS 画（uTools/WorkBuddy 式大圆角）
			BackgroundType:   application.BackgroundTypeTransparent,
			BackgroundColour: application.NewRGBA(0, 0, 0, 0),
			// 关掉 wails 的「frameless 装饰」：否则框架会在窗口右上角注入
			// 原生关闭按钮（✕）并叠一层装饰/阴影，与自定义面板样式打架。
			Windows: application.WindowsWindow{DisableFramelessWindowDecorations: true},
			// 隐藏原生标题栏关闭按钮：frameless 下它会浮在自定义圆角面板右上角
			// （浅色主题悬停是红色 ✕ 方块）。直接改 style 去 WS_CAPTION 会崩
			// wails 的 NC 计算，所以用它自带的按钮状态。
			CloseButtonState: application.ButtonHidden,
			URL:              "/?w=palette",
		})
		app.paletteWin = paletteWin
		if runtime.GOOS == "windows" {
			// 首次显示时套 WS_EX_TOOLWINDOW（不入任务栏/Alt+Tab）；样式只套
			// 一次：每次 Show 都 SetWindowPos(FRAMECHANGED) 会造成瞬态失焦，
			// 前端 blur 收起逻辑会误判「刚弹出就隐藏」。位置每次重新摆正。
			// 首次显示后 Hide/Show 刷一次，让已生成的任务栏按钮随样式消失。
			var paletteStyled bool
			paletteWin.OnWindowEvent(events.Windows.WindowShow, func(*application.WindowEvent) {
				if hwnd := uintptr(paletteWin.NativeWindow()); hwnd != 0 {
					log.Writef("M18: palette WindowShow fired styled=%v floatShown=%v", paletteStyled, floatShown)
					if !paletteStyled {
						paletteStyled = true
						styleToolWindow(hwnd)
						// 同样去掉 DWM 的原生 1px 边框线（浅色主题下是白线，
						// 会在圆角四周描出方框）与原生圆角。
						removeNativeWindowBorder(unsafe.Pointer(hwnd))
						positionPaletteTopCenter(hwnd, 640, 480)
						paletteWin.Hide()
						paletteWin.Show()
						// 悬浮球跟随面板首次显示（主窗口启动不再显示，原来的
						// 挂点失效）。
						if !floatShown {
							floatShown = true
							floatWin.Show()
						}
					}
				}
			})
		}
	}

	// System tray (issue #982): persistent icon with left-click show/hide
	// toggle and a menu (show / hide / reset position / settings / about /
	// quit). Menu labels follow the persisted UI language; English is the
	// fallback for unknown codes. Tray creation must happen after the window
	// exists so the click handlers can drive it.
	setupTray(w3app, app, window, savedSettings.Language)

	// 点右上角 ✕ 收缩到托盘而不是退出：实现在 app_windows.go 的
	// subclassMainWindow —— wails beta.27 内置的 WindowClosing 监听会强制
	// 真关窗口，OnWindowEvent 事件层拦不住，必须在 WndProc 消息层把
	// WM_CLOSE 改成隐藏（与托盘「隐藏到托盘」一致）。真正的退出入口是托
	// 盘菜单「退出」（w3app.Quit() 直接销毁应用，不发 WM_CLOSE）。

	// Global show/hide hotkey (issue #982, Windows + platform backends via
	// the wails GlobalShortcutManager). Default Ctrl+M when unset; can be
	// changed or disabled in Settings → Shortcuts.
	applyGlobalShowHideHotkey(w3app, app, window, trayHotkeyBinding(&savedSettings))

	// M18 全局呼出命令面板（uTools 式）：Alt+Space 切换独立悬浮面板窗 ——
	// 主窗口收进托盘也能呼出，导航动作经后端打开旧主窗口。注册失败（组合键
	// 被其他程序占用）只记日志，不影响启动；窗口内 Ctrl+K 由前端转调同一方法。
	if err := w3app.GlobalShortcut.Register("Alt+Space", func() {
		app.PaletteToggle()
	}); err != nil {
		log.Writef("global hotkey: register Alt+Space failed: %v", err)
	}

	// Local end-to-end update test hook — inert unless the env var is set
	// (see autotest_update.go).
	if os.Getenv("DUIHAO_UPDATE_AUTOTEST") == "1" {
		go app.autotestUpdate()
	}

	// （原「DOM committed 即提前 Show 主窗口」的钩子已删：主窗口现在启动
	// 不显示，由面板动作 / 托盘 / Ctrl+M 按需唤起。）

	err = w3app.Run()
	if err != nil {
		log.Writef("Wails run error: %v", err)
	}

	// Quit-then-spawn relaunch (RelaunchApp / update autotest): the process
	// is exiting and the single-instance lock is released, so the successor
	// starts clean — it would otherwise be rejected as a "second instance".
	if relaunchPending.Load() {
		if err := spawnSuccessorProcess(); err != nil {
			log.Writef("relaunch: spawn successor failed: %v", err)
		}
	}
}

// loadSavedSettings reads the persisted app settings so the window can be
// created with a background colour matching the theme (see
// windowBackgroundColour), the tray menu can follow the UI language, and the
// global hotkey can be registered before Run(). The settings store normally
// initializes later in ServiceStartup, so this goes through the data-dir
// bootstrap directly. Returns the zero value when unavailable (first run /
// read error) — the caller maps that to the dark theme default and Ctrl+M.
func loadSavedSettings() store.AppSettings {
	dd, err := store.ResolveDataDir()
	if err != nil || dd.FirstRun || dd.Path == "" {
		return store.AppSettings{}
	}
	// ResolveDataDir only returns paths that exist (bootstrap paths are
	// validated, the upgrade path is checked for config files), so
	// NewSettingsStore's MkdirAll is a no-op here.
	ss, err := store.NewSettingsStore(dd.Path)
	if err != nil {
		return store.AppSettings{}
	}
	settings, err := ss.Load()
	if err != nil {
		return store.AppSettings{}
	}
	return settings
}

// windowBackgroundColour maps the persisted app theme to the native window
// background colour. That colour is only visible before the webview's first
// paint, so it is matched to the TOP colour of each theme's body gradient in
// frontend/src/style.css — the seam that would otherwise flash in the wrong
// colour on startup. 'system' is resolved from the OS the same way the
// webview engine does (systemtheme_*.go); v3's IsDarkMode() can't be used
// because it reports false before Run().
func windowBackgroundColour(theme string) application.RGBA {
	resolved := theme
	if theme == "" || theme == "system" {
		if systemPrefersDark() {
			resolved = "dark"
		} else {
			resolved = "light"
		}
	}
	switch resolved {
	case "deep-blue":
		return application.RGBA{Red: 16, Green: 23, Blue: 40, Alpha: 255} // #101728
	case "light":
		return application.RGBA{Red: 255, Green: 255, Blue: 255, Alpha: 255} // #ffffff
	default:
		return application.RGBA{Red: 27, Green: 31, Blue: 39, Alpha: 255} // #1b1f27
	}
}

// System tray support (issue #982): a persistent tray icon with a left-click
// show/hide toggle and a small right-click menu. The tray is what makes the
// window "hide to the notification area" recoverable — without it a hidden
// window could only be brought back via the global hotkey.

// trayLabels carries the tray menu strings for every UI language the
// frontend offers. The Go side can't use the frontend's i18n bundles, so the
// labels live here, keyed by the settings language code; unknown codes fall
// back to English.
var trayLabels = map[string][6]string{
	// show, hide, reset, settings, about, quit
	"en":    {"Show Main Window", "Hide to Tray", "Reset IT Tool Window", "Settings", "About", "Quit"},
	"zh-CN": {"显示主窗口", "隐藏到托盘", "重置IT工具窗口", "设置", "关于", "退出"},
	"zh-TW": {"顯示主視窗", "隱藏到系統匣", "重置 IT 工具視窗", "設定", "關於", "結束"},
	"ja":    {"メインウィンドウを表示", "トレイに隠す", "ITツールのウィンドウ位置をリセット", "設定", "バージョン情報", "終了"},
	"ko":    {"메인 창 표시", "트레이로 숨기기", "IT 도구 창 위치 재설정", "설정", "정보", "종료"},
	"de":    {"Hauptfenster anzeigen", "In den Tray minimieren", "IT-Tool-Fensterposition zurücksetzen", "Einstellungen", "Über", "Beenden"},
	"es":    {"Mostrar ventana principal", "Ocultar en la bandeja", "Restablecer posición de la ventana de IT", "Ajustes", "Acerca de", "Salir"},
	"fr":    {"Afficher la fenêtre", "Réduire dans la zone de notification", "Réinitialiser la position de la fenêtre IT", "Paramètres", "À propos", "Quitter"},
	"ru":    {"Показать главное окно", "Свернуть в трей", "Сбросить положение окна IT-инструментов", "Настройки", "О программе", "Выход"},
	"pt":    {"Mostrar janela principal", "Ocultar na bandeja", "Redefinir posição da janela de IT", "Configurações", "Sobre", "Sair"},
	"vi":    {"Hiện cửa sổ chính", "Ẩn vào khay hệ thống", "Đặt lại vị trí cửa sổ IT", "Cài đặt", "Giới thiệu", "Thoát"},
}

func trayLabel(lang string, idx int) string {
	if labels, ok := trayLabels[lang]; ok {
		return labels[idx]
	}
	return trayLabels["en"][idx]
}

const (
	trayShow  = 0
	trayHide  = 1
	trayReset = 2
	traySet   = 3
	trayAbout = 4
	trayQuit  = 5
)

// setupTray creates the system tray icon and menu. lang is the persisted UI
// language (from settings.json) used to localize the menu labels.
func setupTray(w3app *application.App, app *App, window *application.WebviewWindow, lang string) {
	tray := w3app.SystemTray.New()
	applyTrayIcon(tray)
	tray.SetTooltip("对号")

	menu := application.NewMenu()
	// M23：托盘「显示/隐藏」指向 NAS 主窗（应用的脸）；设置/关于仍需要 IT 工具窗口。
	menu.Add(trayLabel(lang, trayShow)).OnClick(func(*application.Context) {
		app.ShowNasWindow()
	})
	menu.Add(trayLabel(lang, trayHide)).OnClick(func(*application.Context) {
		if nw := app.nasWindow(); nw != nil && nw.IsVisible() {
			nw.Hide()
			return
		}
		window.Hide()
	})
	menu.AddSeparator()
	menu.Add(trayLabel(lang, trayReset)).OnClick(func(*application.Context) {
		app.resetWindowGeometry()
	})
	menu.AddSeparator()
	menu.Add(trayLabel(lang, traySet)).OnClick(func(*application.Context) {
		showMainWindow(window)
		app.emit("app:open-settings")
	})
	menu.Add(trayLabel(lang, trayAbout)).OnClick(func(*application.Context) {
		showMainWindow(window)
		app.emit("app:open-about")
	})
	menu.AddSeparator()
	menu.Add(trayLabel(lang, trayQuit)).OnClick(func(*application.Context) {
		w3app.Quit()
	})

	tray.SetMenu(menu)

	// Left click toggles visibility, mirroring MobaXterm's tray behaviour.
	tray.OnClick(func() {
		// M23：左键切 NAS 主窗（没有 NAS 窗的构建退回到 IT 工具窗）。
		if app.nasWindow() == nil {
			if window.IsVisible() {
				window.Hide()
			} else {
				showMainWindow(window)
			}
			return
		}
		app.ToggleNasWindow()
	})

	tray.Show()
}

// showMainWindow brings the window back from hidden/minimised state and
// gives it focus. Safe to call from any goroutine — the wails v3 window
// methods dispatch to the main thread internally.
func showMainWindow(window *application.WebviewWindow) {
	if window == nil {
		return
	}
	if !window.IsVisible() {
		window.Show()
	}
	window.UnMinimise()
	window.Focus()
}

// toggleMainWindow shows the window when hidden (or minimised), hides it
// otherwise.
func toggleMainWindow(window *application.WebviewWindow) {
	if window == nil {
		return
	}
	if window.IsVisible() {
		window.Hide()
	} else {
		showMainWindow(window)
	}
}

// Global show/hide hotkey state. applyGlobalShowHideHotkey swaps the
// registered accelerator; the currently registered string is tracked here so
// unrelated settings saves don't re-register (which would briefly release
// the OS-wide binding). Access is mutex-guarded: SaveSettings runs on a
// binding goroutine while startup applies the initial binding from main.
var (
	trayHotkeyMu      sync.Mutex
	trayHotkeyCurrent string
)

// applyGlobalShowHideHotkey registers (or re-registers) the OS-global
// show/hide shortcut via the wails GlobalShortcutManager, which handles the
// platform backends (RegisterHotKey on Windows, Carbon on macOS, X11 on
// Linux). A nil binding means the default Ctrl+M; a binding with an empty
// key (the UI's "cleared" state) unregisters. Failures (the combo already
// owned by another app) are logged and leave no binding registered.
func applyGlobalShowHideHotkey(w3app *application.App, app *App, window *application.WebviewWindow, binding *store.KeyBinding) {
	if w3app == nil {
		return
	}
	trayHotkeyMu.Lock()
	defer trayHotkeyMu.Unlock()

	if trayHotkeyCurrent != "" {
		if err := w3app.GlobalShortcut.Unregister(trayHotkeyCurrent); err != nil {
			log.Writef("global hotkey: unregister %q failed: %v", trayHotkeyCurrent, err)
		}
		trayHotkeyCurrent = ""
	}
	if binding == nil {
		binding = &store.KeyBinding{Ctrl: true, Key: "m"}
	}
	accel := keyBindingAccelerator(binding)
	if accel == "" {
		// Empty key = the UI's "cleared" state — stays unregistered.
		return
	}
	if err := w3app.GlobalShortcut.Register(accel, func() {
		// M23：全局显隐热键作用在 NAS 主窗（应用的脸）；没有 NAS 窗才退回主窗口。
		if app != nil && app.nasWindow() != nil {
			app.ToggleNasWindow()
			return
		}
		toggleMainWindow(window)
	}); err != nil {
		log.Writef("global hotkey: register %q failed: %v", accel, err)
		return
	}
	trayHotkeyCurrent = accel
}

// keyBindingAccelerator renders a frontend KeyBinding as a wails accelerator
// string ("Ctrl+Alt+M"). Empty string when the binding has no key.
func keyBindingAccelerator(b *store.KeyBinding) string {
	key := strings.ToLower(strings.TrimSpace(b.Key))
	if key == "" {
		return ""
	}
	var parts []string
	if b.Ctrl {
		parts = append(parts, "Ctrl")
	}
	if b.Alt {
		parts = append(parts, "Alt")
	}
	if b.Shift {
		parts = append(parts, "Shift")
	}
	if b.Meta {
		parts = append(parts, "Super")
	}
	return strings.Join(append(parts, key), "+")
}

// trayHotkeyBinding extracts the global show/hide hotkey from the keyboard
// settings map ("trayShowHide" key, same shape as the in-app bindings).
// Absent = default Ctrl+M; present-with-empty-key = disabled.
func trayHotkeyBinding(s *store.AppSettings) *store.KeyBinding {
	if b, ok := s.Keyboard["trayShowHide"]; ok {
		return &b
	}
	return nil
}
