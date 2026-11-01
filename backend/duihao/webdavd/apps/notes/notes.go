// Package notes 笔记（知识卡片）：标题、正文、分类、标签全部存单库（uid 隔离），不落网盘文件。
//
// 接口约定（/-api/notes/<action>，POST JSON）：
//
//	list       {q?, cat?, tag?, from?, to?, sort?, page?, pageSize?}
//	           → {notes:[brief...], cats, tags, stats:{...}, total, page, pageSize}
//	catstats   {q?, cat?, tag?, range?, from?, to?} → {rows:[{cat,count,views}], total} 按分类汇总
//	get        {id}                      → {note:{...}}（顺带 +1 阅读次数）
//	save       {id?, title, body, cat, tags, day?, pinned?, cover?} → {id, updatedAt}
//	delete     {id}                      → {ok:true}
//	upload     multipart(file)           → {name, path, url, size}（存网盘 _appfiles/notes/）
//	pick.list  {dir?}                    → {dir, items:[{name,dir,size,mod,url}]}（网盘选文件）
//	cat.save   {old?, name}              → {ok:true}（重命名时同步改掉笔记里的分类）
//	cat.delete {name}                    → {ok:true}（该分类下的笔记回到「未分类」）
//	export     {}                        → {notes:[full...], exportedAt}
//
// 为什么正文也入库（2026-10-28 定稿）：原子写、无文件锁、搜索与统计都是一条 SQL；
// 代价是不能用外部编辑器改，所以必须保留 export 作为自救出口。
package notes

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/webui"
)

type App struct{}

func New() *App { return &App{} }

func (a *App) ID() string    { return "notes" }
func (a *App) Title() string { return "笔记" }

func (a *App) Page(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	return webui.Render(w, webui.Page{App: a.ID(), Title: a.Title(), User: c.User, Writable: c.Writable, Role: c.Role, Perms: c.Perms})
}

func (a *App) API(c appkit.Context, w http.ResponseWriter, r *http.Request, action string) error {
	switch action {
	case "ping":
		appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "app": a.ID(), "user": c.User})
		return nil
	case "list":
		return list(c, w, r)
	case "catstats":
		return catStats(c, w, r)
	case "get":
		return get(c, w, r)
	case "save":
		return save(c, w, r)
	case "delete":
		return remove(c, w, r)
	case "upload":
		return upload(c, w, r)
	case "pick.list":
		return pickList(c, w, r)
	case "cat.save":
		return catSave(c, w, r)
	case "cat.delete":
		return catDelete(c, w, r)
	case "export":
		return export(c, w)
	}
	return appkit.Errorf(http.StatusNotFound, "未知接口: %s", action)
}

// ── 数据结构 ──

type brief struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Excerpt   string `json:"excerpt"`
	Cat       string `json:"cat"`
	Tags      string `json:"tags"`
	Day       string `json:"day"`
	Cover     string `json:"cover"` // 网盘路径或 dataURL；空 = 无封面
	Views     int    `json:"views"`
	Pinned    bool   `json:"pinned"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type full struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Cat       string `json:"cat"`
	Tags      string `json:"tags"`
	Day       string `json:"day"`
	Cover     string `json:"cover"`
	Views     int    `json:"views"`
	Pinned    bool   `json:"pinned"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type count struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type stats struct {
	Total     int `json:"total"`
	Cats      int `json:"cats"`
	Views     int `json:"views"`
	Today     int `json:"today"`
	Yesterday int `json:"yesterday"`
	Week      int `json:"week"`
	LastWeek  int `json:"lastWeek"`
	Month     int `json:"month"`
	LastMonth int `json:"lastMonth"`
	Year      int `json:"year"`
	LastYear  int `json:"lastYear"`
}

// ── 读 ──

