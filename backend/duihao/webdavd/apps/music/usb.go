// 音乐U盘：枚举可移动盘 + 一键导出（拷贝 + 规范命名 + M3U8 + 导出历史）。
//
// 安全边界：
//   - 只允许 DRIVE_REMOVABLE 的盘符（GetDriveTypeW），固定硬盘/网络盘不出现；
//   - 目标盘与网盘根目录同卷时拒绝（防止「导出」写回自己的数据盘）；
//   - 只新增文件（存在同名同大小即跳过），绝不删除/覆盖 U 盘已有内容；
//   - 盘符入参强校验（单字母 + 冒号），目录名清洗非法字符。
package music

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
)

const driveRemovable = 2

// kernel32 里 x/sys 没包的三个卷管理 API。
var (
	procGetDriveTypeW         = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetDriveTypeW")
	procGetVolumeInformationW = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetVolumeInformationW")
)

// driveTypeOf 盘符类型（DRIVE_REMOVABLE=2；调用失败=0）。
func driveTypeOf(rootPtr *uint16) uint32 {
	r, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(rootPtr)))
	return uint32(r)
}

// driveInfo 一个可移动盘。
type driveInfo struct {
	Letter  string `json:"letter"` // "E:"
	Label   string `json:"label"`
	FS      string `json:"fs"` // FAT32 / exFAT / NTFS
	TotalMB int64  `json:"totalMB"`
	FreeMB  int64  `json:"freeMB"`
}

