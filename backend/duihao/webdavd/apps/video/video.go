// Package video 影视（S4）：扫网盘 _appfiles/video 目录建索引、影视平台式页面、
// 浏览器/电视直接播放、播放进度与继续观看。
//
// 接口约定（/-api/video/<action>，POST JSON；GET 的动作也收 POST）：
//
//	scan           {}                          → {ok,cats,items,added,updated,removed,hint?}
//	list           {q?,cat?,kind?,sort?,page?,pageSize?}
//	                  → {items,total,cats:[{name,count}],pages,page}
//	get            {id}                        → {item, progress:[{path,position,duration}]}
//	meta.save      {id,title?,year?,kind?,tags?,area?,director?,actors?,rating?,intro?}
//	progress.save  {itemId,path,position,duration}
//	continue.list  {}                          → {rows:[{itemId,title,cat,poster,path,position,duration,ep}]}
//	ping
//
// 口径：
//   - 分类 = _appfiles/video 一级子目录（video_cats 只读镜像，改分类 = 改目录）；
//   - 播放直走 WebDAV 文件路径（/_appfiles/video/...，http.ServeContent 支持 Range 拖动）；
//   - 进度按文件路径记（uid,path 主键），续播 = 打开时 seek 到 position。
package video

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/webui"
)

type App struct{}

// catNone 是「未分类」的列表哨兵值：库里这类影片 cat_name=”，而「全部」是不传 cat，
// 两者值相同会导致前端「全部」与「未分类」同时高亮，所以用独立标识区分。
const catNone = "__none__"

func New() *App { return &App{} }

func (a *App) ID() string    { return "video" }
func (a *App) Title() string { return "影视" }

func (a *App) Page(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	return webui.Render(w, webui.Page{App: a.ID(), Title: a.Title(), User: c.User, Writable: c.Writable, Role: c.Role, Perms: c.Perms})
}

func (a *App) API(c appkit.Context, w http.ResponseWriter, r *http.Request, action string) error {
	switch action {
	case "ping":
		appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "app": a.ID(), "user": c.User})
		return nil
	case "scan":
		return doScan(c, w)
	case "list":
		return list(c, w, r)
	case "get":
		return get(c, w, r)
	case "meta.save":
		return metaSave(c, w, r)
	case "progress.save":
		return progressSave(c, w, r)
	case "continue.list":
		return continueList(c, w)
	}
	return appkit.Errorf(http.StatusNotFound, "未知接口: %s", action)
}

// ── scan ──

func doScan(c appkit.Context, w http.ResponseWriter) error {
	res, err := scan(c)
	if err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, res)
	return nil
}

// ── 数据结构（出参） ──

type item struct {
	ID       string   `json:"id"`
	Cat      string   `json:"cat"`
	Title    string   `json:"title"`
	Year     int      `json:"year"`
	Kind     string   `json:"kind"`
	Tags     string   `json:"tags"`
	Area     string   `json:"area"`
	Director string   `json:"director"`
	Actors   string   `json:"actors"`
	Rating   *float64 `json:"rating"`
	Duration int      `json:"duration"`
	Intro    string   `json:"intro"`
	Poster   string   `json:"poster"` // 已转义 URL；空 = 无海报
	Files    []vfile  `json:"files"`
	Size     int64    `json:"size"`
	MTime    string   `json:"mtime"`
	Updated  string   `json:"updated"`
}

// rowItem 列表行（不带 files/intro，减体积）。
type rowItem struct {
	ID      string   `json:"id"`
	Cat     string   `json:"cat"`
	Title   string   `json:"title"`
	Year    int      `json:"year"`
	Kind    string   `json:"kind"`
	Tags    string   `json:"tags"`
	Area    string   `json:"area"`
	Rating  *float64 `json:"rating"`
	Poster  string   `json:"poster"`
	EpCount int      `json:"epCount"`
	Size    int64    `json:"size"`
	Updated string   `json:"updated"`
}

// ── list ──