// listIn list / catstats 共用的筛选入参。
type listIn struct {
	Q        string `json:"q"`
	Cat      string `json:"cat"`
	Tag      string `json:"tag"`
	Range    string `json:"range"` // today/yesterday/week/lastweek/month/lastmonth/year/lastyear
	From     string `json:"from"`
	To       string `json:"to"`
	Sort     string `json:"sort"`
	Page     int    `json:"page"`     // 从 1 开始；list 用，catstats 忽略
	PageSize int    `json:"pageSize"` // 1~100，默认 24
}

// filterWhere 把共用筛选（关键词/分类/标签/日期区间/时间维度）拼成 SQL。
func filterWhere(uid string, in listIn) (string, []any) {
	where := "uid = ?"
	args := []any{uid}
	if q := strings.TrimSpace(in.Q); q != "" {
		like := "%" + escapeLike(q) + "%"
		where += ` and (title like ? escape '\' or body like ? escape '\' or tags like ? escape '\')`
		args = append(args, like, like, like)
	}
	if cat := strings.TrimSpace(in.Cat); cat != "" {
		if cat == "__none__" { // 「未分类」伪分类
			where += " and coalesce(cat,'') = ''"
		} else {
			where += " and cat = ?"
			args = append(args, cat)
		}
	}
	if tag := strings.TrimSpace(in.Tag); tag != "" { // 兼容旧调用（标签过滤）
		where += ` and (',' || lower(tags) || ',') like ?`
		args = append(args, "%,"+strings.ToLower(tag)+",%")
	}
	if d := strings.TrimSpace(in.From); len(d) == 10 {
		where += " and day >= ?"
		args = append(args, d)
	}
	if d := strings.TrimSpace(in.To); len(d) == 10 {
		where += " and day <= ?"
		args = append(args, d)
	}
	// 快速时间维度：手动日期优先，未填时按 range 换算（口径与账本一致）
	if strings.TrimSpace(in.From) == "" && strings.TrimSpace(in.To) == "" {
		if f, t := appkit.Period(strings.TrimSpace(in.Range)); f != "" {
			where += " and day >= ?"
			args = append(args, f)
			where += " and day <= ?"
			args = append(args, t)
		}
	}
	return where, args
}

