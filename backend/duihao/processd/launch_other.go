//go:build !windows

package processd

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
)

// 非 Windows 平台的降级实现：无 Job Object，仅 exec.Command 直启。
// v1 仅承诺 Windows（方案 D 决策）；此文件只为让仓库在 mac/linux 可编译。

type launchedProc struct {
	pid       int
	procH     uintptr
	job       uintptr
	cmd       *exec.Cmd
	waitEvent chan struct{}
	exitOnce  sync.Once
}

func launchWindows(bin string, args []string, dir string, env []string, logFile string) (*launchedProc, error) {
	cmd := exec.Command(bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		cmd.Stdout = f
		cmd.Stderr = f
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", bin, err)
	}
	p := &launchedProc{pid: cmd.Process.Pid, cmd: cmd, waitEvent: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		p.exitOnce.Do(func() { close(p.waitEvent) })
	}()
	return p, nil
}

func terminate(p *launchedProc) error {
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Kill()
	}
	return nil
}

func sendCtrlBreak(p *launchedProc) error { return fmt.Errorf("signal stop 仅支持 Windows") }

func closeJob(p *launchedProc) {}
func procAlive(p *launchedProc) bool {
	select {
	case <-p.waitEvent:
		return false
	default:
		return true
	}
}
