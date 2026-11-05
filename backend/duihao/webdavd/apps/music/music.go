// Package music 音乐（M1）：扫网盘 _appfiles/music 建曲库、按标签聚合歌手/专辑、
// 歌单、浏览器播放，以及核心卖点「音乐U盘」——把歌单一键拷进 U 盘给车机/音响。
//
// 接口约定（/-api/music/<action>，POST JSON；读动作也收 POST）：
//
//	scan          {}                        → {ok,tracks,added,updated,removed,parsed,hint?}
//	list          {q?,artist?,album?,plId?,sort?,page?,pageSize?}
//	                → {items:[…],total,facets:{artists,albums},pages,page}
//	pl.list       {}                        → {playlists:[{id,name,note,count}]}
//	pl.save       {id?,name,note?}          → {id}
//	pl.delete     {id}                      → {ok}
//	pl.items      {id}                      → {items:[…]}（歌单内按 pos 排）
//	pl.add        {id,trackIds:[…]}         → {ok}
//	pl.remove     {id,trackIds:[…]}         → {ok}
//	stats.play    {id}                      → {ok}（播放计数 +1，最近播放排序用）
//	lyrics        {id}                      → {items:[{t,text}],has}（卡拉OK歌词，扫描时从同名 .lrc 解析入库）
//	recent.list   {}                        → {items:[…]}（最近播放 50）
//	drive.list    {}                        → {drives:[{letter,label,fs,totalMB,freeMB}]}
//	usb.export    {name,drive,plId?,trackIds?[]} → {ok,items,skipped,bytes,m3u,warn?}
//	usb.history   {}                        → {items:[…]}
//	ping
//
// 口径：
//   - 标签来自文件本身，扫描写库；播放直走 WebDAV 路径（Range 天然支持拖进度）；
//   - 歌手/专辑聚合按标签（无专辑标签用目录名兜底），不建冗余表；
//   - 音乐U盘 = 拷贝 + 规范命名 + M3U8，不转码（车机不认的格式跳过并提示）；
//   - 导出只往 U 盘新增文件，绝不改动 U 盘已有内容（同名同大小 = 跳过）。
package music

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
	"github.com/duihao/duihao/backend/duihao/webdavd/webui"
)

type App struct{}

func New() *App { return &App{} }

func (a *App) ID() string    { return "music" }
func (a *App) Title() string { return "音乐" }

func (a *App) Page(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	return webui.Render(w, webui.Page{App: a.ID(), Title: a.Title(), User: c.User, Writable: c.Writable, Role: c.Role, Perms: c.Perms})
}

func (a *App) API(c appkit.Context, w http.ResponseWriter, r *http.Request, action string) error {
	switch action {
	case "ping":
		appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "app": a.ID(), "user": c.User})
		return nil
	case "scan":
		res, err := scanScan(c)
		if err != nil {
			return err
		}
		appkit.JSON(w, http.StatusOK, res)
		return nil
	case "list":
		return list(c, w, r)
	case "facets":
		return facets(c, w)
	case "pl.list":
		return plList(c, w)
	case "pl.save":
		return plSave(c, w, r)
	case "pl.delete":
		return plDelete(c, w, r)
	case "pl.items":
		return plItems(c, w, r)
	case "pl.add":
		return plAdd(c, w, r)
	case "pl.remove":
		return plRemove(c, w, r)
	case "stats.play":
		return statsPlay(c, w, r)
	case "lyrics":
		return lyricsGet(c, w, r)
	case "recent.list":
		return recentList(c, w)
	case "drive.list":
		return driveList(c, w)
	case "usb.export":
		return usbExport(c, w, r)
	case "usb.history":
		return usbHistory(c, w)
	}
	return appkit.Errorf(http.StatusNotFound, "未知接口: %s", action)
}

// ── list ──

