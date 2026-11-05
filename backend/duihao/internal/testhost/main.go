// testhost 是 M2 验收测试的宿主进程：读取实例描述 JSON → 创建 processd
// Manager → 启动实例 → 阻塞等待信号。专供「taskkill /F 宿主后子进程回收」
// 的验收场景使用（M0 V2 同款验证法：强杀本进程，Job Object 兜底回收）。
//
// 用法：testhost -spec <instances.json> -id <instanceId>
// 启动成功后打印一行 "RUNNING <id> pids=<pid1>,<pid2>" 并 flush。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	duihao "github.com/duihao/duihao/backend/duihao"
	"github.com/duihao/duihao/backend/duihao/audit"
	"github.com/duihao/duihao/backend/duihao/processd"
	"github.com/duihao/duihao/backend/duihao/runtimepkg"
)

// resolveReq 描述取包请求：存在时先走 runtimepkg 五级取包，再把
// 进程 spec 里的 {runtimeDir} 占位符替换为实际目录。
type resolveReq struct {
	Plugin   string `json:"plugin"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
}

func main() {
	specPath := flag.String("spec", "", "instance spec JSON file")
	id := flag.String("id", "", "instance id (defaults to spec.ID)")
	flag.Parse()

	if *specPath == "" {
		fmt.Fprintln(os.Stderr, "missing -spec")
		os.Exit(2)
	}

	data, err := os.ReadFile(*specPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read spec: %v\n", err)
		os.Exit(2)
	}
	var spec processd.InstanceSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		fmt.Fprintf(os.Stderr, "parse spec: %v\n", err)
		os.Exit(2)
	}
	if *id != "" {
		spec.ID = *id
	}

	al := audit.New(duihao.DirLogs())

	// 可选：先取包（五级解析），bin/args 里的 {runtimeDir} 随后替换。
	var req resolveReq
	if err := json.Unmarshal(data, &struct {
		Resolve *resolveReq `json:"resolve"`
	}{&req}); err == nil && req.Plugin != "" {
		res, err := runtimepkg.Resolve(context.Background(), runtimepkg.VersionSpec{
			PluginID: req.Plugin, Version: req.Version, Platform: req.Platform,
		}, al)
		if err != nil {
			fmt.Fprintf(os.Stderr, "resolve: %v\n", err)
			os.Exit(2)
		}
		spec.RuntimeDir = res.Dir
		for i := range spec.Processes {
			spec.Processes[i].Bin = strings.ReplaceAll(spec.Processes[i].Bin, "{runtimeDir}", res.Dir)
			for j := range spec.Processes[i].Args {
				spec.Processes[i].Args[j] = strings.ReplaceAll(spec.Processes[i].Args[j], "{runtimeDir}", res.Dir)
			}
			if spec.Processes[i].Stop.Cmd != "" {
				spec.Processes[i].Stop.Cmd = strings.ReplaceAll(spec.Processes[i].Stop.Cmd, "{runtimeDir}", res.Dir)
			}
		}
	}

	if err := duihao.EnsureDirs(); err != nil {
		fmt.Fprintf(os.Stderr, "dirs: %v\n", err)
		os.Exit(2)
	}
	mgr, err := processd.NewManager(nil, al)
	if err != nil {
		fmt.Fprintf(os.Stderr, "manager: %v\n", err)
		os.Exit(2)
	}
	if err := mgr.Add(spec); err != nil {
		fmt.Fprintf(os.Stderr, "add: %v\n", err)
		os.Exit(2)
	}
	if err := mgr.Start(spec.ID); err != nil {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
		os.Exit(3)
	}
	pids := mgr.PIDs(spec.ID)
	out := fmt.Sprintf("RUNNING %s state=%s pids=", spec.ID, mgr.Status(spec.ID))
	for i, p := range pids {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprint(p)
	}
	fmt.Println(out)
	os.Stdout.Sync()

	// 阻塞等信号（taskkill /F 不触发信号，Job 兜底回收；Ctrl+C 正常停止）。
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
	fmt.Println("SIGNALED")
	_ = mgr.Stop(spec.ID)
	fmt.Println("STOPPED")
}
