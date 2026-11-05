package voice

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
)

// 到点弹窗（V2）：声音之外再给一张卡片。
//
// 为什么弹窗不放在前端：播报是后端的事，窗口最小化 / 浏览器关掉 / 断网都照响；
// 弹窗必须和声音同一个时机、同一个判断（错过的不补），所以由调度器**发通知**，
// 宿主（对号主进程）负责把卡片摆到屏幕右上角。
//
// 依赖方向：voice 不认识窗口、不认识 wails，只发一个 Notice；主进程用
// SetNotifier 接管。Ack 是闭包 —— 里面已经绑好了 db 与事件 id，UI 只传
// "done"/"snooze"，既不用把 uid/id 暴露到页面，也不会在多实例下改错库。

// SnoozeDefault 「延迟」按钮默认推迟的分钟数。
const SnoozeDefault = 5

// NoticeAutoClose 卡片多久没人理自动收起（秒）。声音已经念过了，卡片只是
// 「我看见了」的确认入口，不该一直压在屏幕上。
const NoticeAutoClose = 45

// Notice 一条到点提醒的「视觉那一半」。
type Notice struct {
	Title     string // 标题（也用于头像下方/卡片主标题）
	Content   string // 正文，前端截两行
	SnoozeMin int    // 0 = 没有「延迟」按钮
	At        time.Time
	// Ack 由调度器闭包提供：action = "done"（我处理了）/ "snooze"（过会儿再提醒）。
	Ack func(action string, minutes int) error
}

var (
	notifyMu   sync.RWMutex
	notifySink func(Notice)
)

// SetNotifier 注册弹窗接管者（对号主进程启动时调用一次）。
// 没注册也能正常出声 —— 弹窗是增强，不是前提。
func SetNotifier(f func(Notice)) {
	notifyMu.Lock()
	notifySink = f
	notifyMu.Unlock()
}

// EmitTest 让页面上的「看看卡片」按钮弹一张预览卡（不写库、不动排期）。
// 返回 false 表示没有接管者（纯网页跑、没开对号桌面端），调用方据此提示。
func EmitTest(title, content string) bool {
	notifyMu.RLock()
	has := notifySink != nil
	notifyMu.RUnlock()
	if !has {
		return false
	}
	emitNotice(Notice{Title: title, Content: content, SnoozeMin: SnoozeDefault, At: time.Now()})
	return true
}

// emitNotice 发给接管者。回收 panic：弹窗把主进程带崩是绝对不能接受的。
func emitNotice(n Notice) {
	notifyMu.RLock()
	f := notifySink
	notifyMu.RUnlock()
	if f == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			Logf("voice: 弹窗失败（不影响播报）: %v", r)
		}
	}()
	f(n)
}

// ack 处理卡片上的两个按钮。
//
//	done   —— 只记一条流水（周期事件的下次时间在到点时已经排好了，不该被清掉；
//	          单次事件 next_at 本来就是空）
//	snooze —— 把 next_at 推到 now+minutes，到点会重新播报 + 重新弹卡片。
//	          单次事件因此「复活」一次，响完仍旧结束（nextDue 返回零值）。
func ack(db *sql.DB, uid, id, action string, minutes int) error {
	if db == nil {
		return fmt.Errorf("提醒数据还没准备好")
	}
	now := time.Now()
	switch action {
	case "done":
		return logAck(db, uid, id, now, "已标记完成")
	case "snooze":
		if minutes <= 0 {
			minutes = SnoozeDefault
		}
		if minutes > 24*60 {
			minutes = 24 * 60
		}
		next := now.Add(time.Duration(minutes) * time.Minute).Format(timeLayout)
		res, err := db.Exec(`update voice_events set next_at=?, updated_at=? where uid=? and id=?`,
			next, now.Format(time.RFC3339), uid, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("这条提醒已经不存在了")
		}
		return logAck(db, uid, id, now, fmt.Sprintf("已推迟 %d 分钟", minutes))
	}
	return fmt.Errorf("未知操作: %s", action)
}

func logAck(db *sql.DB, uid, id string, now time.Time, msg string) error {
	_, err := db.Exec(`insert into voice_logs(uid,id,event_id,title,at,ok,msg)
		values(?,?,?,?,?,?,?)`,
		uid, newID(), id, eventTitle(db, uid, id), now.Format(timeLayout), 1, msg)
	return err
}

// eventTitle 取标题给流水用（事件可能刚被删，取不到就留空）。
func eventTitle(db *sql.DB, uid, id string) string {
	var title string
	_ = db.QueryRow(`select title from voice_events where uid=? and id=?`, uid, id).Scan(&title)
	return title
}

// testNotice 「看看卡片」按钮：用当前编辑中的标题/正文弹一张预览卡。
// 不写库、不动排期；Ack 为 nil —— 点「完成 / 延迟」只会收起卡片。
func testNotice(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	_ = appkit.Decode(r, &in)
	title := strings.TrimSpace(in.Title)
	content := strings.TrimSpace(in.Content)
	if title == "" && content == "" {
		return appkit.Errorf(http.StatusBadRequest, "先写个标题或内容，才能看卡片效果")
	}
	shown := EmitTest(title, content)
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "shown": shown})
	return nil
}
