// 扫描网盘 _appfiles/video 目录建影视索引（S4）。
//
// 目录约定：
//
//	_appfiles/video/<分类>/<影片目录>/*.mp4     → 目录型影片（目录内全部视频文件 = 选集）
//	_appfiles/video/<分类>/<影片>.mp4           → 文件型影片（同名 .jpg/.png 当海报）
//	_appfiles/video/<影片>.mp4                  → 散文件归「未分类」
//
// 重扫口径：影片 id = sha1(分类/名称) 前 12 位，稳定不变；每次扫描只更新
// 文件列表/海报/大小/时间与缺失项，用户填写的字段（评分/简介/自定义标题…）不覆盖；
// 文件已全部消失的影片直接删除。
package video

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
)

const videoRoot = "_appfiles/video" // 网盘根下的影视目录（模块附件区 video 子目录）

var (
	videoExts = map[string]bool{
		".mp4": true, ".mkv": true, ".avi": true, ".mov": true, ".wmv": true,
		".flv": true, ".ts": true, ".m2ts": true, ".webm": true, ".mpg": true,
		".mpeg": true, ".m4v": true, ".rmvb": true, ".rm": true, ".3gp": true,
		".vob": true, ".ogv": true,
	}
	imageExts = map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".bmp": true, ".gif": true,
	}
	posterNames  = []string{"poster", "cover", "folder", "thumb"}
	yearRe       = regexp.MustCompile(`[\(（](\d{4})[\)）]`)
	yearReLoose  = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	epRe         = regexp.MustCompile(`(?i)(?:s\d{1,2}e(\d{1,3}))|(?:ep?(\d{1,3}))|(?:第\s*(\d{1,4})\s*[集话回])|(?:^(\d{1,3})$)`)
	cleanNameRe  = regexp.MustCompile(`[\.\_]+`)
	seriesHintRe = regexp.MustCompile(`(?i)s\d{1,2}e\d{1,3}|ep?\d{1,3}|第\s*\d+\s*[集话回]`)
)

// vfile 影片下的一个视频文件。
type vfile struct {
	Name string `json:"name"`
	Path string `json:"path"` // 共享内斜杠路径
	URL  string `json:"url"`  // 已转义，可直接给 <video src>
	Ep   int    `json:"ep"`   // 推断集数（0 = 未知）
	Size int64  `json:"size"`
}

// scanItem 扫描得到的一部影片（写库前形态）。
type scanItem struct {
	ID     string
	Cat    string
	Title  string
	Year   int
	Kind   string
	Poster string // 斜杠路径
	Files  []vfile
	Size   int64
	MTime  string
}

// scan 扫描入库。返回统计。
func scan(c appkit.Context) (map[string]any, error) {
	if strings.TrimSpace(c.Root) == "" {
		return nil, appkit.Errorf(500, "网盘目录不可用")
	}
	root := filepath.Join(c.Root, videoRoot)
	ents, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			// 顺手把目录建出来：用户在网盘里直接往 _appfiles/video 下放分类目录即可
			_ = os.MkdirAll(root, 0o755)
			return map[string]any{"ok": true, "cats": 0, "items": 0, "added": 0, "updated": 0,
					"removed": 0, "hint": "已创建 _appfiles/video 目录：把视频按分类放进去（_appfiles/video/电影/…）后再扫描"},
				nil
		}
		return nil, appkit.Errorf(500, "读取 _video 失败：%v", err)
	}

	now := time.Now().Format(time.RFC3339)
	seen := map[string]bool{}
	cats := []string{}
	added, updated := 0, 0

	for _, e := range ents {
		if e.Type()&os.ModeSymlink != 0 || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !e.IsDir() {
			// 根下散文件 → 未分类
			if isVideo(e.Name()) {
				info, _ := e.Info()
				it := buildFileItem("", e.Name(), root, info) // dir 传根：允许根下同名海报
				if n, err := upsertItem(c, it, now, seen); err != nil {
					return nil, err
				} else {
					added += n.added
					updated += n.updated
				}
			}
			continue
		}
		cat := e.Name()
		cats = append(cats, cat)
		catDir := filepath.Join(root, cat)
		sub, err := os.ReadDir(catDir)
		if err != nil {
			continue // 分类目录读不了就跳过，不中断整次扫描
		}
		for _, se := range sub {
			if se.Type()&os.ModeSymlink != 0 || strings.HasPrefix(se.Name(), ".") {
				continue
			}
			var it *scanItem
			if se.IsDir() {
				it = buildDirItem(cat, filepath.Join(catDir, se.Name()), se.Name())
			} else if isVideo(se.Name()) {
				info, _ := se.Info()
				it = buildFileItem(cat, se.Name(), catDir, info)
			}
			if it == nil {
				continue
			}
			n, err := upsertItem(c, it, now, seen)
			if err != nil {
				return nil, err
			}
			added += n.added
			updated += n.updated
		}
	}

	// 分类表：按名称排序写入（排序即扫出顺序）
	sort.Strings(cats)
	if _, err := c.DB.Exec(`delete from video_cats where uid=?`, c.User); err != nil {
		return nil, err
	}
	for i, name := range cats {
		if _, err := c.DB.Exec(`insert or ignore into video_cats(uid,name,sort) values(?,?,?)`,
			c.User, name, i*10); err != nil {
			return nil, err
		}
	}

	// 删除文件已全部消失的影片
	removed := 0
	rows, err := c.DB.Query(`select id from video_items where uid=?`, c.User)
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
		if _, err := c.DB.Exec(`delete from video_items where uid=? and id=?`, c.User, id); err != nil {
			return nil, err
		}
		removed++
	}

	// 顺带清理指向已删影片的进度行
	_, _ = c.DB.Exec(`delete from video_progress where uid=? and item_id not in (select id from video_items where uid=?)`, c.User, c.User)

	total := 0
	if err := c.DB.QueryRow(`select count(*) from video_items where uid=?`, c.User).Scan(&total); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "cats": len(cats), "items": total,
		"added": added, "updated": updated, "removed": removed}, nil
}

