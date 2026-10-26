// Package audit 实现对号审计日志（方案铁律 4：所有执行留痕）。
// 格式：logs\audit-YYYYMMDD.jsonl，每行一个 JSON 对象，按本地日期滚动，追加写。
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Logger 并发安全的 JSONL 审计写入口。
type Logger struct {
	dir string
	mu  sync.Mutex
}

// New 创建指向 dir（通常为 logs\）的审计器。
func New(dir string) *Logger { return &Logger{dir: dir} }

// Entry 单条审计记录。
type Entry struct {
	TS     string         `json:"ts"`               // RFC3339Nano 本地时间
	Actor  string         `json:"actor"`            // 动作发起方：app / ui / cli / ai / testhost
	Action string         `json:"action"`           // resolve.runtime / instance.start / cli.invoke / ...
	Target string         `json:"target,omitempty"` // 对象：插件 id、实例 id、cli id
	Result string         `json:"result"`           // ok / fail
	Detail map[string]any `json:"detail,omitempty"`
}

// Log 写入一条审计记录；返回错误仅表示写日志失败（调用方一般忽略或仅打日志）。
func (l *Logger) Log(actor, action, target, result string, detail map[string]any) error {
	if l == nil { // 审计器可选：nil 安全，避免调用方到处判空
		return nil
	}
	e := Entry{
		TS:     time.Now().Format(time.RFC3339Nano),
		Actor:  actor,
		Action: action,
		Target: target,
		Result: result,
		Detail: detail,
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	path := filepath.Join(l.dir, fmt.Sprintf("audit-%s.jsonl", time.Now().Format("20060102")))
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// Ok / Fail 是 Log 的便捷封装。
func (l *Logger) Ok(actor, action, target string, detail map[string]any) {
	_ = l.Log(actor, action, target, "ok", detail)
}

func (l *Logger) Fail(actor, action, target string, detail map[string]any) {
	_ = l.Log(actor, action, target, "fail", detail)
}
