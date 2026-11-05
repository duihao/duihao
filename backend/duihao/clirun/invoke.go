// Package clirun 实现 cli 类插件的调度通道（方案 §4.1）。
//
// cli = 外部二进制按需执行，跑完即退：无守护、无端口、无探活。
// invoke(cliId, args, cwd, env) → 捕获输出 → 写审计。composer / wp-cli / mysqldump 走这里。
package clirun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/duihao/duihao/backend/duihao/audit"
)

// CLISpec 一次 cli 调用的完整描述（entry 模板已由上层渲染为 Bin+Prefix）。
type CLISpec struct {
	ID     string   `json:"id"`              // cli 插件 id（审计用）
	Bin    string   `json:"bin"`             // 可执行文件绝对路径（如 <runtime>/php.exe）
	Prefix []string `json:"prefix,omitempty"` // 固定前置参数（如 wp-cli.phar）
}

// Result 调用结果。
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
}

// Invoke 执行 cli 并捕获输出。cwd 空则用当前目录；env 为追加环境变量。
// 超时默认 10 分钟（composer 类大操作；mysqldump 大库可由调用方覆盖）。
func Invoke(ctx context.Context, spec CLISpec, args []string, cwd string, env []string, timeout time.Duration, auditLog *audit.Logger) (Result, error) {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	if spec.Bin == "" {
		return Result{}, errors.New("cli 可执行文件未指定")
	}
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	argv := append(append([]string{}, spec.Prefix...), args...)
	start := time.Now()
	cmd := exec.CommandContext(c, spec.Bin, argv...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} // GUI 宿主下不弹控制台黑窗
	if cwd != "" {
		cmd.Dir = cwd
	}
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	res := Result{
		ExitCode: cmd.ProcessState.ExitCode(),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
	}

	detail := map[string]any{
		"args":     args,
		"cwd":      cwd,
		"exitCode": res.ExitCode,
		"duration": res.Duration.String(),
	}
	if runErr != nil {
		detail["err"] = runErr.Error()
		auditLog.Fail("clirun", "cli.invoke", spec.ID, detail)
		return res, fmt.Errorf("cli %s 执行失败: %w", spec.ID, runErr)
	}
	auditLog.Ok("clirun", "cli.invoke", spec.ID, detail)
	return res, nil
}
