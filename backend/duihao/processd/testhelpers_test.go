package processd

import (
	"net"
	"testing"
)

// listenFree 分配一个空闲 TCP 端口并保持监听（供占用类测试用）。
func listenFree(t *testing.T) (net.Listener, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln, ln.Addr().(*net.TCPAddr).Port
}
