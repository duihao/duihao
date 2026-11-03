//go:build windows

package sysinfo

// 本机 IPv4 网卡：读当前配置（IP / 掩码 / 网关 / DNS / 是不是 DHCP），
// 以及把某块网卡从「自动获取」切成「固定 IP」、或切回去。
//
// 为什么读注册表而不是解析 ipconfig / netsh 的输出：
//   这两个命令的输出是**本地化**的（「子网掩码」/「Subnet Mask」随系统语言变），
//   解析中文 Windows 的字符串在英文系统上就崩。注册表键名是固定的英文，
//   而且这里要的两个关键量（EnableDHCP、DhcpIPAddress）只有注册表能一次给全。
//   写操作仍走 netsh：它立刻生效并顺带写好注册表，比「改注册表再禁用/启用网卡」
//   温和得多（后者会断网更久，还可能把虚拟网卡搞乱）。
//
// 注册表口径（每块网卡一个 GUID 子键）：
//   EnableDHCP        DWORD     1 = 自动获取，0 = 手动
//   IPAddress         MULTI_SZ  手动配置的 IP（DHCP 时为空）
//   SubnetMask        MULTI_SZ  手动配置的掩码
//   DefaultGateway    MULTI_SZ  手动配置的网关
//   NameServer        SZ        手动配置的 DNS（逗号或空格分隔）
//   DhcpIPAddress     SZ        自动获取到的 IP
//   DhcpSubnetMask    SZ        自动获取到的掩码
//   DhcpDefaultGateway MULTI_SZ 自动获取到的网关
//   DhcpNameServer    SZ        自动获取到的 DNS
//
// 网卡的「友好名」（WLAN / 以太网）在
//   SYSTEM\CurrentControlSet\Control\Network\{4D36E972-...}\{GUID}\Connection 的 Name
// netsh 的 name= 参数要的正是这个名字。

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	regTcpipIfaces = `SYSTEM\CurrentControlSet\Services\Tcpip\Parameters\Interfaces`
	regNetClassAdp = `SYSTEM\CurrentControlSet\Control\Network\{4D36E972-E325-11CE-BFC1-08002BE10318}`
)

// LanIPv4 一块网卡的当前 IPv4 配置。
type LanIPv4 struct {
	Name    string   `json:"name"`    // 友好名，如 "WLAN"
	GUID    string   `json:"guid"`    // 注册表子键名（诊断用）
	IP      string   `json:"ip"`      // 当前 IPv4
	Mask    string   `json:"mask"`    //
	Gateway string   `json:"gateway"` // 默认网关（可能为空）
	DNS     []string `json:"dns"`     // 当前 DNS（可能为空）
	DHCP    bool     `json:"dhcp"`    // true = 自动获取
}

// Valid 有没有拿到一个像样的 IPv4 配置。
func (a LanIPv4) Valid() bool { return a.Name != "" && a.IP != "" }

// privateIPv4 内网地址（10/8、172.16/12、192.168/16）。169.254 的 APIPA 与
// 回环都不算 —— 网线没插时网卡会挂个 169.254，拿它去「固定」毫无意义。
func privateIPv4(s string) bool {
	ip := net.ParseIP(s).To4()
	if ip == nil {
		return false
	}
	switch {
	case ip[0] == 10:
		return true
	case ip[0] == 172 && ip[1]&0xF0 == 16:
		return true
	case ip[0] == 192 && ip[1] == 168:
		return true
	}
	return false
}

func regStr(k registry.Key, name string) string {
	if s, _, err := k.GetStringValue(name); err == nil {
		return strings.TrimSpace(s)
	}
	return ""
}