// driveList 枚举可移动盘（音乐U盘的候选目标）。
func driveList(c appkit.Context, w http.ResponseWriter) error {
	drives := []driveInfo{}
	for l := byte('A'); l <= 'Z'; l++ {
		root := string(l) + `:\`
		p, err := syscall.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		if driveTypeOf(p) != driveRemovable {
			continue
		}
		var d driveInfo
		d.Letter = string(l) + ":"
		var volBuf, fsBuf [261]uint16
		var serial, maxLen, flags uint32
		r, _, _ := procGetVolumeInformationW.Call(uintptr(unsafe.Pointer(p)),
			uintptr(unsafe.Pointer(&volBuf[0])), uintptr(len(volBuf)),
			uintptr(unsafe.Pointer(&serial)), uintptr(unsafe.Pointer(&maxLen)),
			uintptr(unsafe.Pointer(&flags)), uintptr(unsafe.Pointer(&fsBuf[0])), uintptr(len(fsBuf)))
		if r != 0 {
			d.Label = utf16PtrToString(&volBuf[0])
			d.FS = utf16PtrToString(&fsBuf[0])
		}
		var free, total, avail uint64
		if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &free); err == nil {
			d.TotalMB = int64(total) / 1024 / 1024
			d.FreeMB = int64(free) / 1024 / 1024
		} else {
			continue // 盘在但读不了（未格式化等）：不作为候选
		}
		drives = append(drives, d)
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"drives": drives})
	return nil
}

func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	n := 0
	for ptr := unsafe.Pointer(p); *(*uint16)(ptr) != 0; n++ {
		ptr = unsafe.Pointer(uintptr(ptr) + 2)
	}
	return string(utf16.Decode(unsafe.Slice(p, n)))
}

// ── 导出 ──

var illegalNameRe = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)

// sanitizeName 清洗文件/目录名（Windows 保留字符 + 控制字符 → 空格）。
func sanitizeName(s string) string {
	s = illegalNameRe.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	s = strings.Trim(s, ".")
	s = strings.TrimSpace(s)
	if len([]rune(s)) > 100 {
		s = string([]rune(s)[:100])
	}
	return s
}

func usbExport(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name     string   `json:"name"`  // 目标子目录名（默认歌单名 / 「音乐U盘」）
		Drive    string   `json:"drive"` // "E:" 或 "E:\"
		PlID     string   `json:"plId"`
		TrackIDs []string `json:"trackIds"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	in.Drive = strings.ToUpper(strings.TrimSuffix(strings.TrimSpace(in.Drive), `\`))
	if len(in.Drive) != 2 || in.Drive[1] != ':' || in.Drive[0] < 'A' || in.Drive[0] > 'Z' {
		return appkit.Errorf(http.StatusBadRequest, "盘符不合法")
	}
	// 目标必须是可移动盘
	p, _ := syscall.UTF16PtrFromString(in.Drive + `:\`)
	if driveTypeOf(p) != driveRemovable {
		return appkit.Errorf(http.StatusBadRequest, "目标不是可移动盘（音乐U盘只写U盘，不写固定硬盘）")
	}
	// 不许写回网盘所在卷
	if c.Root != "" {
		if vol := volumeNameOf(c.Root); vol != "" && strings.EqualFold(vol, in.Drive+`\`) {
			return appkit.Errorf(http.StatusBadRequest, "目标盘与网盘在同一磁盘，不能导出到自己")
		}
	}

	// 曲目清单：歌单序 / 显式列表 / 整个曲库
	var tracks []trk
	if in.PlID != "" {
		rows, err := c.DB.Query(`select t.path, t.title, coalesce(nullif(t.artist,''),'未知歌手'),
			t.duration, t.size from music_tracks t
			join music_pl_items pi on pi.uid=t.uid and pi.track_id=t.id and pi.pl_id=?
			where t.uid=? order by pi.pos`, in.PlID, c.User)
		if err != nil {
			return err
		}
		tracks = scanTrk(rows)
	} else if len(in.TrackIDs) > 0 {
		if len(in.TrackIDs) > 2000 {
			return appkit.Errorf(http.StatusBadRequest, "一次最多导出 2000 首")
		}
		for _, id := range in.TrackIDs {
			var t trk
			err := c.DB.QueryRow(`select path, title, coalesce(nullif(artist,''),'未知歌手'),
				duration, size from music_tracks where uid=? and id=?`, c.User, id).
				Scan(&t.path, &t.title, &t.artist, &t.dur, &t.size)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			tracks = append(tracks, t)
		}
	} else {
		rows, err := c.DB.Query(`select path, title, coalesce(nullif(artist,''),'未知歌手'),
			duration, size from music_tracks where uid=? order by artist, album, disc_no, track_no, title`, c.User)
		if err != nil {
			return err
		}
		tracks = scanTrk(rows)
	}
	if len(tracks) == 0 {
		return appkit.Errorf(http.StatusBadRequest, "没有可导出的曲目（先扫描曲库或选择歌曲）")
	}

	name := sanitizeName(in.Name)
	if name == "" {
		if in.PlID != "" {
			var pn string
			if err := c.DB.QueryRow(`select name from music_playlists where uid=? and id=?`,
				c.User, in.PlID).Scan(&pn); err == nil {
				name = sanitizeName(pn)
			}
		}
		if name == "" {
			name = "音乐U盘"
		}
	}
	dest := in.Drive + `\` + name

	// 空间预检
	var needBytes int64
	for _, t := range tracks {
		needBytes += int64(t.size)
	}
	var free, total, avail uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &free); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "读取U盘空间失败：%v", err)
	}
	if int64(needBytes) > int64(free) {
		return appkit.Errorf(http.StatusBadRequest,
			"U盘剩余空间不足：需要 %s，剩余 %s", humanBytes(needBytes), humanBytes(int64(free)))
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return appkit.Errorf(500, "创建目标目录失败：%v", err)
	}

	items, skipped, copied, warn, err := doExport(c, tracks, dest)
	if err != nil {
		return err
	}

	// 历史
	id := newID()
	_, _ = c.DB.Exec(`insert into music_exports(uid,id,label,drive,dest,items,skipped,bytes,created_at)
		values(?,?,?,?,?,?,?,?,?)`, c.User, id, name, in.Drive+`\`, dest, items, skipped, copied,
		time.Now().Format(time.RFC3339))

	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "dest": dest,
		"items": items, "skipped": skipped, "bytes": copied,
		"m3u": "playlist.m3u8", "total": len(tracks), "warn": warn})
	return nil
}

// doExport 拷贝 + 规范命名 + M3U8（从 HTTP 处理器拆出来，便于直测）。
// 出错即停（U 盘拔了/写保护），半截文件不留。
func doExport(c appkit.Context, tracks []trk, dest string) (items, skipped int, copied int64, warn string, err error) {
	m3u := strings.Builder{}
	m3u.WriteString("#EXTM3U\n")
	for i, t := range tracks {
		// 源路径必须落在 _appfiles/music 内（库数据本身可信，仍做一道防御）
		src := filepath.Join(c.Root, filepath.FromSlash(t.path))
		if !strings.HasPrefix(filepath.ToSlash(strings.ToLower(src)),
			strings.ToLower(filepath.ToSlash(filepath.Join(c.Root, musicRoot))+"/")) {
			skipped++
			continue
		}
		dstName := fmt.Sprintf("%02d - %s - %s%s", i+1,
			sanitizeName(t.title), sanitizeName(t.artist), strings.ToLower(filepath.Ext(t.path)))
		dst := filepath.Join(dest, dstName)
		if st, serr := os.Stat(dst); serr == nil && st.Size() == int64(t.size) {
			skipped++ // 同名同大小 = 已导过，跳过
		} else {
			if cerr := copyFile(src, dst, int64(t.size)); cerr != nil {
				warn = fmt.Sprintf("「%s」拷贝失败：%v", t.title, cerr)
				break
			}
			items++
			copied += int64(t.size)
		}
		fmt.Fprintf(&m3u, "#EXTINF:%d,%s - %s\n%s\n", t.dur, t.artist, t.title, dstName)
	}
	_ = os.WriteFile(filepath.Join(dest, "playlist.m3u8"), []byte(m3u.String()), 0o644)
	return items, skipped, copied, warn, nil
}

func usbHistory(c appkit.Context, w http.ResponseWriter) error {
	rows, err := c.DB.Query(`select id,label,drive,dest,items,skipped,bytes,created_at
		from music_exports where uid=? order by created_at desc limit 50`, c.User)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, label, drive, dest, created string
		var items, skipped int
		var bytes int64
		if err := rows.Scan(&id, &label, &drive, &dest, &items, &skipped, &bytes, &created); err != nil {
			return err
		}
		out = append(out, map[string]any{"id": id, "label": label, "drive": drive,
			"dest": dest, "items": items, "skipped": skipped, "bytes": bytes, "createdAt": created})
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"items": out})
	return nil
}

// ── 小工具 ──

// trk 导出清单里的一首。
type trk struct {
	path, title, artist string
	dur, size           int
}

func scanTrk(rows *sql.Rows) []trk {
	defer rows.Close()
	var out []trk
	for rows.Next() {
		var t trk
		if rows.Scan(&t.path, &t.title, &t.artist, &t.dur, &t.size) == nil {
			out = append(out, t)
		}
	}
	return out
}

// volumeNameOf 目录所在卷的根（如 E:\）。
func volumeNameOf(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	return filepath.VolumeName(abs) + `\`
}

// copyFile 带大小预检的拷贝。
func copyFile(src, dst string, size int64) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if st.Size() != size {
		return fmt.Errorf("文件大小已变化")
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dst) // 半截文件不留
	}
	return err
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d KB", n/(1<<10))
	}
}
