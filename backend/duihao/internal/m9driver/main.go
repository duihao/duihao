// m9driver：FTPServer（embedded）端到端验收。
// 真实 FTP 客户端（jlaffaye/ftp）连上去做 登录 / 列目录 / 上传 / 下载 / 改密码 / 只读 / 改端口。
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"

	duihao "github.com/duihao/duihao/backend/duihao"
	"github.com/duihao/duihao/backend/duihao/audit"
	"github.com/duihao/duihao/backend/duihao/ftpd"
	"github.com/duihao/duihao/backend/duihao/instance"
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

func dial(port int, user, pass string) (*ftp.ServerConn, error) {
	c, err := ftp.Dial(fmt.Sprintf("127.0.0.1:%d", port),
		ftp.DialWithTimeout(5*time.Second), ftp.DialWithDisabledEPSV(true))
	if err != nil {
		return nil, err
	}
	if err := c.Login(user, pass); err != nil {
		_ = c.Quit()
		return nil, err
	}
	return c, nil
}

func main() {
	if err := duihao.EnsureDirs(); err != nil {
		fmt.Println("ENSURE-ERR:", err)
		os.Exit(1)
	}
	settings, err := duihao.LoadSettings()
	if err != nil {
		settings = duihao.DefaultSettings()
	}
	mgr, err := instance.NewManager(settings, audit.New(duihao.DirLogs()))
	if err != nil {
		fmt.Println("MGR-ERR:", err)
		os.Exit(2)
	}
	mgr.RegisterEmbedded("ftpserver", func(cfg instance.EmbeddedConfig) (instance.EmbeddedServer, error) {
		return ftpd.FromValues(cfg.Dir, cfg.LogPath, cfg.Ports, cfg.Values)
	})

	fmt.Println("== 1) 创建实例（embedded，无下载）")
	rec, err := mgr.Create("ftpserver", "", "", nil)
	if err != nil {
		fmt.Println("CREATE-ERR:", err)
		os.Exit(3)
	}
	port := rec.Ports["ftp"]
	ck("实例已创建且版本为 builtin", rec.Version == "builtin", rec.Version)
	ck("端口已分配（ftp 池 2121-2199）", port >= 2121 && port <= 2199, port)

	fmt.Println("== 2) 启动")
	if err := mgr.Start(rec.ID); err != nil {
		fmt.Println("START-ERR:", err)
		os.Exit(4)
	}
	ck("状态 running", mgr.Status(rec.ID) == "running", mgr.Status(rec.ID))

	fmt.Println("== 3) 真实 FTP 客户端：登录 / 列目录 / 上传 / 下载")
	c, err := dial(port, "duihao", "duihao")
	ck("默认账号可登录", err == nil, err)
	if err == nil {
		names, lerr := c.List("/")
		ck("LIST 根目录成功", lerr == nil, lerr)
		_ = names
		content := "duihao-ftp-roundtrip-" + fmt.Sprint(time.Now().UnixNano())
		uerr := c.Stor("hello.txt", strings.NewReader(content))
		ck("STOR 上传成功", uerr == nil, uerr)
		r, rerr := c.Retr("hello.txt")
		got := ""
		if rerr == nil {
			b, _ := io.ReadAll(r)
			got = string(b)
			_ = r.Close()
		}
		ck("RETR 下载内容一致", rerr == nil && got == content, rerr, got)
		_ = c.Logout()
		_ = c.Quit()
	}

	fmt.Println("== 4) 错误密码被拒")
	if bad, err := dial(port, "duihao", "wrong-pass"); err == nil {
		_ = bad.Quit()
		ck("错误密码应拒绝", false)
	} else {
		ck("错误密码应拒绝", true)
	}

	fmt.Println("== 5) 运行中改密码 → 自动重启生效")
	if err := mgr.ApplySettings(rec.ID, map[string]string{"ftp.password": "s3cret!"}); err != nil {
		ck("应用设置成功", false, err)
	} else {
		ck("应用设置成功", true)
		if c2, err := dial(port, "duihao", "s3cret!"); err == nil {
			ck("新密码可登录", true)
			_ = c2.Quit()
		} else {
			ck("新密码可登录", false, err)
		}
		if c3, err := dial(port, "duihao", "duihao"); err == nil {
			_ = c3.Quit()
			ck("旧密码应失效", false)
		} else {
			ck("旧密码应失效", true)
		}
	}

	fmt.Println("== 6) 只读模式：能下载不能上传")
	if err := mgr.ApplySettings(rec.ID, map[string]string{"ftp.password": "s3cret!", "ftp.readonly": "1"}); err != nil {
		ck("开启只读应用成功", false, err)
	} else {
		if c4, err := dial(port, "duihao", "s3cret!"); err == nil {
			_, rerr := c4.Retr("hello.txt")
			ck("只读下 RETR 仍可用", rerr == nil, rerr)
			serr := c4.Stor("blocked.txt", strings.NewReader("x"))
			ck("只读下 STOR 被拒", serr != nil, serr)
			_ = c4.Quit()
		} else {
			ck("只读模式登录", false, err)
		}
	}

	fmt.Println("== 7) 运行中改端口（SetPort 链路）")
	newPort := port + 3
	if err := mgr.SetPort(rec.ID, "ftp", newPort); err != nil {
		ck("改端口成功", false, err)
	} else {
		c5, err := dial(newPort, "duihao", "s3cret!")
		ck("新端口可登录", err == nil, err)
		if c5 != nil {
			_ = c5.Quit()
		}
	}

	fmt.Println("== 8) 恢复默认（ResetSettings）")
	if err := mgr.ResetSettings(rec.ID); err != nil {
		ck("恢复默认成功", false, err)
	} else {
		c6, err := dial(newPort, "duihao", "duihao")
		ck("默认密码生效", err == nil, err)
		if c6 != nil {
			_ = c6.Quit()
		}
	}

	fmt.Println("== 9) 日志文件落盘")
	logPath := duihao.DirProcessLogs() + string(os.PathSeparator) + rec.ID + ".log"
	if b, err := os.ReadFile(logPath); err == nil && len(b) > 0 {
		ck("FTP 日志已写入 logs/process/"+rec.ID+".log", true)
	} else {
		ck("FTP 日志已写入", false, err)
	}

	fmt.Println("== 10) 停止 / 卸载")
	if err := mgr.Stop(rec.ID); err != nil {
		ck("停止成功", false, err)
	} else {
		ck("停止后状态", mgr.Status(rec.ID) == "stopped", mgr.Status(rec.ID))
	}
	if _, err := dial(newPort, "duihao", "duihao"); err == nil {
		ck("停止后端口应无响应", false)
	} else {
		ck("停止后端口应无响应", true)
	}
	if err := mgr.Remove(rec.ID); err != nil {
		ck("卸载成功", false, err)
	} else {
		ck("卸载成功", true)
	}

	if fails == 0 {
		fmt.Println("M9-ALL-PASS")
	} else {
		fmt.Printf("M9-FAILED=%d\n", fails)
		os.Exit(9)
	}
}
