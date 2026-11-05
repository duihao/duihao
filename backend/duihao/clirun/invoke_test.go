package clirun

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/duihao/duihao/backend/duihao/audit"
)

const phpstudyPHP = `D:/phpstudy_pro/Extensions/php/php8.2.9nts/php.exe`

// TestInvokePhpV 验收项：cli 通道跑 php -v（用本机 phpstudy 运行时，缺席则跳过）。
func TestInvokePhpV(t *testing.T) {
	if _, err := os.Stat(phpstudyPHP); err != nil {
		t.Skip("本机无 phpstudy php8.2.9nts，跳过")
	}
	// 审计器落在独立临时目录，避免污染真实数据根
	dir := t.TempDir()
	al := audit.New(filepath.Join(dir, "logs"))

	res, err := Invoke(context.Background(),
		CLISpec{ID: "php-test", Bin: phpstudyPHP},
		[]string{"-v"}, "", nil, 30*time.Second, al)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%s", res.ExitCode, res.Stderr)
	}
	if len(res.Stdout) < 3 || res.Stdout[:3] != "PHP" {
		t.Fatalf("stdout 应以 PHP 开头: %q", res.Stdout[:min(40, len(res.Stdout))])
	}

	// 审计留痕已写入
	entries, _ := os.ReadDir(filepath.Join(dir, "logs"))
	if len(entries) == 0 {
		t.Fatal("cli.invoke 未写审计日志")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestInvokeTimeoutCmd 用 cmd /c ping 实现可超时的慢命令（验证超时强杀不挂死）。
func TestInvokeTimeoutCmd(t *testing.T) {
	cmd := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	if _, err := os.Stat(cmd); err != nil {
		t.Skip("无 cmd.exe")
	}
	dir := t.TempDir()
	// cmd /c ping -n 6 127.0.0.1 → 约 5 秒；超时 600ms 应被杀
	_, err := Invoke(context.Background(), CLISpec{ID: "slow", Bin: cmd},
		[]string{"/c", "ping", "-n", "6", "127.0.0.1"}, "", nil, 600*time.Millisecond, audit.New(dir))
	if err == nil {
		t.Fatal("超时应报错")
	}
}