// countRes upsert 结果。
type countRes struct{ added, updated int }

// upsertItem 写入一条影片；返回是否新增。保留用户字段。
func upsertItem(c appkit.Context, it *scanItem, now string, seen map[string]bool) (countRes, error) {
	var res countRes
	seen[it.ID] = true
	filesJSON, _ := json.Marshal(it.Files)

	var custom int
	var createdAt string
	err := c.DB.QueryRow(`select custom, created_at from video_items where uid=? and id=?`,
		c.User, it.ID).Scan(&custom, &createdAt)
	switch {
	case err == nil:
		// 用户改过标题 → 保留磁盘名之外的一切照常更新（标题除外）
		if custom == 1 {
			_, err = c.DB.Exec(`update video_items set cat_name=?, poster=?, files=?, size=?, mtime=?, updated_at=?
				where uid=? and id=?`,
				it.Cat, ns(it.Poster), string(filesJSON), it.Size, it.MTime, now, c.User, it.ID)
		} else {
			_, err = c.DB.Exec(`update video_items set cat_name=?, title=?, year=?, kind=?, poster=?, files=?, size=?, mtime=?, updated_at=?
				where uid=? and id=?`,
				it.Cat, it.Title, it.Year, it.Kind, ns(it.Poster), string(filesJSON), it.Size, it.MTime, now, c.User, it.ID)
		}
		res.updated = 1
		return res, err

	case errors.Is(err, sql.ErrNoRows):
		_, err = c.DB.Exec(`insert into video_items
			(uid,id,cat_name,title,custom,year,kind,poster,files,size,mtime,created_at,updated_at)
			values(?,?,?,?,0,?,?,?,?,?,?,?,?)`,
			c.User, it.ID, it.Cat, it.Title, it.Year, it.Kind, ns(it.Poster),
			string(filesJSON), it.Size, it.MTime, now, now)
		res.added = 1
		return res, err

	default:
		return res, err
	}
}

func isNoRows(err error) bool { return err.Error() == "sql: no rows in result set" }

