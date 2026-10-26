// M4 验收驱动：站点层端到端（无 UI）。
//
//	phase=site  建 nginx/php 实例 → 建站点（渲染 server 块 + reload）→
//	            启动站点（编排实例）→ HTTP(Host 头) 取 PHP 页面 → 引用完整性 →
//	            停止 → 删除站点（根目录保留）→ 打印 SITE-DONE
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	duihao "github.com/ys-ll/uniterm/backend/duihao"
	"github.com/ys-ll/uniterm/backend/duihao/audit"
	"github.com/ys-ll/uniterm/backend/duihao/instance"
	"github.com/ys-ll/uniterm/backend/duihao/site"
)

func main() {
	phase := flag.String("phase", "site", "site")
	root := flag.String("root", "", "站点根目录（默认 <data>/www/site1）")
	flag.Parse()

	if err := duihao.EnsureDirs(); err != nil {
		fail("EnsureDirs: %v", err)
	}
	settings, err := duihao.LoadSettings()
	if err != nil {
		settings = duihao.DefaultSettings()
	}
	auditLog := audit.New(duihao.DirLogs())
	mgr, err := instance.NewManager(settings, auditLog)
	if err != nil {
		fail("instance manager: %v", err)
	}
	sites, err := site.NewManager(mgr, auditLog)
	if err != nil {
		fail("site manager: %v", err)
	}

	switch *phase {
	case "site":
		runSite(mgr, sites, *root)
	default:
		fail("未知 phase: %s", *phase)
	}
}

func runSite(mgr *instance.Manager, sites *site.Manager, root string) {
	// 1) 实例（幂等：已有则复用）
	nginxRec, err := mgr.Create("nginx", "1.15.11", "", nil)
	if err != nil {
		fail("创建 nginx 实例失败: %v", err)
	}
	fmt.Printf("NGINX id=%s port=%d\n", nginxRec.ID, nginxRec.Ports["http"])

	phpRec, err := mgr.Create("php", "8.2.29", "", nil)
	if err != nil {
		fail("创建 php 实例失败: %v", err)
	}
	fmt.Printf("PHP id=%s port=%d workers=%d\n", phpRec.ID, phpRec.Ports["fcgi"], phpRec.Workers)

	// 2) 站点根目录 + PHP 探针文件
	if root == "" {
		root = filepath.Join(duihao.DataRoot(), "www", "site1")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		fail("创建站点根目录失败: %v", err)
	}
	probe := filepath.Join(root, "index.php")
	if err := os.WriteFile(probe, []byte("<?php echo 'duihao-site-ok php=' . PHP_VERSION;"), 0o644); err != nil {
		fail("写 index.php 失败: %v", err)
	}

	// 3) 建站点（渲染片段 → nginx reload）
	// 3) 建站点：站点独立端口（默认 80；测试用 18080 避开系统端口）
	sitePort := 18080
	rec, err := sites.Create(site.CreateOpts{
		Domains: []string{"m4.test"}, Port: sitePort, Root: root,
		Web: nginxRec.ID, PHP: phpRec.ID,
	})
	if err != nil {
		fail("创建站点失败: %v", err)
	}
	fmt.Printf("SITE id=%s port=%d domains=%v web=%d php=%d\n",
		rec.ID, rec.EffectivePort(), rec.Domains, nginxRec.Ports["http"], phpRec.Ports["fcgi"])

	// 4) 启动站点（db → php → web 编排）
	if err := sites.Start(rec.ID); err != nil {
		fail("启动站点失败: %v", err)
	}
	fmt.Printf("SITE-RUNNING web=%s php=%s\n", mgr.Status(nginxRec.ID), mgr.Status(phpRec.ID))

	// 5) HTTP 探活：直接打站点自己的端口，不带 Host 头（无需 hosts）
	body, code, err := getWithHost(fmt.Sprintf("http://127.0.0.1:%d/", sitePort), "")
	if err != nil {
		fail("HTTP 请求失败: %v", err)
	}
	fmt.Printf("HTTP status=%d body=%s\n", code, body)

	// 5a) 默认欢迎页的编码：响应头必须带 charset=utf-8，否则中文页面在
	// 中文 Windows 上会被浏览器按 GBK 猜成乱码（用户实测反馈）。
	idxURL := fmt.Sprintf("http://127.0.0.1:%d/", nginxRec.Ports["http"])
	if ct, icode, _, ierr := getWithHeader(idxURL, ""); ierr == nil {
		fmt.Printf("INDEX status=%d content-type=%s\n", icode, ct)
	} else {
		fmt.Printf("INDEX-ERR %v\n", ierr)
	}

	// 5b) 改端口 → 老端口失效、新端口可用
	newPort := sitePort + 1
	if err := sites.SetPort(rec.ID, newPort); err != nil {
		fail("修改端口失败: %v", err)
	}
	body2, code2, err := getWithHost(fmt.Sprintf("http://127.0.0.1:%d/", newPort), "")
	if err != nil {
		fail("改端口后 HTTP 失败: %v", err)
	}
	fmt.Printf("PORT-CHANGED %d->%d status=%d body=%s\n", sitePort, newPort, code2, body2)
	if err := sites.SetPort(rec.ID, sitePort); err != nil {
		fail("回滚端口失败: %v", err)
	}

	// 5c) 端口占用必须被拒（同实例端口唯一）
	if _, err := sites.Create(site.CreateOpts{
		Domains: []string{"dup.test"}, Port: sitePort, Root: root,
		Web: nginxRec.ID,
	}); err == nil {
		fail("同端口重复建站未被拒绝")
	} else {
		fmt.Printf("PORT-DUP-REJECTED %v\n", err)
	}

	// 6) 引用完整性：web 实例被站点引用
	refs := sites.ReferencesInstance(nginxRec.ID)
	fmt.Printf("REFS %v\n", refs)

	// 7) hosts 提示
	fmt.Printf("HOSTS-LINE %s\n", site.HostsLine(rec))

	// 8) 停止 → 删除（根目录保留）
	if err := sites.Stop(rec.ID); err != nil {
		fmt.Fprintf(os.Stderr, "STOP-WARN %v\n", err)
	}
	fmt.Printf("SITE-STOPPED web=%s php=%s\n", mgr.Status(nginxRec.ID), mgr.Status(phpRec.ID))
	if err := sites.Delete(rec.ID); err != nil {
		fail("删除站点失败: %v", err)
	}
	if _, err := os.Stat(probe); err != nil {
		fail("根目录被误删（应当保留）: %v", err)
	}
	fmt.Printf("ROOT-KEPT %s\n", root)
	fmt.Println("SITE-DONE")
}

// getWithHost 带 Host 头请求（nginx 虚拟主机按 server_name 路由）。
func getWithHost(url, host string) (string, int, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	req.Host = host
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return string(b), resp.StatusCode, err
}

func getWithHeader(url, host string) (ct string, code int, body string, err error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", 0, "", err
	}
	if host != "" {
		req.Host = host
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp.Header.Get("Content-Type"), resp.StatusCode, string(b), err
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(3)
}