func list(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in listIn
	if err := decodeOpt(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	where, args := filterWhere(c.User, in)

	order := "order by pinned desc, created_at desc"
	switch in.Sort {
	case "old":
		order = "order by created_at asc"
	case "updated":
		order = "order by updated_at desc"
	}

	// 分页：先数总数，再把页码夹到有效范围内
	pageSize := in.PageSize
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 24
	}
	var total int
	if err := c.DB.QueryRow(`select count(*) from notes where `+where, args...).Scan(&total); err != nil {
		return err
	}
	page := in.Page
	if page <= 0 {
		page = 1
	}
	if maxPage := (total + pageSize - 1) / pageSize; maxPage > 0 && page > maxPage {
		page = maxPage
	}
	offset := (page - 1) * pageSize

	rows, err := c.DB.Query(`select id, title, body, coalesce(cat,''), coalesce(tags,''), day, coalesce(cover,''),
		views, pinned, created_at, updated_at from notes where `+where+` `+order+` limit ? offset ?`,
		append(args, pageSize, offset)...)
	if err != nil {
		return err
	}
	out := make([]brief, 0, 32)
	for rows.Next() {
		var b brief
		var body string
		var pinned int
		if err := rows.Scan(&b.ID, &b.Title, &body, &b.Cat, &b.Tags, &b.Day, &b.Cover,
			&b.Views, &pinned, &b.CreatedAt, &b.UpdatedAt); err != nil {
			_ = rows.Close()
			return err
		}
		b.Pinned = pinned != 0
		b.Excerpt = snippet(body, 110)
		out = append(out, b)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	cats, err := allCats(c)
	if err != nil {
		return err
	}
	tags, err := allTags(c)
	if err != nil {
		return err
	}
	st, err := counts(c)
	if err != nil {
		return err
	}
	st.Cats = 0
	for _, k := range cats {
		if k.Name != "" {
			st.Cats++
		}
	}
	appkit.JSON(w, http.StatusOK, map[string]any{
		"notes": out, "cats": cats, "tags": tags, "stats": st,
		"total": total, "page": page, "pageSize": pageSize,
	})
	return nil
}

// catRow 分类汇总行。
type catRow struct {
	Cat   string `json:"cat"`
	Count int    `json:"count"`
	Views int    `json:"views"`
}

// catStats 按分类汇总：与 list 共用同一套筛选（关键词/分类/标签/日期/时间维度），
// 聚合出每个分类的篇数与累计阅读，按篇数降序。空分类聚合为「未分类」。
func catStats(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in listIn
	if err := decodeOpt(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	where, args := filterWhere(c.User, in)
	rows, err := c.DB.Query(`select coalesce(nullif(cat,''),'__none__'), count(*), coalesce(sum(views),0)
		from notes where `+where+` group by coalesce(nullif(cat,''),'__none__') order by count(*) desc, cat asc`, args...)
	if err != nil {
		return err
	}
	out := make([]catRow, 0, 8)
	total := 0
	for rows.Next() {
		var cr catRow
		if err := rows.Scan(&cr.Cat, &cr.Count, &cr.Views); err != nil {
			_ = rows.Close()
			return err
		}
		if cr.Cat == "__none__" {
			cr.Cat = ""
		}
		total += cr.Count
		out = append(out, cr)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"rows": out, "total": total})
	return nil
}

// allCats 分类清单：note_cats 里的空分类 + 笔记里实际用到的分类，带篇数。
func allCats(c appkit.Context) ([]count, error) {
	seen := map[string]int{}
	rows, err := c.DB.Query("select coalesce(cat,''), count(*) from notes where uid = ? and coalesce(cat,'') <> '' group by cat", c.User)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			_ = rows.Close()
			return nil, err
		}
		seen[name] = n
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = c.DB.Query("select name from note_cats where uid = ? order by sort, name", c.User)
	if err != nil {
		return nil, err
	}
	extra := make([]string, 0, 8)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if _, ok := seen[name]; !ok {
			extra = append(extra, name)
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]count, 0, len(seen)+len(extra)+1)
	for _, name := range extra {
		out = append(out, count{Name: name, Count: 0})
	}
	for name, n := range seen {
		out = append(out, count{Name: name, Count: n})
	}
	// 稳定排序：有笔记的在前，再按篇数降序、名称升序
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Count > out[i].Count ||
				(out[j].Count == out[i].Count && out[j].Name < out[i].Name) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// allTags 全量标签计数（兼容旧数据；卡片形态下 UI 用的是分类）。
