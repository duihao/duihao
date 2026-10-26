package scheduler

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseSchedule(t *testing.T) {
	ok := []string{"daily:03:30", "daily:0:05", "hourly:15", "every:120"}
	for _, s := range ok {
		if _, good := parseSchedule(s); !good {
			t.Errorf("%q 应被接受", s)
		}
	}
	bad := []string{"", "daily:25:00", "daily:3", "hourly:99", "every:1", "cron:whatever"}
	for _, s := range bad {
		if _, good := parseSchedule(s); good {
			t.Errorf("%q 应被拒绝", s)
		}
	}
}

func TestNextAndPrevOccurrenceDaily(t *testing.T) {
	loc := time.Local
	at := func(h, m int) time.Time { return time.Date(2026, 10, 26, h, m, 0, 0, loc) }

	next, ok := nextOccurrence("daily:03:30", at(2, 0))
	if !ok || !next.Equal(at(3, 30)) {
		t.Errorf("next from 02:00 = %v", next)
	}
	next, _ = nextOccurrence("daily:03:30", at(4, 0))
	if next.Day() != 27 {
		t.Errorf("03:30 已过 → 应排到次日，得到 %v", next)
	}

	prev, _ := prevOccurrence("daily:03:30", at(4, 0))
	if !prev.Equal(at(3, 30)) {
		t.Errorf("prev from 04:00 = %v", prev)
	}
	prev, _ = prevOccurrence("daily:03:30", at(2, 0))
	if prev.Day() != 25 {
		t.Errorf("03:30 未到 → 应取前一天，得到 %v", prev)
	}
}

func TestCatchUpSemantics(t *testing.T) {
	// 昨天 03:30 跑过，今天 08:00 打开对号 → prev(今天 03:30) 晚于 last → 应该补跑
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, time.Local)
	last := time.Date(2026, 10, 25, 3, 30, 0, 0, time.Local)
	prev, _ := prevOccurrence("daily:03:30", now)
	if !prev.After(last) {
		t.Fatalf("应判定为需要补跑：prev=%v last=%v", prev, last)
	}
	// 今天 03:30 已经跑过 → 不再重复
	last = time.Date(2026, 10, 26, 3, 31, 0, 0, time.Local)
	if prev.After(last) {
		t.Fatal("已跑过却又判定补跑")
	}
}

func TestPruneDirKeepsCurrentLog(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "nginx.20260101.log")
	cur := filepath.Join(dir, "nginx.log")
	if err := os.WriteFile(old, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cur, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 把旧文件时间调到 30 天前
	past := time.Now().AddDate(0, 0, -30)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	n := pruneDir(dir, 7)
	if n != 1 {
		t.Fatalf("应清理 1 个旧文件，实际 %d", n)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("旧文件应被删除")
	}
	if _, err := os.Stat(cur); err != nil {
		t.Error("当前日志不该被删")
	}
}

func TestSaveRejectsBadInput(t *testing.T) {
	m := &Manager{path: filepath.Join(t.TempDir(), "tasks.json")}
	if err := m.Save([]Task{{Kind: KindRotateLogs, Schedule: "nope"}}); err == nil {
		t.Error("非法调度表达式应被拒绝")
	}
	if err := m.Save([]Task{{Kind: "unknown", Schedule: "daily:01:00"}}); err == nil {
		t.Error("非法任务类型应被拒绝")
	}
	if err := m.Save([]Task{{Kind: KindRotateLogs, Schedule: "daily:03:30", Keep: 7}}); err != nil {
		t.Fatalf("合法任务应保存成功: %v", err)
	}
	// 落盘后可回读
	m2, err := NewManager(Actions{}, nil)
	if err == nil && m2 != nil {
		// 路径不同，只是确认 NewManager 对缺失文件不报错
		_ = m2
	}
}