func ns(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// buildDirItem 目录型影片：目录内递归（深度≤3）收集视频文件。
func buildDirItem(cat, full, name string) *scanItem {
	files := walkVideos(full, cat+"/"+name, 0)
	if len(files) == 0 {
		return nil
	}
	var size int64
	for _, f := range files {
		size += f.Size
	}
	poster := findPoster(full, cat, name)
	kind := "movie"
	if len(files) > 1 || seriesHintRe.MatchString(name) {
		kind = "series"
	}
	sortFiles(files)
	return &scanItem{
		ID: itemID(cat, name), Cat: cat, Title: cleanTitle(name),
		Year: extractYear(name), Kind: kind, Poster: poster,
		Files: files, Size: size,
	}
}

// buildFileItem 文件型影片：分类目录（或 _video 根）下的单个视频文件。
func buildFileItem(cat, name, dir string, info os.FileInfo) *scanItem {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	rel := path.Join("/", videoRoot, cat, name)
	ep := extractEp(name)
	f := vfile{Name: name, Path: rel, URL: fileURL(rel), Ep: ep}
	if info != nil {
		f.Size = info.Size()
	}
	poster := ""
	if dir != "" {
		poster = findNamedPoster(dir, base, cat)
	}
	kind := "movie"
	if ep > 0 || seriesHintRe.MatchString(base) {
		kind = "series"
	}
	it := &scanItem{
		ID: itemID(cat, base), Cat: cat, Title: cleanTitle(base),
		Year: extractYear(base), Kind: kind, Poster: poster,
		Files: []vfile{f},
	}
	if info != nil {
		it.Size = info.Size()
		if info.ModTime().Year() > 1980 {
			it.MTime = info.ModTime().Format("2006-01-02 15:04")
		}
	}
	return it
}

// walkVideos 递归收集目录里的视频文件（深度限制防失控）。
func walkVideos(dir, relBase string, depth int) []vfile {
	if depth > 3 {
		return nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []vfile
	for _, e := range ents {
		if e.Type()&os.ModeSymlink != 0 || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			out = append(out, walkVideos(p, relBase+"/"+e.Name(), depth+1)...)
			continue
		}
		if !isVideo(e.Name()) {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		rel := path.Join("/", videoRoot, relBase, e.Name())
		out = append(out, vfile{Name: e.Name(), Path: rel, URL: fileURL(rel),
			Ep: extractEp(e.Name()), Size: info.Size()})
	}
	return out
}

// findPoster 目录型影片海报：poster/cover/folder/thumb 任一图片名 → 目录里第一张图片。
func findPoster(dir, cat, name string) string {
	for _, n := range posterNames {
		for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
			p := filepath.Join(dir, n+ext)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Size() > 1024 {
				return path.Join("/", videoRoot, cat, name, n+ext)
			}
		}
	}
	// 兜底：目录里任意图片
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if imageExts[ext] {
			info, ierr := e.Info()
			if ierr == nil && info.Size() > 1024 {
				return path.Join("/", videoRoot, cat, name, e.Name())
			}
		}
	}
	return ""
}

// findNamedPoster 文件型影片海报：同名 .jpg/.png（可带 -poster/-cover 后缀）。
// cat 为空表示文件直接放在 _appfiles/video 根下。
func findNamedPoster(dir, base, cat string) string {
	cands := []string{base + ".jpg", base + ".jpeg", base + ".png", base + ".webp",
		base + "-poster.jpg", base + "-poster.png", base + "-cover.jpg"}
	for _, n := range cands {
		p := filepath.Join(dir, n)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Size() > 1024 {
			return path.Join("/", videoRoot, cat, n)
		}
	}
	return ""
}

func isVideo(name string) bool { return videoExts[strings.ToLower(filepath.Ext(name))] }

func fileURL(rel string) string { return (&url.URL{Path: rel}).EscapedPath() }

// itemID 影片 id：sha1(分类/名称) 前 12 位（重扫稳定、目录改名才变）。
func itemID(cat, name string) string {
	h := sha1.Sum([]byte(cat + "/" + name))
	return hex.EncodeToString(h[:])[:12]
}

// cleanTitle 展示名：去掉括号年份、点/下划线分隔转空格，保留中文原名。
func cleanTitle(name string) string {
	s := yearRe.ReplaceAllString(strings.TrimSpace(name), " ")
	s = cleanNameRe.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > 80 {
		s = string([]rune(s)[:80])
	}
	return s
}

// extractYear 从名称提取年份：优先括号里的（并从标题剔除括号段），否则就近匹配。
func extractYear(name string) int {
	if m := yearRe.FindStringSubmatch(name); m != nil {
		return atoi4(m[1])
	}
	if m := yearReLoose.FindStringSubmatch(name); m != nil {
		return atoi4(m[1])
	}
	return 0
}

// extractEp 推断集数：S01E02 / EP3 / 第2集 / 纯数字文件名。
func extractEp(name string) int {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if m := epRe.FindStringSubmatch(base); m != nil {
		for _, g := range m[1:] {
			if g != "" {
				return atoi4(g)
			}
		}
	}
	return 0
}

func atoi4(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
		if n > 9999 {
			return 9999
		}
	}
	return n
}

// sortFiles 选集排序：有集数按集数，没有按文件名（自然序近似：先数字后字符串）。
func sortFiles(fs []vfile) {
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Ep != fs[j].Ep && fs[i].Ep > 0 && fs[j].Ep > 0 {
			return fs[i].Ep < fs[j].Ep
		}
		return strings.ToLower(fs[i].Name) < strings.ToLower(fs[j].Name)
	})
}

// Rescan 供其它模块调用（下载完成）：按库同步一次，不写 HTTP 响应。
// 影视页原本只在「空库」时懒同步一次，所以外部把文件放进来之后必须显式喊一声。
func Rescan(c appkit.Context) error {
	_, err := scan(c)
	return err
}
