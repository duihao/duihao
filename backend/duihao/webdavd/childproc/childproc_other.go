//go:build !windows

// Package childproc 受管子进程（非 Windows 对齐实现）。
//
// Windows 那套 CREATE_NO_WINDOW / Job Object 在别的平台没有对应物，
// 这里退化成普通 exec.Cmd：Stop 直接 Kill 整组（Setsid 后杀进程组）。
// 对号 v1 只发 Windows 包，这份实现是为了让 go vet / 单测能在别的平台跑。
package childproc

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
)

// Proc 一个受管子进程。
type Proc struct {
	cmd  *exec.Cmd
	done atomic.Bool
}

// Options 启动选项。
type Options struct {
	LogPath    string
	StdoutPipe bool
}

// Start 启动子进程。
func Start(bin string, args []string, opt Options) (*Proc, io.ReadCloser, error) {
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	var logf *os.File
	if opt.LogPath != "" {
		if err := os.MkdirAll(filepath.Dir(opt.LogPath), 0o755); err == nil {
			logf, _ = os.OpenFile(opt.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		}
	}
	if logf != nil {
		cmd.Stderr = logf
		if !opt.StdoutPipe {
			cmd.Stdout = logf
		}
	}

	var stdout io.ReadCloser
	if opt.StdoutPipe {
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			if logf != nil {
				logf.Close()
			}
			return nil, nil, err
		}
		stdout = pipe
	}

	if err := cmd.Start(); err != nil {
		if logf != nil {
			logf.Close()
		}
		return nil, nil, fmt.Errorf("启动 %s 失败: %w", bin, err)
	}
	p := &Proc{cmd: cmd}
	go func() {
		_ = cmd.Wait()
		p.done.Store(true)
		if logf != nil {
			logf.Close()
		}
	}()
	return p, stdout, nil
}

// Stop 结束进程组；幂等。
func (p *Proc) Stop() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil || p.done.Load() {
		return
	}
	if pgid, err := syscall.Getpgid(p.cmd.Process.Pid); err == nil && pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		return
	}
	_ = p.cmd.Process.Kill()
}

// Running 进程是否还活着。
func (p *Proc) Running() bool { return p != nil && !p.done.Load() }

// Pid 进程号。
func (p *Proc) Pid() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}
