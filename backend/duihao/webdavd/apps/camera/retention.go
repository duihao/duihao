package camera

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
)

// 保留策略：先按天数，再按磁盘。
//
//	1) 超过 retain_days 的片段直接删（按文件名里的时间判断，不依赖索引）；
//	2) 卷可用空间 < 2GB 时，从最旧的开始继续删，直到回到 4GB 以上；
//	3) 删完把索引里「文件已不存在」的行收尸，页面不会留死链。

// listSegFiles 列出目录下的 mp4，按开始时间升序。
func listSegFiles(dir string) []segFileInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]segFileInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".mp4") {
			continue
		}
		start, ok := segName(e.Name())
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, segFileInfo{name: e.Name(), start: start, size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start.Before(out[j].start) })
	return out
}

type segFileInfo struct {
	name  string
	start time.Time
	size  int64
}

// pruneByAge 删超过 retainDays 的片段；retainDays<=0 表示不按天数删。
func pruneByAge(dir string, retainDays int, now time.Time) (files int, freed int64) {
	if retainDays <= 0 {
		return 0, 0
	}
	cutoff := now.AddDate(0, 0, -retainDays)
	for _, f := range listSegFiles(dir) {
		if f.start.After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, f.name)); err == nil {
			files++
			freed += f.size
		}
	}
	return
}

// pruneForSpace 空间不足时从最旧的删到 free 回到 want 以上，最多删 maxFiles 个。
func pruneForSpace(dir string, free, want int64, maxFiles int) (files int, freed int64) {
	if free >= want {
		return 0, 0
	}
	for _, f := range listSegFiles(dir) {
		if files >= maxFiles {
			break
		}
		if err := os.Remove(filepath.Join(dir, f.name)); err == nil {
			files++
			freed += f.size
			if free+freed >= want {
				break
			}
		}
	}
	return
}

// reapIndex 把索引里已经没有文件的片段行删掉（文件是真相）。
func reapIndex(c appkit.Context, camID string) {
	dir := camDir(c.Root, camID)
	rows, err := c.DB.Query(`select id,path from camera_segments where uid=? and cam_id=?`, c.User, camID)
	if err != nil {
		return
	}
	type row struct{ id, path string }
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.path); err == nil {
			all = append(all, r)
		}
	}
	rows.Close()
	for _, r := range all {
		full := filepath.Join(c.Root, filepath.FromSlash(r.path))
		if _, err := os.Stat(full); os.IsNotExist(err) {
			_, _ = c.DB.Exec(`delete from camera_segments where uid=? and id=?`, c.User, r.id)
		} else if !strings.HasPrefix(filepath.Clean(full), filepath.Clean(dir)) {
			// 索引里混进了别的目录 → 只删索引，不动文件
			_, _ = c.DB.Exec(`delete from camera_segments where uid=? and id=?`, c.User, r.id)
		}
	}
}

// runRetention 对某账号的所有相机做一次清理，返回汇总（页面「清理」按钮也调它）。
func (a *App) runRetention(c appkit.Context) (files int, freed int64) {
	rows, err := c.DB.Query(`select id,retain_days from cameras where uid=?`, c.User)
	if err != nil {
		return 0, 0
	}
	type camRet struct {
		id     string
		retain int
	}
	var cams []camRet
	for rows.Next() {
		var cr camRet
		if err := rows.Scan(&cr.id, &cr.retain); err == nil {
			cams = append(cams, cr)
		}
	}
	rows.Close()

	now := time.Now()
	for _, cr := range cams {
		dir := camDir(c.Root, cr.id)
		f, b := pruneByAge(dir, cr.retain, now)
		files += f
		freed += b
		if free := diskFree(c.Root); free > 0 && free < 2<<30 {
			f2, b2 := pruneForSpace(dir, free, 4<<30, 500)
			files += f2
			freed += b2
		}
		reapIndex(c, cr.id)
	}
	return files, freed
}