func regMulti(k registry.Key, name string) []string {
	vs, _, err := k.GetStringsValue(name)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// splitDNS "8.8.8.8,1.1.1.1" / "8.8.8.8 1.1.1.1" -> ["8.8.8.8","1.1.1.1"]
func splitDNS(s string) []string {
	f := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' })
	out := make([]string, 0, len(f))
	for _, v := range f {
		if net.ParseIP(v) != nil {
			out = append(out, v)
		}
	}
	return out
}

// adapterName 注册表 GUID -> 友好名。
func adapterName(guid string) string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, regNetClassAdp+`\`+guid+`\Connection`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	return regStr(k, "Name")
}

// upNames 当前「已连接」的网卡名集合：注册表里有十几块虚拟/断开的网卡
// （VMware、蓝牙、WSL），用 net.Interfaces 的 up 标志把它们筛掉，
// 用户在下拉里就不会看到一堆没法用的名字。
func upNames() map[string]bool {
	m := map[string]bool{}
	ifs, err := net.Interfaces()
	if err != nil {
		return m
	}
	for _, i := range ifs {
		if i.Flags&net.FlagUp != 0 && i.Flags&net.FlagLoopback == 0 {
			m[i.Name] = true
		}
	}
	return m
}

// LanIPv4All 所有已连接、且有内网 IPv4 的网卡。
func LanIPv4All() []LanIPv4 {
	up := upNames()
	root, err := registry.OpenKey(registry.LOCAL_MACHINE, regTcpipIfaces, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer root.Close()
	guids, err := root.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}
	out := make([]LanIPv4, 0, 4)
	for _, guid := range guids {
		k, err := registry.OpenKey(root, guid, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		name := adapterName(guid)
		dhcp := false
		if v, _, err := k.GetIntegerValue("EnableDHCP"); err == nil && v == 1 {
			dhcp = true
		}
		a := LanIPv4{Name: name, GUID: guid, DHCP: dhcp}
		if dhcp {
			a.IP = regStr(k, "DhcpIPAddress")
			a.Mask = regStr(k, "DhcpSubnetMask")
			if gw := regMulti(k, "DhcpDefaultGateway"); len(gw) > 0 {
				a.Gateway = gw[0]
			}
			a.DNS = splitDNS(regStr(k, "DhcpNameServer"))
		} else {
			if ips := regMulti(k, "IPAddress"); len(ips) > 0 {
				a.IP = ips[0]
				if ms := regMulti(k, "SubnetMask"); len(ms) > 0 {
					a.Mask = ms[0]
				}
			}
			if gw := regMulti(k, "DefaultGateway"); len(gw) > 0 {
				a.Gateway = gw[0]
			}
			a.DNS = splitDNS(regStr(k, "NameServer"))
		}
		// DNS 没取到时用配置里 DHCP 下发过的兜底（切静态后系统仍会用它们，
		// 但界面上「DNS: —」会让用户以为要手填）
		if len(a.DNS) == 0 {
			a.DNS = splitDNS(regStr(k, "DhcpNameServer"))
		}
		k.Close()
		if name == "" || !up[name] || !privateIPv4(a.IP) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// LanIPv4Current 挑「当前正在用的那块」：
//  1. 优先 hint 里那台主机 IP 所在的网卡（NAS 地址就是走这块出去的，
//     多网卡时这是唯一不会选错的依据）；
//  2. 否则取第一块有默认网关的内网网卡（真正的出口）；
//  3. 再否则取第一块内网网卡。
func LanIPv4Current(hint string) (LanIPv4, bool) {
	all := LanIPv4All()
	if len(all) == 0 {
		return LanIPv4{}, false
	}
	if host := hostOnly(hint); host != "" {
		for _, a := range all {
			if a.IP == host {
				return a, true
			}
		}
	}
	for _, a := range all {
		if a.Gateway != "" {
			return a, true
		}
	}
	return all[0], true
}

// hostOnly "http://192.168.43.4:9111/" -> "192.168.43.4"
func hostOnly(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		s = h
	}
	if net.ParseIP(s).To4() == nil {
		return ""
	}
	return s
}

// IsElevated 当前进程有没有管理员权限（UAC 提升过的令牌）。
func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// netArgs 把 netsh 的一串参数拼成命令行（给提权分支用），
// 网卡名带空格时必须自己带引号，否则 cmd 会在空格处断开。
func quoteName(name string) string { return `"` + name + `"` }

// netshRun 直接跑 netsh（当前进程已是管理员时走这条）。
func netshRun(args ...string) error {
	cmd := exec.Command("netsh", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("netsh 执行失败: %s", msg)
	}
	return nil
}

// netshElevated 用 UAC 提权跑一条命令（当前不是管理员时走这条）。
// 返回 nil 只代表「UAC 已放行、命令已发出」，结果要靠重新读注册表确认。
func netshElevated(cmdline string) error {
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString("cmd.exe")
	if err != nil {
		return err
	}
	args, err := windows.UTF16PtrFromString(`/c ` + cmdline)
	if err != nil {
		return err
	}
	if err := windows.ShellExecute(0, verb, file, args, nil, windows.SW_HIDE); err != nil {
		if err == windows.ERROR_CANCELLED {
			return fmt.Errorf("已取消管理员授权")
		}
		return fmt.Errorf("无法请求管理员权限: %w", err)
	}
	return nil
}

// staticCmd 生成「切成固定 IP」的命令串；dns 为空就不动 DNS。
func staticCmd(a LanIPv4) string {
	var b strings.Builder
	fmt.Fprintf(&b, `netsh interface ipv4 set address name=%s source=static address=%s mask=%s`,
		quoteName(a.Name), a.IP, a.Mask)
	if a.Gateway != "" {
		fmt.Fprintf(&b, ` gateway=%s gwmetric=1`, a.Gateway)
	}
	for i, d := range a.DNS {
		if i == 0 {
			// register=primary：把 DNS 也一并向注册表登记，否则重启后又回到自动
			fmt.Fprintf(&b, ` & netsh interface ipv4 set dnsservers name=%s source=static address=%s register=primary`,
				quoteName(a.Name), d)
		} else {
			fmt.Fprintf(&b, ` & netsh interface ipv4 add dnsservers name=%s address=%s index=%d`,
				quoteName(a.Name), d, i+1)
		}
	}
	return b.String()
}

// dhcpCmd 生成「恢复自动获取」的命令串。
func dhcpCmd(name string) string {
	return fmt.Sprintf(`netsh interface ipv4 set address name=%s source=dhcp & netsh interface ipv4 set dnsservers name=%s source=dhcp`,
		quoteName(name), quoteName(name))
}

// FixStatic 把这块网卡按「当前值」固定下来（IP / 掩码 / 网关 / DNS 原样保留，
// 只把获取方式从 DHCP 改成手动）。非管理员时抛 UAC —— 用户只需点一次「是」，
// 不用先退出应用、再右键「以管理员身份运行」。
func (a LanIPv4) FixStatic() (elevated bool, err error) {
	if !a.Valid() {
		return false, fmt.Errorf("没读到网卡配置，无法固定")
	}
	if a.Mask == "" {
		return false, fmt.Errorf("没读到子网掩码，无法固定（请先在系统里确认这块网卡是自动获取）")
	}
	if IsElevated() {
		id := []string{"interface", "ipv4", "set", "address", "name=" + a.Name,
			"source=static", "address=" + a.IP, "mask=" + a.Mask}
		if a.Gateway != "" {
			id = append(id, "gateway="+a.Gateway, "gwmetric=1")
		}
		if err := netshRun(id...); err != nil {
			return false, err
		}
		for i, d := range a.DNS {
			if i == 0 {
				if err := netshRun("interface", "ipv4", "set", "dnsservers", "name="+a.Name,
					"source=static", "address="+d, "register=primary"); err != nil {
					return false, err
				}
			} else {
				if err := netshRun("interface", "ipv4", "add", "dnsservers", "name="+a.Name,
					"address="+d, "index="+strconv.Itoa(i+1)); err != nil {
					return false, err
				}
			}
		}
		return false, nil
	}
	return true, netshElevated(staticCmd(a))
}

// RestoreDHCP 切回自动获取（同样自带提权）。
func (a LanIPv4) RestoreDHCP() (elevated bool, err error) {
	if a.Name == "" {
		return false, fmt.Errorf("没读到网卡名，无法恢复")
	}
	if IsElevated() {
		if err := netshRun("interface", "ipv4", "set", "address", "name="+a.Name, "source=dhcp"); err != nil {
			return false, err
		}
		// DNS 恢复失败不算致命：地址已经是自动获取了，DNS 还能手改
		_ = netshRun("interface", "ipv4", "set", "dnsservers", "name="+a.Name, "source=dhcp")
		return false, nil
	}
	return true, netshElevated(dhcpCmd(a.Name))
}

// OpenNetworkSettings 打开系统「网络连接」面板（控制面板的 ncpa.cpl）：
// 用户想自己改、或想确认改完的结果时，不用再教他在设置里翻三层。
func OpenNetworkSettings() error {
	return windows.ShellExecute(0, nil, windows.StringToUTF16Ptr("ncpa.cpl"), nil, nil, windows.SW_SHOWNORMAL)
}
