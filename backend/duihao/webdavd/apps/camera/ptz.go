// 云台控制（PTZ）：海康 ISAPI 与大华 CGI 两条实现。
//
// 只做「按方向转 + 变倍 + 预置位」，不做巡航/花样扫描这些厂家私有玩法 ——
// 页面上有个能转的摇杆就够了，加密钥级的自动化不是这个模块的事。
//
// 两家都要 HTTP 摘要认证（Digest），而 Go 标准库不提供客户端实现，所以
// digestAuth 在这里手写一份：MD5 三件套 + qop=auth，够用且没有第三方依赖。
package camera

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ptzKind 从 ptz 列（"hik:1" / "dahua:1"）或品牌推断控制方式。
// 返回 "" 表示这台相机不支持（页面就不显示摇杆）。
func ptzKind(cam Cam) (kind string, ch int) {
	raw := strings.TrimSpace(cam.Ptz)
	if raw != "" {
		parts := strings.SplitN(raw, ":", 2)
		kind = strings.ToLower(strings.TrimSpace(parts[0]))
		ch = 1
		if len(parts) == 2 {
			if n, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil && n > 0 {
				ch = n
			}
		}
		return kind, ch
	}
	switch strings.ToLower(cam.Vendor) {
	case "hikvision":
		return "hik", 1
	case "dahua":
		return "dahua", 1
	}
	return "", 0
}

// ptzBase 相机的 HTTP 根地址。
func ptzBase(cam Cam) string {
	h := strings.TrimSpace(cam.Host)
	if h == "" {
		if u, err := url.Parse(cam.URLMain); err == nil {
			h = u.Host
		}
	}
	if h == "" {
		return ""
	}
	if strings.HasPrefix(h, "http://") || strings.HasPrefix(h, "https://") {
		return strings.TrimRight(h, "/")
	}
	return "http://" + strings.TrimRight(h, "/")
}

