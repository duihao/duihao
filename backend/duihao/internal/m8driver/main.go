// m8driver：M8 声明式设置端到端验收。
// 用法：DUIHAO_DATA_ROOT=<隔离目录> m8driver.exe
// 流程：真实下载 php/redis/nginx → 装配 → 表单式改设置 → 配置文件生效 →
//       端口走 SetPort 链路 → 非法值被拒 → ResetSettings 回默认 → 审计。
package main

import (
	"fmt"
	"os"
	"strings"

	duihao "github.com/duihao/duihao/backend/duihao"
	"github.com/duihao/duihao/backend/duihao/audit"
	"github.com/duihao/duihao/backend/duihao/instance"
)

func fail(format string, args ...any) {
	fmt.Printf("FAIL: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	settings, err := duihao.LoadSettings()
	if err != nil {
		settings = duihao.DefaultSettings()
	}
	mgr, err := instance.NewManager(settings, audit.New(duihao.DirLogs()))
	if err != nil {
		fail("init: %v", err)
	}

	// ── 1) PHP：设置注入 php.ini ──
	phpRec, err := mgr.Create("php", "8.2.29", "php-m8", nil)
	if err != nil {
		fail("php create: %v", err)
	}
	ini, _ := mgr.ReadInstanceFile(phpRec.ID, "php.ini")
	if !strings.Contains(ini, "memory_limit = 256M") {
		fail("php.ini 未按默认值渲染: %q", firstLine(ini, "memory_limit"))
	}
	fmt.Println("PHP-DEFAULT memory_limit=256M")
	if err := mgr.ApplySettings(phpRec.ID, map[string]string{
		"php.memory_limit": "512", "php.display_errors": "1",
	}); err != nil {
		fail("php apply: %v", err)
	}
	ini, _ = mgr.ReadInstanceFile(phpRec.ID, "php.ini")
	if !strings.Contains(ini, "memory_limit = 512M") || !strings.Contains(ini, "display_errors = on") {
		fail("php 设置未生效")
	}
	fmt.Println("PHP-APPLIED memory_limit=512M display_errors=on")

	// ── 2) Redis：密码（setif）+ maxmemory + 运行中应用自动重启 ──
	redisRec, err := mgr.Create("redis", "5.0.14.1", "redis-m8", nil)
	if err != nil {
		fail("redis create: %v", err)
	}
	if err := mgr.ApplySettings(redisRec.ID, map[string]string{
		"redis.requirepass": "m8secret", "redis.maxmemory": "512",
	}); err != nil {
		fail("redis apply: %v", err)
	}
	conf, _ := mgr.ReadInstanceFile(redisRec.ID, "redis.conf")
	if !strings.Contains(conf, "requirepass m8secret") || !strings.Contains(conf, "maxmemory 512mb") {
		fail("redis.conf 未生效")
	}
	fmt.Printf("REDIS-APPLIED port=%d\n", redisRec.Ports["redis"])
	if err := mgr.Start(redisRec.ID); err != nil {
		fail("redis start: %v", err)
	}
	// 运行中改密码 → restart 链路（stop→start）
	if err := mgr.ApplySettings(redisRec.ID, map[string]string{"redis.requirepass": "n3wpass"}); err != nil {
		fail("redis running apply: %v", err)
	}
	if st := mgr.Status(redisRec.ID); st != "running" {
		fail("redis 运行中应用设置后状态异常: %s", st)
	}
	fmt.Println("REDIS-RUNNING-APPLIED restart-chain-ok")

	// ── 3) Nginx：reload 链路 + 端口走 SetPort + 非法拒绝 + Reset ──
	ngxRec, err := mgr.Create("nginx", "1.15.11", "nginx-m8", nil)
	if err != nil {
		fail("nginx create: %v", err)
	}
	if err := mgr.Start(ngxRec.ID); err != nil {
		fail("nginx start: %v", err)
	}
	oldPort := ngxRec.Ports["http"]
	if err := mgr.ApplySettings(ngxRec.ID, map[string]string{
		"nginx.worker_processes": "2", "nginx.port": fmt.Sprint(oldPort + 1),
	}); err != nil {
		fail("nginx apply: %v", err)
	}
	conf2, _ := mgr.ReadInstanceFile(ngxRec.ID, "nginx.conf")
	if !strings.Contains(conf2, "worker_processes 2;") ||
		!strings.Contains(conf2, fmt.Sprintf("listen %d;", oldPort+1)) {
		fail("nginx.conf 未生效")
	}
	fmt.Printf("NGINX-APPLIED port=%d->%d worker=2\n", oldPort, oldPort+1)

	if err := mgr.ApplySettings(ngxRec.ID, map[string]string{"nginx.port": "70000"}); err == nil {
		fail("非法端口未被拒绝")
	}
	fmt.Println("NGINX-INVALID-REJECTED port=70000")

	if err := mgr.ResetSettings(ngxRec.ID); err != nil {
		fail("nginx reset: %v", err)
	}
	conf2, _ = mgr.ReadInstanceFile(ngxRec.ID, "nginx.conf")
	if !strings.Contains(conf2, "worker_processes 4;") {
		fail("ResetSettings 未回默认")
	}
	fmt.Println("NGINX-RESET worker=4")

	// ── 4) MySQL：首装 initialize → my.ini 注入 → 运行中改配置走重启链路 ──
	myRec, err := mgr.Create("mysql", "8.0.40", "mysql-m8", nil)
	if err != nil {
		fail("mysql create: %v", err)
	}
	if err := mgr.ApplySettings(myRec.ID, map[string]string{
		"mysql.max_connections": "500", "mysql.charset": "utf8",
	}); err != nil {
		fail("mysql apply: %v", err)
	}
	ini2, _ := mgr.ReadInstanceFile(myRec.ID, "my.ini")
	if !strings.Contains(ini2, "max_connections=500") || !strings.Contains(ini2, "character-set-server=utf8") {
		fail("my.ini 未生效")
	}
	fmt.Printf("MYSQL-APPLIED port=%d\n", myRec.Ports["db"])
	if err := mgr.Start(myRec.ID); err != nil {
		fail("mysql start: %v", err)
	}
	// 运行中改 innodb_buffer_pool_size → Restart 链路
	if err := mgr.ApplySettings(myRec.ID, map[string]string{"mysql.innodb_buffer_pool_size": "256"}); err != nil {
		fail("mysql running apply: %v", err)
	}
	if st := mgr.Status(myRec.ID); st != "running" {
		fail("mysql 运行中应用设置后状态异常: %s", st)
	}
	fmt.Println("MYSQL-RUNNING-APPLIED restart-chain-ok")

	// 收尾
	_ = mgr.Stop(ngxRec.ID)
	_ = mgr.Stop(redisRec.ID)
	_ = mgr.Stop(myRec.ID)
	fmt.Println("M8-DONE")
}

func firstLine(s, prefix string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return "(not found)"
}
