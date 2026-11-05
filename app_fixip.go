package main

// 「固定内网地址」弹窗的后端（/?w=fixip）。
//
// 做什么：把当前正在用的那块网卡从「自动获取（DHCP）」切成「固定 IP」，
// 用的就是它现在拿到的 IP / 掩码 / 网关 / DNS —— 不改值，只改获取方式。
// 这样 NAS 的地址不再随路由器租约变化，手机、电视、别的电脑收藏的链接不会哪天失效。
//
// 为什么需要管理员：改网卡 IPv4 配置是系统级操作。普通权限下我们**不**让用户
// 退出应用再右键「以管理员身份运行」（易等那种三步引导），而是直接对 netsh
// 弹一次 UAC —— 用户点一下「是」就完事，应用本身不用重启（重启会把 NAS 服务
// 一起带走，正在传的文件会断）。
//
// 状态判断一律「改完再读一遍」，不信 netsh 的返回：提权是另一个进程，
// 它成没成功只有注册表知道。

import (
	"fmt"
	"strings"
	"time"

	"github.com/duihao/duihao/backend/log"

	"github.com/duihao/duihao/backend/duihao/webdavd/sysinfo"
)

// fixIpHint 上一次读取时用的线索（NAS 的访问地址）：多网卡时用它定位
// 「NAS 走的是哪块网卡」。点「确认」时状态里没带 hint，所以要记住。
var fixIpHint string

// FixIPState 弹窗一次渲染所需的全部状态。
type FixIPState struct {
	Found    bool     `json:"found"`    // 找到可用网卡（false 时其余字段为空）
	Name     string   `json:"name"`     // 网卡友好名（WLAN / 以太网）
	IP       string   `json:"ip"`       // 当前 IPv4
	Mask     string   `json:"mask"`     // 子网掩码
	Gateway  string   `json:"gateway"`  // 默认网关（可能为空）
	DNS      []string `json:"dns"`      // 当前 DNS（可能为空）
	DHCP     bool     `json:"dhcp"`     // true = 自动获取
	Elevated bool     `json:"elevated"` // 当前进程是不是管理员
	Pending  bool     `json:"pending"`  // 刚发起过 UAC，系统可能还没落盘
	Hint     string   `json:"hint"`     // 本次用来定位网卡的 NAS 地址
}

// fixIpRead 读当前状态；hint 为空时沿用上次的。
func fixIpRead(hint string) FixIPState {
	if strings.TrimSpace(hint) != "" {
		fixIpHint = hint
	}
	st := FixIPState{Hint: fixIpHint, Elevated: sysinfo.IsElevated()}
	a, ok := sysinfo.LanIPv4Current(fixIpHint)
	if !ok {
		return st
	}
	st.Found = true
	st.Name = a.Name
	st.IP = a.IP
	st.Mask = a.Mask
	st.Gateway = a.Gateway
	st.DNS = a.DNS
	st.DHCP = a.DHCP
	if st.DNS == nil {
		st.DNS = []string{}
	}
	return st
}

// FixIpLoad 打开弹窗时先读一遍（只读，不触发任何提权）。
// hint 传 NAS 的当前访问地址（如 http://192.168.43.4:9111），用它挑网卡。
func (a *App) FixIpLoad(hint string) (*FixIPState, error) {
	st := fixIpRead(hint)
	log.Writef("fixip: load hint=%q found=%v name=%s ip=%s dhcp=%v elevated=%v",
		st.Hint, st.Found, st.Name, st.IP, st.DHCP, st.Elevated)
	return &st, nil
}

// fixIpWait 等一下再读：提权后的 netsh 是另一个进程，注册表落盘有几毫秒到
// 一两秒的延迟。只等「取到期望状态」，最多 timeout。
func fixIpWait(hint string, wantDHCP bool, timeout time.Duration) FixIPState {
	deadline := time.Now().Add(timeout)
	st := fixIpRead(hint)
	for time.Now().Before(deadline) {
		if !st.Found || st.DHCP == wantDHCP {
			return st
		}
		time.Sleep(250 * time.Millisecond)
		st = fixIpRead(hint)
	}
	return st
}

// FixIpApply 固定当前地址。已经是固定的直接返回（幂等，重复点不会再来一次 UAC）。
func (a *App) FixIpApply() (*FixIPState, error) {
	st := fixIpRead("")
	if !st.Found {
		return &st, fmt.Errorf("没找到可用的内网网卡：请先连上 Wi-Fi 或插好网线")
	}
	if !st.DHCP {
		return &st, nil
	}
	a4, ok := sysinfo.LanIPv4Current(st.Hint)
	if !ok {
		return &st, fmt.Errorf("网卡状态读不到了，刷新后再试")
	}
	elevated, err := a4.FixStatic()
	if err != nil {
		log.Writef("fixip: apply failed: %v", err)
		return &st, err
	}
	if elevated {
		// UAC 已放行（ShellExecute 会等用户点完才返回），等系统把新配置落盘
		log.Writef("fixip: netsh 已提权执行，等待落盘 (name=%s ip=%s)", a4.Name, a4.IP)
		after := fixIpWait(st.Hint, false, 6*time.Second)
		after.Pending = after.DHCP
		log.Writef("fixip: after apply dhcp=%v", after.DHCP)
		return &after, nil
	}
	after := fixIpRead(st.Hint)
	log.Writef("fixip: applied (admin) dhcp=%v", after.DHCP)
	return &after, nil
}

// FixIpRestoreDHCP 恢复自动获取（改坏了/换了路由器时的退路）。
func (a *App) FixIpRestoreDHCP() (*FixIPState, error) {
	st := fixIpRead("")
	if !st.Found {
		return &st, fmt.Errorf("没找到可用的内网网卡")
	}
	if st.DHCP {
		return &st, nil
	}
	a4, ok := sysinfo.LanIPv4Current(st.Hint)
	if !ok {
		return &st, fmt.Errorf("网卡状态读不到了，刷新后再试")
	}
	elevated, err := a4.RestoreDHCP()
	if err != nil {
		log.Writef("fixip: restore failed: %v", err)
		return &st, err
	}
	if elevated {
		after := fixIpWait(st.Hint, true, 8*time.Second)
		after.Pending = !after.DHCP
		log.Writef("fixip: after restore dhcp=%v", after.DHCP)
		return &after, nil
	}
	after := fixIpRead(st.Hint)
	return &after, nil
}

// FixIpOpenNetworkSettings 打开系统「网络连接」面板（ncpa.cpl）。
func (a *App) FixIpOpenNetworkSettings() error {
	return sysinfo.OpenNetworkSettings()
}
