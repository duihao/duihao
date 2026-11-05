//go:build windows

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/duihao/duihao/backend/log"
	"github.com/duihao/duihao/backend/platform"
	"github.com/duihao/duihao/backend/session"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func additionalWindowOptions(options application.WebviewWindowOptions) application.WebviewWindowOptions {
	return options
}

func (a *App) GetAvailableShells() []string {
	var shells []string
	var seen = make(map[string]bool)

	add := func(path string) {
		if path == "" {
			return
		}
		abs, err := exec.LookPath(path)
		if err != nil {
			return
		}
		key := strings.ToLower(strings.ReplaceAll(abs, `\`, `/`))
		if seen[key] {
			return
		}
		seen[key] = true
		shells = append(shells, abs)
	}

	hasShell := func(name string) bool {
		for _, sh := range shells {
			if strings.EqualFold(filepath.Base(sh), name) {
				return true
			}
		}
		return false
	}

	add("pwsh.exe")
	add("powershell.exe")
	add("cmd.exe")
	// CMD (Clink) rides the user's installed clink (1.1+); the Tabby-style
	// profile tuning happens at session start (backend/session/local_clink.go).
	if clink := findClink(); clink != "" {
		shells = append(shells, session.ClinkShellPathPrefix+clink)
	}
	// A user-configured Git Bash location (settings.gitBashPath) wins over the
	// auto-probed ones, so it is listed first among the bash shells.
	if a.settingsStore != nil {
		if st, err := a.settingsStore.Load(); err == nil {
			if p := resolveGitBashPath(st.GitBashPath, fileExists); p != "" {
				add(p)
			}
		}
	}
	for _, p := range gitBashCandidates() {
		add(p)
	}
	for _, p := range []string{
		`C:\Program Files\Git\bin\bash.exe`,
		`C:\Program Files (x86)\Git\bin\bash.exe`,
		`C:\ProgramData\chocolatey\bin\bash.exe`,
	} {
		add(p)
	}
	// Third-party shells (Cygwin, MSYS2, Nushell): probe well-known install
	// locations and the registry so they are offered automatically, without
	// any user configuration. Added after the built-ins and before the
	// generic PATH bash.exe fallback, whose System32 hit they preempt when a
	// real bash exists.
	for _, sh := range detectThirdPartyShells(probeCygwinRootdir(),
		func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
		exec.LookPath) {
		add(sh)
	}
	if !hasShell("bash.exe") {
		add("bash.exe")
	}
	if distros, _ := listWSLDistros(); len(distros) > 0 {
		for _, d := range distros {
			shells = append(shells, "wsl://"+d)
		}
	}
	// Administrator variants ride on the detected cmd/powershell paths (UAC
	// elevation via the broker in backend/session/local_admin_windows.go).
	// Only offered when Duihao itself is unelevated — as admin they would be
	// indistinguishable duplicates of the plain entries.
	if !session.IsProcessElevated() {
		for _, sh := range shells {
			base := strings.ToLower(filepath.Base(sh))
			if base == "cmd.exe" || base == "powershell.exe" {
				shells = append(shells, session.AdminShellPathPrefix+sh)
			}
		}
		for _, sh := range shells {
			if _, ok := session.ParseClinkShellPath(sh); ok {
				shells = append(shells, session.AdminShellPathPrefix+sh)
			}
		}
	}
	return shells
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// resolveGitBashPath turns a user-supplied Git location into a bash.exe path.
// It accepts bash.exe itself, a Git for Windows root (such as a scoop
// `apps\git\current` directory) or its bin / usr\bin subdirectory. Returns ""
// when nothing usable exists.
func resolveGitBashPath(input string, exists func(string) bool) string {
	input = strings.Trim(strings.TrimSpace(input), `"`)
	if input == "" {
		return ""
	}
	input = os.ExpandEnv(strings.ReplaceAll(input, "%USERPROFILE%", "$USERPROFILE"))
	if strings.HasSuffix(strings.ToLower(input), ".exe") {
		if exists(input) {
			return input
		}
		return ""
	}
	for _, rel := range [][]string{{"bin", "bash.exe"}, {"usr", "bin", "bash.exe"}, {"bash.exe"}} {
		p := filepath.Join(append([]string{input}, rel...)...)
		if exists(p) {
			return p
		}
	}
	return ""
}

// gitBashCandidates lists per-user Git for Windows installs (scoop) that the
// fixed Program Files probes miss.
func gitBashCandidates() []string {
	var out []string
	for _, root := range []string{
		filepath.Join(os.Getenv("USERPROFILE"), "scoop"),
		os.Getenv("SCOOP"),
	} {
		if root == "" || root == "scoop" {
			continue
		}
		if p := resolveGitBashPath(filepath.Join(root, "apps", "git", "current"), fileExists); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// detectThirdPartyShells probes well-known third-party shell installs that
// should be offered alongside the built-in shells: Cygwin bash, MSYS2 bash
// and Nushell. cygwinRegRoot is the Cygwin setup registry probe result (""
// when absent). Only paths that actually exist are returned; duplicates are
// removed case-insensitively.
func detectThirdPartyShells(cygwinRegRoot string, exists func(string) bool, lookPath func(string) (string, error)) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(path string) {
		if path == "" {
			return
		}
		key := strings.ToLower(path)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, path)
	}

	// Cygwin: setup.exe records the install root in the registry; also probe
	// the conventional install dirs.
	cygwinRoots := []string{`C:\cygwin64`, `C:\cygwin`, `C:\tools\cygwin64`, `C:\tools\cygwin`}
	if cygwinRegRoot != "" {
		cygwinRoots = append([]string{cygwinRegRoot}, cygwinRoots...)
	}
	for _, root := range cygwinRoots {
		bash := filepath.Join(root, "bin", "bash.exe")
		if exists(bash) {
			add(bash)
		}
	}

	// MSYS2: bash lives under usr\bin and there is no registry entry.
	msysRoots := []string{`C:\msys64`, `C:\msys32`, `C:\tools\msys64`, `C:\tools\msys32`}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		msysRoots = append(msysRoots, filepath.Join(home, "msys64"), filepath.Join(home, "msys32"))
	}
	for _, root := range msysRoots {
		bash := filepath.Join(root, "usr", "bin", "bash.exe")
		if exists(bash) {
			add(bash)
		}
	}

	// Nushell: usually on PATH (scoop/choco/winget shims).
	if nu, err := lookPath("nu.exe"); err == nil && nu != "" {
		add(nu)
	}

	return out
}

// probeCygwinRootdir reads the Cygwin setup install root from the registry.
// Returns "" when Cygwin is absent or the key cannot be read.
func probeCygwinRootdir() string {
	keys := []string{
		`SOFTWARE\Cygwin\setup`,
		`SOFTWARE\WOW6432Node\Cygwin\setup`,
	}
	for _, k := range keys {
		for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
			key, err := registry.OpenKey(root, k, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			rootdir, _, err := key.GetStringValue("rootdir")
			key.Close()
			if err == nil && rootdir != "" {
				return rootdir
			}
		}
	}
	return ""
}

// findClink locates the user's clink (1.1+): PATH first (covers installs
// that put themselves on PATH, scoop shims and chocolatey shims), then the
// well known install directories clink's setup and package managers use.
// The release layout ships arch-named executables (clink_x64.exe,
// clink_arm64.exe, ... from the official zip), older/manual installs have a
// plain clink.exe — both are accepted. Returns "" when clink is not found.
func findClink() string {
	archExe := map[string]string{
		"amd64": "clink_x64.exe",
		"arm64": "clink_arm64.exe",
		"386":   "clink_x86.exe",
	}[runtime.GOARCH]
	for _, name := range []string{archExe, "clink.exe"} {
		if name == "" {
			continue
		}
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	for _, dir := range []string{
		filepath.Join(os.Getenv("ProgramFiles"), "clink"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "clink"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "clink"),
		filepath.Join(os.Getenv("USERPROFILE"), "scoop", "apps", "clink", "current"),
		filepath.Join(os.Getenv("SCOOP"), "apps", "clink", "current"),
	} {
		if dir == "" {
			continue
		}
		for _, name := range []string{archExe, "clink.exe"} {
			if name == "" {
				continue
			}
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

func listWSLDistros() ([]string, error) {
	cmd := exec.Command("wsl.exe", "-l", "-q")
	platform.HideConsoleWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, nil
	}
	return parseWSLDistros(out), nil
}

func parseWSLDistros(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}

	content := string(raw)
	if len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE {
		u16 := make([]uint16, 0, len(raw)/2)
		for i := 2; i+1 < len(raw); i += 2 {
			u16 = append(u16, uint16(raw[i])|uint16(raw[i+1])<<8)
		}
		content = string(utf16.Decode(u16))
	}

	var distros []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		line = strings.ReplaceAll(line, "\x00", "")
		line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "docker-desktop") {
			continue
		}
		if !seen[line] {
			seen[line] = true
			distros = append(distros, line)
		}
	}
	return distros
}

const (
	GWLP_WNDPROC     = ^uintptr(3)
	GWL_STYLE        = ^uintptr(16)
	GWL_EXSTYLE      = ^uintptr(19)
	WM_CLOSE         = 0x0010
	WM_ENTERSIZEMOVE = 0x0231
	WM_EXITSIZEMOVE  = 0x0232
	WM_SIZE          = 0x0005

	// 原生 caption 位（保留常量备用；不用改 style 去 caption，会破坏 wails
	// 的 WM_NCCALCSIZE 计算导致启动即崩——改用 CloseButtonState: ButtonHidden）
	WS_BORDER   = 0x00800000
	WS_DLGFRAME = 0x00400000
	WS_CAPTION  = 0x00C00000
	WS_SYSMENU  = 0x00080000

	WS_EX_TOOLWINDOW = 0x00000080
	WS_EX_APPWINDOW  = 0x00040000
	SM_CXSCREEN      = 0
	SM_CYSCREEN      = 1
	SWP_NOSIZE       = 0x0001
	SWP_NOMOVE       = 0x0002
	SWP_NOZORDER     = 0x0004
	SWP_NOACTIVATE   = 0x0010
	SWP_FRAMECHANGED = 0x0020
)

// setWindowPosPhysical moves the window so its top-left corner lands on the
// given PHYSICAL pixel coordinates, leaving size and z-order alone. Used for
// the normal-mode placement: wails' SetPosition converts through its own DIP
// pipeline and landed well off the work-area edge on this machine (M21 实测
// 偏右上 ~480 DIP), while SetWindowPos is exact.
func setWindowPosPhysical(hwnd unsafe.Pointer, x, y int) {
	if hwnd == nil {
		return
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	user32.NewProc("SetWindowPos").Call(
		uintptr(hwnd), 0, uintptr(x), uintptr(y), 0, 0,
		SWP_NOSIZE|SWP_NOZORDER|SWP_NOACTIVATE)
}

// nasRightPosition 算出 NAS 主窗「贴主屏工作区右侧、垂直居中」的**物理像素**
// 落点。刻意不用 wails 的 Screen 数据：它给的是 DIP 工作区，经自己的 DIP 管线
// 换算后落点会偏（M25 实测离右缘还差 ~79 DIP、垂直偏下 ~30 DIP）。这里直接问
// 系统三件事：显示器工作区（物理）、窗口当前 rect（物理）、该窗的 DPI。
//
// margin 是「离右缘留多少 DIP」，按窗口 DPI 折算成物理像素后再扣。
//
// expW/expH 是期望的窗口物理尺寸（0 = 不校验）。必须校验：SetSize 是异步下发
// 的，刚建窗/刚 Show 时 GetWindowRect 拿到的往往还是上一个尺寸（甚至默认尺寸），
// 用错的宽去算 x 就会落到屏幕中间偏左 —— 表现就是「弹出时没在右边」。尺寸对不上
// 就返回 false，让调用方下一轮再试。
func nasRightPosition(hwnd unsafe.Pointer, margin, expW, expH int) (int, int, bool) {
	if hwnd == nil {
		return 0, 0, false
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	pGetRect := user32.NewProc("GetWindowRect")
	pMonFromWin := user32.NewProc("MonitorFromWindow")
	pGetMonInfo := user32.NewProc("GetMonitorInfoW")

	type rect struct{ Left, Top, Right, Bottom int32 }
	type monitorInfo struct {
		CbSize    uint32
		RcMonitor rect
		RcWork    rect
		DwFlags   uint32
	}
	const monitorDefaultToNearest = 2 // MONITOR_DEFAULTTONEAREST

	var wr rect
	if r, _, _ := pGetRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wr))); r == 0 {
		return 0, 0, false
	}
	w, h := int(wr.Right-wr.Left), int(wr.Bottom-wr.Top)
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	// 尺寸还没落到目标值（SetSize 未生效）就别急着算 x，否则右边会空一大截。
	const sizeTol = 8
	if (expW > 0 && (w < expW-sizeTol || w > expW+sizeTol)) ||
		(expH > 0 && (h < expH-sizeTol || h > expH+sizeTol)) {
		return 0, 0, false
	}
	mon, _, _ := pMonFromWin.Call(uintptr(hwnd), monitorDefaultToNearest)
	mi := monitorInfo{}
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if r, _, _ := pGetMonInfo.Call(mon, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return 0, 0, false
	}
	workW := int(mi.RcWork.Right - mi.RcWork.Left)
	workH := int(mi.RcWork.Bottom - mi.RcWork.Top)
	if workW <= 0 || workH <= 0 {
		return 0, 0, false
	}
	// margin 按窗口 DPI 折算（96 = 100%）
	m := margin
	if pGetDpi := user32.NewProc("GetDpiForWindow"); true {
		if dpi, _, _ := pGetDpi.Call(uintptr(hwnd)); dpi >= 96 {
			m = margin * int(dpi) / 96
		}
	}
	x := int(mi.RcWork.Right) - w - m
	y := int(mi.RcWork.Top) + (workH-h)/2
	if y < int(mi.RcWork.Top) {
		y = int(mi.RcWork.Top)
	}
	return x, y, true
}

// styleToolWindow gives the M18-C palette window WS_EX_TOOLWINDOW so it stays
// out of the taskbar and Alt+Tab, like uTools' floating panel.
func styleToolWindow(hwnd uintptr) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	pGet := user32.NewProc("GetWindowLongPtrW")
	pSet := user32.NewProc("SetWindowLongPtrW")
	pSetWindowPos := user32.NewProc("SetWindowPos")
	st, _, _ := pGet.Call(hwnd, GWL_EXSTYLE)
	// 只加 TOOLWINDOW 不够：wails 创建无 owner 的无边框窗口时会带 WS_EX_APPWINDOW
	// （0x40000），它会强制出现任务栏按钮、压过 TOOLWINDOW —— 必须同时清掉。
	st = (st | WS_EX_TOOLWINDOW) &^ WS_EX_APPWINDOW
	pSet.Call(hwnd, GWL_EXSTYLE, st)
	pSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE|SWP_NOZORDER|SWP_FRAMECHANGED)
}

