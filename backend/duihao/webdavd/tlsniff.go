package webdavd

// 同端口双协议（TLS 开启时生效）：TLS ClientHello 的首字节恒为 0x16，
// 在 Accept 时偷看一个字节分流——握手包包上 tls.Server 正常走 https，
// 其它字节就是明文 HTTP。
//
// 明文放不放行由允许明文决定（dav.plain）：
//   - 允许（默认）：局域网 / 公网的 http 一律放行。挂载端最省事——Windows
//     资源管理器、RaiDrive、老播放器都不必处理自签证书；代价是 Basic Auth
//     的密码在明文段上可被同链路嗅探，所以界面常驻提示。
//   - 不允许：只有本机回环走明文（浏览器信任回环、不弹证书警告），外部
//     明文连接喂一个哑管道让它枯萎，LAN 保持 https-only。

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
)

// newDualListener 把普通监听器包成「TLS / 明文」双协议。
// allowPlain=false 时明文只放行回环来源（严格加密模式）。
func newDualListener(inner net.Listener, cfg *tls.Config, allowPlain bool) net.Listener {
	return &sniffListener{inner: inner, cfg: cfg, allowPlain: allowPlain}
}

type sniffListener struct {
	inner      net.Listener
	cfg        *tls.Config
	allowPlain bool
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
// TLS 握手字节 → tls.Server；明文 → 放行（严格模式下只放行回环）；
// 严格模式下的外部明文 → 掐断。
func (l *sniffListener) classify(c net.Conn, br *bufio.Reader) net.Conn {
	if head, perr := br.Peek(1); perr == nil && len(head) == 1 && head[0] == 0x16 {
		return tls.Server(&peekConn{Conn: c, r: br}, l.cfg)
	}
	if l.allowPlain {
		return &peekConn{Conn: c, r: br}
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
