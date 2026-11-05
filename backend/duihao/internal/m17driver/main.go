// m17driver：站点按版本绑定 PHP + 多版本 php-cgi 并存（phpStudy 式）端到端验收。
//
// 真实 nginx + 两个真实 PHP 运行时（隔离 DUIHAO_DATA_ROOT），断言：
//
//	两个站点分别绑 PHP 8.2 / 8.3 → 同时启动 → 各自页面报告的版本不同
//	停掉一个站点 → 另一个照常，且没人用的 PHP 版本被回收
//	再启动 → 对应版本 php-cgi 自动拉起（不需要去运行时面板点启动）
//	把站点切到同一个 PHP 版本 → 共用同一份 php-cgi
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	duihao "github.com/duihao/duihao/backend/duihao"
	"github.com/duihao/duihao/backend/duihao/audit"
	"github.com/duihao/duihao/backend/duihao/instance"
	"github.com/duihao/duihao/backend/duihao/processd"
	"github.com/duihao/duihao/backend/duihao/site"
)

var fails int

func ck(name string, ok bool, extra ...any) {
	if ok {
		fmt.Printf("  [PASS] %s\n", name)
		return
	}
	fails++
	fmt.Printf("  [FAIL] %s %v\n", name, extra)
}

// get 直接打站点端口（Proxy:nil 绕开沙箱代理），返回状态码与正文。
func get(url string) (int, string, error) {
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	r, err := c.Get(url)
	if err != nil {
		return 0, "", err
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r.StatusCode, string(b), nil
}

func must(err error, msg string) {
	if err != nil {
		fmt.Println(msg+":", err)
		os.Exit(1)
	}
}

func writePHP(root, body string) {
	must(os.MkdirAll(root, 0o755), "mkdir root")
	must(os.WriteFile(filepath.Join(root, "index.php"), []byte("<?php echo '"+body+"'.PHP_VERSION;"), 0o644), "write index.php")
}

func main() {
	must(duihao.EnsureDirs(), "ENSURE-ERR")
	settings, err := duihao.LoadSettings()
	if err != nil {
		settings = duihao.DefaultSettings()
	}
	auditLog := audit.New(duihao.DirLogs())
	mgr, err := instance.NewManager(settings, auditLog)
	must(err, "MGR-ERR")
	sites, err := site.NewManager(mgr, auditLog)
	must(err, "SITE-ERR")

	fmt.Println("== 1) 运行时实例：nginx + php8.2 + php8.3")
	web, err := mgr.Create("nginx", "1.15.11", "", nil)
	must(err, "NGINX-ERR")
	p82, err := mgr.Create("php", "8.2.29", "", nil)
	must(err, "PHP82-ERR")
	p83, err := mgr.Create("php", "8.3.14", "", nil)
	must(err, "PHP83-ERR")
	fmt.Printf("  nginx=%s:%d php82=%s:%d php83=%s:%d\n",
		web.ID, web.Ports["http"], p82.ID, p82.Ports["fcgi"], p83.ID, p83.Ports["fcgi"])
	ck("两个 PHP 实例端口不冲突", p82.Ports["fcgi"] != p83.Ports["fcgi"], p82.Ports, p83.Ports)

	rootA := filepath.Join(duihao.DataRoot(), "www", "m17a")
	rootB := filepath.Join(duihao.DataRoot(), "www", "m17b")
	writePHP(rootA, "A:")
	writePHP(rootB, "B:")

	portA, portB := 18180, 18181
	sa, err := sites.Create(site.CreateOpts{
		Domains: []string{"m17a.test"}, Port: portA, Root: rootA, Web: web.ID, PHP: p82.ID,
	})
	must(err, "CREATE-A")
	sb, err := sites.Create(site.CreateOpts{
		Domains: []string{"m17b.test"}, Port: portB, Root: rootB, Web: web.ID, PHP: p83.ID,
	})
	must(err, "CREATE-B")
	urlA := fmt.Sprintf("http://127.0.0.1:%d/index.php", portA)
	urlB := fmt.Sprintf("http://127.0.0.1:%d/index.php", portB)

	fmt.Println("== 2) 启动 A（未事先启动任何 PHP / nginx）")
	must(sites.Start(sa.ID), "START-A")
	ck("A 自动拉起 nginx", mgr.Status(web.ID) == processd.StateRunning, mgr.Status(web.ID))
	ck("A 自动拉起 php 8.2", mgr.Status(p82.ID) == processd.StateRunning, mgr.Status(p82.ID))
	code, body, err := get(urlA)
	ck("A 页面 200 且跑 8.2", err == nil && code == 200 && strings.Contains(body, "A:8.2"), code, body, err)

	fmt.Println("== 3) 启动 B（另一个 PHP 版本，不改动 A）")
	must(sites.Start(sb.ID), "START-B")
	codeB, bodyB, errB := get(urlB)
	ck("B 页面 200 且跑 8.3", errB == nil && codeB == 200 && strings.Contains(bodyB, "B:8.3"), codeB, bodyB, errB)
	ck("两个 PHP 版本同时运行", mgr.Status(p82.ID) == processd.StateRunning && mgr.Status(p83.ID) == processd.StateRunning,
		mgr.Status(p82.ID), mgr.Status(p83.ID))
	codeA2, bodyA2, errA2 := get(urlA)
	ck("B 起来后 A 仍然 200 / 8.2", errA2 == nil && codeA2 == 200 && strings.Contains(bodyA2, "A:8.2"), codeA2, bodyA2, errA2)

	fmt.Println("== 4) 停用 A：B 不受影响，没人用的 8.2 被回收")
	must(sites.Stop(sa.ID), "STOP-A")
	_, _, errA3 := get(urlA)
	ck("A 端口不再响应", errA3 != nil, errA3)
	codeB2, bodyB2, errB2 := get(urlB)
	ck("B 仍然 200", errB2 == nil && codeB2 == 200 && strings.Contains(bodyB2, "B:8.3"), codeB2, errB2)
	ck("nginx 未被停掉（B 还要用）", mgr.Status(web.ID) == processd.StateRunning, mgr.Status(web.ID))
	ck("8.2 已回收", mgr.Status(p82.ID) != processd.StateRunning, mgr.Status(p82.ID))
	ck("8.3 仍在跑", mgr.Status(p83.ID) == processd.StateRunning, mgr.Status(p83.ID))
	off := filepath.Join(mgr.InstanceDirPath(web.ID), "sites", sa.ID+".conf.off")
	on := filepath.Join(mgr.InstanceDirPath(web.ID), "sites", sa.ID+".conf")
	_, offErr := os.Stat(off)
	_, onErr := os.Stat(on)
	ck("A 片段改为 .conf.off 且 .conf 已移除", offErr == nil && os.IsNotExist(onErr), offErr, onErr)

	fmt.Println("== 5) 再启动 A：8.2 自动重新拉起")
	must(sites.Start(sa.ID), "RESTART-A")
	ck("8.2 重新运行", mgr.Status(p82.ID) == processd.StateRunning, mgr.Status(p82.ID))
	codeA4, bodyA4, errA4 := get(urlA)
	ck("A 恢复 200 / 8.2", errA4 == nil && codeA4 == 200 && strings.Contains(bodyA4, "A:8.2"), codeA4, bodyA4, errA4)
	ck("B 依旧 200（两版本再次并存）", func() bool { c, b, e := get(urlB); return e == nil && c == 200 && strings.Contains(b, "B:8.3") }(), nil)

	fmt.Println("== 6) 把 B 切到 8.2：与 A 共用同一份 php-cgi，8.3 回收")
	portB2 := portB
	must(sites.Update(sb.ID, site.UpdateOpts{
		Name: sb.Name, Domains: []string{"m17b.test"}, Port: portB2, Root: rootB,
		PHP: p82.ID, DB: "", Database: "", TLS: false,
		Options: site.Options{},
	}), "UPDATE-B")
	codeB3, bodyB3, errB3 := get(urlB)
	ck("B 切版本后 200 且跑 8.2", errB3 == nil && codeB3 == 200 && strings.Contains(bodyB3, "B:8.2"), codeB3, bodyB3, errB3)
	ck("不再使用的 8.3 被回收", mgr.Status(p83.ID) != processd.StateRunning, mgr.Status(p83.ID))
	ck("8.2 仍在跑（A/B 共用）", mgr.Status(p82.ID) == processd.StateRunning, mgr.Status(p82.ID))

	fmt.Println("== 7) 删除 A：B 不受影响")
	must(sites.Delete(sa.ID), "DELETE-A")
	codeB4, _, errB4 := get(urlB)
	ck("B 仍可访问", errB4 == nil && codeB4 == 200, codeB4, errB4)
	if _, err := os.Stat(rootA); err != nil {
		ck("删站点保留根目录", false, err)
	} else {
		ck("删站点保留根目录", true)
	}

	// ───── 并发场景：goroutine 同时启动 / 停止，验证共享实例不被并发撕坏 ─────

	fmt.Println("== 8) 停用 B，准备并发场景")
	must(sites.Stop(sb.ID), "STOP-B-PRE")

	rootC := filepath.Join(duihao.DataRoot(), "www", "m17c")
	writePHP(rootC, "C:")
	sc, err := sites.Create(site.CreateOpts{
		Domains: []string{"m17c.test"}, Port: 18182, Root: rootC, Web: web.ID, PHP: p83.ID,
	})
	must(err, "CREATE-C")
	urlC := fmt.Sprintf("http://127.0.0.1:%d/index.php", 18182)

	fmt.Println("== 9) 并发启动两个站点（不同 PHP 版本）")
	var eB, eC error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); eB = sites.Start(sb.ID) }()
	go func() { defer wg.Done(); eC = sites.Start(sc.ID) }()
	wg.Wait()
	ck("并发 Start(B) 无报错", eB == nil, eB)
	ck("并发 Start(C) 无报错", eC == nil, eC)
	ck("8.2 在跑", mgr.Status(p82.ID) == processd.StateRunning, mgr.Status(p82.ID))
	ck("8.3 在跑", mgr.Status(p83.ID) == processd.StateRunning, mgr.Status(p83.ID))
	cB, bB, eB2 := get(urlB)
	ck("B 页面 200 / 8.2", eB2 == nil && cB == 200 && strings.Contains(bB, "B:8.2"), cB, bB, eB2)
	cC, bC, eC2 := get(urlC)
	ck("C 页面 200 / 8.3", eC2 == nil && cC == 200 && strings.Contains(bC, "C:8.3"), cC, bC, eC2)

	fmt.Println("== 10) 全部停用后并发启动同版本（共享实例竞态）")
	must(sites.Stop(sb.ID), "STOP-B2")
	must(sites.Stop(sc.ID), "STOP-C2")
	must(sites.Update(sc.ID, site.UpdateOpts{
		Name: sc.Name, Domains: []string{"m17c.test"}, Port: 18182, Root: rootC,
		PHP: p82.ID, DB: "", Database: "", TLS: false, Options: site.Options{},
	}), "UPDATE-C")
	wg.Add(2)
	go func() { defer wg.Done(); eB = sites.Start(sb.ID) }()
	go func() { defer wg.Done(); eC = sites.Start(sc.ID) }()
	wg.Wait()
	ck("并发同版本 Start 无报错", eB == nil && eC == nil, eB, eC)
	ck("仍只有 8.2 一份 php-cgi（8.3 未被拉起）",
		mgr.Status(p82.ID) == processd.StateRunning && mgr.Status(p83.ID) != processd.StateRunning,
		mgr.Status(p82.ID), mgr.Status(p83.ID))
	cB2, bB2, eB3 := get(urlB)
	ck("B 页面 200 / 8.2", eB3 == nil && cB2 == 200 && strings.Contains(bB2, "B:8.2"), cB2, bB2, eB3)
	cC2, bC2, eC3 := get(urlC)
	ck("C 页面 200 / 8.2（共用）", eC3 == nil && cC2 == 200 && strings.Contains(bC2, "C:8.2"), cC2, bC2, eC3)

	fmt.Println("== 11) 并发 停 B / 启 C（共享实例交错）")
	var eStop, eStart error
	wg.Add(2)
	go func() { defer wg.Done(); eStop = sites.Stop(sb.ID) }()
	go func() { defer wg.Done(); eStart = sites.Start(sc.ID) }()
	wg.Wait()
	ck("交错操作无报错", eStop == nil && eStart == nil, eStop, eStart)
	cC3, _, eC4 := get(urlC)
	ck("C 仍 200", eC4 == nil && cC3 == 200, cC3, eC4)
	ck("php 8.2 在跑（C 还要用）", mgr.Status(p82.ID) == processd.StateRunning, mgr.Status(p82.ID))

	must(sites.Delete(sc.ID), "DELETE-C")

	fmt.Println("== 12) 停用 B（最后一个站点）→ nginx / php 回收")
	must(sites.Stop(sb.ID), "STOP-B")
	ck("nginx 已停", mgr.Status(web.ID) != processd.StateRunning, mgr.Status(web.ID))
	ck("php 8.2 已停", mgr.Status(p82.ID) != processd.StateRunning, mgr.Status(p82.ID))

	must(sites.Delete(sb.ID), "DELETE-B")
	fmt.Printf("\nRESULT fails=%d\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
	fmt.Println("M17-DONE")
}
