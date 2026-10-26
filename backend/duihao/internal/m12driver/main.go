// m12driver：站点基本配置（https / 生产环境 / 创建环境）端到端验收。
//
// 全部用真东西（隔离 DUIHAO_DATA_ROOT）：
//   nginx 1.15.11 + php 8.2.29 + 内置 FTPServer
//   自签名证书 → 真 https 请求（InsecureSkipVerify）→ 断言 200
//   改域名 → 断言证书重签（SAN 覆盖新域名）
//   关 https → 断言片段回到明文 listen、URL 变 http、证书文件保留
//   「创建环境」的 FTP 分支 → 真 FTP 客户端登录站点根目录
//
// 跑法：go run ./backend/duihao/internal/m12driver
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"

	duihao "github.com/ys-ll/uniterm/backend/duihao"
	"github.com/ys-ll/uniterm/backend/duihao/audit"
	"github.com/ys-ll/uniterm/backend/duihao/ftpd"
	"github.com/ys-ll/uniterm/backend/duihao/instance"
	"github.com/ys-ll/uniterm/backend/duihao/service"
	"github.com/ys-ll/uniterm/backend/duihao/site"
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

func must(err error) {
	if err != nil {
		fmt.Println("FATAL:", err)
		os.Exit(1)
	}
}

// get 发一次请求；tlsInsecure 时跳过证书校验（自签名证书必须）。
func get(url string, tlsInsecure bool) (int, string, error) {
	tr := &http.Transport{Proxy: nil}
	if tlsInsecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	c := &http.Client{Timeout: 5 * time.Second, Transport: tr}
	r, err := c.Get(url)
	if err != nil {
		return 0, "", err
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r.StatusCode, string(b), nil
}

// poll 等 reload 落定（nginx reload 是异步的）。
func poll(ms int, cond func() bool) bool {
	deadline := time.Now().Add(time.Duration(ms) * time.Millisecond)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(120 * time.Millisecond)
	}
	return cond()
}

func fragText(mgr *instance.Manager, web, siteID string) string {
	b, err := os.ReadFile(filepath.Join(mgr.InstanceDirPath(web), "sites", siteID+".conf"))
	if err != nil {
		return ""
	}
	return string(b)
}

// certSAN 读证书里的 DNS 名（验证换域名后是否重签）。
func certSAN(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(c.DNSNames))
	for _, d := range c.DNSNames {
		if d != "localhost" {
			out = append(out, d)
		}
	}
	return out
}

