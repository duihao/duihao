//go:build windows

package sysinfo

import (
	"net"
	"time"
)

// isGlobalIPv6 是不是「能上公网」的 IPv6：
//   - 2000::/3 是全球单播（GUA）；
//   - fe80::/10 链路本地、fc00::/7 ULA（家庭内网自分配）都不要；
//   - fd 前缀是 Windows 有时给虚拟网卡瞎配的，一并排除在外。
func isGlobalIPv6(ip net.IP) bool {
	if ip.To16() == nil || ip.To4() != nil {
		return false
	}
	if ip.IsLinkLocalUnicast() || ip.IsLoopback() || ip.IsPrivate() {
		return false
	}
	return ip.IsGlobalUnicast() && (ip[0]&0xE0) == 0x20
}

// GlobalIPv6 返回本机一个可用的公网 IPv6 地址，没有就返回空串。
// （net.InterfaceAddrs 给的 IP 不带 zone，用户拿到的是纯地址。）
// 多个地址时取第一个 GUA；接口地址顺序稳定，DDNS 更新前后取到同一个地址。
func GlobalIPv6() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if isGlobalIPv6(ipn.IP) {
			return ipn.IP.String()
		}
	}
	return ""
}

// IPv6Outbound IPv6 能不能真正出公网：拨一个公共 IPv6 DNS 的 443（阿里 2400:3200::1 /
// Cloudflare 2606:4700:4700::1111），谁通用谁。「有 GUA 地址」不等于「有出口」，
// 光猫 IPv6 没开、防火墙拦出站时地址照样分得到。
func IPv6Outbound() bool {
	targets := []string{
		"[2400:3200::1]:443",
		"[2606:4700:4700::1111]:443",
	}
	for _, t := range targets {
		c, err := net.DialTimeout("tcp", t, 3*time.Second)
		if err == nil {
			_ = c.Close()
			return true
		}
	}
	return false
}
