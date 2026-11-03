//go:build windows

package sysinfo

import (
	"strings"
	"testing"
)

// hostOnly 从各种形态的地址里抠出纯 IPv4：NAS 地址有多种写法（带协议/端口/
// 路径，甚至带尾斜杠），抠错了就选错网卡。
func TestHostOnly(t *testing.T) {
	cases := map[string]string{
		"http://192.168.43.4:9111/":  "192.168.43.4",
		"https://192.168.43.4/":      "192.168.43.4",
		"192.168.43.4:9111":          "192.168.43.4",
		"192.168.43.4":               "192.168.43.4",
		"  http://10.0.0.2:8080/x  ": "10.0.0.2",
		"[fe80::1]":                  "",
		"nas.local":                  "",
		"":                           "",
	}
	for in, want := range cases {
		if got := hostOnly(in); got != want {
			t.Errorf("hostOnly(%q) = %q, want %q", in, got, want)
		}
	}
}

// privateIPv4 只认内网段：169.254（网线没插）与公网地址都不该被「固定」。
func TestPrivateIPv4(t *testing.T) {
	yes := []string{"192.168.43.4", "10.1.2.3", "172.16.0.9", "172.31.255.254"}
	no := []string{"169.254.1.1", "172.32.0.1", "8.8.8.8", "127.0.0.1", "", "fe80::1"}
	for _, s := range yes {
		if !privateIPv4(s) {
			t.Errorf("privateIPv4(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if privateIPv4(s) {
			t.Errorf("privateIPv4(%q) = true, want false", s)
		}
	}
}

// staticCmd 生成的命令串：网卡名必须带引号（中文名/带空格的名字否则会断），
// 网关缺省时不能出现空的 gateway=，多条 DNS 用 add + index。
func TestStaticCmd(t *testing.T) {
	a := LanIPv4{Name: "WLAN", IP: "192.168.43.4", Mask: "255.255.255.0",
		Gateway: "192.168.43.1", DNS: []string{"192.168.43.1", "223.5.5.5"}}
	got := staticCmd(a)
	for _, want := range []string{
		`set address name="WLAN" source=static address=192.168.43.4 mask=255.255.255.0 gateway=192.168.43.1`,
		`set dnsservers name="WLAN" source=static address=192.168.43.1 register=primary`,
		`add dnsservers name="WLAN" address=223.5.5.5 index=2`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("staticCmd 缺片段 %q\n got=%s", want, got)
		}
	}

	noGw := staticCmd(LanIPv4{Name: "以太网", IP: "10.0.0.5", Mask: "255.255.255.0"})
	if strings.Contains(noGw, "gateway=") {
		t.Errorf("无网关时不该出现 gateway=：%s", noGw)
	}
	if !strings.Contains(noGw, `name="以太网"`) {
		t.Errorf("网卡名要带引号：%s", noGw)
	}
	if strings.Contains(noGw, "dnsservers") {
		t.Errorf("无 DNS 时不该动 DNS：%s", noGw)
	}
}

func TestDHCPCmd(t *testing.T) {
	got := dhcpCmd("WLAN")
	if !strings.Contains(got, `set address name="WLAN" source=dhcp`) ||
		!strings.Contains(got, `set dnsservers name="WLAN" source=dhcp`) {
		t.Errorf("dhcpCmd = %s", got)
	}
}
