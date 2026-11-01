package voice

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/store"
)

// freshDB 打开一个跑完迁移的临时库（与线上同一套表结构）。
func freshDB(t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("打开库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, st.DB()
}

func insertEvent(t *testing.T, db *sql.DB, uid string, ev event) {
	t.Helper()
	_, err := db.Exec(`insert into voice_events(uid,id,title,content,voice,tone,times,period,
		weekdays,interval_min,day_from,day_to,start_date,at_time,next_at,enabled,audio,audio_ms,
		action,action_arg,last_at,created_at,updated_at)
		values(?,?,?,?,?,?,?,?,'',60,'09:00','18:00','2026-01-01','23:59',?,1,'',0,'none','','',?,?)`,
		uid, ev.ID, ev.Title, ev.Content, ev.Voice, ev.Tone, ev.Times, ev.Period,
		ev.NextAt, time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("插入事件失败: %v", err)
	}
}

func logsOf(t *testing.T, db *sql.DB, uid string) []string {
	t.Helper()
	rows, err := db.Query(`select msg from voice_logs where uid=? order by at`, uid)
	if err != nil {
		t.Fatalf("读流水失败: %v", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var m string
		_ = rows.Scan(&m)
		out = append(out, m)
	}
	return out
}

// 到点（未过期）：写一条流水、推进 next_at、不算错过。
func TestSweepFires(t *testing.T) {
	_, db := freshDB(t)
	now := time.Now()
	insertEvent(t, db, "u1", event{
		ID: "e1", Title: "喝水提醒", Voice: "huihui-std", Tone: "warm",
		Times: 0, Period: "daily",
		NextAt: now.Add(-time.Minute).Format(timeLayout),
	})

	sweep(db, "", now)

	logs := logsOf(t, db, "u1")
	if len(logs) != 1 {
		t.Fatalf("应有 1 条流水, got %v", logs)
	}
	if !strings.Contains(logs[0], "只提醒不出声") {
		t.Fatalf("times=0 的说明不对: %q", logs[0])
	}

	var next string
	if err := db.QueryRow(`select next_at from voice_events where uid='u1' and id='e1'`).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if next == "" || next <= now.Format(timeLayout) {
		t.Fatalf("next_at 应被推进到未来, got %q", next)
	}
}

// 错过太久（>10 分钟）：只推进，不补播，流水里写明原因。
func TestSweepMissed(t *testing.T) {
	_, db := freshDB(t)
	now := time.Now()
	insertEvent(t, db, "u1", event{
		ID: "e2", Title: "旧提醒", Voice: "huihui-std", Tone: "normal",
		Times: 1, Period: "daily",
		NextAt: now.Add(-2 * time.Hour).Format(timeLayout),
	})

	sweep(db, "", now)

	logs := logsOf(t, db, "u1")
	if len(logs) != 1 || !strings.Contains(logs[0], "错过") {
		t.Fatalf("应记「错过」: %v", logs)
	}
	// 再过 15 秒扫一次，不应该再触发第二次
	sweep(db, "", now.Add(15*time.Second))
	if l := logsOf(t, db, "u1"); len(l) != 1 {
		t.Fatalf("不该重复触发: %v", l)
	}
}

// once 触发后 next_at 清空（列表显示「已结束」），且不再被扫到。
func TestSweepOnceDone(t *testing.T) {
	_, db := freshDB(t)
	now := time.Now()
	_, err := db.Exec(`insert into voice_events(uid,id,title,content,voice,tone,times,period,
		weekdays,interval_min,day_from,day_to,start_date,at_time,next_at,enabled,audio,audio_ms,
		action,action_arg,last_at,created_at,updated_at)
		values('u1','e3','一次性','','huihui-std','normal',0,'once','',60,'09:00','18:00',
		?,'00:01',?,1,'',0,'none','','','','')`,
		now.Format(dateLayout), now.Add(-time.Minute).Format(timeLayout))
	if err != nil {
		t.Fatal(err)
	}
	sweep(db, "", now)

	var next string
	var enabled int
	if err := db.QueryRow(`select next_at, enabled from voice_events where uid='u1' and id='e3'`).
		Scan(&next, &enabled); err != nil {
		t.Fatal(err)
	}
	if next != "" {
		t.Fatalf("单次触发后 next_at 应为空, got %q", next)
	}
	if enabled != 1 {
		t.Fatal("单次触发只是「结束」，不该自动停用")
	}

	sweep(db, "", now.Add(20*time.Second))
	if l := logsOf(t, db, "u1"); len(l) != 1 {
		t.Fatalf("已结束的不该再触发: %v", l)
	}
}

// 停用的事件不参与扫描。
func TestSweepSkipsDisabled(t *testing.T) {
	_, db := freshDB(t)
	now := time.Now()
	insertEvent(t, db, "u1", event{ID: "e4", Title: "停用的", Voice: "huihui-std", Tone: "normal",
		Times: 0, Period: "daily", NextAt: now.Add(-time.Minute).Format(timeLayout)})
	if _, err := db.Exec(`update voice_events set enabled=0 where uid='u1' and id='e4'`); err != nil {
		t.Fatal(err)
	}
	sweep(db, "", now)
	if l := logsOf(t, db, "u1"); len(l) != 0 {
		t.Fatalf("停用的不该触发: %v", l)
	}
}

// 到点动作白名单：外链前缀校验 + 文件必须存在。
func TestRunActionWhitelist(t *testing.T) {
	if err := runAction(event{Action: "url", ActionArg: "javascript:alert(1)"}); err == nil {
		t.Fatal("非 http(s) 应被拒")
	}
	if err := runAction(event{Action: "path", ActionArg: `Z:\不存在的文件.txt`}); err == nil {
		t.Fatal("不存在的文件应被拒")
	}
	if err := runAction(event{Action: "none"}); err != nil {
		t.Fatalf("none 不该报错: %v", err)
	}
}