// ptzMove 让相机朝某个方向动（speed 1~10，0 或 "stop" 表示停）。
func ptzMove(cam Cam, dir string, speed int) error {
	kind, ch := ptzKind(cam)
	if kind == "" {
		return fmt.Errorf("这台相机没配云台控制（在相机设置里选品牌后会自动带上）")
	}
	base := ptzBase(cam)
	if base == "" {
		return fmt.Errorf("缺相机 IP，云台要直接连相机")
	}
	pass, err := camPass(cam)
	if err != nil {
		return err
	}
	if cam.CredUser == "" {
		return fmt.Errorf("云台要走 HTTP 鉴权，请先填相机账号密码")
	}
	speed = clampInt(speed, 1, 10)
	sp := speed * 10 // 海康 1~100
	cli := &ptzClient{user: cam.CredUser, pass: pass}

	switch kind {
	case "hik":
		pan, tilt, zoom := 0, 0, 0
		switch strings.ToLower(dir) {
		case "up":
			tilt = sp
		case "down":
			tilt = -sp
		case "left":
			pan = -sp
		case "right":
			pan = sp
		case "zoomin":
			zoom = sp
		case "zoomout":
			zoom = -sp
		case "stop", "":
			// 全 0 = 停
		default:
			return fmt.Errorf("不认识的方向：%s", dir)
		}
		body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><PTZData><pan>%d</pan><tilt>%d</tilt><zoom>%d</zoom></PTZData>`,
			pan, tilt, zoom)
		u := fmt.Sprintf("%s/ISAPI/PTZCtrl/channels/%d/continuous", base, ch)
		code, err := cli.do(http.MethodPut, u, []byte(body), "application/xml")
		if err != nil {
			return err
		}
		if code != 200 {
			return fmt.Errorf("相机返回 %d（海康要开 ISAPI，且账号要有云台权限）", code)
		}
	case "dahua":
		code, act := "Up", "start"
		switch strings.ToLower(dir) {
		case "up":
			code = "Up"
		case "down":
			code = "Down"
		case "left":
			code = "Left"
		case "right":
			code = "Right"
		case "zoomin":
			code = "ZoomTele"
		case "zoomout":
			code = "ZoomWide"
		case "stop", "":
			act, code = "stop", "Up" // 大华 stop 要不带具体方向，随便给一个
		default:
			return fmt.Errorf("不认识的方向：%s", dir)
		}
		v := speed // 大华常用 1~8，这里直接把 1~10 传过去，超了它会自己夹
		u := fmt.Sprintf("%s/cgi-bin/ptz.cgi?action=%s&channel=%d&code=%s&arg1=%d&arg2=%d&arg3=%d",
			base, act, ch, code, v, v, v)
		status, err := cli.do(http.MethodGet, u, nil, "")
		if err != nil {
			return err
		}
		if status != 200 {
			return fmt.Errorf("相机返回 %d（大华 CGI 要开，账号要有云台权限）", status)
		}
	default:
		return fmt.Errorf("暂不支持 %s 的云台", kind)
	}
	return nil
}

// ptzPreset 预置位：act = goto | set，n = 1~255。
func ptzPreset(cam Cam, act string, n int) error {
	kind, ch := ptzKind(cam)
	if kind == "" {
		return fmt.Errorf("这台相机没配云台控制")
	}
	base := ptzBase(cam)
	if base == "" {
		return fmt.Errorf("缺相机 IP")
	}
	pass, err := camPass(cam)
	if err != nil {
		return err
	}
	if cam.CredUser == "" {
		return fmt.Errorf("云台要走 HTTP 鉴权，请先填相机账号密码")
	}
	n = clampInt(n, 1, 255)
	cli := &ptzClient{user: cam.CredUser, pass: pass}

	switch kind {
	case "hik":
		u := fmt.Sprintf("%s/ISAPI/PTZCtrl/channels/%d/presets/%d", base, ch, n)
		if strings.EqualFold(act, "goto") {
			u += "/goto"
		}
		body := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><PTZPreset><id>%d</id></PTZPreset>`, n)
		code, err := cli.do(http.MethodPut, u, []byte(body), "application/xml")
		if err != nil {
			return err
		}
		if code != 200 {
			return fmt.Errorf("相机返回 %d", code)
		}
	case "dahua":
		a := "gotopreset"
		if strings.EqualFold(act, "set") {
			a = "setpreset"
		}
		u := fmt.Sprintf("%s/cgi-bin/ptz.cgi?action=%s&channel=%d&code=%s&arg1=0&arg2=%d&arg3=0",
			base, a, ch, map[string]string{"gotopreset": "GotoPreset", "setpreset": "SetPreset"}[a], n)
		code, err := cli.do(http.MethodGet, u, nil, "")
		if err != nil {
			return err
		}
		if code != 200 {
			return fmt.Errorf("相机返回 %d", code)
		}
	default:
		return fmt.Errorf("暂不支持 %s 的预置位", kind)
	}
	return nil
}

// ---------- 摘要认证客户端 ----------

type ptzClient struct {
	user string
	pass string
}

func (p *ptzClient) httpClient() *http.Client {
	return &http.Client{
		Timeout: 6 * time.Second,
		Transport: &http.Transport{
			Proxy:             nil, // 走系统代理会把局域网请求送去外网（项目里踩过）
			DisableKeepAlives: true,
		},
	}
}

