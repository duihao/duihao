package runtimepkg

import (
	"sync"
	"sync/atomic"
	"time"
)

// 取包进度：M14 之前下载是「黑盒」——244MB 的 MySQL 包在国内源上要几分钟，
// 界面只有「安装中」三个字，用户无法判断是在下、卡住、还是死了。
// 这里维护一份按插件 id 归档的最新快照，供 UI 轮询（GetRuntimeProgress）。
//
// 只做「最新态」，不做历史：进度条只需要当前值，历史留在审计日志里。

// Phase 取包阶段。
const (
	PhaseResolve  = "resolve"  // 定位来源（installed/bundled/mirror）
	PhaseConnect  = "connect"  // 已选源，正在建连
	PhaseDownload = "download" // 正在下载
	PhaseVerify   = "verify"   // 校验 SHA256 / 大小
	PhaseExtract  = "extract"  // 解压 + 落盘
	PhaseDone     = "done"     // 完成
	PhaseFail     = "fail"     // 失败
)

// Progress 一次取包的进度快照（JSON 直接给前端）。
type Progress struct {
	PluginID string `json:"pluginId"`
	Version  string `json:"version"`
	Phase    string `json:"phase"`
	Source   string `json:"source,omitempty"`
	URL      string `json:"url,omitempty"`
	// Received/Total 字节；Total<0 表示服务端没给 Content-Length（进度条改走不确定态）。
	Received int64   `json:"received"`
	Total    int64   `json:"total"`
	Percent  float64 `json:"percent"`
	// Speed 近期平均速度（字节/秒），滑动窗口，0 表示还没算出。
	Speed   int64  `json:"speed"`
	Attempt int    `json:"attempt,omitempty"` // 第几次尝试（源或重试）
	Message string `json:"message,omitempty"` // 面向用户的补充说明（如「已切换备用源」）
	// Active 为 false 表示这一轮已结束（done/fail），UI 可收起进度条。
	Active bool  `json:"active"`
	Start  int64 `json:"start"` // 起始时间（unix 毫秒），UI 算总耗时
	TS     int64 `json:"ts"`    // 本快照时间（unix 毫秒）
}

var (
	progMu   sync.Mutex
	progLast = map[string]*Progress{} // key: pluginID
	progHook atomic.Pointer[func(Progress)]
)

// SetProgressHook 注册进度回调（服务层用来 emit Wails 事件）；传 nil 取消。
func SetProgressHook(fn func(Progress)) {
	if fn == nil {
		progHook.Store(nil)
		return
	}
	progHook.Store(&fn)
}

func report(p Progress) {
	now := time.Now().UnixMilli()
	p.TS = now
	progMu.Lock()
	old := progLast[p.PluginID]
	if old == nil || old.Start == 0 || old.Phase == PhaseDone || old.Phase == PhaseFail {
		p.Start = now
	} else {
		p.Start = old.Start
	}
	if p.Total > 0 && p.Received >= 0 {
		p.Percent = float64(p.Received) / float64(p.Total) * 100
		if p.Percent > 100 {
			p.Percent = 100
		}
	}
	cp := p
	progLast[p.PluginID] = &cp
	progMu.Unlock()

	if h := progHook.Load(); h != nil {
		(*h)(cp)
	}
}

// Snapshot 取所有插件的当前进度（UI 轮询入口）。
func Snapshot() []Progress {
	progMu.Lock()
	defer progMu.Unlock()
	out := make([]Progress, 0, len(progLast))
	for _, p := range progLast {
		out = append(out, *p)
	}
	return out
}

// ProgressOf 取单个插件的最新进度（没有则 ok=false）。
func ProgressOf(pluginID string) (Progress, bool) {
	progMu.Lock()
	defer progMu.Unlock()
	p, ok := progLast[pluginID]
	if !ok {
		return Progress{}, false
	}
	return *p, true
}

// forgetProgress 清掉一个插件的进度（重装前调用，避免残留上一轮的 done）。
func forgetProgress(pluginID string) {
	progMu.Lock()
	delete(progLast, pluginID)
	progMu.Unlock()
}

// movingAvg 速度滑动平均：新值占 3 成，避免瞬间抖动让速度数字乱跳。
func movingAvg(prev, cur int64) int64 {
	if prev <= 0 {
		return cur
	}
	if cur < 0 {
		cur = 0
	}
	return (prev*7 + cur*3) / 10
}