func main() {
	fmt.Println("数据根:", duihao.DataRoot())
	must(duihao.EnsureDirs())
	settings, err := duihao.LoadSettings()
	if err != nil {
		settings = duihao.DefaultSettings()
	}
	auditLog := audit.New(duihao.DirLogs())
	mgr, err := instance.NewManager(settings, auditLog)
	if err != nil {
		fmt.Println("MGR-ERR:", err)
		os.Exit(2)
	}
	mgr.RegisterEmbedded("ftpserver", func(cfg instance.EmbeddedConfig) (instance.EmbeddedServer, error) {
		return ftpd.FromValues(cfg.Dir, cfg.LogPath, cfg.Ports, cfg.Values)
	})
	sites, err := site.NewManager(mgr, auditLog)
	if err != nil {
		fmt.Println("SITE-ERR:", err)
		os.Exit(3)
	}
	siteSvc := service.NewSiteService(sites)
	ftpSvc := service.NewFTPService(mgr, auditLog)
	ftpSvc.SetSiteManager(sites)

	fmt.Println("== 1) 实例")
	web, err := mgr.Create("nginx", "1.15.11", "", nil)
	must(err)
	phpIns, err := mgr.Create("php", "8.2.29", "", nil)
	must(err)
	ftpIns, err := mgr.Create("ftpserver", "builtin", "", nil)
	must(err)
	fmt.Printf("  nginx=%s:%d php=%s:%d ftp=%s:%d\n",
		web.ID, web.Ports["http"], phpIns.ID, phpIns.Ports["fcgi"], ftpIns.ID, ftpIns.Ports["ftp"])

	root := filepath.Join(duihao.DataRoot(), "www", "m12")
	must(os.MkdirAll(root, 0o755))
	must(os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html><h1>M12-HTTPS</h1>"), 0o644))

	ck("默认根目录为数据目录下 www", strings.HasSuffix(filepath.ToSlash(siteSvc.SiteDefaultRoot()), "/www"),
		siteSvc.SiteDefaultRoot())
	ports := siteSvc.SiteDefaultPorts()
	ck("默认端口 http=80 / https=443", ports["http"] == 80 && ports["https"] == 443, ports)

	fmt.Println("== 2) 创建 https 站点（自签名 + 生产环境）")
	port := 18443
	opts := site.Options{Production: true, AccessLog: true}
	view, err := siteSvc.CreateSite("m12-https", []string{"m12.test"}, port, root, web.ID, phpIns.ID, "", "", true, opts)
	if err != nil {
		fmt.Println("CREATE-ERR:", err)
		os.Exit(6)
	}
	ck("站点记录标记 https", view.TLSMode == site.TLSSelfSigned, view.TLSMode)
	ck("URL 用 https 前缀", strings.HasPrefix(view.URL, "https://"), view.URL)

	certPath, keyPath := site.CertPaths(mgr.InstanceDirPath(web.ID), view.ID)
	st, cerr := os.Stat(certPath)
	ck("证书已签发", cerr == nil && st.Size() > 0, certPath, cerr)
	ck("证书包含站点域名", strings.Contains(strings.Join(certSAN(certPath), ","), "m12.test"), certSAN(certPath))
	if _, err := os.Stat(keyPath); err != nil {
		ck("私钥已生成", false, err)
	} else {
		// Windows 上 os.WriteFile 的 perm 不生效（只有只读位）——文件落在用户自己的
		// AppData 下，ACL 继承已足够；这里只断言文件存在且非空。
		st, _ := os.Stat(keyPath)
		ck("私钥已生成且非空", st.Size() > 0, st.Size())
	}

	frag := fragText(mgr, web.ID, view.ID)
	ck("片段 listen 带 ssl", strings.Contains(frag, fmt.Sprintf("listen %d ssl;", port)), frag)
	ck("片段引用证书", strings.Contains(frag, "ssl_certificate ") && strings.Contains(frag, "ssl_certificate_key"))
	ck("片段写入生产环境标记", strings.Contains(frag, `PHP_ADMIN_VALUE "display_errors=Off"`))

	fmt.Println("== 3) 启动并真访问 https")
	must(sites.Start(view.ID))
	base := fmt.Sprintf("https://127.0.0.1:%d", port)
	var code int
	var body string
	ok := poll(6000, func() bool {
		c, b, err := get(base+"/", true)
		code, body = c, b
		return err == nil && c == 200
	})
	ck("https 首页 200 且内容正确", ok && strings.Contains(body, "M12-HTTPS"), code, body)

	code, _, err = get(fmt.Sprintf("http://127.0.0.1:%d/", port), false)
	ck("同端口明文请求被拒绝（不是 http 服务）", err != nil || code >= 400, code, err)

	fmt.Println("== 4) 编辑：换域名 → 证书重签")
	view2, err := siteSvc.UpdateSite(view.ID, "", []string{"m12b.test", "www.m12b.test"}, port, root,
		phpIns.ID, "", "", true, opts)
	if err != nil {
		ck("Update 换域名成功", false, err)
	} else {
		ck("Update 换域名成功", true)
		ck("视图域名已更新", strings.Join(view2.Domains, ",") == "m12b.test,www.m12b.test", view2.Domains)
		san := strings.Join(certSAN(certPath), ",")
		ck("证书 SAN 覆盖新域名", strings.Contains(san, "m12b.test") && strings.Contains(san, "www.m12b.test"), san)
		ck("首页仍 200（reload 后新配置生效）", poll(6000, func() bool {
			c, _, err := get(base+"/", true)
			return err == nil && c == 200
		}))
	}

	fmt.Println("== 5) 编辑：关闭 https → 回到明文")
	view3, err := siteSvc.UpdateSite(view.ID, "", []string{"m12b.test", "www.m12b.test"}, port, root,
		phpIns.ID, "", "", false, opts)
	if err != nil {
		ck("关闭 https 成功", false, err)
	} else {
		ck("关闭 https 成功", true)
		ck("记录回到 tlsMode=none", view3.TLSMode == "" || view3.TLSMode == site.TLSNone, view3.TLSMode)
		ck("URL 变回 http", strings.HasPrefix(view3.URL, "http://"), view3.URL)
		f := fragText(mgr, web.ID, view.ID)
		ck("片段无 ssl 监听", !strings.Contains(f, " ssl;") && !strings.Contains(f, "ssl_certificate"))
		ck("明文首页 200", poll(6000, func() bool {
			c, _, err := get(fmt.Sprintf("http://127.0.0.1:%d/", port), false)
			return err == nil && c == 200
		}))
		ck("证书文件保留（下次开 https 直接复用）", func() bool {
			_, err := os.Stat(certPath)
			return err == nil
		}())
	}

	fmt.Println("== 6) 创建环境：FTP 账号指向站点根目录")
	if err := mgr.Start(ftpIns.ID); err != nil {
		ck("启动 FTP 实例", false, err)
	} else {
		ck("启动 FTP 实例", true)
	}
	if err := ftpSvc.CreateFTPAccount(ftpIns.ID, "m12user", "M12#pass", root, false, "m12 站点"); err != nil {
		ck("创建 FTP 账号", false, err)
	} else {
		ck("创建 FTP 账号", true)
		ftpPort := ftpIns.Ports["ftp"]
		var names []string
		okFTP := poll(4000, func() bool {
			c, err := ftp.Dial(fmt.Sprintf("127.0.0.1:%d", ftpPort),
				ftp.DialWithTimeout(4*time.Second), ftp.DialWithDisabledEPSV(true))
			if err != nil {
				return false
			}
			defer c.Quit()
			if err := c.Login("m12user", "M12#pass"); err != nil {
				return false
			}
			es, err := c.List(".")
			if err != nil {
				return false
			}
			names = names[:0]
			for _, e := range es {
				names = append(names, e.Name)
			}
			return true
		})
		ck("FTP 登录并看到站点根目录文件", okFTP && strings.Contains(strings.Join(names, ","), "index.html"), names)
	}

	fmt.Println("== 7) 清理")
	_ = sites.Stop(view.ID)
	for _, id := range []string{ftpIns.ID, phpIns.ID, web.ID} {
		_ = mgr.Stop(id)
		_ = mgr.Remove(id)
	}
	fmt.Println()

	if fails == 0 {
		fmt.Println("M12 全部通过")
		return
	}
	fmt.Printf("M12 有 %d 项失败\n", fails)
	os.Exit(1)
}