func list(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Q        string `json:"q"`
		Cat      string `json:"cat"`
		Kind     string `json:"kind"`
		Sort     string `json:"sort"` // recent / rating / year / title
		Page     int    `json:"page"`
		PageSize int    `json:"pageSize"`
	}
	if err := decodeOpt(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	page, size := clampPage(in.Page, in.PageSize)

	// 首次进入自动同步一次：库里既没分类也没影片时，直接按 _appfiles/video 的
	// 一级子目录建分类，省掉「先点扫描才看得到东西」这一步。失败不阻塞列表。
	if c.Writable && strings.TrimSpace(c.Root) != "" {
		var nCats, nItems int
		err := c.DB.QueryRow(`select (select count(*) from video_cats where uid=?),
			(select count(*) from video_items where uid=?)`, c.User, c.User).Scan(&nCats, &nItems)
		if err == nil && nCats == 0 && nItems == 0 {
			_, _ = scan(c)
		}
	}

	where := ` where uid=?`
	args := []any{c.User}
	if in.Q != "" {
		where += ` and (title like ? or tags like ? or actors like ? or director like ? or intro like ?)`
		like := "%" + in.Q + "%"
		args = append(args, like, like, like, like, like)
	}
	// 未分类用哨兵值区分「全部」：全部 = 不传 cat，未分类 = cat="__none__"
	// （两者在库里都对应 cat_name=''，必须靠入参区分）
	if in.Cat == catNone {
		where += ` and cat_name=''`
	} else if in.Cat != "" {
		where += ` and cat_name=?`
		args = append(args, in.Cat)
	}
	if in.Kind != "" {
		where += ` and kind=?`
		args = append(args, in.Kind)
	}
	order := ` order by updated_at desc`
	switch in.Sort {
	case "rating":
		order = ` order by (rating is null), rating desc, updated_at desc`
	case "year":
		order = ` order by (year is null or year=0), year desc, updated_at desc`
	case "title":
		order = ` order by title`
	}

	var total int
	if err := c.DB.QueryRow(`select count(*) from video_items`+where, args...).Scan(&total); err != nil {
		return err
	}
	rows, err := c.DB.Query(`select id,cat_name,title,coalesce(year,0),kind,tags,area,rating,poster,
		json_array_length(files),size,updated_at from video_items`+where+order+` limit ? offset ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []rowItem{}
	for rows.Next() {
		var it rowItem
		var poster any
		if err := rows.Scan(&it.ID, &it.Cat, &it.Title, &it.Year, &it.Kind, &it.Tags, &it.Area,
			&it.Rating, &poster, &it.EpCount, &it.Size, &it.Updated); err != nil {
			return err
		}
		if s, ok := poster.(string); ok {
			it.Poster = fileURL(s)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// 分类 chips（按 video_cats 的目录顺序；未分类散文件排最后）
	counts := map[string]int{}
	crows, err := c.DB.Query(`select cat_name, count(*) from video_items where uid=? group by cat_name`, c.User)
	if err != nil {
		return err
	}
	defer crows.Close()
	for crows.Next() {
		var name string
		var n int
		if crows.Scan(&name, &n) == nil {
			counts[name] = n
		}
	}
	crows.Close()
	cats := []map[string]any{}
	nameRows, err := c.DB.Query(`select name from video_cats where uid=? order by sort`, c.User)
	if err != nil {
		return err
	}
	defer nameRows.Close()
	for nameRows.Next() {
		var name string
		if nameRows.Scan(&name) == nil {
			cats = append(cats, map[string]any{"name": name, "count": counts[name]})
			delete(counts, name)
		}
	}
	nameRows.Close()
	for name, n := range counts { // 目录已删但库里有残留 / 未分类散文件
		row := map[string]any{"name": name, "count": n}
		if name == "" {
			row["name"] = catNone // 哨兵：与「全部」区分（全部=不传 cat）
			row["label"] = "未分类"
		}
		cats = append(cats, row)
	}

	appkit.JSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "page": page, "pageSize": size,
		"pages": (total + size - 1) / size, "cats": cats,
	})
	return nil
}

// ── get ──

func get(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := decodeOpt(r, &in); err != nil || strings.TrimSpace(in.ID) == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id")
	}
	it, err := loadItem(c, in.ID)
	if err != nil {
		return err
	}
	// 各文件进度
	prows, err := c.DB.Query(`select path,position,duration from video_progress where uid=? and item_id=? order by updated_at desc`,
		c.User, in.ID)
	if err != nil {
		return err
	}
	defer prows.Close()
	progress := []map[string]any{}
	for prows.Next() {
		var p, d int64
		var path string
		if prows.Scan(&path, &p, &d) == nil {
			progress = append(progress, map[string]any{"path": path, "position": p, "duration": d})
		}
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"item": it, "progress": progress})
	return nil
}

func loadItem(c appkit.Context, id string) (*item, error) {
	var it item
	var poster, filesJSON any
	err := c.DB.QueryRow(`select id,cat_name,title,coalesce(year,0),kind,tags,area,director,actors,rating,
		duration,intro,poster,files,size,coalesce(mtime,''),updated_at
		from video_items where uid=? and id=?`, c.User, id).
		Scan(&it.ID, &it.Cat, &it.Title, &it.Year, &it.Kind, &it.Tags, &it.Area, &it.Director, &it.Actors,
			&it.Rating, &it.Duration, &it.Intro, &poster, &filesJSON, &it.Size, &it.MTime, &it.Updated)
	if err != nil {
		return nil, appkit.Errorf(http.StatusNotFound, "影片不存在")
	}
	if s, ok := poster.(string); ok {
		it.Poster = fileURL(s)
	}
	if s, ok := filesJSON.(string); ok {
		_ = json.Unmarshal([]byte(s), &it.Files)
	}
	if it.Files == nil {
		it.Files = []vfile{}
	}
	return &it, nil
}

// ── meta.save ──

func metaSave(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID       string   `json:"id"`
		Title    *string  `json:"title"`
		Year     *int     `json:"year"`
		Kind     *string  `json:"kind"`
		Tags     *string  `json:"tags"`
		Area     *string  `json:"area"`
		Director *string  `json:"director"`
		Actors   *string  `json:"actors"`
		Rating   *float64 `json:"rating"`
		Intro    *string  `json:"intro"`
	}
	if err := appkit.Decode(r, &in); err != nil || strings.TrimSpace(in.ID) == "" {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	if in.Title != nil && len([]rune(*in.Title)) > 120 {
		return appkit.Errorf(http.StatusBadRequest, "标题过长")
	}
	if in.Kind != nil {
		switch *in.Kind {
		case "movie", "series", "variety", "anime", "doc", "mv":
		default:
			return appkit.Errorf(http.StatusBadRequest, "未知类型")
		}
	}
	if in.Rating != nil && (*in.Rating < 0 || *in.Rating > 10) {
		return appkit.Errorf(http.StatusBadRequest, "评分应在 0~10")
	}

	// 标题改过 → custom=1，重扫不覆盖
	sets := []string{"updated_at=?"}
	args := []any{time.Now().Format(time.RFC3339)}
	if in.Title != nil {
		sets = append(sets, "title=?,custom=1")
		args = append(args, strings.TrimSpace(*in.Title))
	}
	if in.Year != nil {
		sets = append(sets, "year=?")
		args = append(args, *in.Year)
	}
	if in.Kind != nil {
		sets = append(sets, "kind=?")
		args = append(args, *in.Kind)
	}
	if in.Tags != nil {
		sets = append(sets, "tags=?")
		args = append(args, strings.TrimSpace(*in.Tags))
	}
	if in.Area != nil {
		sets = append(sets, "area=?")
		args = append(args, strings.TrimSpace(*in.Area))
	}
	if in.Director != nil {
		sets = append(sets, "director=?")
		args = append(args, strings.TrimSpace(*in.Director))
	}
	if in.Actors != nil {
		sets = append(sets, "actors=?")
		args = append(args, strings.TrimSpace(*in.Actors))
	}
	if in.Rating != nil {
		sets = append(sets, "rating=?")
		args = append(args, *in.Rating)
	}
	if in.Intro != nil {
		sets = append(sets, "intro=?")
		args = append(args, strings.TrimSpace(*in.Intro))
	}
	args = append(args, c.User, in.ID)
	res, err := c.DB.Exec(`update video_items set `+strings.Join(sets, ",")+` where uid=? and id=?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return appkit.Errorf(http.StatusNotFound, "影片不存在")
	}
	it, err := loadItem(c, in.ID)
	if err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"item": it})
	return nil
}

// ── progress ──

func progressSave(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ItemID   string `json:"itemId"`
		Path     string `json:"path"`
		Position int64  `json:"position"`
		Duration int64  `json:"duration"`
	}
	if err := appkit.Decode(r, &in); err != nil || in.Path == "" {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	if in.Position < 0 {
		in.Position = 0
	}
	_, err := c.DB.Exec(`insert into video_progress(uid,path,item_id,position,duration,updated_at)
		values(?,?,?,?,?,?)
		on conflict(uid,path) do update set item_id=excluded.item_id, position=excluded.position,
			duration=excluded.duration, updated_at=excluded.updated_at`,
		c.User, in.Path, ns(in.ItemID), in.Position, in.Duration,
		time.Now().Format(time.RFC3339))
	if err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// continueList 继续观看：最近播过且没看完的（>30s、距结尾 >60s）。
func continueList(c appkit.Context, w http.ResponseWriter) error {
	rows, err := c.DB.Query(`
		select p.item_id, i.title, i.cat_name, i.poster, p.path, p.position, p.duration, p.updated_at
		from video_progress p join video_items i on i.uid=p.uid and i.id=p.item_id
		where p.uid=? and p.position > 5 and (p.duration = 0 or p.position < p.duration * 95 / 100)
		order by p.updated_at desc limit 60`, c.User)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, title, cat, path, updated string
		var poster any
		var pos, dur int64
		if err := rows.Scan(&id, &title, &cat, &poster, &path, &pos, &dur, &updated); err != nil {
			return err
		}
		posterURL := ""
		if s, ok := poster.(string); ok {
			posterURL = fileURL(s)
		}
		out = append(out, map[string]any{
			"itemId": id, "title": title, "cat": cat, "poster": posterURL,
			"path": path, "position": pos, "duration": dur, "updated": updated,
		})
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"rows": out})
	return nil
}

// ── 小工具 ──

func clampPage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 24
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

// decodeOpt 解析可选 JSON 请求体（空体 = 全零值）。
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