func (p *ptzClient) do(method, rawurl string, body []byte, ctype string) (int, error) {
	mk := func(auth string) (*http.Request, error) {
		var rd io.Reader
		if body != nil {
			rd = strings.NewReader(string(body))
		}
		req, err := http.NewRequest(method, rawurl, rd)
		if err != nil {
			return nil, err
		}
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		return req, nil
	}

	hc := p.httpClient()
	req, err := mk("")
	if err != nil {
		return 0, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("连不上相机（%s）：%w", ptzBaseHost(rawurl), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return resp.StatusCode, nil
	}
	ch := resp.Header.Get("WWW-Authenticate")
	if !strings.HasPrefix(strings.ToLower(ch), "digest ") {
		return resp.StatusCode, fmt.Errorf("相机要求认证但不支持摘要认证（%s）", ch)
	}
	auth := digestAuth(ch, method, rawurl, p.user, p.pass)
	req2, err := mk(auth)
	if err != nil {
		return 0, err
	}
	resp2, err := hc.Do(req2)
	if err != nil {
		return 0, err
	}
	defer resp2.Body.Close()
	if resp2.StatusCode == http.StatusUnauthorized {
		return resp2.StatusCode, fmt.Errorf("相机拒绝了这个账号密码（401）")
	}
	return resp2.StatusCode, nil
}

func ptzBaseHost(rawurl string) string {
	if u, err := url.Parse(rawurl); err == nil {
		return u.Host
	}
	return rawurl
}

// digestAuth 拼 Authorization: Digest …（qop=auth）。
func digestAuth(header, method, rawurl, user, pass string) string {
	kv := parseDigestHeader(header)
	realm, nonce := kv["realm"], kv["nonce"]
	if realm == "" || nonce == "" {
		return ""
	}
	u, err := url.Parse(rawurl)
	if err != nil {
		return ""
	}
	uri := u.RequestURI()
	nc := "00000001"
	cn := cnonce()
	qop := strings.ToLower(kv["qop"])
	if !strings.Contains(qop, "auth") {
		qop = ""
	}
	resp := digestResponse(user, pass, realm, nonce, nc, cn, method, uri, qop)

	parts := []string{
		`username="` + user + `"`,
		`realm="` + realm + `"`,
		`nonce="` + nonce + `"`,
		`uri="` + uri + `"`,
		`algorithm=MD5`,
		`response="` + resp + `"`,
	}
	if qop != "" {
		parts = append(parts, "qop=auth", "nc="+nc, `cnonce="`+cn+`"`)
	}
	if op := kv["opaque"]; op != "" {
		parts = append(parts, `opaque="`+op+`"`)
	}
	return "Digest " + strings.Join(parts, ", ")
}

// digestResponse RFC 2617 的 response 值（抽成纯函数，便于用官方例子做单测）。
func digestResponse(user, pass, realm, nonce, nc, cnonce, method, uri, qop string) string {
	ha1 := md5hex(user + ":" + realm + ":" + pass)
	ha2 := md5hex(method + ":" + uri)
	if strings.Contains(strings.ToLower(qop), "auth") {
		return md5hex(ha1 + ":" + nonce + ":" + nc + ":" + cnonce + ":auth:" + ha2)
	}
	return md5hex(ha1 + ":" + nonce + ":" + ha2)
}

func parseDigestHeader(h string) map[string]string {
	out := map[string]string{}
	h = strings.TrimSpace(h)
	if i := strings.IndexByte(h, ' '); i >= 0 {
		h = h[i+1:]
	}
	// 逗号分隔，但要跳过引号里的逗号
	var buf strings.Builder
	inQuote := false
	flush := func() {
		seg := strings.TrimSpace(buf.String())
		buf.Reset()
		if seg == "" {
			return
		}
		if i := strings.IndexByte(seg, '='); i > 0 {
			k := strings.ToLower(strings.TrimSpace(seg[:i]))
			v := strings.TrimSpace(seg[i+1:])
			v = strings.Trim(v, `"`)
			out[k] = v
		}
	}
	for _, r := range h {
		switch r {
		case '"':
			inQuote = !inQuote
			buf.WriteRune(r)
		case ',':
			if inQuote {
				buf.WriteRune(r)
			} else {
				flush()
			}
		default:
			buf.WriteRune(r)
		}
	}
	flush()
	return out
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func cnonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0a1b2c3d4e5f6071"
	}
	return hex.EncodeToString(b[:])
}
