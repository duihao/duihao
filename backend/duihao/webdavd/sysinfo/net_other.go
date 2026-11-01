//go:build !windows

package sysinfo

// 非 Windows 对齐实现：对号 v1 只发 Windows 包，这里返回「无」即可，
// 接口保持两端一致，编译不断。
func GlobalIPv6() string { return "" }
func IPv6Outbound() bool { return false }
