//go:build windows

// Package childproc 受管子进程：隐藏控制台黑框 + Job Object 兜底回收。
//
// 对号里凡是「拉起一个外部命令行程序」的地方都该走这里：监控的 ffmpeg、
// 下载的 aria2c。要点只有两个：
//
//  1. CREATE_NO_WINDOW —— 这些程序都是控制台程序，不在启动参数里藏窗口，
//     用户会看到一个黑框一闪。
//  2. Job Object + KILL_ON_JOB_CLOSE —— 对号被强杀（任务管理器结束进程、
//     蓝屏后残留）时，组内子进程跟着死，不留孤儿占着端口与文件句柄。
//     这跟 processd 的做法一致，只是这里只管单个模块自己的子进程。
package childproc

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// createNoWindow 见 https://learn.microsoft.com/windows/win32/procthread/process-creation-flags
const createNoWindow = 0x08000000

// Proc 一个受管子进程。零值不可用，用 Start 构造。
type Proc struct {
	cmd  *exec.Cmd
	job  windows.Handle
	done atomic.Bool
}

// Options 启动选项。
type Options struct {
	// LogPath 非空时把 stdout/stderr 追加到这里（自动建目录）。
	LogPath string
	// StdoutPipe true 时把 stdout 交给调用方（流式读取），stderr 仍写日志。
	StdoutPipe bool
}

func newJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// assign 把已启动的进程放进 Job。先起再放：这些程序没有 worker 派生逃逸的
// 问题（ffmpeg / aria2c 都是单进程），不需要 CREATE_SUSPENDED 那套顺序约束。
func assign(job windows.Handle, pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(job, h)
}

// Start 启动子进程。返回的 Proc 可能为 nil（仅在 Start 报错时），
// 调用方务必先判 err。
func Start(bin string, args []string, opt Options) (*Proc, io.ReadCloser, error) {
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}

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

	job, err := newJob()
	if err == nil {
		if aerr := assign(job, cmd.Process.Pid); aerr != nil {
			windows.CloseHandle(job)
			job = 0
		}
	}
	p := &Proc{cmd: cmd, job: job}
	go func() {
		_ = cmd.Wait()
		p.done.Store(true)
		if logf != nil {
			logf.Close()
		}
	}()
	return p, stdout, nil
}

// Stop 结束整组进程；幂等，对 nil 安全。
func (p *Proc) Stop() {
	if p == nil {
		return
	}
	if p.job != 0 {
		_ = windows.TerminateJobObject(p.job, 1)
		windows.CloseHandle(p.job)
		p.job = 0
		return
	}
	if p.cmd != nil && p.cmd.Process != nil && !p.done.Load() {
		_ = p.cmd.Process.Kill()
	}
}

// Running 进程是否还活着（Stop 之后、或自行退出后为 false）。
func (p *Proc) Running() bool { return p != nil && !p.done.Load() }

// Pid 进程号（未启动时为 0）。
func (p *Proc) Pid() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}
