// 扫描网盘 _appfiles/music 目录建曲库索引。
//
// 目录约定（与影视同款思路）：
//
//	_appfiles/music/**.mp3|flac|wav|…   递归（深度 ≤ 4）收集音频文件
//	_appfiles/music/<目录>/cover.jpg    目录封面（cover/folder/front 任一）
//	_appfiles/music/.covers/            内嵌封面的导出落点（点前缀，扫描跳过）
//
// 重扫口径：track id = sha1(path) 前 12 位；mtime+size 没变的跳过标签解析；
// 用户改过标题/歌手（custom=1）不覆盖；文件消失的曲目删除并清歌单引用。
package music

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
)

const musicRoot = "_appfiles/music" // 网盘根下的音乐目录

var audioExts = map[string]bool{
	".mp3": true, ".flac": true, ".wav": true, ".m4a": true, ".aac": true,
	".ogg": true, ".opus": true, ".ape": true, ".wma": true,
}

var coverNames = []string{"cover", "folder", "front", "album"}

// scanScan 扫描入库，返回统计（动作名 scan，函数名错开关键字）。
func scanScan(c appkit.Context) (map[string]any, error) {
	if strings.TrimSpace(c.Root) == "" {
		return nil, appkit.Errorf(500, "网盘目录不可用")
	}
	root := filepath.Join(c.Root, musicRoot)
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			_ = os.MkdirAll(root, 0o755)
			return map[string]any{"ok": true, "tracks": 0, "added": 0, "updated": 0, "removed": 0,
				"hint": "已创建 _appfiles/music 目录：把音乐文件（含子目录）放进去后再扫描"}, nil
		}
		return nil, appkit.Errorf(500, "读取 _music 失败：%v", err)
	}

	// 目录封面：目录 → 共享内路径
	dirCovers := map[string]string{}
	collectDirCovers(root, "", dirCovers)

	now := time.Now().Format(time.RFC3339)
	seen := map[string]bool{}
	added, updated, parsed := 0, 0, 0

	err := walkMusic(root, "", 0, func(full, rel string, info os.FileInfo) {
		ext := strings.ToLower(filepath.Ext(rel))
		if !audioExts[ext] {
			return
		}
		relPath := path.Join("/", musicRoot, rel)
		id := trackID(relPath)
		seen[id] = true
		dir := ""
		if i := strings.LastIndexByte(rel, '/'); i >= 0 {
			dir = rel[:i]
		}
		mtime := ""
		if info.ModTime().Year() > 1980 {
			mtime = info.ModTime().Format("2006-01-02 15:04")
		}

		// 没变（mtime+size 一致）就跳过标签解析（歌词还是要对一眼，.lrc 可能单独改过）
		var omtime string
		var osize int64
		var custom int
		var olyrics string
		e := c.DB.QueryRow(`select coalesce(mtime,''), size, custom, coalesce(lyrics,'') from music_tracks where uid=? and id=?`,
			c.User, id).Scan(&omtime, &osize, &custom, &olyrics)
		if e == nil && omtime == mtime && osize == info.Size() {
			_, _ = c.DB.Exec(`update music_tracks set dir=?, cover=coalesce(nullif(?,''),cover), lyrics=?, updated_at=?
				where uid=? and id=?`, dir, dirCovers[dir], lyricsJSON(readLRC(full)), now, c.User, id)
			updated++
			return
		}

		var t tags
		if audioExts[ext] && (ext == ".mp3" || ext == ".flac" || ext == ".wav") {
			tt, err := readTags(full, ext)
			if err == nil {
				t = tt
			}
			parsed++
		}
		title, artist := t.Title, t.Artist
		if title == "" {
			title = fallbackTitle(filepath.Base(rel))
		}
		if artist == "" {
			artist = fallbackArtist(filepath.Base(rel))
		}
		album := t.Album
		if album == "" && dir != "" {
			album = filepath.Base(dir) // 无专辑标签 → 目录名当专辑，前端好分组
		}
		cover := dirCovers[dir]
		if len(t.Cover) > 0 && len(t.Cover) < 8<<20 { // 内嵌封面落盘（8MB 封顶）
			if p, werr := saveCover(root, relPath, t.Cover, t.CoverExt); werr == nil {
				cover = p
			}
		}
		if custom == 1 { // 用户改过 → 保留标题/歌手
			title, artist = "", ""
		}
		lyrics := lyricsJSON(readLRC(full))

		if e == nil { // update
			if custom == 1 {
				_, e = c.DB.Exec(`update music_tracks set dir=?, album=?, album_artist=?, genre=?, year=?,
					track_no=?, disc_no=?, ext=?, size=?, duration=?, bitrate=?, cover=?, mtime=?, lyrics=?, updated_at=?
					where uid=? and id=?`,
					dir, album, t.AlbumArtist, t.Genre, t.Year, t.TrackNo, t.DiscNo,
					strings.TrimPrefix(ext, "."), info.Size(), t.Duration, t.Bitrate, cover, mtime, lyrics, now,
					c.User, id)
			} else {
				_, e = c.DB.Exec(`update music_tracks set dir=?, title=?, artist=?, album=?, album_artist=?,
					genre=?, year=?, track_no=?, disc_no=?, ext=?, size=?, duration=?, bitrate=?, cover=?, mtime=?, lyrics=?, updated_at=?
					where uid=? and id=?`,
					dir, title, artist, album, t.AlbumArtist, t.Genre, t.Year, t.TrackNo, t.DiscNo,
					strings.TrimPrefix(ext, "."), info.Size(), t.Duration, t.Bitrate, cover, mtime, lyrics, now,
					c.User, id)
			}
			updated++
		} else if errors.Is(e, sql.ErrNoRows) { // insert
			_, e = c.DB.Exec(`insert into music_tracks
				(uid,id,path,dir,title,artist,album,album_artist,genre,year,track_no,disc_no,
				 ext,size,duration,bitrate,cover,mtime,lyrics,created_at,updated_at)
				values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				c.User, id, relPath, dir, title, artist, album, t.AlbumArtist, t.Genre,
				t.Year, t.TrackNo, t.DiscNo, strings.TrimPrefix(ext, "."), info.Size(),
				t.Duration, t.Bitrate, cover, mtime, lyrics, now, now)
			added++
		}
		// 单文件写库失败不中断整次扫描：丢这一条的统计即可
		_ = e
	})
	if err != nil {
		return nil, appkit.Errorf(500, "扫描失败：%v", err)
	}

	// 删除文件已消失的曲目 + 清歌单引用
	removed := 0
	rows, err := c.DB.Query(`select id from music_tracks where uid=?`, c.User)
	if err != nil {
		return nil, err
	}
	var gone []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil && !seen[id] {
			gone = append(gone, id)
		}
	}
	rows.Close()
	for _, id := range gone {
		if _, err := c.DB.Exec(`delete from music_tracks where uid=? and id=?`, c.User, id); err != nil {
			return nil, err
		}
		_, _ = c.DB.Exec(`delete from music_pl_items where uid=? and track_id=?`, c.User, id)
		_, _ = c.DB.Exec(`delete from music_stats where uid=? and track_id=?`, c.User, id)
		removed++
	}

	total := 0
	if err := c.DB.QueryRow(`select count(*) from music_tracks where uid=?`, c.User).Scan(&total); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "tracks": total, "added": added,
		"updated": updated, "removed": removed, "parsed": parsed}, nil
}

// walkMusic 递归走音频目录（深度 ≤ 4，跳过点前缀与符号链接）。
func walkMusic(dir, rel string, depth int, fn func(full, rel string, info os.FileInfo)) error {
	if depth > 4 {
		return nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range ents {
		if e.Type()&os.ModeSymlink != 0 || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		r := e.Name()
		if rel != "" {
			r = rel + "/" + e.Name()
		}
		full := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if err := walkMusic(full, r, depth+1, fn); err != nil {
				return err
			}
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		fn(full, r, info)
	}
	return nil
}

// collectDirCovers 收集各目录的封面图（cover/folder/front 任一 + 常见图片后缀）。
func collectDirCovers(root, rel string, out map[string]string) {
	full := root
	if rel != "" {
		full = filepath.Join(root, filepath.FromSlash(rel))
	}
	ents, err := os.ReadDir(full)
	if err != nil {
		return
	}
	firstImage := ""
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil || info.Size() < 1024 {
			continue
		}
		p := path.Join("/", musicRoot, rel, e.Name())
		if firstImage == "" {
			firstImage = p
		}
		for _, n := range coverNames {
			if base == n {
				out[rel] = p
				return
			}
		}
	}
	if rel != "" && firstImage != "" {
		out[rel] = firstImage // 目录里随便一张图也行（比没有强）
	}
}

// saveCover 内嵌封面落盘到 .covers/（内容哈希命名：重扫幂等）。返回共享内路径。
func saveCover(root, trackRel string, data []byte, ext string) (string, error) {
	sum := sha1.Sum(data)
	name := hex.EncodeToString(sum[:])[:16] + ext
	dir := filepath.Join(root, ".covers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err != nil {
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return "", err
		}
	}
	return path.Join("/", musicRoot, ".covers", name), nil
}

func trackID(p string) string {
	h := sha1.Sum([]byte(p))
	return hex.EncodeToString(h[:])[:12]
}

// fallbackTitle 文件名当曲名：去扩展名、转点为空格。
func fallbackTitle(name string) string {
	s := strings.TrimSuffix(name, filepath.Ext(name))
	s = strings.NewReplacer("_", " ").Replace(s)
	s = strings.TrimSpace(s)
	if len([]rune(s)) > 80 {
		s = string([]rune(s)[:80])
	}
	return s
}

// fallbackArtist 「歌手 - 曲名」文件名模式取歌手。
func fallbackArtist(name string) string {
	s := strings.TrimSuffix(name, filepath.Ext(name))
	if a, _, ok := strings.Cut(s, " - "); ok && a != "" && len([]rune(a)) <= 40 {
		return strings.TrimSpace(a)
	}
	return ""
}

// Rescan 供其它模块调用（下载完成）：按库同步一次，不写 HTTP 响应。
func Rescan(c appkit.Context) error {
	_, err := scanScan(c)
	return err
}
