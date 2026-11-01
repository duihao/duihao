package webdavd

// 同端口双协议（仅 TLS 开启时生效）：TLS ClientHello 的首字节恒为 0x16，
// 在 Accept 时偷看一个字节分流——握手包包上 tls.Server 正常走 https；
// 明文连接只放行本机回环（127.0.0.1 / ::1，浏览器对回环信任、不弹自签
// 证书警告，桌面直登与「本机」地址因此可以恒用 http），外部明文连接喂
// 一个哑管道让它枯萎——局域网里仍然 https-only，Basic Auth 不裸奔。

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
)

// newPlainLoopbackListener 把普通监听器包成「TLS / 回环明文」双协议。
func newPlainLoopbackListener(inner net.Listener, cfg *tls.Config) net.Listener {
	return &sniffListener{inner: inner, cfg: cfg}
}

type sniffListener struct {
	inner net.Listener
	cfg   *tls.Config
}

func (l *sniffListener) Accept() (net.Conn, error) {
	c, err := l.inner.Accept()
	if err != nil {
		return nil, err
	}
	br := bufio.NewReader(c)
	return l.classify(c, br), nil
}

// classify 按首字节与来源分流（拆出来便于单测）：
// TLS 握手字节 → tls.Server；回环明文 → 放行；外部明文 → 掐断。
func (l *sniffListener) classify(c net.Conn, br *bufio.Reader) net.Conn {
	if head, perr := br.Peek(1); perr == nil && len(head) == 1 && head[0] == 0x16 {
		return tls.Server(&peekConn{Conn: c, r: br}, l.cfg)
	}
	if ra := c.RemoteAddr(); ra != nil {
		if host, _, serr := net.SplitHostPort(ra.String()); serr == nil {
			if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
				return &peekConn{Conn: c, r: br}
			}
		}
	}
	// 外部明文：掐掉，但 Accept 必须返回 conn（返回 error 会停掉整个 Serve）。
	_ = c.Close()
	return deadConn{Conn: c}
}

func (l *sniffListener) Close() error   { return l.inner.Close() }
func (l *sniffListener) Addr() net.Addr { return l.inner.Addr() }

// peekConn 已偷看首字节的连接：Read 走 bufio（自动重放偷看的字节）。
type peekConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekConn) Read(b []byte) (int, error) { return c.r.Read(b) }

// deadConn 已掐断的外部明文连接：读 EOF 写断，http.Server 会静默关掉它。
type deadConn struct{ net.Conn }

func (deadConn) Read([]byte) (int, error)  { return 0, io.EOF }
func (deadConn) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
