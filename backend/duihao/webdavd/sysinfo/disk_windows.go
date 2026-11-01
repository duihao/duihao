//go:build windows

// Package sysinfo 本机资源探测（目前只有卷可用空间）。
//
// 监控与下载都要做同一件事：**磁盘是硬约束**。录像写满网盘、下载把系统盘
// 塞爆，都是「功能正常但产品出事」的典型，所以两者都先问一句还剩多少。
package sysinfo

import "golang.org/x/sys/windows"

// DiskFree 返回该路径所在卷的可用字节数；取不到返回 0（调用方按「未知」处理，不拦）。
func DiskFree(path string) int64 {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0
	}
	return int64(free)
}
