// m7probe：验证 Redis 真实下载 → 装配 → 无窗口启动 → TCP 健康 → 停止。
// 用法：DUIHAO_DATA_ROOT=<隔离数据目录> m7probe.exe
package main

import (
	"fmt"
	"net"
	"os"
	"time"

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

	// 1) 装配（首次运行触发真实下载 gh-proxy 源；已装则直接复用）。
	rec := mgr.GetRecord("redis-1")
	if rec == nil {
		var err error
		rec, err = mgr.Create("redis", "5.0.14.1", "redis-1", nil)
		if err != nil {
			fmt.Println("CREATE-ERR:", err)
			os.Exit(2)
		}
	}
	fmt.Printf("CREATED id=%s port=%d\n", rec.ID, rec.Ports["redis"])

	// 2) 启动（CREATE_NO_WINDOW 生效则不弹窗；无头环境验证进程存活 + TCP）。
	if err := mgr.Start(rec.ID); err != nil {
		fmt.Println("START-ERR:", err)
		os.Exit(3)
	}
	fmt.Printf("STARTED status=%s port=%d\n", mgr.Status(rec.ID), rec.Ports["redis"])

	// 3) TCP 探活（redis 不认 HTTP）。
	addr := fmt.Sprintf("127.0.0.1:%d", rec.Ports["redis"])
	ok := false
	for i := 0; i < 15; i++ {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			c.Close()
			ok = true
			break
		}
		time.Sleep(time.Second)
	}
	if !ok {
		fmt.Println("TCP-FAIL", addr)
		os.Exit(4)
	}
	fmt.Println("TCP-OK", addr)

	// 4) 停止。
	if err := mgr.Stop(rec.ID); err != nil {
		fmt.Println("STOP-ERR:", err)
		os.Exit(5)
	}
	fmt.Printf("STOPPED status=%s\n", mgr.Status(rec.ID))

	// 5) 清理。
	if err := mgr.Remove(rec.ID); err != nil {
		fmt.Println("REMOVE-ERR:", err)
		os.Exit(6)
	}
	fmt.Println("REDIS-DONE")
}