// positionPaletteTopCenter places the frameless palette window top-center on
// the primary screen (uTools shows its panel the same way).
func positionPaletteTopCenter(hwnd uintptr, w, h int) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	pGet := user32.NewProc("GetSystemMetrics")
	pSetWindowPos := user32.NewProc("SetWindowPos")
	sw, _, _ := pGet.Call(SM_CXSCREEN)
	sh, _, _ := pGet.Call(SM_CYSCREEN)
	if sw == 0 || sh == 0 {
		return
	}
	x := (int(sw) - w) / 2
	y := int(sh) / 6
	pSetWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), 0, 0, SWP_NOSIZE|SWP_NOZORDER)
}

// forceWindowSize re-applies the intended w×h (logical px, DPI-scaled) to a
// small window whose creation size was clamped up by SM_CXMINTRACK (≈136px).
// Position is left untouched — the frontend owns the floating button's spot.
func forceWindowSize(hwnd uintptr, w, h int) {
	if hwnd == 0 {
		return
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	if pGetDpi := user32.NewProc("GetDpiForWindow"); true {
		if dpi, _, _ := pGetDpi.Call(hwnd); dpi >= 96 {
			w = w * int(dpi) / 96
			h = h * int(dpi) / 96
		}
	}
	user32.NewProc("SetWindowPos").Call(hwnd, 0, 0, 0, uintptr(w), uintptr(h),
		SWP_NOMOVE|SWP_NOZORDER)
}

// positionFloatingIcon was removed: the floating button's position is now
// owned by the frontend (FloatIcon onMounted restores the remembered spot or
// computes the default), so the Go first-show hook can no longer clobber the
// user's last drag position.

// findMainWindow 定位「IT 工具」窗口（原主窗口）的原生句柄。
//
// M23 拆窗后 NAS 主窗也叫「对号」（用户定稿：标题保持「对号」，不写成
// 「对号 · NAS」），所以「标题前缀 + 不含 · 」这条判据会同时命中两个窗口。
// 这里补两道区分：
//  1. 排掉 app.nasHwnd（NAS 主窗创建/显示时记下的句柄）；
//  2. 兜底按窗口面积取最大 —— NAS 窗宽锁死 330，面积一定小于 IT 工具窗。
//
// 另外一旦 mainHwnd 确定过就直接复用（IsWindow 校验），不再重复枚举。
func (a *App) findMainWindow() uintptr {
	if a.mainHwnd != 0 && a.isLiveWindow(a.mainHwnd) {
		return a.mainHwnd
	}

	pid := windows.GetCurrentProcessId()
	var best uintptr
	var bestArea int64

	nasHwnd := a.nasHwnd
	if nasHwnd == 0 && a.nasWin != nil {
		if h := a.nasWin.NativeWindow(); h != nil {
			nasHwnd = uintptr(h)
		}
	}

	user32 := windows.NewLazySystemDLL("user32.dll")
	procEnumWindows := user32.NewProc("EnumWindows")
	procGetWindowThreadProcessId := user32.NewProc("GetWindowThreadProcessId")
	procGetWindowTextW := user32.NewProc("GetWindowTextW")
	procGetWindowRect := user32.NewProc("GetWindowRect")

	cb := windows.NewCallback(func(hwnd windows.HWND, lParam uintptr) uintptr {
		var wndPid uint32
		procGetWindowThreadProcessId.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wndPid)))
		if wndPid != pid {
			return 1 // continue
		}
		// Verify it has our window title so we don't pick up invisible helper windows.
		// M19：专有版会把标题改成「对号网盘」等品牌名 —— 用前缀匹配，同时排除
		// 面板/悬浮/菜单等其他 webview（它们的标题形如「对号 · 面板」）。
		buf := make([]uint16, 256)
		procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), 255)
		title := windows.UTF16ToString(buf)
		if !strings.HasPrefix(title, "对号") || strings.Contains(title, " · ") {
			return 1 // continue
		}
		h := uintptr(hwnd)
		if nasHwnd != 0 && h == nasHwnd {
			return 1 // NAS 主窗，跳过
		}
		type rect struct{ L, T, R, B int32 }
		var rc rect
		procGetWindowRect.Call(h, uintptr(unsafe.Pointer(&rc)))
		area := int64(rc.R-rc.L) * int64(rc.B-rc.T)
		if area > bestArea {
			bestArea, best = area, h
		}
		return 1 // continue：两个同名窗都收，取面积最大的那个
	})
	procEnumWindows.Call(cb, 0)
	return best
}