func allTags(c appkit.Context) ([]count, error) {
	rows, err := c.DB.Query("select tags from notes where uid = ? and tags <> ''", c.User)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	counts := map[string]int{}
	for rows.Next() {
		var tags string
		if err := rows.Scan(&tags); err != nil {
			return nil, err
		}
		for _, t := range splitTags(tags) {
			counts[t]++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]count, 0, len(counts))
	for name, n := range counts {
		out = append(out, count{Name: name, Count: n})
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Count > out[i].Count ||
				(out[j].Count == out[i].Count && out[j].Name < out[i].Name) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// counts 统计卡：总数 / 阅读累计 / 覆盖分类 + 8 个时间维度的新增篇数。
// 时间口径统一走 appkit.Period，与账本的时间范围 tabs 完全一致。
func counts(c appkit.Context) (stats, error) {
	var st stats
	now := time.Now()
	today := now.Format("2006-01-02")
	month := now.Format("2006-01")
	thisYear, lastY := now.Format("2006"), now.AddDate(-1, 0, 0).Format("2006")
	yf, yt := appkit.Period("yesterday")
	wf, wt := appkit.Period("week")
	lf, lt := appkit.Period("lastweek")
	mf, mt := appkit.Period("lastmonth")
	err := c.DB.QueryRow(`select count(*),
		coalesce(sum(views), 0),
		coalesce(sum(day = ?), 0),
		coalesce(sum(day between ? and ?), 0),
		coalesce(sum(day between ? and ?), 0),
		coalesce(sum(day between ? and ?), 0),
		coalesce(sum(substr(day, 1, 7) = ?), 0),
		coalesce(sum(day between ? and ?), 0),
		coalesce(sum(substr(day, 1, 4) = ?), 0),
		coalesce(sum(substr(day, 1, 4) = ?), 0)
		from notes where uid = ?`,
		today, yf, yt, wf, wt, lf, lt, month, mf, mt, thisYear, lastY, c.User).
		Scan(&st.Total, &st.Views, &st.Today, &st.Yesterday, &st.Week, &st.LastWeek,
			&st.Month, &st.LastMonth, &st.Year, &st.LastYear)
	return st, err
}

func get(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil || strings.TrimSpace(in.ID) == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id")
	}
	// 阅读计数：打开即 +1（列表里的「阅读 N 次」就是它）
	if _, err := c.DB.Exec("update notes set views = views + 1 where uid = ? and id = ?", c.User, in.ID); err != nil {
		return err
	}
	var n full
	var pinned int
	err := c.DB.QueryRow(`select id, title, body, coalesce(cat,''), coalesce(tags,''), day, coalesce(cover,''),
		views, pinned, created_at, updated_at from notes where uid = ? and id = ?`, c.User, in.ID).
		Scan(&n.ID, &n.Title, &n.Body, &n.Cat, &n.Tags, &n.Day, &n.Cover, &n.Views, &pinned, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return appkit.Errorf(http.StatusNotFound, "笔记不存在")
	}
	if err != nil {
		return err
	}
	n.Pinned = pinned != 0
	appkit.JSON(w, http.StatusOK, map[string]any{"note": n})
	return nil
}

func export(c appkit.Context, w http.ResponseWriter) error {
	rows, err := c.DB.Query(`select id, title, body, coalesce(cat,''), coalesce(tags,''), day, coalesce(cover,''),
		views, pinned, created_at, updated_at from notes where uid = ? order by day, created_at`, c.User)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	out := make([]full, 0, 64)
	for rows.Next() {
		var n full
		var pinned int
		if err := rows.Scan(&n.ID, &n.Title, &n.Body, &n.Cat, &n.Tags, &n.Day, &n.Cover,
			&n.Views, &pinned, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return err
		}
		n.Pinned = pinned != 0
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{
		"app": "notes", "user": c.User, "count": len(out), "notes": out,
		"exportedAt": time.Now().Format(time.RFC3339),
	})
	return nil
}

// ── 写 ──

func save(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Cat    string `json:"cat"`
		Tags   string `json:"tags"`
		Day    string `json:"day"`
		Cover  string `json:"cover"`
		Pinned bool   `json:"pinned"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	// 封面：网盘路径（/ 开头）或 dataURL 图片；空串 = 清除封面
	cover := strings.TrimSpace(in.Cover)
	if cover != "" && !strings.HasPrefix(cover, "/") && !strings.HasPrefix(cover, "data:image/") {
		return appkit.Errorf(http.StatusBadRequest, "封面格式不支持")
	}
	if len(cover) > 3<<20 {
		return appkit.Errorf(http.StatusBadRequest, "封面过大")
	}
	body := Sanitize(in.Body)
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = deriveTitle(body)
	}
	if r := []rune(title); len(r) > 120 {
		title = string(r[:120])
	}
	cat := strings.TrimSpace(in.Cat)
	if r := []rune(cat); len(r) > 24 {
		return appkit.Errorf(http.StatusBadRequest, "分类名称最多 24 个字")
	}
	tags := normalizeTags(in.Tags)
	day := strings.TrimSpace(in.Day)
	if day != "" {
		if _, err := time.Parse("2006-01-02", day); err != nil {
			return appkit.Errorf(http.StatusBadRequest, "日期格式应为 YYYY-MM-DD")
		}
	}
	now := time.Now()
	stamp := now.Format(time.RFC3339)
	pinned := 0
	if in.Pinned {
		pinned = 1
	}
	if cat != "" { // 用了新分类就顺手登记，分类管理里能看到
		if _, err := c.DB.Exec("insert into note_cats(uid, name) values(?, ?) on conflict(uid, name) do nothing", c.User, cat); err != nil {
			return err
		}
	}

	if strings.TrimSpace(in.ID) == "" {
		id := newID()
		if day == "" {
			day = now.Format("2006-01-02")
		}
		if _, err := c.DB.Exec(`insert into notes(uid,id,title,body,cat,tags,pinned,day,cover,views,created_at,updated_at)
			values(?,?,?,?,?,?,?,?,?,0,?,?)`,
			c.User, id, title, body, cat, tags, pinned, day, cover, stamp, stamp); err != nil {
			return err
		}
		appkit.JSON(w, http.StatusOK, map[string]any{"id": id, "updatedAt": stamp})
		return nil
	}

	// 更新：day 为空时保留原值（用户只想改正文时不必传）
	res, err := c.DB.Exec(`update notes set title=?, body=?, cat=?, tags=?, pinned=?, cover=?,
		day = coalesce(nullif(?, ''), day), updated_at = ? where uid=? and id=?`,
		title, body, cat, tags, pinned, cover, day, stamp, c.User, in.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return appkit.Errorf(http.StatusNotFound, "笔记不存在")
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"id": in.ID, "updatedAt": stamp})
	return nil
}

func remove(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil || strings.TrimSpace(in.ID) == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id")
	}
	if _, err := c.DB.Exec("delete from notes where uid = ? and id = ?", c.User, in.ID); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func catSave(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Old  string `json:"old"`
		Name string `json:"name"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return appkit.Errorf(http.StatusBadRequest, "分类名称不能为空")
	}
	if n := []rune(name); len(n) > 24 {
		return appkit.Errorf(http.StatusBadRequest, "分类名称最多 24 个字")
	}
	old := strings.TrimSpace(in.Old)
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if old != "" && old != name {
		if _, err := tx.Exec("update notes set cat = ? where uid = ? and cat = ?", name, c.User, old); err != nil {
			return err
		}
		if _, err := tx.Exec("delete from note_cats where uid = ? and name = ?", c.User, old); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("insert into note_cats(uid, name) values(?, ?) on conflict(uid, name) do nothing", c.User, name); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})
	return nil
}

