// stats.go 共享根目录的存储统计（网盘网页左栏「存储空间」卡）。
//
// 取舍（保持简单）：磁盘总量/剩余 = 一次 GetDiskFreeSpaceEx，请求时同步取；
// 目录占用 = walk 全根目录，可能很慢（大目录），异步算 + 5 分钟缓存，
// 没算完时 Used=-1，前端隐藏「已用」只显示磁盘容量。不做全局索引。
package webdavd

import (
	"io/fs"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// volStat 一次统计快照。
type volStat struct {
	Total uint64 // 所在盘总容量（字节）
	Free  uint64 // 所在盘剩余（字节）
	Used  int64  // 根目录累计占用（字节）；-1 = 尚未算完
	At    time.Time
}

const statTTL = 5 * time.Minute

// getStat 取根目录统计：缓存新鲜直接返回；过期则同步刷磁盘部分（syscall
// 便宜），占用部分异步重算（沿用旧值先顶上），算完回填缓存。
func (s *Server) getStat(root string) *volStat {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	s.statMu.Lock()
	st := s.statCache[abs]
	s.statMu.Unlock()
	if st != nil && time.Since(st.At) <= statTTL {
		return st
	}

	st = &volStat{Used: -1}
	if old := s.statCache[abs]; old != nil {
		st.Used = old.Used // 旧占用先顶上，异步算完再覆盖
	}
	var total, free, avail uint64
	if err := windows.GetDiskFreeSpaceEx(
		windows.StringToUTF16Ptr(filepath.VolumeName(abs)+`\`), &avail, &total, &free); err == nil {
		st.Total, st.Free = total, free
	}
	st.At = time.Now()
	s.statMu.Lock()
	s.statCache[abs] = st
	s.statMu.Unlock()

	go func() {
		n, err := dirSize(abs)
		if err != nil {
			return
		}
		s.statMu.Lock()
		if cur := s.statCache[abs]; cur != nil {
			cur.Used = n
			cur.At = time.Now()
		}
		s.statMu.Unlock()
	}()
	return st
}

// statJSON 塞进文件管理器页面的存储统计（nil = 取不到，前端隐藏数字）。
func (s *Server) statJSON(root string) map[string]any {
	st := s.getStat(root)
	if st == nil || st.Total == 0 {
		return nil
	}
	return map[string]any{"total": st.Total, "free": st.Free, "used": st.Used}
}

// VolumeStatsOf 只取「盘符」的容量：root 所在卷的总量 / 剩余（一次
// GetDiskFreeSpaceEx，微秒级）。ok=false 表示取不到（盘没就绪/路径不存在）。
//
// 注意这里刻意**只算当前盘符**，不汇总任何「整块物理硬盘」或其它分区：
// 用户的网盘在 D:，界面就该说 D: 多大、还剩多少，别把 C: 也算进来。
func VolumeStatsOf(root string) (total, free uint64, ok bool) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return 0, 0, false
	}
	var avail uint64
	if err := windows.GetDiskFreeSpaceEx(
		windows.StringToUTF16Ptr(filepath.VolumeName(abs)+`\`), &avail, &total, &free); err != nil {
		return 0, 0, false
	}
	return total, free, total > 0
}

// DriveOf 取 root 所在盘符（形如 "D:"），取不到返回空串。
func DriveOf(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	return filepath.VolumeName(abs)
}

// DirUsage 递归统计目录占用，可能很慢（大目录几十秒），调用方自己决定
// 是否异步/缓存。ok=false 表示 walk 失败。
func DirUsage(root string) (int64, bool) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return 0, false
	}
	n, err := dirSize(abs)
	if err != nil {
		return 0, false
	}
	return n, true
}

// StorageStatsOf 独立统计（不依赖运行中的 Server）：磁盘总量/剩余同步取，
// 目录占用同步 walk。调用方自己加缓存。
func StorageStatsOf(root string) (total, free uint64, used int64) {
	total, free, ok := VolumeStatsOf(root)
	if !ok {
		return 0, 0, -1
	}
	used = -1
	if n, ok := DirUsage(root); ok {
		used = n
	}
	return total, free, used
}

// dirSize 递归累计目录大小；不可读的子项跳过（网络盘/权限目录不该让整体失败）。
func dirSize(root string) (int64, error) {
	var sum int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, ierr := d.Info(); ierr == nil {
				sum += info.Size()
			}
		}
		return nil
	})
	return sum, err
}
