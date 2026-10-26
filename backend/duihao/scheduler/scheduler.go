// Package scheduler 定时任务（M23）：库备份、Redis 备份、日志切割。
//
// 桌面端不是常驻服务器：任务调度必须能「补跑」。做法是每次判定「最近一次应该
// 跑的时间点」是否晚于上次实际执行时间——对号关了一夜，第二天打开会立刻补一次
// 03:30 的备份，而不是白等一天。
//
// 任务存 config/tasks.json（整表替换，与站点/实例注册表同构）。
// 只做三件事，且都走已有实现：mysqldump / redis BGSAVE / 文件改名。
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	duihao "github.com/ys-ll/uniterm/backend/duihao"
	"github.com/ys-ll/uniterm/backend/duihao/audit"
)

// 任务类型。
const (
	KindMySQLBackup = "backup-mysql"
	KindRedisBackup = "backup-redis"
	KindRotateLogs  = "rotate-logs"
)

// Task 一条定时任务。
type Task struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Instance string `json:"instance,omitempty"` // 空 = 该类型全部实例
	// Schedule 调度表达式：daily:03:30 | hourly:30 | every:120（分钟）
	Schedule string `json:"schedule"`
	Keep     int    `json:"keep,omitempty"` // 保留天数（0 = 不清理旧产物）
	Enabled  bool   `json:"enabled"`

	LastRun    string `json:"lastRun,omitempty"`
	LastResult string `json:"lastResult,omitempty"` // "ok" 或错误摘要
	NextRun    string `json:"nextRun,omitempty"`    // 只读，列表回显用
}

// Actions 调度器需要的外部能力（由 main 注入，避免 scheduler 依赖 service）。
type Actions struct {
	MySQLDatabases func(instanceID string) ([]string, error)
	MySQLBackup    func(instanceID, db string) (string, error)
	RedisBackup    func(instanceID string) (string, error)
	// Instances 返回某插件的全部实例 id（"mysql" / "redis"），用于 instance 为空时。
	Instances func(pluginID string) []string
	// ReloadWeb 重开 nginx 日志文件（切割后 nginx 还握着旧 fd，必须 reload）。
	ReloadWeb func() error
}

type file struct {
	SchemaVersion int     `json:"schemaVersion"`
	Tasks         []*Task `json:"tasks"`
}

// Manager 调度器。
type Manager struct {
	path  string
	acts  Actions
	audit *audit.Logger

	mu    sync.Mutex
	tasks []*Task

	stop chan struct{}
	once sync.Once
}

// NewManager 加载任务表（文件不存在 = 空表）。
func NewManager(acts Actions, a *audit.Logger) (*Manager, error) {
	m := &Manager{
		path:  filepath.Join(duihao.DirConfig(), "tasks.json"),
		acts:  acts,
		audit: a,
		stop:  make(chan struct{}),
	}
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("tasks.json 解析失败: %w", err)
	}
	m.tasks = f.Tasks
	return m, nil
}

// Start 启动后台调度循环（20s 一次；幂等）。
func (m *Manager) Start() {
	m.once.Do(func() {
		go m.loop()
	})
}

// Stop 结束调度循环。
func (m *Manager) Stop() {
	select {
	case <-m.stop:
	default:
		close(m.stop)
	}
}

func (m *Manager) loop() {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	// 启动时先跑一遍：关了一夜的任务要立刻补上
	m.tick()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			m.tick()
		}
	}
}

// tick 检查所有任务，到点就跑。
func (m *Manager) tick() {
	now := time.Now()
	m.mu.Lock()
	snapshot := make([]*Task, len(m.tasks))
	copy(snapshot, m.tasks)
	m.mu.Unlock()

	for _, t := range snapshot {
		if !t.Enabled {
			continue
		}
		prev, ok := prevOccurrence(t.Schedule, now)
		if !ok {
			continue
		}
		last := parseTime(t.LastRun)
		if last.IsZero() {
			// 新建任务：先记基线，等下一个时间点，不立刻跑
			m.markRun(t.ID, now, "ok：已排定，等待首个执行点")
			continue
		}
		if !prev.After(last) {
			continue
		}
		msg := m.run(t)
		m.markRun(t.ID, now, msg)
	}
}

// RunNow 手动执行一次。
func (m *Manager) RunNow(id string) (string, error) {
	m.mu.Lock()
	var target *Task
	for _, t := range m.tasks {
		if t.ID == id {
			cp := *t
			target = &cp
			break
		}
	}
	m.mu.Unlock()
	if target == nil {
		return "", fmt.Errorf("任务不存在: %s", id)
	}
	msg := m.run(target)
	m.markRun(id, time.Now(), msg)
	if strings.HasPrefix(msg, "ok") {
		return msg, nil
	}
	return msg, fmt.Errorf("%s", msg)
}