func catDelete(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name string `json:"name"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少分类名称")
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// 分类删掉，笔记不删：回到「未分类」
	if _, err := tx.Exec("update notes set cat = '' where uid = ? and cat = ?", c.User, name); err != nil {
		return err
	}
	if _, err := tx.Exec("delete from note_cats where uid = ? and name = ?", c.User, name); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// ── 工具 ──

// decodeOpt 允许空请求体（前端搜「全部」时可能不带参数）。
func decodeOpt(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := appkit.Decode(r, v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// escapeLike 转义 LIKE 通配符，避免用户输入的 % _ 把整库搜出来。
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// normalizeTags 逗号（含中文逗号、空格分隔）拆开、去空、去重，存成 "a,b,c"。
func normalizeTags(raw string) string {
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	for _, t := range splitTags(raw) {
		key := strings.ToLower(t)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
	}
	return strings.Join(out, ",")
}

func splitTags(raw string) []string {
	f := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == '#' || r == '、'
	})
	out := make([]string, 0, len(f))
	for _, t := range f {
		t = strings.TrimSpace(t)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// deriveTitle 没写标题时取正文第一行（HTML 或 Markdown 都能取到纯文本），截断 40 字。
func deriveTitle(body string) string {
	line := firstLine(body)
	if line == "" {
		return "无标题"
	}
	if r := []rune(line); len(r) > 40 {
		return string(r[:40])
	}
	return line
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
