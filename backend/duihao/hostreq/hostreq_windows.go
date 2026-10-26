//go:build windows

package hostreq

import "golang.org/x/sys/windows"

// diskFree 查询 dir 所在卷的剩余空间（字节）。
func diskFree(dir string) (int64, error) {
	var free, total, avail uint64
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &avail); err != nil {
		return 0, err
	}
	return int64(free), nil
}
