package webdavd

// 主题跟随（M24）：NAS 网页的明暗跟着桌面端的外观设置走，切换后所有已经
// 打开的页面实时更新。
//
// 分工：
//   - 真源只有一个 —— 桌面端 settings.json 的 theme 字段。main/SaveSettings
//     每次变更调 SetAppTheme 推上来，这里不落盘、不读配置。
//   - 渲染期：ThemeAttr() 映射成 dark/light/system 写进 <html data-theme>，
//     页面首帧就是对的（不会先白后黑闪一下）。
//   - 运行期：SSE /-api/theme/stream 把变化推给已打开的页面（webui/theme.js）。
//
// 为什么是包级变量而不是 Server 字段：主题是"这台机器上的一个偏好"，同一
// 进程里所有网盘实例共用；挂到实例上反而要挨个同步，还容易漏。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/webui"
)

var (
	themeMu   sync.RWMutex
	appTheme  = "system" // 原始值：dark/light/system/deep-blue/duihao-red/duihao-light
	themeSubs = map[chan string]struct{}{}
)

// normalizeTheme 收敛到前端 AppSettings.theme 认识的取值；空值 / 脏值一律
// 当 system（跟随系统），这是最不容易出错的一档。
func normalizeTheme(t string) string {
	switch t {
	case "dark", "light", "system", "deep-blue", "duihao-red", "duihao-light":
		return t
	default:
		return "system"
	}
}

// themeAttrOf 把前端配色名**原样**透传给网页的 data-theme。
//
// 以前这里只映射成 dark/light 两档：深蓝落到通用 dark、深红也落到通用 dark，
// 网页永远配不上主窗那层「深底 + 品牌色」的调子（用户 2026-11-02 反馈「NAS 主题
// 样式与 NAS 主窗口不一致」）。现在 shared.css / davCSS 按桌面端 style.css 备了
// 六档变量，直接透传即可；脏值已由 normalizeTheme 收成 system。
//
// 注意这是**不加锁**的纯函数：ThemeAttr / SetAppTheme 都在持锁时调它，
// 若这里再去抢锁就是 RWMutex 自死锁（不可重入，踩过）。
func themeAttrOf(t string) string { return t }

// ThemeAttr 当前主题对应的 data-theme 值。
func ThemeAttr() string {
	themeMu.RLock()
	t := appTheme
	themeMu.RUnlock()
	return themeAttrOf(t)
}

// SetAppTheme 更新主题并推给所有订阅中的页面（幂等：值没变就不广播）。
func SetAppTheme(t string) {
	t = normalizeTheme(t)
	themeMu.Lock()
	if appTheme == t {
		themeMu.Unlock()
		return
	}
	appTheme = t
	attr := themeAttrOf(t)
	subs := make([]chan string, 0, len(themeSubs))
	for ch := range themeSubs {
		subs = append(subs, ch)
	}
	themeMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- attr:
		default: // 缓冲满了说明这个页面卡住了，丢一次没关系（下一次变化还会推）
		}
	}
}

// AppTheme 当前原始主题值（诊断/测试用）。
func AppTheme() string {
	themeMu.RLock()
	defer themeMu.RUnlock()
	return appTheme
}

// themeSubscribe 订阅主题变化，返回当前值 + 变更通道 + 取消函数。
func themeSubscribe() (string, chan string, func()) {
	themeMu.Lock()
	defer themeMu.Unlock()
	ch := make(chan string, 4)
	themeSubs[ch] = struct{}{}
	// 持锁中：必须用不加锁的 themeAttrOf，再去抢读锁就自死锁
	return themeAttrOf(appTheme), ch, func() {
		themeMu.Lock()
		if _, ok := themeSubs[ch]; ok {
			delete(themeSubs, ch)
			close(ch)
		}
		themeMu.Unlock()
	}
}

func init() {
	// webui 渲染时取这个钩子写进 <html data-theme>（webui 不认识配色名，
	// 由这边把映射函数挂上去，避免两个包互相 import 造成环）。
	webui.ThemeAttr = ThemeAttr
}

// handleThemeAPI 返回当前主题（SSE 建连失败时的兜底，也可供脚本主动查一次）。
func (s *Server) handleThemeAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"theme": ThemeAttr()})
}

// handleThemeStream 是 SSE：客户端用 EventSource 连着，主题一变就推一行。
//
// 注意三点：① 必须 Flush 才会真正发出去；② 中间若有反代要关缓冲；③ 空闲
// 15 秒发一行注释保活，否则浏览器/系统会当死连接掐掉（EventSource 会重连，
// 但重连那一小段里切换主题就漏了）。
func (s *Server) handleThemeStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	cur, ch, cancel := themeSubscribe()
	defer cancel()

	fmt.Fprintf(w, "retry: 3000\n\n")
	push := func(t string) bool {
		if _, err := fmt.Fprintf(w, "data: {\"theme\":%q}\n\n", t); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	if !push(cur) {
		return
	}

	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case t, ok := <-ch:
			if !ok {
				return
			}
			if !push(t) {
				return
			}
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