func list(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Q        string `json:"q"`
		Artist   string `json:"artist"`
		Album    string `json:"album"`
		PlID     string `json:"plId"`
		Sort     string `json:"sort"` // default(专辑序) / added / title / artist
		Page     int    `json:"page"`
		PageSize int    `json:"pageSize"`
	}
	if err := decodeOpt(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	page, size := clampPage(in.Page, in.PageSize)

	// 首次进入自动扫描一次（与影视同款；只读账号不做）
	if c.Writable && strings.TrimSpace(c.Root) != "" {
		var n int
		if err := c.DB.QueryRow(`select count(*) from music_tracks where uid=?`, c.User).Scan(&n); err == nil && n == 0 {
			_, _ = scanScan(c)
		}
	}

	// 参数顺序 = 占位符在 SQL 里出现的顺序：join 在 where 之前
	join := ""
	args := []any{}
	if in.PlID != "" {
		join = ` join music_pl_items p on p.uid=t.uid and p.track_id=t.id and p.pl_id=?`
		args = append(args, in.PlID)
	}
	where := ` where t.uid=?`
	args = append(args, c.User)
	if in.Q != "" {
		where += ` and (t.title like ? or t.artist like ? or t.album like ?)`
		like := "%" + in.Q + "%"
		args = append(args, like, like, like)
	}
	if in.Artist != "" {
		where += ` and t.artist=?`
		args = append(args, in.Artist)
	}
	if in.Album != "" {
		where += ` and t.album=?`
		args = append(args, in.Album)
	}
	order := ` order by t.artist, t.album, t.disc_no, t.track_no, t.title`
	switch in.Sort {
	case "added":
		order = ` order by t.created_at desc, t.title`
	case "title":
		order = ` order by t.title`
	case "artist":
		order = ` order by t.artist, t.title`
	}
	if in.PlID != "" {
		order = ` order by p.pos`
	}

	var total int
	if err := c.DB.QueryRow(`select count(*) from music_tracks t`+join+where, args...).Scan(&total); err != nil {
		return err
	}
	q := `select t.id,t.path,t.title,t.artist,t.album,t.ext,t.size,t.duration,t.cover,t.track_no,t.year,
		coalesce(s.play_count,0),coalesce(s.last_at,'')
		from music_tracks t` + join + `
		left join music_stats s on s.uid=t.uid and s.track_id=t.id` +
		where + order + ` limit ? offset ?`
	rows, err := c.DB.Query(q, append(args, size, (page-1)*size)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []rowItem{}
	for rows.Next() {
		var it rowItem
		var cover any
		if err := rows.Scan(&it.ID, &it.Path, &it.Title, &it.Artist, &it.Album, &it.Ext,
			&it.Size, &it.Duration, &cover, &it.TrackNo, &it.Year, &it.PlayCount, &it.LastAt); err != nil {
			return err
		}
		if s, ok := cover.(string); ok && s != "" {
			it.Cover = fileURL(s)
		}
		it.URL = fileURL(it.Path)
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	appkit.JSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "page": page, "pageSize": size,
		"pages": (total + size - 1) / size,
	})
	return nil
}

// facets 左栏聚合：歌手 / 专辑 chips（带计数）。
func facets(c appkit.Context, w http.ResponseWriter) error {
	artists, err := facetRows(c, `select coalesce(nullif(artist,''),'未知歌手') as g, count(*) as n
		from music_tracks where uid=? group by g order by n desc, g limit 200`, c.User)
	if err != nil {
		return err
	}
	albums, err := facetRows(c, `select coalesce(nullif(album,''),'未知专辑') as g, count(*) as n
		from music_tracks where uid=? group by g order by n desc, g limit 200`, c.User)
	if err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"artists": artists, "albums": albums})
	return nil
}

func facetRows(c appkit.Context, q string, args ...any) ([]map[string]any, error) {
	rows, err := c.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"name": name, "count": n})
	}
	return out, rows.Err()
}

// ── stats ──

// statsPlay 播放计数 +1（前端 audio onplay 时调一次）。
func statsPlay(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil || strings.TrimSpace(in.ID) == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id")
	}
	now := time.Now().Format(time.RFC3339)
	if _, err := c.DB.Exec(`insert into music_stats(uid,track_id,play_count,last_at) values(?,?,1,?)
		on conflict(uid,track_id) do update set play_count=play_count+1, last_at=excluded.last_at`,
		c.User, in.ID, now); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// recentList 最近播放。
func recentList(c appkit.Context, w http.ResponseWriter) error {
	items, err := queryTracks(c, `select `+trackCols+` from music_tracks t
		join music_stats s on s.uid=t.uid and s.track_id=t.id
		where t.uid=? order by s.last_at desc limit 50`, c.User)
	if err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

// trackCols 列表行的列清单（与 queryTracks 的 Scan 顺序一致）。
const trackCols = `t.id,t.path,t.title,t.artist,t.album,t.ext,t.size,t.duration,t.cover,t.track_no,t.year`

// queryTracks 通用曲目查询（select 列必须是 trackCols）。
func queryTracks(c appkit.Context, q string, args ...any) ([]rowItem, error) {
	rows, err := c.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []rowItem{}
	for rows.Next() {
		var it rowItem
		var cover any
		if err := rows.Scan(&it.ID, &it.Path, &it.Title, &it.Artist, &it.Album, &it.Ext,
			&it.Size, &it.Duration, &cover, &it.TrackNo, &it.Year); err != nil {
			return nil, err
		}
		if s, ok := cover.(string); ok && s != "" {
			it.Cover = fileURL(s)
		}
		it.URL = fileURL(it.Path)
		out = append(out, it)
	}
	return out, rows.Err()
}

// rowItem 列表行。
type rowItem struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Album     string `json:"album"`
	Ext       string `json:"ext"`
	Size      int64  `json:"size"`
	Duration  int    `json:"duration"`
	Cover     string `json:"cover"`
	TrackNo   int    `json:"trackNo"`
	Year      int    `json:"year"`
	PlayCount int    `json:"playCount"`
	LastAt    string `json:"lastAt"`
}

// ── 小工具 ──

func clampPage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 100
	}
	if size > 200 {
		size = 200
	}
	return page, size
}

func decodeOpt(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}

// newID 短随机 id（歌单 / 导出记录用）。
func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("t%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// fileURL 转义成可直接给 src 的 URL 路径。
func fileURL(rel string) string {
	return (&url.URL{Path: rel}).EscapedPath()
}
