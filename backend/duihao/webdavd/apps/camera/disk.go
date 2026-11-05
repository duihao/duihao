package camera

import "github.com/duihao/duihao/backend/duihao/webdavd/sysinfo"

// diskFree 该路径所在卷的可用字节数（0 = 取不到，按未知处理不拦）。
// 实现在 sysinfo 包 —— 下载模块也用同一份。
func diskFree(path string) int64 { return sysinfo.DiskFree(path) }
