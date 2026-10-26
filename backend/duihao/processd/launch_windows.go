//go:build windows

package processd

import (
	"fmt"
	"os"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// launchedProc 一个已启动 OS 进程的句柄集。procH 用于 wait/terminate，
// job 用于 KILL_ON_JOB_CLOSE 兜底回收。
type launchedProc struct {
	pid       int
	procH     windows.Handle
	job       windows.Handle
	waitEvent chan struct{}
	exitOnce  sync.Once
}

// makeJob 创建 KILL_ON_JOB_CLOSE 的 Job Object：宿主句柄全部关闭（含被
// taskkill /F 强杀）时，内核终止组内所有进程 —— 无残留的兜底保证（方案 §3.3）。
func makeJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// launchWindows 以 CREATE_SUSPENDED 启动进程 → AssignProcessToJobObject →
// ResumeThread。顺序为 M0 实测铁律：先创建再赋值的话，nginx/mysqld 的 worker
// 会在赋值前逃出 Job。stdout/stderr 追加到 logFile（可空=丢弃），stdin 关闭。
func launchWindows(bin string, args []string, dir string, env []string, logFile string) (*launchedProc, error) {
	job, err := makeJob()
	if err != nil {
		return nil, fmt.Errorf("CreateJobObject: %w", err)
	}
	success := false
	defer func() {
		if !success {
			windows.TerminateJobObject(job, 1)
			windows.CloseHandle(job)
		}
	}()

	var logH windows.Handle
	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, err
		}
		logH = windows.Handle(f.Fd())
		// 让子进程可继承该句柄。
		if err := windows.SetHandleInformation(logH, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			f.Close()
			return nil, err
		}
		defer f.Close() // 父进程自己的句柄可关；子进程持有的继承副本独立存活
	}

	argv, _ := windows.UTF16PtrFromString(buildCommandLine(bin, args))
	appName, _ := windows.UTF16PtrFromString(bin)
	var dirPtr *uint16
	if dir != "" {
		// M0 实测：lpCurrentDirectory 传空字符串会报 filename syntax incorrect，必须 nil。
		dirPtr, _ = windows.UTF16PtrFromString(dir)
	}
	var envPtr *uint16
	if len(env) > 0 {
		blk, err := envBlock(env)
		if err != nil {
			return nil, err
		}
		envPtr = blk
	}

	var si windows.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdInput = 0 // 无效句柄 = 子进程 stdin 关闭
	si.StdOutput = logH
	si.StdErr = logH

	// CREATE_SUSPENDED + CREATE_NEW_PROCESS_GROUP：挂起创建（铁律）；
	// 独立进程组让 CTRL_BREAK 可以只发给该子进程（优雅停止用）。
	// CREATE_NO_WINDOW：redis-server / php-cgi / mysqld 都是控制台程序，
	// GUI 宿主下不给这个标志每个进程弹一个黑窗；用户关窗=杀进程 → 健康检查
	// 判定崩溃 → 退避重启 → 再弹窗（死循环，实测）。日志已重定向到文件，无需控制台。
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW)
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(appName, argv, nil, nil, logH != 0, flags, envPtr, dirPtr, &si, &pi); err != nil {
		return nil, fmt.Errorf("CreateProcess %s: %w", bin, err)
	}
	if err := windows.AssignProcessToJobObject(job, pi.Process); err != nil {
		windows.TerminateProcess(pi.Process, 1)
		windows.CloseHandle(pi.Process)
		windows.CloseHandle(pi.Thread)
		return nil, fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		windows.TerminateProcess(pi.Process, 1)
		windows.CloseHandle(pi.Process)
		windows.CloseHandle(pi.Thread)
		return nil, fmt.Errorf("ResumeThread: %w", err)
	}
	windows.CloseHandle(pi.Thread) // 主线程句柄用完即关；进程句柄留给 wait/kill

	success = true
	proc := &launchedProc{
		pid:       int(pi.ProcessId),
		procH:     pi.Process,
		job:       job,
		waitEvent: make(chan struct{}),
	}
	go func() {
		_, _ = windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		proc.exitOnce.Do(func() { close(proc.waitEvent) })
	}()
	return proc, nil
}

// terminate 强杀单个进程（优雅停止超时后的兜底）。
func terminate(p *launchedProc) error {
	return windows.TerminateProcess(p.procH, 1)
}

// sendCtrlBreak 向子进程所在进程组发送 CTRL_BREAK（stop.type=signal 的 Windows 语义）。
func sendCtrlBreak(p *launchedProc) error {
	// CTRL_BREAK_EVENT = 1；只发给目标进程组（子进程以 CREATE_NEW_PROCESS_GROUP 创建）。
	return windows.GenerateConsoleCtrlEvent(1, uint32(p.pid))
}

// closeJob 关闭 Job 句柄（进程全部退出后调用；KILL_ON_JOB_CLOSE 对已退出进程无副作用）。
func closeJob(p *launchedProc) {
	if p.job != 0 {
		windows.CloseHandle(p.job)
		p.job = 0
	}
}

// procAlive 非阻塞探测进程是否仍存活。
func procAlive(p *launchedProc) bool {
	select {
	case <-p.waitEvent:
		return false
	default:
		return true
	}
}

// buildCommandLine 按 Windows 规则拼接命令行（首参不转义，后续逐个转义）。
func buildCommandLine(bin string, args []string) string {
	line := bin
	if len(args) > 0 {
		line += " " + escapeArgs(args)
	}
	return line
}

func escapeArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = syscall.EscapeArg(a)
	}
	return joinArgs(out)
}

func joinArgs(parts []string) string {
	s := ""
	for i, p := range parts {
		if i > 0 {
			s += " "
		}
		s += p
	}
	return s
}

// envBlock 构造 CreateProcess 的环境块（UTF-16，双 NUL 结尾）。
// 在父环境基础上追加/覆盖自定义变量。
func envBlock(extra []string) (*uint16, error) {
	env := append(os.Environ(), extra...)
	var size uintptr
	for _, kv := range env {
		size += uintptr(len(kv)*2 + 4) // 每项 UTF-16 字节 + NUL + 分隔
	}
	buf := make([]byte, size+2)
	pos := 0
	for _, kv := range env {
		for _, b := range []byte(kv) {
			buf[pos] = b
			buf[pos+1] = 0
			pos += 2
		}
		buf[pos], buf[pos+1] = 0, 0 // 项分隔 NUL
		pos += 2
	}
	buf[pos], buf[pos+1] = 0, 0 // 结束双 NUL
	return (*uint16)(unsafe.Pointer(&buf[0])), nil
}
