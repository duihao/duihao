package voice

import (
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"
)

// 弹窗的 Go 侧：到点发通知、按钮回调改库。窗口本身没法在单测里开，
// 所以这里钉的是「发不发」「发的是什么」「按钮改了什么」。

// 到点未过期 → 发通知，卡片带标题/正文/延迟分钟数 + 可用的 Ack。
func TestFireEmitsNotice(t *testing.T) {
	_, db := freshDB(t)
	now := time.Now()
	insertEvent(t, db, "u1", event{
		ID: "e1", Title: "喝水提醒", Content: "宝贝，该喝水了", Times: 0,
		Period: "daily", NextAt: now.Add(-time.Minute).Format(timeLayout),
	})

	var got []Notice
	SetNotifier(func(n Notice) { got = append(got, n) })
	t.Cleanup(func() { SetNotifier(nil) })

	sweep(db, "", now)

	if len(got) != 1 {
		t.Fatalf("应该弹 1 张卡片，实际 %d", len(got))
	}
	n := got[0]
	if n.Title != "喝水提醒" || n.Content != "宝贝，该喝水了" {
		t.Fatalf("卡片内容不对: %+v", n)
	}
	if n.SnoozeMin != SnoozeDefault {
		t.Fatalf("延迟分钟数应为默认 %d，实际 %d", SnoozeDefault, n.SnoozeMin)
	}
	if n.Ack == nil {
		t.Fatal("Ack 闭包不能为空 —— 否则卡片上的按钮点了没反应")
	}
}

// 错过的（>10 分钟）只推进时间，不弹卡片：否则一开机连弹十几张旧提醒。
func TestFireSkipsNoticeWhenLate(t *testing.T) {
	_, db := freshDB(t)
	now := time.Now()
	insertEvent(t, db, "u1", event{
		ID: "e1", Title: "喝水提醒", Times: 1, Period: "daily",
		NextAt: now.Add(-30 * time.Minute).Format(timeLayout),
	})

	var got int
	SetNotifier(func(Notice) { got++ })
	t.Cleanup(func() { SetNotifier(nil) })

	sweep(db, "", now)

	if got != 0 {
		t.Fatalf("错过的提醒不该弹卡片，实际弹了 %d 张", got)
	}
}

// 没注册接管者（比如纯 headless 跑）不能崩，也不能卡住播报。
func TestFireWithoutNotifier(t *testing.T) {
	_, db := freshDB(t)
	SetNotifier(nil)
	now := time.Now()
	insertEvent(t, db, "u1", event{
		ID: "e1", Title: "x", Times: 0, Period: "daily",
		NextAt: now.Add(-time.Minute).Format(timeLayout),
	})
	sweep(db, "", now) // 不 panic 即通过
}

// 弹窗实现里 panic 不能带崩主进程（提示音已经响了，卡片挂了就挂了）。
func TestEmitNoticeRecoversPanic(t *testing.T) {
	SetNotifier(func(Notice) { panic("boom") })
	t.Cleanup(func() { SetNotifier(nil) })
	emitNotice(Notice{Title: "x"}) // 不 panic 即通过
}

func TestAckDone(t *testing.T) {
	_, db := freshDB(t)
	insertEvent(t, db, "u1", event{ID: "e1", Title: "喝水", Times: 1, Period: "daily", NextAt: "2026-10-30 09:00"})

	if err := ack(db, "u1", "e1", "done", 0); err != nil {
		t.Fatalf("ack done 失败: %v", err)
	}
	// 「完成」只记流水，不该动排期（周期事件的下次时间到点时就排好了）
	if got := nextAtOf(t, db, "u1", "e1"); got != "2026-10-30 09:00" {
		t.Fatalf("done 不该改 next_at，实际 %s", got)
	}
	if logs := logsOf(t, db, "u1"); len(logs) != 1 || !strings.Contains(logs[0], "完成") {
		t.Fatalf("应写一条完成流水，实际 %v", logs)
	}
}

func TestAckSnooze(t *testing.T) {
	_, db := freshDB(t)
	insertEvent(t, db, "u1", event{ID: "e1", Title: "喝水", Times: 1, Period: "once", NextAt: ""})

	before := time.Now()
	if err := ack(db, "u1", "e1", "snooze", 5); err != nil {
		t.Fatalf("ack snooze 失败: %v", err)
	}
	next, err := time.ParseInLocation(timeLayout, nextAtOf(t, db, "u1", "e1"), time.Local)
	if err != nil {
		t.Fatalf("snooze 后的 next_at 不是合法时间: %v", err)
	}
	// 允许 1 分钟的粒度误差（next_at 只存到分钟）
	d := next.Sub(before)
	if d < 4*time.Minute || d > 6*time.Minute {
		t.Fatalf("snooze 5 分钟后应约 5 分钟，实际 %v", d)
	}
	if logs := logsOf(t, db, "u1"); len(logs) != 1 || !strings.Contains(logs[0], "推迟 5 分钟") {
		t.Fatalf("应写一条推迟流水，实际 %v", logs)
	}
}

// 分钟数夹在 1~1440：前端传 0 或负数走默认，传一年也只推迟一天。
func TestAckSnoozeClamps(t *testing.T) {
	_, db := freshDB(t)
	insertEvent(t, db, "u1", event{ID: "e1", Title: "喝水", Period: "once"})
	before := time.Now()

	if err := ack(db, "u1", "e1", "snooze", 0); err != nil {
		t.Fatalf("ack 失败: %v", err)
	}
	next, _ := time.ParseInLocation(timeLayout, nextAtOf(t, db, "u1", "e1"), time.Local)
	if d := next.Sub(before); d < 4*time.Minute || d > 6*time.Minute {
		t.Fatalf("0 应回落默认 5 分钟，实际 %v", d)
	}

	if err := ack(db, "u1", "e1", "snooze", 100000); err != nil {
		t.Fatalf("ack 失败: %v", err)
	}
	next, _ = time.ParseInLocation(timeLayout, nextAtOf(t, db, "u1", "e1"), time.Local)
	if d := next.Sub(before); d > 25*time.Hour {
		t.Fatalf("应夹到 24 小时以内，实际 %v", d)
	}
}

func TestAckUnknownAction(t *testing.T) {
	_, db := freshDB(t)
	insertEvent(t, db, "u1", event{ID: "e1", Title: "喝水", Period: "once"})
	if err := ack(db, "u1", "e1", "delete-everything", 0); err == nil {
		t.Fatal("未知动作必须报错（加字段不扩权）")
	}
}

// 事件已被删掉的卡片：推迟要给出可读错误，而不是静默成功。
func TestAckSnoozeMissingEvent(t *testing.T) {
	_, db := freshDB(t)
	if err := ack(db, "u1", "gone", "snooze", 5); err == nil {
		t.Fatal("事件不存在时应报错")
	}
}

// 并发：调度器和 UI 可能同时碰 notifier，不能 data race（go test -race 会抓）。
func TestSetNotifierConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); SetNotifier(func(Notice) {}) }()
		go func() { defer wg.Done(); emitNotice(Notice{Title: "x"}) }()
	}
	wg.Wait()
	SetNotifier(nil)
}

func nextAtOf(t *testing.T, db *sql.DB, uid, id string) string {
	t.Helper()
	var next string
	if err := db.QueryRow(`select next_at from voice_events where uid=? and id=?`, uid, id).Scan(&next); err != nil {
		t.Fatalf("读 next_at 失败: %v", err)
	}
	return next
}
