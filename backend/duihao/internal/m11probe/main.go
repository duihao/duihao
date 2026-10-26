// m11probe：保留现场用的一次性探针（不做清理），排查 MySQL 管理连接。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	duihao "github.com/ys-ll/uniterm/backend/duihao"
	"github.com/ys-ll/uniterm/backend/duihao/audit"
	"github.com/ys-ll/uniterm/backend/duihao/instance"
)

func main() {
	settings, _ := duihao.LoadSettings()
	if settings == nil {
		settings = duihao.DefaultSettings()
	}
	mgr, err := instance.NewManager(settings, audit.New(duihao.DirLogs()))
	if err != nil {
		panic(err)
	}
	rec, err := mgr.Create("mysql", "8.0.40", "", nil)
	if err != nil {
		panic(err)
	}
	fmt.Println("实例:", rec.ID, "端口:", rec.Ports["db"])
	dir := mgr.InstanceDirPath(rec.ID)
	ini, _ := os.ReadFile(filepath.Join(dir, "my.ini"))
	fmt.Println("--- my.ini ---")
	fmt.Println(string(ini))
	sql, _ := os.ReadFile(filepath.Join(dir, "init-admin.sql"))
	fmt.Println("--- init-admin.sql ---")
	fmt.Println(string(sql))

	if err := mgr.Start(rec.ID); err != nil {
		fmt.Println("START-ERR:", err)
		os.Exit(1)
	}
	time.Sleep(2 * time.Second)
	fmt.Println("状态:", mgr.Status(rec.ID))
	logs, _ := mgr.Logs(rec.ID, 40)
	fmt.Println("--- 进程日志 ---")
	fmt.Println(logs)
	fmt.Println("数据目录保留在:", dir)
}
