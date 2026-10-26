package site

import (
	"net"
	"testing"
)

// 端口预检：站点「启动」前必须先确认端口没被别人占着。
//
// 不加这一步时，nginx 因为 bind 失败会立刻退出，或者更糟——端口被别的程序
// 听着，waitPortListen 拨得通，一路判成功，界面显示运行中、浏览器打不开。
func TestPortBlockedSeesForeignListener(t *testing.T) {
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("准备监听失败: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	blocked, who := portBlocked(port)
	if !blocked {
		t.Fatalf("端口 %d 正被监听，portBlocked 应为 true", port)
	}
	if who == "" {
		t.Errorf("应能说出占用者，实际为空")
	}
	t.Logf("占用者: %s", who)

	if err := ln.Close(); err != nil {
		t.Fatalf("关闭监听失败: %v", err)
	}
	// 关掉后应当又能绑（Windows 上偶有 TIME_WAIT，允许一次重试）
	for i := 0; i < 3; i++ {
		if blocked, _ := portBlocked(port); !blocked {
			return
		}
	}
	t.Errorf("端口 %d 已释放，portBlocked 仍报被占", port)
}
