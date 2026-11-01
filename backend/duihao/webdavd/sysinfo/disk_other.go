//go:build !windows

// Package sysinfo 本机资源探测（非 Windows 对齐实现）。
package sysinfo

// DiskFree 非 Windows 不做磁盘守护（对号 v1 只发 Windows 包）。
func DiskFree(string) int64 { return 0 }
