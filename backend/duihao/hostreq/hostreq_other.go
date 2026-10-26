//go:build !windows

package hostreq

import "syscall"

// diskFree 非 Windows 降级实现（v1 仅承诺 Windows）。
func diskFree(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail * uint64(st.Bsize)), nil
}
