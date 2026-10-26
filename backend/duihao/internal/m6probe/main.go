// m6probe：验证「老实例自动迁到清单首选端口（80）」与「手动改端口」。
// 用法：DUIHAO_DATA_ROOT=<隔离数据目录> m6probe.exe
package main

import (
	"fmt"
	"os"
	"path/filepath"

	duihao "github.com/ys-ll/uniterm/backend/duihao"
	"github.com/ys-ll/uniterm/backend/duihao/audit"
	"github.com/ys-ll/uniterm/backend/duihao/instance"
)

func main() {
	settings, err := duihao.LoadSettings()
	if err != nil {
		settings = duihao.DefaultSettings()
	}
	mgr, err := instance.NewManager(settings, audit.New(duihao.DirLogs()))
	if err != nil {
		fmt.Println("ERR init:", err)
		os.Exit(1)
	}
	rec := mgr.GetRecord("nginx-1")
	if rec == nil {
		fmt.Println("NO-INSTANCE nginx-1")
		os.Exit(2)
	}

	// 1) 模拟「装包升级前的老实例」：端口是池里的 8085，且没有配置签名文件。
	if err := mgr.SetPort("nginx-1", "http", 8085); err != nil {
		fmt.Println("SETPORT-ERR:", err)
		os.Exit(3)
	}
	_ = os.Remove(filepath.Join(mgr.InstanceDirPath("nginx-1"), ".conf-signature"))
	fmt.Printf("BEFORE port=%d\n", mgr.GetRecord("nginx-1").Ports["http"])

	// 2) 启动：ensureConfig 应先 promotePorts 到 80 再重渲染（charset 同时补齐）。
	if err := mgr.Start("nginx-1"); err != nil {
		fmt.Println("START-ERR:", err)
		os.Exit(4)
	}
	fmt.Printf("AFTER port=%d live=%s\n", mgr.GetRecord("nginx-1").Ports["http"], mgr.Status("nginx-1"))

	// 3) 手动改端口到 81：运行中应重渲染 + reload，端口真的换。
	port := 81
	if err := mgr.SetPort("nginx-1", "http", port); err != nil {
		fmt.Println("CHANGE-ERR:", err)
		os.Exit(5)
	}
	fmt.Printf("CHANGED port=%d live=%s\n", mgr.GetRecord("nginx-1").Ports["http"], mgr.Status("nginx-1"))

	// 4) 非法端口必须被拒。
	if err := mgr.SetPort("nginx-1", "http", 70000); err != nil {
		fmt.Printf("INVALID-REJECTED %v\n", err)
	} else {
		fmt.Println("INVALID-ACCEPTED")
	}

	_ = mgr.Stop("nginx-1")
	fmt.Println("M6PROBE-DONE")
}
