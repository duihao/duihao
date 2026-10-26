// M3 验收驱动：无 UI 的实例装配 / 启停 / cli 通道执行器。
//
//	phase=run  创建 nginx 实例（mirror 级取包）→ 启动 → 打印 RUNNING 行 →
//	           等待 SIGINT/SIGTERM → 优雅停止 → 打印 STOPPED
//	phase=cli  InvokeCli("composer")：entry {runtime:php:8.2.29} 借用 php 运行时
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	duihao "github.com/ys-ll/uniterm/backend/duihao"
	"github.com/ys-ll/uniterm/backend/duihao/audit"
	"github.com/ys-ll/uniterm/backend/duihao/instance"
)

func main() {
	phase := flag.String("phase", "run", "run | autostop | cli")
	flag.Parse()

	if err := duihao.EnsureDirs(); err != nil {
		fmt.Fprintf(os.Stderr, "EnsureDirs: %v\n", err)
		os.Exit(2)
	}
	settings, err := duihao.LoadSettings()
	if err != nil {
		settings = duihao.DefaultSettings()
	}
	mgr, err := instance.NewManager(settings, audit.New(duihao.DirLogs()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "manager: %v\n", err)
		os.Exit(2)
	}

	switch *phase {
	case "run":
		run(mgr)
	case "autostop":
		autostop(mgr)
	case "cli":
		cli(mgr)
	default:
		fmt.Fprintf(os.Stderr, "未知 phase: %s\n", *phase)
		os.Exit(2)
	}
}

func run(mgr *instance.Manager) {
	rec, err := mgr.Create("nginx", "1.15.11", "", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "CREATE-FAIL: %v\n", err)
		os.Exit(3)
	}
	fmt.Printf("CREATED id=%s ports=%v\n", rec.ID, rec.Ports)

	if err := mgr.Start(rec.ID); err != nil {
		fmt.Fprintf(os.Stderr, "START-FAIL: %v\n", err)
		os.Exit(4)
	}
	var pids []int
	for _, v := range mgr.List() {
		if v.ID == rec.ID {
			pids = v.Pids
		}
	}
	var pidStrs []string
	for _, p := range pids {
		pidStrs = append(pidStrs, fmt.Sprint(p))
	}
	fmt.Printf("RUNNING id=%s port=%d pids=%s state=%s\n",
		rec.ID, rec.Ports["http"], strings.Join(pidStrs, ","), mgr.Status(rec.ID))

	// 等终止信号（PS 用 taskkill /F 时不会走到这里；Ctrl+C 会）。
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	if err := mgr.Stop(rec.ID); err != nil {
		fmt.Fprintf(os.Stderr, "STOP-FAIL: %v\n", err)
		os.Exit(5)
	}
	fmt.Println("STOPPED")
}

// autostop：创建 → 启动（Start 内含 tcp 健康等待）→ 优雅停止 → 退出。
// 验证清单 stop 声明（nginx -s stop）与状态机落盘。
func autostop(mgr *instance.Manager) {
	rec, err := mgr.Create("nginx", "1.15.11", "", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "CREATE-FAIL: %v\n", err)
		os.Exit(3)
	}
	if err := mgr.Start(rec.ID); err != nil {
		fmt.Fprintf(os.Stderr, "START-FAIL: %v\n", err)
		os.Exit(4)
	}
	fmt.Printf("RUNNING id=%s port=%d state=%s\n", rec.ID, rec.Ports["http"], mgr.Status(rec.ID))
	if err := mgr.Stop(rec.ID); err != nil {
		fmt.Fprintf(os.Stderr, "STOP-FAIL: %v\n", err)
		os.Exit(5)
	}
	fmt.Printf("GRACEFUL-OK id=%s state=%s\n", rec.ID, mgr.Status(rec.ID))
}

func cli(mgr *instance.Manager) {
	res, err := mgr.InvokeCli(context.Background(), "composer", "2.8.4", []string{"-V"}, "", 60)
	if err != nil {
		fmt.Fprintf(os.Stderr, "CLI-FAIL: %v\n", err)
		os.Exit(6)
	}
	fmt.Printf("CLI exit=%d out=%s\n", res.ExitCode, strings.TrimSpace(res.Stdout))
}
