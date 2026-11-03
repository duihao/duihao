//go:build !windows

package sysinfo

import "fmt"

// 非 Windows 对齐实现：对号 v1 只发 Windows 包（固定网卡地址要靠 netsh +
// 注册表，macOS/Linux 是另一套命令），这里全部返回「不支持」，
// 接口保持两端一致，编译不断。

type LanIPv4 struct {
	Name    string   `json:"name"`
	GUID    string   `json:"guid"`
	IP      string   `json:"ip"`
	Mask    string   `json:"mask"`
	Gateway string   `json:"gateway"`
	DNS     []string `json:"dns"`
	DHCP    bool     `json:"dhcp"`
}

func (a LanIPv4) Valid() bool { return false }

func LanIPv4All() []LanIPv4 { return nil }

func LanIPv4Current(hint string) (LanIPv4, bool) { return LanIPv4{}, false }

func IsElevated() bool { return false }

func (a LanIPv4) FixStatic() (bool, error) {
	return false, fmt.Errorf("固定内网地址目前只支持 Windows")
}

func (a LanIPv4) RestoreDHCP() (bool, error) {
	return false, fmt.Errorf("固定内网地址目前只支持 Windows")
}

func OpenNetworkSettings() error { return fmt.Errorf("仅支持 Windows") }
