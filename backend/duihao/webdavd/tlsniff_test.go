package webdavd

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mustSelfCert 测试用自签证书（落临时目录，客户端不验）。
func mustSelfCert(t *testing.T) tls.Certificate {
	t.Helper()
	b, err := ensureSelfSigned(t.TempDir(), "test")
	if err != nil {
		t.Fatalf("自签证书: %v", err)
	}
	return b.cert
}

// TestSniffListenerPlainLoopbackAndTLS 同端口双协议：回环明文 GET 能通，
// TLS 客户端握手也能通（嗅探按 ClientHello 首字节分流）。
func TestSniffListenerPlainLoopbackAndTLS(t *testing.T) {
	cfg := &tls.Config{ // 测试自签，客户端不验
		Certificates: []tls.Certificate{mustSelfCert(t)},
	}
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	srv.Listener = newPlainLoopbackListener(inner, cfg)
	srv.Start()
	defer srv.Close()

	// 1) 明文 http（回环）。
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("明文回环请求失败: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" {
		t.Fatalf("明文响应 = %q", body)
	}

	// 2) TLS 客户端（不验证书，模拟浏览器信任自签后）。
	tlsCli := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	resp2, err := tlsCli.Get("https://" + inner.Addr().String())
	if err != nil {
		t.Fatalf("TLS 请求失败: %v", err)
	}
	b2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if string(b2) != "ok" {
		t.Fatalf("TLS 响应 = %q", b2)
	}
}

// TestSniffClassify 分流判定：TLS 字节→tls.Conn；回环明文→放行；
// 外部明文→掐断（LAN 保持 https-only，Basic Auth 不裸奔）。
// 用假连接注入来源地址——Windows 上没法用局域网源 IP 连回环，端到端模拟不了。
func TestSniffClassify(t *testing.T) {
	l := newPlainLoopbackListener(nil, &tls.Config{Certificates: []tls.Certificate{mustSelfCert(t)}}).(*sniffListener)

	// 1) TLS ClientHello 首字节 0x16 → tls.Conn。
	tlsC := l.classify(&fakeConn{}, bufio.NewReader(strings.NewReader("\x16hello")))
	if _, ok := tlsC.(*tls.Conn); !ok {
		t.Fatalf("TLS 握手字节应得到 *tls.Conn, got %T", tlsC)
	}

	// 2) 回环明文 → 放行。
	lb := l.classify(&fakeConn{remote: "127.0.0.1:5555"}, bufio.NewReader(strings.NewReader("GET / HTTP/1.1\r\n")))
	if _, ok := lb.(*peekConn); !ok {
		t.Fatalf("回环明文应放行, got %T", lb)
	}
	lb = l.classify(&fakeConn{remote: "[::1]:5555"}, bufio.NewReader(strings.NewReader("GET / HTTP/1.1\r\n")))
	if _, ok := lb.(*peekConn); !ok {
		t.Fatalf("IPv6 回环明文应放行, got %T", lb)
	}

	// 3) 外部明文（局域网/公网来源）→ deadConn 且底层已关。
	for _, remote := range []string{"192.168.0.9:5555", "203.0.113.7:9999"} {
		fc := &fakeConn{remote: remote}
		out := l.classify(fc, bufio.NewReader(strings.NewReader("GET / HTTP/1.1\r\n")))
		if _, ok := out.(deadConn); !ok {
			t.Fatalf("外部明文(%s)应掐断, got %T", remote, out)
		}
		if !fc.closed {
			t.Fatalf("外部明文(%s)底层连接未关闭", remote)
		}
	}
}

// fakeConn 最小 net.Conn 实现：只提供 RemoteAddr，其余报错。
type fakeConn struct {
	remote string
	closed bool
}

func (c *fakeConn) RemoteAddr() net.Addr {
	host := "127.0.0.1"
	if c.remote != "" {
		host, _, _ = net.SplitHostPort(c.remote)
	}
	return &net.TCPAddr{IP: net.ParseIP(host), Port: 1}
}
func (c *fakeConn) Close() error                       { c.closed = true; return nil }
func (c *fakeConn) Read([]byte) (int, error)           { return 0, io.EOF }
func (c *fakeConn) Write([]byte) (int, error)          { return 0, io.ErrClosedPipe }
func (c *fakeConn) Deadline(time.Time) error           { return nil }
func (c *fakeConn) SetDeadline(time.Time) error        { return nil }
func (c *fakeConn) SetReadDeadline(time.Time) error    { return nil }
func (c *fakeConn) SetWriteDeadline(time.Time) error   { return nil }
func (c *fakeConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }

