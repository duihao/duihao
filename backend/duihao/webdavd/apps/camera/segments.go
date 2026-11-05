package camera

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
)

// 片段目录索引。
//
// 真相是磁盘：ffmpeg 自己写文件，我们只负责「扫一遍 → 把没登记的补进索引」。
// 这样掉电、被杀进程、手工拷贝进来、删相机留下的老录像，全都能自愈，
// 不需要在写入路径上做任何钩子。

// segName 从文件名解析出开始时间（ffmpeg -strftime 产物 yyyymmdd_hhmmss.mp4）。
func segName(name string) (time.Time, bool) {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	t, err := time.Parse("20060102_150405", base)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// syncSegments 扫目录补索引。返回本次新增条数。
func (a *App) syncSegments(c appkit.Context, camID string) (int, error) {
	dir := camDir(c.Root, camID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	// 已登记的 path 集合
	known := map[string]string{} // path → id
	rows, err := c.DB.Query(`select id,path from camera_segments where uid=? and cam_id=?`, c.User, camID)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var id, p string
		if err := rows.Scan(&id, &p); err != nil {
			rows.Close()
			return 0, err
		}
		known[p] = id
	}
	rows.Close()

	added := 0
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".mp4") {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(camRoot, camID, e.Name()))
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		if id, ok := known[rel]; ok {
			// 正在写的文件大小会变：顺手更新字节数
			_, _ = c.DB.Exec(`update camera_segments set bytes=? where uid=? and id=?`, info.Size(), c.User, id)
			continue
		}
		start, ok := segName(e.Name())
		if !ok {
			continue // 不是我们命名的文件（用户手工放进来的）→ 跳过，不猜
		}
		_, err := c.DB.Exec(`insert into camera_segments(uid,id,cam_id,path,start_at,end_at,bytes,kind,mark)
			values(?,?,?,?,?,?,?,?,?)`,
			c.User, newID(), camID, rel,
			start.Format(timeLayout),
			start.Add(segDur).Format(timeLayout),
			info.Size(), "continuous", "")
		if err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}

type segment struct {
	ID      string `json:"id"`
	File    string `json:"file"`
	StartAt string `json:"startAt"`
	EndAt   string `json:"endAt"`
	Bytes   int64  `json:"bytes"`
	Kind    string `json:"kind"`
	Mark    string `json:"mark"`
}

// segList 按日期查片段；days 返回有录像的日期（页面日历用）。
func (a *App) segList(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	camID := r.URL.Query().Get("cam")
	if camID == "" {
		var in struct {
			Cam  string `json:"cam"`
			Date string `json:"date"`
		}
		_ = appkit.Decode(r, &in)
		camID = in.Cam
	}
	if camID == "" {
		return appkit.Errorf(400, "缺少相机 id")
	}
	if _, err := a.syncSegments(c, camID); err != nil {
		return err
	}
	date := strings.TrimSpace(r.URL.Query().Get("date"))
	if date == "" {
		date = time.Now().Format(dateLayout)
	}
	if _, err := time.Parse(dateLayout, date); err != nil {
		return appkit.Errorf(400, "日期格式应为 2006-01-02")
	}

	rows, err := c.DB.Query(`select id,path,start_at,end_at,bytes,kind,mark from camera_segments
		where uid=? and cam_id=? and start_at like ? order by start_at`, c.User, camID, date+"%")
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []segment{}
	for rows.Next() {
		var s segment
		var p string
		if err := rows.Scan(&s.ID, &p, &s.StartAt, &s.EndAt, &s.Bytes, &s.Kind, &s.Mark); err != nil {
			return err
		}
		s.File = filepath.Base(p)
		items = append(items, s)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// 有录像的日期（最近 30 天）
	days := []string{}
	drows, err := c.DB.Query(`select distinct substr(start_at,1,10) d from camera_segments
		where uid=? and cam_id=? order by d desc limit 30`, c.User, camID)
	if err == nil {
		for drows.Next() {
			var d string
			if err := drows.Scan(&d); err == nil {
				days = append(days, d)
			}
		}
		drows.Close()
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	appkit.JSON(w, 200, map[string]any{"items": items, "days": days, "date": date})
	return nil
}

// safeSegPath 把 (cam, file) 收敛成监控目录内的绝对路径，挡住路径穿越。
func safeSegPath(root, camID, file string) (string, error) {
	file = filepath.Base(strings.TrimSpace(file))
	if file == "" || file == "." || strings.ContainsAny(file, `/\`) {
		return "", appkit.Errorf(400, "文件名非法")
	}
	if _, ok := segName(file); !ok && !strings.EqualFold(filepath.Ext(file), ".mp4") {
		return "", appkit.Errorf(400, "只允许 mp4 片段")
	}
	dir := camDir(root, camID)
	full := filepath.Join(dir, file)
	// 双保险：拼出来的路径必须仍在相机目录下
	rel, err := filepath.Rel(dir, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", appkit.Errorf(400, "文件名非法")
	}
	return full, nil
}

// segFile 输出 mp4（带 Range，回放能拖）。
func (a *App) segFile(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	camID := r.URL.Query().Get("cam")
	file := r.URL.Query().Get("f")
	full, err := safeSegPath(c.Root, camID, file)
	if err != nil {
		return err
	}
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		return appkit.Errorf(404, "片段不存在")
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	// ServeFile 自带 Range / If-Modified-Since
	http.ServeFile(w, r, full)
	return nil
}

func (a *App) segDelete(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Cam  string `json:"cam"`
		File string `json:"file"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(400, "参数解析失败：%v", err)
	}
	full, err := safeSegPath(c.Root, in.Cam, in.File)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	rel := filepath.ToSlash(filepath.Join(camRoot, in.Cam, filepath.Base(in.File)))
	_, _ = c.DB.Exec(`delete from camera_segments where uid=? and cam_id=? and path=?`, c.User, in.Cam, rel)
	addLog(c, in.Cam, "info", "删除片段 "+filepath.Base(in.File))
	appkit.JSON(w, 200, map[string]any{"ok": true, "path": rel})
	return nil
}

// segMark 给片段加个标记（「有人经过」「车」…），是 v1 的事件口径：
// 不假装做 AI 识别，也不订阅 ONVIF 事件，人看到什么就标什么。
func (a *App) segMark(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Cam  string `json:"cam"`
		File string `json:"file"`
		Mark string `json:"mark"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(400, "参数解析失败：%v", err)
	}
	mark := strings.TrimSpace(in.Mark)
	if len([]rune(mark)) > 40 {
		return appkit.Errorf(400, "标记太长（40 字以内）")
	}
	kind := "continuous"
	if mark != "" {
		kind = "event"
	}
	rel := filepath.ToSlash(filepath.Join(camRoot, in.Cam, filepath.Base(in.File)))
	res, err := c.DB.Exec(`update camera_segments set kind=?, mark=? where uid=? and cam_id=? and path=?`,
		kind, mark, c.User, in.Cam, rel)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return appkit.Errorf(404, "片段不存在")
	}
	appkit.JSON(w, 200, map[string]any{"ok": true, "kind": kind})
	return nil
}