// List 任务列表（含下次执行时间）。
func (m *Manager) List() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Task, 0, len(m.tasks))
	now := time.Now()
	for _, t := range m.tasks {
		cp := *t
		if next, ok := nextOccurrence(cp.Schedule, now); ok {
			cp.NextRun = next.Format("2006-01-02 15:04")
		}
		out = append(out, cp)
	}
	return out
}

// Save 整表替换（前端提交整份任务表）。
func (m *Manager) Save(tasks []Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	out := make([]*Task, 0, len(tasks))
	for i := range tasks {
		t := tasks[i]
		t.Schedule = strings.TrimSpace(t.Schedule)
		if _, ok := parseSchedule(t.Schedule); !ok {
			return fmt.Errorf("第 %d 条任务的调度表达式无法识别：%q（示例 daily:03:30 / hourly:30 / every:120）", i+1, t.Schedule)
		}
		if t.Kind != KindMySQLBackup && t.Kind != KindRedisBackup && t.Kind != KindRotateLogs {
			return fmt.Errorf("第 %d 条任务类型未知：%s", i+1, t.Kind)
		}
		if t.ID == "" {
			t.ID = fmt.Sprintf("task-%d", time.Now().UnixNano())
		}
		if seen[t.ID] {
			return fmt.Errorf("任务 id 重复: %s", t.ID)
		}
		seen[t.ID] = true
		if t.Keep < 0 {
			t.Keep = 0
		}
		cp := t
		out = append(out, &cp)
	}
	m.tasks = out
	return m.saveLocked()
}

func (m *Manager) saveLocked() error {
	data, err := json.MarshalIndent(file{SchemaVersion: 1, Tasks: m.tasks}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func (m *Manager) markRun(id string, at time.Time, result string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		if t.ID == id {
			t.LastRun = at.Format(time.RFC3339)
			t.LastResult = result
			break
		}
	}
	_ = m.saveLocked()
}

// run 执行一条任务，返回结果文案（"ok ……" 或错误摘要）。
func (m *Manager) run(t *Task) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	_ = ctx

	switch t.Kind {
	case KindMySQLBackup:
		ids := m.targetInstances(t, "mysql")
		if len(ids) == 0 {
			return "跳过：没有可备份的 MySQL 实例"
		}
		n := 0
		for _, id := range ids {
			dbs, err := m.acts.MySQLDatabases(id)
			if err != nil {
				return "失败：" + err.Error()
			}
			for _, db := range dbs {
				if _, err := m.acts.MySQLBackup(id, db); err != nil {
					return fmt.Sprintf("失败：%s 的库 %s —— %v", id, db, err)
				}
				n++
			}
		}
		pruned := pruneDir(filepath.Join(duihao.DirBackup(), "mysql"), t.Keep)
		m.ok("scheduler.mysql-backup", n, pruned)
		return fmt.Sprintf("ok：备份 %d 个库，清理 %d 个旧文件", n, pruned)

	case KindRedisBackup:
		ids := m.targetInstances(t, "redis")
		if len(ids) == 0 {
			return "跳过：没有可备份的 Redis 实例"
		}
		n := 0
		for _, id := range ids {
			if _, err := m.acts.RedisBackup(id); err != nil {
				return "失败：" + err.Error()
			}
			n++
		}
		pruned := pruneDir(filepath.Join(duihao.DirBackup(), "redis"), t.Keep)
		m.ok("scheduler.redis-backup", n, pruned)
		return fmt.Sprintf("ok：备份 %d 个 Redis 实例，清理 %d 个旧文件", n, pruned)

	case KindRotateLogs:
		rotated, pruned := m.rotateLogs(t.Keep)
		m.ok("scheduler.rotate-logs", rotated, pruned)
		return fmt.Sprintf("ok：切割 %d 个日志，清理 %d 个旧文件", rotated, pruned)
	}
	return "跳过：未知任务类型"
}

func (m *Manager) ok(action string, a, b int) {
	if m.audit != nil {
		m.audit.Ok("scheduler", action, "auto", map[string]any{"count": a, "pruned": b})
	}
}

func (m *Manager) targetInstances(t *Task, plugin string) []string {
	if t.Instance != "" {
		return []string{t.Instance}
	}
	if m.acts.Instances == nil {
		return nil
	}
	return m.acts.Instances(plugin)
}

// rotateLogs 切割对号自己管的日志文件（进程日志 + 站点 access/error）。
//
// 命名：<name>.log → <name>.YYYYMMDD.log。同一天重复触发不会二次切割。
func (m *Manager) rotateLogs(keepDays int) (int, int) {
	var targets []string
	targets = append(targets, globLogs(duihao.DirProcessLogs())...)
	instRoot := duihao.DirInstances()
	if entries, err := os.ReadDir(instRoot); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(instRoot, e.Name(), "logs")
			targets = append(targets, globLogs(dir)...)
			targets = append(targets, globLogs(filepath.Join(dir, "sites"))...)
		}
	}
	today := time.Now().Format("20060102")
	rotated := 0
	needReload := false
	for _, p := range targets {
		st, err := os.Stat(p)
		if err != nil || st.Size() == 0 {
			continue
		}
		dst := strings.TrimSuffix(p, ".log") + "." + today + ".log"
		if _, err := os.Stat(dst); err == nil {
			continue // 今天已经切过
		}
		if err := os.Rename(p, dst); err != nil {
			continue
		}
		rotated++
		if strings.Contains(filepath.ToSlash(p), "/sites/") {
			needReload = true
		}
	}
	// 站点日志被改名后 nginx 仍握着旧文件句柄，不 reload 会一直写进改名后的文件
	if needReload && m.acts.ReloadWeb != nil {
		_ = m.acts.ReloadWeb()
	}
	pruned := pruneDir(duihao.DirLogs(), keepDays)
	return rotated, pruned
}

