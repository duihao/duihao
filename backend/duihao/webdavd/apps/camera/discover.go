package camera

import (
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
)

// ONVIF 发现：往 WS-Discovery 组播地址发一个 Probe，局域网里的摄像机自己回话。
//
// 只做「发现」，不做 ONVIF 鉴权/PTZ——拿到 IP 之后地址还是按品牌模板拼，
// 因为各家 ONVIF 服务的鉴权差异很大，v1 不碰（等真要做 PTZ 再说）。
// 有些相机默认关着 ONVIF，所以发现不到不代表相机不在，页面同时给「手动填 IP」。

const wsDiscoveryAddr = "239.255.255.250:3702"

var probeTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope"
  xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing"
  xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"
  xmlns:dn="http://www.onvif.org/ver10/network/wsdl">
  <e:Header>
    <w:MessageID>uuid:%s</w:MessageID>
    <w:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</w:To>
    <w:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</w:Action>
  </e:Header>
  <e:Body>
    <d:Probe><d:Types>dn:NetworkVideoTransmitter</d:Types></d:Probe>
  </e:Body>
</e:Envelope>`

var xaddrRe = regexp.MustCompile(`(?is)<[^>]*XAddrs>\s*([^<]+?)\s*</`)
var uuidRe = regexp.MustCompile(`uuid:[0-9a-fA-F-]+`)

type foundCam struct {
	IP    string `json:"ip"`
	XAddr string `json:"xaddr"`
	Hint  string `json:"hint"`
}

// discoverLAN 发一次 Probe，等 2.5 秒收集回包。
func discoverLAN() []foundCam {
	out := []foundCam{}
	addr, err := net.ResolveUDPAddr("udp4", wsDiscoveryAddr)
	if err != nil {
		return out
	}
	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		return out
	}
	defer conn.Close()

	msg := []byte(strings.Replace(probeTemplate, "%s", newID()+"-0000-0000-0000-000000000000", 1))
	if _, err := conn.Write(msg); err != nil {
		return out
	}
	_ = conn.SetReadDeadline(time.Now().Add(2500 * time.Millisecond))

	seen := map[string]bool{}
	buf := make([]byte, 8192)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		body := string(buf[:n])
		xaddrs := ""
		if m := xaddrRe.FindStringSubmatch(body); len(m) > 1 {
			xaddrs = strings.TrimSpace(m[1])
		}
		ip := ""
		if src != nil {
			ip = src.IP.String()
		} else if i := strings.Index(xaddrs, "://"); i > 0 {
			rest := xaddrs[i+3:]
			if j := strings.IndexAny(rest, ":/"); j > 0 {
				ip = rest[:j]
			}
		}
		if ip == "" || seen[ip] {
			continue
		}
		seen[ip] = true
		hint := "支持 ONVIF"
		for _, v := range vendorPresetsList {
			if v.Key != "custom" && strings.Contains(strings.ToLower(body), v.Key) {
				hint = v.Name
			}
		}
		out = append(out, foundCam{IP: ip, XAddr: xaddrs, Hint: hint})
	}
	return out
}

// discover API：扫局域网（一次 2.5 秒，页面按钮触发，不后台常驻扫）。
func (a *App) discover(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	items := discoverLAN()
	appkit.JSON(w, 200, map[string]any{"items": items, "count": len(items)})
	return nil
}

// uuidRe 目前只用于将来解析 SOAP 头（保留以免删掉正则时丢信息）。
var _ = uuidRe
