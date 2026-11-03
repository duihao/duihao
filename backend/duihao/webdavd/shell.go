package webdavd

// 内置窗口的「窗口按钮」桥。
//
// 为什么需要这一层：NAS 各模块是在客户端里用独立 webview 打开的，窗口是
// Frameless（没有原生标题栏）。那些页面加载的是真实网页
// （http://127.0.0.1:<port>/-notes …），不是 Wails 的 assetserver 页面，
// 因此拿不到 window.wails 运行时 —— 网页既没法调 Window.Close()，也没法用
// --wails-draggable 拖窗口。最小化/最大化/关闭/拖动只能「网页画按钮 → 回本机
// 请 Go 执行」。
//
// 安全边界（这套东西会碰到窗口，不进文件层也不进账号层）：
//   - 只认回环来源（票是主程序给本机 webview 现发的，不该出现在网络上）；
//   - 每窗一张 32 位十六进制随机票，关窗即作废；
//   - 动作只有 min / max / state / close / drag 五个，白名单之外一律 404；
//   - 主程序没注册（纯网页部署）时端点整体 404，网页也就不画按钮。

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

// shellDo 执行动作并回报执行后的最大化状态。第二个返回值给网页同步
// 「最大化 / 向下还原」那颗按钮的图标 —— 只靠前端点击翻转会和真实状态
// 走散（例如窗口已最大化时切到另一个应用，新页面无从知道，图标就是反的）。
var shellDo func(token, act string) (ok, maximised bool)

// SetShell 由主程序注册窗口动作的执行者。未注册时 handleShell 一律 404。
func SetShell(f func(token, act string) (ok, maximised bool)) { shellDo = f }

// shellActs 允许的动作白名单。state 只查询不执行，用于首帧和 resize 后对表。
var shellActs = map[string]bool{"min": true, "max": true, "state": true, "close": true, "drag": true}

// handleShell 处理 /-shell/<act>?w=<token>；不是该前缀返回 false（交回主链）。
func (s *Server) handleShell(w http.ResponseWriter, r *http.Request) bool {
	const prefix = "/-shell/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	act := strings.TrimPrefix(r.URL.Path, prefix)
	token := r.URL.Query().Get("w")
	if !shellActs[act] || len(token) != 32 || shellDo == nil || !loopbackOnly(r) {
		http.NotFound(w, r)
		return true
	}
	ok, maximised := shellDo(token, act)
	if !ok {
		http.NotFound(w, r)
		return true
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		Max bool `json:"max"`
	}{Max: maximised})
	return true
}

// loopbackOnly 判定请求是否来自本机回环（127.0.0.0/8 或 ::1）。
func loopbackOnly(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
