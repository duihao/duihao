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
	srv.Listener = newDualListener(inner, cfg, true)
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

// TestSniffClassify 分流判定：TLS 字节→tls.Conn；明文按 dav.plain 决定——
// 允许明文时局域网/公网来源一律放行（同端口 http），
// 严格模式时只放行回环，外部明文掐断（https-only）。
// 用假连接注入来源地址——Windows 上没法用局域网源 IP 连回环，端到端模拟不了。
func TestSniffClassify(t *testing.T) {
	base := &tls.Config{Certificates: []tls.Certificate{mustSelfCert(t)}}
	l := newDualListener(nil, base, false).(*sniffListener) // 严格模式（dav.plain=0）
	open := newDualListener(nil, base, true).(*sniffListener)

	// 1) TLS ClientHello 首字节 0x16 → tls.Conn。
	tlsC := l.classify(&fakeConn{}, bufio.NewReader(strings.NewReader("\x16hello")))
	if _, ok := tlsC.(*tls.Conn); !ok {
		t.Fatalf("TLS 握手字节应得到 *tls.Conn, got %T", tlsC)
	}

	// 2) 回环明文 → 放行（严格模式也放行，保证本机恒 http）。
	lb := l.classify(&fakeConn{remote: "127.0.0.1:5555"}, bufio.NewReader(strings.NewReader("GET / HTTP/1.1\r\n")))
	if _, ok := lb.(*peekConn); !ok {
		t.Fatalf("回环明文应放行, got %T", lb)
	}
	lb = l.classify(&fakeConn{remote: "[::1]:5555"}, bufio.NewReader(strings.NewReader("GET / HTTP/1.1\r\n")))
	if _, ok := lb.(*peekConn); !ok {
		t.Fatalf("IPv6 回环明文应放行, got %T", lb)
	}

	// 3) 严格模式下外部明文（局域网/公网来源）→ deadConn 且底层已关。
	for _, remote := range []string{"192.168.0.9:5555", "203.0.113.7:9999"} {
		fc := &fakeConn{remote: remote}
		out := l.classify(fc, bufio.NewReader(strings.NewReader("GET / HTTP/1.1\r\n")))
		if _, ok := out.(deadConn); !ok {
			t.Fatalf("严格模式外部明文(%s)应掐断, got %T", remote, out)
		}
		if !fc.closed {
			t.Fatalf("外部明文(%s)底层连接未关闭", remote)
		}
	}

	// 4) 允许明文 → 同一个外部来源也放行（局域网/公网直接 http）。
	for _, remote := range []string{"192.168.0.9:5555", "203.0.113.7:9999"} {
		out := open.classify(&fakeConn{remote: remote}, bufio.NewReader(strings.NewReader("GET / HTTP/1.1\r\n")))
		if _, ok := out.(*peekConn); !ok {
			t.Fatalf("允许明文时外部明文(%s)应放行, got %T", remote, out)
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