// isLiveWindow 判断句柄是否仍是一个有效窗口（缓存复用前的校验）。
func (a *App) isLiveWindow(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	ok, _, _ := user32.NewProc("IsWindow").Call(hwnd)
	return ok != 0
}

// bringMainWindowToFront raises the main window to the foreground once. After a
// relaunch the fresh instance can otherwise open behind other windows on Windows:
// SetForegroundWindow is normally restricted, but a process spawned by the
// currently-foreground process is one of the documented exceptions, so a single
// raise works while the old instance (the foreground process) is still alive.
// No-op when no main window has been created yet.
func (a *App) bringMainWindowToFront() {
	hwnd := a.mainHwnd
	if hwnd == 0 {
		hwnd = a.findMainWindow()
	}
	if hwnd == 0 {
		return
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	procSetForegroundWindow := user32.NewProc("SetForegroundWindow")
	procBringWindowToTop := user32.NewProc("BringWindowToTop")
	procSetForegroundWindow.Call(hwnd)
	procBringWindowToTop.Call(hwnd)
}

// emitMoveResize sends a move/resize event to the frontend without blocking.
// It must never be called from within the WndProc modal resize/move loop.
func (a *App) emitMoveResize(event string) {
	if a.moveResizeCh == nil {
		return
	}
	select {
	case a.moveResizeCh <- event:
	default:
	}
}

func (a *App) subclassMainWindow() {
	if a.mainHwnd == 0 {
		return
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	procSetWindowLongPtrW := user32.NewProc("SetWindowLongPtrW")
	procCallWindowProcW := user32.NewProc("CallWindowProcW")

	cb := windows.NewCallback(func(hwnd windows.HWND, msg uint32, wparam, lparam uintptr) uintptr {
		switch msg {
		case WM_CLOSE:
			// 点右上角 ✕ 收缩到托盘而不是退出。必须在消息层拦截：wails
			// beta.27 内置的 WindowClosing 监听会把窗口强制真关（置
			// unconditionallyClose 后走 DefWindowProc），事件层 OnWindowEvent
			// 拦不住。这里在消息到达 wails 之前把 WM_CLOSE 改成隐藏窗口，
			// 行为与托盘菜单「隐藏到托盘」一致；真正的退出走托盘「退出」
			// （app.Quit() 直接销毁应用，不发 WM_CLOSE）。
			if w := a.win(); w != nil {
				w.Hide()
			}
			return 0
		case WM_ENTERSIZEMOVE:
			a.inSizeMove = true
		case WM_EXITSIZEMOVE:
			a.inSizeMove = false
			a.emitMoveResize("window:resize-end")
		case WM_SIZE:
			if !a.inSizeMove {
				a.emitMoveResize("window:resize-end")
			}
		}
		ret, _, _ := procCallWindowProcW.Call(a.originalWndProc, uintptr(hwnd), uintptr(msg), wparam, lparam)
		return ret
	})
	a.wndProcCb = cb

	orig, _, _ := procSetWindowLongPtrW.Call(a.mainHwnd, GWLP_WNDPROC, cb)
	a.originalWndProc = orig
}

func (a *App) unsubclassMainWindow() {
	if a.originalWndProc == 0 || a.mainHwnd == 0 {
		return
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	procSetWindowLongPtrW := user32.NewProc("SetWindowLongPtrW")
	procSetWindowLongPtrW.Call(a.mainHwnd, GWLP_WNDPROC, a.originalWndProc)
	a.originalWndProc = 0
}

// configureMacKeyRepeat is a no-op on Windows; the press-and-hold accent
// picker only exists on macOS. See app_darwin.go for details.
func (a *App) configureMacKeyRepeat() {}

// hideProcWindow prevents batch-file shims (e.g. VS Code's code.cmd) from
// flashing a console window: shims can only run through cmd.exe, which
// allocates a console of its own when launched from a GUI process that has
// none. HideWindow (STARTF_USESHOWWINDOW, SW_HIDE) suppresses that console.
// It must only be applied to shims: GUI exes honor STARTF_USESHOWWINDOW for
// their first window too, so hiding them would leave the editor invisible.
// Editors needing a CLI shim for wait semantics (VS Code's -w lives in the
// node cli.js, not in Code.exe) are launched via their shim + HideWindow.
func hideProcWindow(cmd *exec.Cmd) {
	ext := strings.ToLower(filepath.Ext(cmd.Path))
	if ext == ".cmd" || ext == ".bat" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}
}

// openAsInfo mirrors the shell32 OPENASINFO structure for SHOpenWithDialog.
type openAsInfo struct {
	file  uintptr // PCSZW: file to open
	class uintptr // PCSZW: optional ProgID hint (unused)
	flags uint32
}

const (
	oaifAllowRegistration   = 0x00000001 // offer the "always use this app" checkbox
	oaifExec                = 0x00000004 // execute the picked application
	coinitApartmentThreaded = 0x2        // COINIT_APARTMENTTHREADED
)

// openWithSystem pops the system "open with" dialog for the file and executes
// the application the user picks (owned by the main window, so it stays on
// top). It calls the documented SHOpenWithDialog API directly: the legacy
// rundll32 shell32.dll,OpenAs_RunDLLW entry was tried first, but its
// dialog-to-app hand-off corrupts the path on Win10 — the picked app received
// mojibake instead of the file (reproduced with Notepad). The call blocks
// until the dialog closes; cancelling it counts as success.
func (a *App) openWithSystem(p string) error {
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return err
	}
	// SHOpenWithDialog requires COM (STA) on the calling thread. S_OK (0)
	// means this call initialized it and must pair an uninitialize; S_FALSE
	// (1, already initialized) must not.
	ole32 := windows.NewLazySystemDLL("ole32.dll")
	if r, _, _ := ole32.NewProc("CoInitializeEx").Call(0, coinitApartmentThreaded); r == 0 {
		defer ole32.NewProc("CoUninitialize").Call()
	}
	info := openAsInfo{file: uintptr(unsafe.Pointer(p16)), flags: oaifAllowRegistration | oaifExec}
	shell32 := windows.NewLazySystemDLL("shell32.dll")
	r1, _, callErr := shell32.NewProc("SHOpenWithDialog").Call(a.findMainWindow(), uintptr(unsafe.Pointer(&info)))
	if r1 != 0 {
		return nil
	}
	// User closed the dialog without choosing an app — not an error.
	if errno, ok := callErr.(windows.Errno); ok && errno == windows.ERROR_CANCELLED {
		return nil
	}
	return fmt.Errorf("open-with dialog failed: %w", callErr)
}

// detectExternalEditors scans for text editors installed on this Windows host
// and returns only those actually found. Console-based editors (Vim, Neovim,
// nano, Micro) are excluded: spawned from a GUI app without a console they
// exit immediately, so they aren't offered. Every GUI editor is probed on PATH
// first, then falls back to common install dirs (globbing versioned folders),
// because default Windows installs often leave these off PATH.
func detectExternalEditors() []ExternalEditorOption {
	type fixed struct{ glob, suffix string }
	type editor struct {
		name, prog, pathCmd string
		fixed               []fixed
	}
	// VS Code family: the -w (wait) flag is implemented by the node cli.js
	// invoked from the bin\*.cmd shim, NOT by the GUI exe — launching Code.exe
	// directly exits immediately and breaks auto-upload. So even when the exe
	// is found at a fixed path, the command must point at the bin shim.
	editors := []editor{
		{"VS Code", "code", "code -w", []fixed{
			{filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Microsoft VS Code", "bin", "code.cmd"), " -w"},
			{filepath.Join(os.Getenv("ProgramFiles"), "Microsoft VS Code", "bin", "code.cmd"), " -w"},
		}},
		{"VS Code Insiders", "code-insiders", "code-insiders -w", []fixed{
			{filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Microsoft VS Code Insiders", "bin", "code-insiders.cmd"), " -w"},
			{filepath.Join(os.Getenv("ProgramFiles"), "Microsoft VS Code Insiders", "bin", "code-insiders.cmd"), " -w"},
		}},
		{"VSCodium", "codium", "codium -w", []fixed{
			{filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "VSCodium", "bin", "codium.cmd"), " -w"},
			{filepath.Join(os.Getenv("ProgramFiles"), "VSCodium", "bin", "codium.cmd"), " -w"},
		}},
		{"Sublime Text", "subl", "subl -w", []fixed{
			{filepath.Join(os.Getenv("ProgramFiles"), "Sublime Text", "subl.exe"), " -w"},
			{filepath.Join(os.Getenv("ProgramFiles(x86)"), "Sublime Text", "subl.exe"), " -w"},
			{filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Sublime Text", "subl.exe"), " -w"},
		}},
		{"Atom", "atom", "atom", []fixed{
			{filepath.Join(os.Getenv("LOCALAPPDATA"), "atom", "bin", "atom.exe"), ""},
			{filepath.Join(os.Getenv("ProgramFiles"), "Atom", "bin", "atom.exe"), ""},
		}},
		{"gVim", "gvim", "gvim", []fixed{
			{filepath.Join(os.Getenv("ProgramFiles"), "Vim", "vim*", "gvim.exe"), ""},
			{filepath.Join(os.Getenv("ProgramFiles(x86)"), "Vim", "vim*", "gvim.exe"), ""},
		}},
		{"Emacs", "emacs", "emacs", []fixed{
			{filepath.Join(os.Getenv("ProgramFiles"), "Emacs", "emacs-*", "bin", "emacs.exe"), ""},
			{filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Emacs", "emacs-*", "bin", "emacs.exe"), ""},
		}},
		{"Notepad++", "notepad++", "notepad++", []fixed{
			{filepath.Join(os.Getenv("ProgramFiles"), "Notepad++", "notepad++.exe"), ""},
			{filepath.Join(os.Getenv("ProgramFiles(x86)"), "Notepad++", "notepad++.exe"), ""},
			{filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Notepad++", "notepad++.exe"), ""},
		}},
		{"EmEditor", "emeditor", "emeditor", []fixed{
			{filepath.Join(os.Getenv("ProgramFiles"), "EmEditor", "emeditor.exe"), ""},
			{filepath.Join(os.Getenv("ProgramFiles(x86)"), "EmEditor", "emeditor.exe"), ""},
			{filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "EmEditor", "emeditor.exe"), ""},
		}},
		{"Notepad--", "notepad--", "notepad--", []fixed{
			{filepath.Join(os.Getenv("ProgramFiles"), "Notepad--", "Notepad--.exe"), ""},
			{filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Notepad--", "Notepad--.exe"), ""},
		}},
		// Notepad lives in System32, which is always on PATH.
		{"Notepad", "notepad", "notepad", nil},
	}

	var out []ExternalEditorOption
	seen := map[string]bool{} // by canonical display name
	add := func(name, command string) {
		if seen[name] {
			return
		}
		seen[name] = true
		out = append(out, ExternalEditorOption{Label: name + " (" + command + ")", Value: command})
	}

	for _, e := range editors {
		// Prefer the real GUI exe in known install dirs: launching it directly
		// avoids the batch shim (code.cmd) and the console window it flashes.
		for _, f := range e.fixed {
			matches, _ := filepath.Glob(f.glob)
			if len(matches) > 0 {
				add(e.name, "\""+matches[0]+"\""+f.suffix)
				break
			}
		}
		if seen[e.name] {
			continue
		}
		// Fall back to PATH (may resolve to a shim — covered by hideProcWindow).
		if _, err := exec.LookPath(e.prog); err == nil {
			add(e.name, e.pathCmd)
		}
	}

	return out
}

// Win11 rounds the corners of top-level windows whose DWM frame is intact.
// Wails' frameless windows keep WS_THICKFRAME, but Wails only extends the DWM
// frame (DwmExtendFrameIntoClientArea) from its WM_ACTIVATE handler, and the
// window's first activation happens inside CreateWindowEx (WS_VISIBLE) before
// Wails' WndProc is hooked — so that first extension is missed. A freshly
// launched binary then shows square corners until the next activation
// (minimise/restore, alt-tab) reapplies it. Asking DWM directly for rounded
// corners makes the first paint correct regardless of activation timing; on
// pre-Win11 systems the unsupported attribute call fails silently, which is
// fine.
const (
	dwmwaWindowCornerPreference = 33
	dwmwaBorderColor            = 34
	dwmwcpDoNotRound            = 1
	// DWMWA_COLOR_NONE: tells DWM to draw no border at all. The window shape
	// is CSS-drawn, so the native 1px frame line (white in light Windows
	// themes) would otherwise trace a square outline over the rounded corners.
	dwmwaColorNone = 0xFFFFFFFE
)

var procDwmSetWindowAttribute = syscall.NewLazyDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")

// removeNativeWindowBorder strips the native window chrome on the given HWND:
// DWMWCP_DONOTROUND (the window shape is CSS-drawn on a transparent window)
// and DWMWA_BORDER_COLOR = DWMWA_COLOR_NONE (no 1px DWM frame line, which
// shows as a white square outline around rounded corners in light themes).
func removeNativeWindowBorder(hwnd unsafe.Pointer) {
	if hwnd == nil {
		return
	}
	preference := uint32(dwmwcpDoNotRound)
	_, _, _ = procDwmSetWindowAttribute.Call(
		uintptr(hwnd),
		dwmwaWindowCornerPreference,
		uintptr(unsafe.Pointer(&preference)),
		unsafe.Sizeof(preference),
	)
	border := uint32(dwmwaColorNone)
	_, _, _ = procDwmSetWindowAttribute.Call(
		uintptr(hwnd),
		dwmwaBorderColor,
		uintptr(unsafe.Pointer(&border)),
		unsafe.Sizeof(border),
	)
}

// systemPrefersDark reports whether Windows is in app dark mode, reading the
// same Personalization registry value the WebView2 engine maps to the CSS
// prefers-color-scheme media query. Needed because v3's IsDarkMode() is
// unavailable before Run() and the startup background colour must be resolved
// at window creation. Any failure defaults to dark.
func systemPrefersDark() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer k.Close()
	val, _, err := k.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return true
	}
	return val == 0
}

// waitForRelaunchParent and the DUIHAO_RELAUNCH_PID handshake were removed:
// the relaunch is now quit-then-spawn (relaunchPending + spawnSuccessorProcess
// in main.go), so the successor never sees the lock held.

// spawnSuccessorProcess starts a fresh, detached copy of the current
// executable. Called from main() AFTER w3app.Run() has returned and the
// process is about to exit, so the successor starts with the single-instance
// lock free and the stores quiesced (see RelaunchApp for why spawn happens
// after quit).
func spawnSuccessorProcess() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe)
	cmd.Dir = filepath.Dir(exe)
	return cmd.Start()
}

// applyTrayIcon installs the tray icon: the colored glyph PNG pre-scaled to
// the exact OS small-icon size. Windows tray icons keep their own colors —
// no template-image treatment like macOS (see app_darwin.go).
func applyTrayIcon(tray *application.SystemTray) {
	tray.SetIcon(trayIconPNG())
}

// trayIconPNG returns the tray icon bytes. The build ships one PNG per OS
// small-icon size (SM_CXSMICON — 16px at 100% DPI, 20/24/32 at higher
// scaling), each pre-rendered on its exact pixel grid by
// scripts/gen_tray_icons.py: strokes fill whole pixels and edges stay crisp,
// which rescaling a single large PNG can never achieve. Falls back to
// box-filtering the 64px legacy tray PNG for any size without an exact
// entry. Called once during tray setup; per-monitor DPI changes are not
// re-rendered (rare for a tray icon).
func trayIconPNG() []byte {
	user32 := windows.NewLazySystemDLL("user32.dll")
	const smCxSmIcon = 49
	size, _, _ := user32.NewProc("GetSystemMetrics").Call(smCxSmIcon)
	if size == 0 {
		size = 16
	}
	n := int(size)
	if b, err := appIconTraySet.ReadFile(fmt.Sprintf("build/tray/tray-%02d.png", n)); err == nil {
		return b
	}
	return scalePNG(appIconTrayPNG, n, n)
}

// scalePNG decodes a PNG and box-filter downsamples it to w×h, returning the
// result re-encoded as PNG. On any decode error the input is returned
// unchanged (wails then falls back to GDI scaling). Box filtering averages
// each destination pixel over its source rectangle — the right filter for
// large ratios and cheap enough for a one-shot 64→16px icon.
func scalePNG(data []byte, w, h int) []byte {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw == 0 || sh == 0 {
		return data
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		sy0, sy1 := y*sh/h, (y+1)*sh/h
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < w; x++ {
			sx0, sx1 := x*sw/w, (x+1)*sw/w
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			// Average in premultiplied space so transparent edges don't
			// bleed dark fringes into the glyph.
			var rs, gs, bs, as float64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					r, g, bl, a := src.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					rs += float64(r)
					gs += float64(g)
					bs += float64(bl)
					as += float64(a)
				}
			}
			n := float64((sx1 - sx0) * (sy1 - sy0))
			rs, gs, bs, as = rs/n, gs/n, bs/n, as/n
			a8 := uint8(as/257 + 0.5)
			off := (y*w + x) * 4
			if a8 == 0 {
				continue // leave transparent
			}
			dst.Pix[off+0] = uint8(rs/as*257 + 0.5)
			dst.Pix[off+1] = uint8(gs/as*257 + 0.5)
			dst.Pix[off+2] = uint8(bs/as*257 + 0.5)
			dst.Pix[off+3] = a8
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return data
	}
	return buf.Bytes()
}