func globLogs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".log") {
			continue
		}
		// 已切割过的（xxx.20260101.log）不再参与
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if len(base) >= 8 && isAllDigits(base[len(base)-8:]) {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	return out
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// pruneDir 删除 root 下修改时间早于 keepDays 天的**已切割/备份**文件。
// 只删「带日期后缀」的文件，绝不动当前正在写的 .log。
func pruneDir(root string, keepDays int) int {
	if keepDays <= 0 {
		return 0
	}
	cutoff := time.Now().AddDate(0, 0, -keepDays)
	n := 0
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.ModTime().After(cutoff) {
			return nil
		}
		name := info.Name()
		// 只清理「日志/备份」类产物
		lower := strings.ToLower(name)
		isLog := strings.HasSuffix(lower, ".log") || strings.HasSuffix(lower, ".rdb")
		isSQL := strings.HasSuffix(lower, ".sql") || strings.HasSuffix(lower, ".zip")
		if !isLog && !isSQL {
			return nil
		}
		if err := os.Remove(path); err == nil {
			n++
		}
		return nil
	})
	return n
}

// ---------- 调度表达式 ----------

type sched struct {
	kind  string // daily | hourly | every
	hour  int
	min   int
	every int // 分钟
}

func parseSchedule(s string) (sched, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	switch {
	case strings.HasPrefix(s, "daily:"):
		hm := strings.Split(strings.TrimPrefix(s, "daily:"), ":")
		if len(hm) != 2 {
			return sched{}, false
		}
		h, err1 := strconv.Atoi(hm[0])
		m, err2 := strconv.Atoi(hm[1])
		if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
			return sched{}, false
		}
		return sched{kind: "daily", hour: h, min: m}, true
	case strings.HasPrefix(s, "hourly:"):
		m, err := strconv.Atoi(strings.TrimPrefix(s, "hourly:"))
		if err != nil || m < 0 || m > 59 {
			return sched{}, false
		}
		return sched{kind: "hourly", min: m}, true
	case strings.HasPrefix(s, "every:"):
		n, err := strconv.Atoi(strings.TrimPrefix(s, "every:"))
		if err != nil || n < 5 {
			return sched{}, false
		}
		return sched{kind: "every", every: n}, true
	}
	return sched{}, false
}

// nextOccurrence 下一次应该执行的时间（严格晚于 from）。
func nextOccurrence(expr string, from time.Time) (time.Time, bool) {
	s, ok := parseSchedule(expr)
	if !ok {
		return time.Time{}, false
	}
	switch s.kind {
	case "daily":
		t := time.Date(from.Year(), from.Month(), from.Day(), s.hour, s.min, 0, 0, from.Location())
		if !t.After(from) {
			t = t.AddDate(0, 0, 1)
		}
		return t, true
	case "hourly":
		t := time.Date(from.Year(), from.Month(), from.Day(), from.Hour(), s.min, 0, 0, from.Location())
		if !t.After(from) {
			t = t.Add(time.Hour)
		}
		return t, true
	case "every":
		return from.Add(time.Duration(s.every) * time.Minute), true
	}
	return time.Time{}, false
}

// prevOccurrence 最近一次「应该已执行」的时间点（不晚于 from）。
func prevOccurrence(expr string, from time.Time) (time.Time, bool) {
	s, ok := parseSchedule(expr)
	if !ok {
		return time.Time{}, false
	}
	switch s.kind {
	case "daily":
		t := time.Date(from.Year(), from.Month(), from.Day(), s.hour, s.min, 0, 0, from.Location())
		if t.After(from) {
			t = t.AddDate(0, 0, -1)
		}
		return t, true
	case "hourly":
		t := time.Date(from.Year(), from.Month(), from.Day(), from.Hour(), s.min, 0, 0, from.Location())
		if t.After(from) {
			t = t.Add(-time.Hour)
		}
		return t, true
	case "every":
		// 每 N 分钟：按 from 向下取整到 N 分钟网格
		step := time.Duration(s.every) * time.Minute
		base := from.Truncate(time.Hour)
		for base.Add(step).Before(from) || base.Add(step).Equal(from) {
			base = base.Add(step)
		}
		return base, true
	}
	return time.Time{}, false
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// SortByID 稳定排序（列表展示用）。
func SortByID(tasks []Task) {
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
}
