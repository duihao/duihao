// 歌单：CRUD 与曲目增删（music_playlists / music_pl_items）。
package music

import (
	"net/http"
	"strings"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
)

func plList(c appkit.Context, w http.ResponseWriter) error {
	rows, err := c.DB.Query(`select p.id, p.name, p.note, p.created_at,
		(select count(*) from music_pl_items i where i.uid=p.uid and i.pl_id=p.id)
		from music_playlists p where p.uid=? order by p.created_at`, c.User)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, note, created string
		var n int
		if err := rows.Scan(&id, &name, &note, &created, &n); err != nil {
			return err
		}
		out = append(out, map[string]any{"id": id, "name": name, "note": note,
			"createdAt": created, "count": n})
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"playlists": out})
	return nil
}

func plSave(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Note string `json:"note"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return appkit.Errorf(http.StatusBadRequest, "歌单名称不能为空")
	}
	if len([]rune(in.Name)) > 40 {
		return appkit.Errorf(http.StatusBadRequest, "歌单名称过长")
	}
	now := time.Now().Format(time.RFC3339)
	if in.ID != "" {
		res, err := c.DB.Exec(`update music_playlists set name=?, note=?, updated_at=? where uid=? and id=?`,
			in.Name, strings.TrimSpace(in.Note), now, c.User, in.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return appkit.Errorf(http.StatusNotFound, "歌单不存在")
		}
		appkit.JSON(w, http.StatusOK, map[string]any{"id": in.ID})
		return nil
	}
	id := newID()
	if _, err := c.DB.Exec(`insert into music_playlists(uid,id,name,note,created_at,updated_at)
		values(?,?,?,?,?,?)`, c.User, id, in.Name, strings.TrimSpace(in.Note), now, now); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"id": id})
	return nil
}

func plDelete(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil || strings.TrimSpace(in.ID) == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id")
	}
	if _, err := c.DB.Exec(`delete from music_playlists where uid=? and id=?`, c.User, in.ID); err != nil {
		return err
	}
	if _, err := c.DB.Exec(`delete from music_pl_items where uid=? and pl_id=?`, c.User, in.ID); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// plItems 歌单内容（按 pos 排；缺项自动跳过）。
func plItems(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil || strings.TrimSpace(in.ID) == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id")
	}
	items, err := queryTracks(c, `select `+trackCols+` from music_tracks t
		join music_pl_items p on p.uid=t.uid and p.track_id=t.id and p.pl_id=?
		where t.uid=? order by p.pos`, in.ID, c.User)
	if err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"items": items})
	return nil
}

// plAdd 加曲目进歌单（已存在的不动，追加在尾部）。
func plAdd(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID       string   `json:"id"`
		TrackIDs []string `json:"trackIds"`
	}
	if err := appkit.Decode(r, &in); err != nil || strings.TrimSpace(in.ID) == "" || len(in.TrackIDs) == 0 {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id 或 trackIds")
	}
	if len(in.TrackIDs) > 2000 {
		return appkit.Errorf(http.StatusBadRequest, "一次最多加 2000 首")
	}
	var maxPos int
	if err := c.DB.QueryRow(`select coalesce(max(pos),0) from music_pl_items where uid=? and pl_id=?`,
		c.User, in.ID).Scan(&maxPos); err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339)
	n := 0
	for _, tid := range in.TrackIDs {
		if strings.TrimSpace(tid) == "" {
			continue
		}
		maxPos++
		res, err := c.DB.Exec(`insert or ignore into music_pl_items(uid,pl_id,track_id,pos,added_at)
			values(?,?,?,?,?)`, c.User, in.ID, tid, maxPos, now)
		if err != nil {
			return err
		}
		if m, _ := res.RowsAffected(); m > 0 {
			n++
		}
	}
	_, _ = c.DB.Exec(`update music_playlists set updated_at=? where uid=? and id=?`, now, c.User, in.ID)
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "added": n})
	return nil
}

// plRemove 从歌单移除曲目（不动曲库）。
func plRemove(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID       string   `json:"id"`
		TrackIDs []string `json:"trackIds"`
	}
	if err := appkit.Decode(r, &in); err != nil || strings.TrimSpace(in.ID) == "" || len(in.TrackIDs) == 0 {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id 或 trackIds")
	}
	n := 0
	for _, tid := range in.TrackIDs {
		res, err := c.DB.Exec(`delete from music_pl_items where uid=? and pl_id=? and track_id=?`,
			c.User, in.ID, tid)
		if err != nil {
			return err
		}
		if m, _ := res.RowsAffected(); m > 0 {
			n++
		}
	}
	// 重排 pos 保持连续
	_, _ = c.DB.Exec(`update music_pl_items set pos = (select count(*) from music_pl_items x
		where x.uid=music_pl_items.uid and x.pl_id=music_pl_items.pl_id and x.pos<=music_pl_items.pos)
		where uid=? and pl_id=?`, c.User, in.ID)
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "removed": n})
	return nil
}