// Clipboard file bindings for the IronRDP engine's local→remote file paste:
// the frontend polls ClipboardGetFiles to detect an CF_HDROP clipboard, and
// the WASM client's cliprdr layer translates the advertised list into a
// FileGroupDescriptorW format list; the remote then fetches chunks through
// ClipboardReadFileChunk (via the client's FileContentsRequest flow).

// setClipboardFiles publishes completed downloads as CF_HDROP so Explorer
// and other native applications can paste them with the ordinary Ctrl+V.
func (a *App) setClipboardFiles(paths []string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	user32 := windows.NewLazySystemDLL("user32.dll")
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	var names []uint16
	for _, path := range paths {
		names = append(names, utf16.Encode([]rune(path))...)
		names = append(names, 0)
	}
	names = append(names, 0)
	data := make([]byte, 20+len(names)*2)
	binary.LittleEndian.PutUint32(data, 20)
	binary.LittleEndian.PutUint32(data[16:], 1) // DROPFILES.fWide
	for i, c := range names {
		binary.LittleEndian.PutUint16(data[20+i*2:], c)
	}
	handle, _, err := kernel32.NewProc("GlobalAlloc").Call(0x42, uintptr(len(data))) // MOVEABLE | ZEROINIT
	if handle == 0 {
		return fmt.Errorf("GlobalAlloc: %w", err)
	}
	defer func() {
		if handle != 0 {
			kernel32.NewProc("GlobalFree").Call(handle)
		}
	}()
	ptr, _, err := kernel32.NewProc("GlobalLock").Call(handle)
	if ptr == 0 {
		return fmt.Errorf("GlobalLock: %w", err)
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(ptr)), len(data)), data)
	kernel32.NewProc("GlobalUnlock").Call(handle)
	deadline := time.Now().Add(time.Second)
	for {
		opened, _, err := user32.NewProc("OpenClipboard").Call(a.mainHwnd)
		if opened != 0 {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("OpenClipboard: %w", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	defer user32.NewProc("CloseClipboard").Call()
	if ok, _, err := user32.NewProc("EmptyClipboard").Call(); ok == 0 {
		return fmt.Errorf("EmptyClipboard: %w", err)
	}
	if ok, _, err := user32.NewProc("SetClipboardData").Call(15, handle); ok == 0 {
		return fmt.Errorf("SetClipboardData: %w", err)
	}
	handle = 0 // Ownership transferred to Windows.
	return nil
}

var (
	clipboardFilesMu    sync.Mutex
	clipboardFilesPaths []string // full paths of the last advertised list, indexed by lindex
	clipboardFilesByID  = make(map[string]string)
)

// ClipboardGetFiles returns the files currently on the clipboard (CF_HDROP)
// and remembers their full paths for ClipboardReadFileChunk. Directories are
// skipped in v1.
func (a *App) ClipboardGetFiles() []ClipboardFileInfo {
	clipboardFilesMu.Lock()
	defer clipboardFilesMu.Unlock()

	paths, err := readClipboardHDROP()
	if err != nil {
		// Most common case: the clipboard holds no file list (or another
		// process holds it open). Not worth logging at volume.
		return []ClipboardFileInfo{}
	}

	out := make([]ClipboardFileInfo, 0, len(paths))
	var advertisedPaths []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		out = append(out, ClipboardFileInfo{
			ID:           fmt.Sprintf("%x", sha256.Sum256([]byte(p))),
			Name:         filepath.Base(p),
			Size:         st.Size(),
			LastModified: st.ModTime().UnixMilli(),
		})
		advertisedPaths = append(advertisedPaths, p)
		clipboardFilesByID[out[len(out)-1].ID] = p
	}
	clipboardFilesPaths = advertisedPaths
	return out
}

// ClipboardReadFileChunk reads up to `size` bytes of the advertised file at
// `index` starting at `offset` (the remote's FileContentsRequest flow) and
// returns it base64-encoded.
func (a *App) ClipboardReadFileChunk(index int, offset int64, size int) (string, error) {
	clipboardFilesMu.Lock()
	if index < 0 || index >= len(clipboardFilesPaths) {
		count := len(clipboardFilesPaths)
		clipboardFilesMu.Unlock()
		return "", fmt.Errorf("clipboard file index %d out of range (%d advertised)", index, count)
	}
	path := clipboardFilesPaths[index]
	clipboardFilesMu.Unlock()
	return readClipboardFileChunk(path, offset, size)
}

// ClipboardReadFileChunkByID keeps an advertised file bound to its original
// path even when polling discovers a new CF_HDROP list during a transfer.
func (a *App) ClipboardReadFileChunkByID(id string, offset int64, size int) (string, error) {
	clipboardFilesMu.Lock()
	path, ok := clipboardFilesByID[id]
	clipboardFilesMu.Unlock()
	if !ok {
		return "", fmt.Errorf("unknown clipboard file")
	}
	return readClipboardFileChunk(path, offset, size)
}

func readClipboardFileChunk(path string, offset int64, size int) (string, error) {
	if offset < 0 || size < 0 || size > 8*1024*1024 {
		return "", fmt.Errorf("invalid clipboard file range")
	}

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open clipboard file: %w", err)
	}
	defer f.Close()

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return "", fmt.Errorf("seek clipboard file: %w", err)
		}
	}
	buf := make([]byte, size)
	n, err := io.ReadFull(f, buf)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		buf = buf[:n]
	} else if err != nil {
		return "", fmt.Errorf("read clipboard file: %w", err)
	} else {
		buf = buf[:n]
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

// readClipboardHDROP enumerates the CF_HDROP file list on the clipboard.
func readClipboardHDROP() ([]string, error) {
	// OpenClipboard/CloseClipboard belong to an OS thread, not a goroutine.
	// Migration between these calls can leave the clipboard locked for every app.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	const CF_HDROP = 15

	user32 := windows.NewLazySystemDLL("user32.dll")
	openClipboard := user32.NewProc("OpenClipboard")
	closeClipboard := user32.NewProc("CloseClipboard")
	getClipboardData := user32.NewProc("GetClipboardData")
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	globalLock := kernel32.NewProc("GlobalLock")
	globalUnlock := kernel32.NewProc("GlobalUnlock")
	globalSize := kernel32.NewProc("GlobalSize")

	if err := openClipboard.Find(); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(100 * time.Millisecond)
	for {
		r, _, _ := openClipboard.Call(0)
		if r != 0 {
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("OpenClipboard failed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	defer closeClipboard.Call()

	r2, _, _ := getClipboardData.Call(CF_HDROP)
	if r2 == 0 {
		return nil, fmt.Errorf("clipboard holds no CF_HDROP")
	}
	hmem := windows.Handle(r2)

	locked, _, _ := globalLock.Call(r2)
	if locked == 0 {
		return nil, fmt.Errorf("GlobalLock failed")
	}
	defer globalUnlock.Call(uintptr(hmem))

	total, _, _ := globalSize.Call(r2)
	if total == 0 || total > 64*1024*1024 {
		return nil, fmt.Errorf("bad clipboard block size %d", total)
	}
	base := unsafe.Pointer(locked)

	// DROPFILES: pFiles (offset to the file list, DWORD), POINT pt (8),
	// fNC (4), fWide (4).
	if total < 20 {
		return nil, fmt.Errorf("DROPFILES header too small")
	}
	pFiles := *(*uint32)(unsafe.Pointer(base))
	fWide := *(*uint32)(unsafe.Pointer(uintptr(base) + 16))
	if pFiles == 0 || uintptr(pFiles) >= total {
		return nil, fmt.Errorf("bad DROPFILES pFiles offset %d", pFiles)
	}
	if fWide == 0 {
		return nil, fmt.Errorf("ANSI CF_HDROP is not supported")
	}

	wide := unsafe.Slice((*uint16)(unsafe.Pointer(uintptr(base)+uintptr(pFiles))), (total-uintptr(pFiles))/2)

	var names []string
	start := -1
	for i, c := range wide {
		if c == 0 {
			if start >= 0 {
				if i > start {
					names = append(names, windows.UTF16ToString(wide[start:i]))
				}
				start = -1
				// The list is double-null terminated: an empty entry ends it.
				if i+1 < len(wide) && wide[i+1] == 0 {
					break
				}
			} else {
				break // leading empty string = end of list
			}
		} else if start < 0 {
			start = i
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("CF_HDROP holds no files")
	}
	return names, nil
}

// 悬浮球右键菜单：显示 / 定位 / 隐藏自绘菜单窗（uTools 同款圆角卡片）。
// 不用原生 TrackPopupMenu 的原因：系统菜单做不出圆角卡片观感，且锚点只能
// 取窗口矩形（球窗被误拉大时离球很远）。
const (
	// M23：菜单入口（主窗 = NAS，IT工具 = 原主窗口）+ 设置 + 官网（官网排最后）。
	// 退出项已从菜单去掉（走托盘菜单），命令值保留不动，避免和托盘那套串号。
	floatMenuMain = 1 // 主窗（NAS）
	floatMenuIT   = 2 // IT工具（原主窗口）
	floatMenuSite = 3 // 官网
	floatMenuSet  = 4 // 设置
	floatMenuQuit = 5 // 退出（菜单里已不给入口，仅保留命令值）

	floatMenuW = 96  // 菜单窗逻辑宽（与 main.go 创建尺寸一致，含 14px 阴影留白）
	floatMenuH = 193 // 菜单窗逻辑高（4 项 + 1 条分隔 + 上下各 14px 留白）
	// floatMenuGap：卡片顶到球**可见边缘**（白边外沿）的间距。
	// 不能拿球窗底边当锚点 —— 球窗 78 高里图标下沿离窗底还有 20px（留给
	// 投影），直接贴窗底会多出 20px 空隙，看着就是「菜单离球太远」。
	floatMenuGap = 6
	// floatMenuTopPad：菜单窗顶部透明留白（FloatMenu.vue 的 .fm-root padding）
	floatMenuTopPad = 14
	// floatBallBottomPad：球窗底边到「图标+白边」下沿（78 - 图标底 54 - 白边 4）
	floatBallBottomPad = 20
	// floatBallTopPad：球窗顶边到「图标+白边」上沿（padding 10 + 图标偏移 4 - 白边 4）
	floatBallTopPad = 10
)

// 菜单收起监听：每次弹出换一代，旧协程自行退出。
var (
	floatMenuWatchMu  sync.Mutex
	floatMenuWatchGen int
)

// ShowFloatMenu 把菜单窗摆到悬浮球正下方并显示。坐标按窗口 DPI 从物理像素
// 换算回 DIP（wails 的 SetPosition 用 DIP，内部再转物理像素）。
func (a *App) ShowFloatMenu() error {
	fw, mw := a.floatWin, a.floatMenuWin
	if fw == nil || mw == nil || fw.NativeWindow() == nil {
		return fmt.Errorf("windows not ready")
	}
	hwnd := uintptr(fw.NativeWindow())

	user32 := windows.NewLazySystemDLL("user32.dll")
	type rect struct{ L, T, R, B int32 }
	var rc rect
	user32.NewProc("GetWindowRect").Call(hwnd, uintptr(unsafe.Pointer(&rc)))
	dpi, _, _ := user32.NewProc("GetDpiForWindow").Call(hwnd)
	if dpi < 96 {
		dpi = 96
	}
	scale := 96.0 / float64(dpi)
	ballW := float64(rc.R-rc.L) * scale
	leftDip := float64(rc.L) * scale
	bottomDip := float64(rc.B) * scale

	// 水平居中于球；靠屏幕边时钳回屏内
	x := leftDip + ballW/2 - floatMenuW/2
	screenW, screenH := windowWorkAreaDIP(mw)
	if screenW > 0 && x+floatMenuW > float64(screenW) {
		x = float64(screenW - floatMenuW)
	}
	if x < 0 {
		x = 0
	}
	// 默认紧贴球下方：锚点取「图标+白边」的下沿，而不是球窗底边（窗底还有
	// 20px 投影留白，用它当锚点会凭空多出 20px 空隙）。下方放不下（球在屏幕
	// 底部）就翻到球上方 —— 否则菜单会整个掉到屏幕外，像「没弹出来」。
	cardH := float64(floatMenuH - 2*floatMenuTopPad)
	gap := float64(floatMenuGap)
	y := bottomDip - float64(floatBallBottomPad) + gap - float64(floatMenuTopPad)
	ballTopDip := float64(rc.T) * scale
	if screenH > 0 && y+float64(floatMenuH) > float64(screenH) {
		y = ballTopDip + float64(floatBallTopPad) - gap - cardH - float64(floatMenuTopPad)
	}
	if y < 0 {
		y = 0
	}

	mw.SetPosition(int(x), int(y))
	if !mw.IsVisible() {
		mw.Show()
	}
	mw.Focus()
	// wails 的 Focus() 对 HiddenOnTaskbar + AlwaysOnTop 的小窗不总能拿到前台
	// （前台锁），拿不到就收不到 focus 事件、菜单会被自己的失焦逻辑判成
	// 「已经点到别处」而立刻收起。这里补一次 SetForegroundWindow。
	if mwHwnd := mw.NativeWindow(); mwHwnd != nil {
		user32.NewProc("SetForegroundWindow").Call(uintptr(mwHwnd))
	}
	log.Writef("FLOATMENU: show at %d,%d (ball %d,%d dpi=%d)", int(x), int(y), rc.L, rc.T, dpi)
	a.watchFloatMenuDismiss()
	return nil
}

// watchFloatMenuDismiss 轮询前台窗口决定何时收起菜单窗。
// 为什么不用前端的 blur 事件：这个 HiddenOnTaskbar + AlwaysOnTop 的小窗经常
// 拿不到/守不住前台（wails Focus 与 SetForegroundWindow 都可能被系统驳回），
// 收不到 focus 事件或立刻被夺焦，菜单就会「闪一下就没」。
// 判定：前台既不是本进程任何窗口、也不是桌面时，连续两次（400ms）才收起，
// 避免切换瞬间误杀；点本进程其它窗口（球/面板/主窗）不算离开。
func (a *App) watchFloatMenuDismiss() {
	floatMenuWatchMu.Lock()
	floatMenuWatchGen++
	gen := floatMenuWatchGen
	floatMenuWatchMu.Unlock()

	user32 := windows.NewLazySystemDLL("user32.dll")
	getForeground := user32.NewProc("GetForegroundWindow")
	getPID := user32.NewProc("GetWindowThreadProcessId")

	go func() {
		time.Sleep(400 * time.Millisecond) // 刚弹出的宽限期
		misses := 0
		for {
			time.Sleep(200 * time.Millisecond)
			floatMenuWatchMu.Lock()
			stale := gen != floatMenuWatchGen
			floatMenuWatchMu.Unlock()
			if stale {
				return
			}
			mw := a.floatMenuWin
			if mw == nil || !mw.IsVisible() {
				return
			}
			fg, _, _ := getForeground.Call()
			away := true
			if fg != 0 {
				var pid uint32
				getPID.Call(fg, uintptr(unsafe.Pointer(&pid)))
				away = int(pid) != os.Getpid()
			}
			if away {
				misses++
			} else {
				misses = 0
			}
			if misses >= 2 {
				a.HideFloatMenu()
				return
			}
		}
	}()
}

// HideFloatMenu 收起菜单窗（选完菜单项 / 失焦 / 再次按住球时调用）。
func (a *App) HideFloatMenu() {
	if a.floatMenuWin != nil && a.floatMenuWin.IsVisible() {
		a.floatMenuWin.Hide()
	}
}

// FloatMenuAction 执行菜单项：1 主窗（NAS）/ 2 IT 工具 / 3 官网 / 4 设置 / 5 退出。
// 先收菜单再动作。
func (a *App) FloatMenuAction(cmd int) {
	a.HideFloatMenu()
	switch cmd {
	case floatMenuMain:
		a.ShowNasWindow()
	case floatMenuIT:
		a.ShowITWindow()
	case floatMenuSite:
		if a.app != nil {
			_ = a.app.Browser.OpenURL(duihaoSiteURL)
		}
	case floatMenuSet:
		a.ShowITWindow()
		a.emit("app:open-settings")
	case floatMenuQuit:
		a.QuitApp()
	}
}

// duihaoSiteURL 官网地址（与「关于」里的链接同源）。
const duihaoSiteURL = "https://duihao.cn"

// windowWorkAreaDIP 返回主屏工作区的逻辑宽高（DIP，与 SetPosition 同口径），
// 用于把菜单钳在屏内。取工作区而非整屏：任务栏那一侧也别压住。
func windowWorkAreaDIP(w *application.WebviewWindow) (int, int) {
	if w == nil || w.NativeWindow() == nil {
		return 0, 0
	}
	hwnd := uintptr(w.NativeWindow())
	user32 := windows.NewLazySystemDLL("user32.dll")
	type rect struct{ L, T, R, B int32 }
	var rc rect
	// SPI_GETWORKAREA = 0x0030，取主屏工作区（多屏时悬浮球通常在主屏）
	user32.NewProc("SystemParametersInfoW").Call(0x0030, 0, uintptr(unsafe.Pointer(&rc)), 0)
	dpi, _, _ := user32.NewProc("GetDpiForWindow").Call(hwnd)
	if dpi < 96 {
		dpi = 96
	}
	return int(float64(rc.R-rc.L) * 96.0 / float64(dpi)), int(float64(rc.B-rc.T) * 96.0 / float64(dpi))
}
