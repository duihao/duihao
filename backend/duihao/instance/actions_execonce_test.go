package instance

import (
	"strings"
	"testing"

	"github.com/duihao/duihao/backend/duihao/audit"
	"github.com/duihao/duihao/backend/duihao/pluginc"
)

// TestExecOnceGuardAcceptsForwardSlash 回归：applyTokens 把 {bin} 渲染成
// 正斜杠路径，守卫必须归一后比对（MySQL 首装被拒的真实 bug）。
// 守卫只做路径前缀判定、不 stat 文件 —— 用指向不存在 exe 的运行时内路径
// 即可验证「放行」（放行后 execOnce 报 file not found，而非守卫拒绝）。
func TestExecOnceGuardAcceptsForwardSlash(t *testing.T) {
	rt := t.TempDir() // 原生反斜杠运行时目录
	dir := t.TempDir()

	v := Vars{
		ID: "t-1", Dir: dir, RuntimeDir: rt,
		Bin:   rt + `\bin\nosuch.exe`,
		Ports: map[string]int{},
	}
	acts := []pluginc.Action{{
		Action: "exec-once",
		Cmd:    "{bin}", // 渲染后变正斜杠 —— 正是被拒的形态
		Args:   []string{},
	}}
	err := RunActions(acts, v, nil, audit.New(t.TempDir()))
	if err == nil {
		t.Fatal("预期 execOnce 找不到文件报错，实际为 nil")
	}
	if strings.Contains(err.Error(), "只允许执行运行时二进制") {
		t.Fatalf("守卫误拒合法 {bin}: %v", err)
	}
	t.Logf("guard passed, exec result: %v", err)
}

// TestExecOnceGuardRejectsOutside 守卫仍须拒绝运行时目录外的命令。
func TestExecOnceGuardRejectsOutside(t *testing.T) {
	outside := `C:\Windows\System32\cmd.exe`
	v := Vars{
		ID: "t-2", Dir: t.TempDir(), RuntimeDir: t.TempDir(),
		Bin: `x\y`, Ports: map[string]int{},
	}
	acts := []pluginc.Action{{Action: "exec-once", Cmd: outside, Args: []string{}}}
	err := RunActions(acts, v, nil, audit.New(t.TempDir()))
	if err == nil || !strings.Contains(err.Error(), "只允许执行运行时二进制") {
		t.Fatalf("运行时外命令应被拒，得到: %v", err)
	}
}
