// Package home 收藏夹（原「导航」，常用网站收藏）：书签与分组存单库（uid 隔离）。
//
// 接口约定（/-api/home/<action>，POST JSON）：
//
//	list            → {groups:[...], bookmarks:[...]}（首次进入播种默认收藏）
//	bookmark.save   {id?, groupId?, title, url, descr?, account?, pwdHint?} → {id}
//	bookmark.delete {id}
//	bookmark.move   {id, groupId, index} → {ok}（管理弹窗拖拽：跨分类 + 组内排序）
//	group.save      {id?, title} → {id}
//	group.delete    {id}
//	group.move      {id, index}（拖拽重排分类）
//	import          {text} → {added, skipped}（浏览器书签 HTML 或 "标题,网址" 行）
//	favicon         GET ?host=example.com → 图标字节流（服务端取一次缓存在库，见 favicon.go）
package home

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
	"github.com/duihao/duihao/backend/duihao/webdavd/webui"
)

type App struct{}

func New() *App { return &App{} }

func (a *App) ID() string    { return "home" }
func (a *App) Title() string { return "收藏夹" }

func (a *App) Page(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	return webui.Render(w, webui.Page{App: a.ID(), Title: a.Title(), User: c.User, Writable: c.Writable, Role: c.Role, Perms: c.Perms})
}

type group struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Sort  int    `json:"sort"`
}

type bookmark struct {
	ID      string `json:"id"`
	GroupID string `json:"groupId"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Descr   string `json:"descr"` // 描述（存 note 列）
	Account string `json:"account"`
	PwdHint string `json:"pwdHint"`
	Sort    int    `json:"sort"`
}

func (a *App) API(c appkit.Context, w http.ResponseWriter, r *http.Request, action string) error {
	switch action {
	case "ping":
		appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "app": a.ID(), "user": c.User})
		return nil
	case "list":
		return list(c, w)
	case "bookmark.save":
		return saveBookmark(c, w, r)
	case "bookmark.delete":
		return deleteBookmark(c, w, r)
	case "bookmark.move":
		return moveBookmark(c, w, r)
	case "group.save":
		return saveGroup(c, w, r)
	case "group.delete":
		return deleteGroup(c, w, r)
	case "group.move":
		return moveGroup(c, w, r)
	case "import":
		return importLinks(c, w, r)
	case "favicon":
		return serveFavicon(c, w, r)
	}
	return appkit.Errorf(http.StatusNotFound, "未知接口: %s", action)
}

func list(c appkit.Context, w http.ResponseWriter) error {
	if err := seed(c); err != nil {
		return err
	}
	// 未分组的收藏一律并进「其他」，否则首页那几个站没有分类可管
	if err := foldUngrouped(c); err != nil {
		return err
	}
	groups := make([]group, 0, 8)
	rows, err := c.DB.Query("select id, title, sort from bookmark_groups where uid = ? order by sort, title", c.User)
	if err != nil {
		return err
	}
	for rows.Next() {
		var g group
		if err := rows.Scan(&g.ID, &g.Title, &g.Sort); err != nil {
			_ = rows.Close()
			return err
		}
		groups = append(groups, g)
	}
	_ = rows.Close()

	marks := make([]bookmark, 0, 64)
	rows, err = c.DB.Query("select id, coalesce(group_id,''), title, url, coalesce(note,''), coalesce(account,''), coalesce(pwd_hint,''), sort from bookmarks where uid = ? order by sort, title", c.User)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var b bookmark
		if err := rows.Scan(&b.ID, &b.GroupID, &b.Title, &b.URL, &b.Descr, &b.Account, &b.PwdHint, &b.Sort); err != nil {
			return err
		}
		marks = append(marks, b)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"groups": groups, "bookmarks": marks})
	return nil
}

// ── 写操作 ──

type saveBookmarkReq struct {
	ID      string `json:"id"`
	GroupID string `json:"groupId"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Descr   string `json:"descr"`
	Account string `json:"account"`
	PwdHint string `json:"pwdHint"`
}

func saveBookmark(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in saveBookmarkReq
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	u, err := normalizeURL(in.URL)
	if err != nil {
		return appkit.Errorf(http.StatusBadRequest, "%s", err.Error())
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = hostOf(u)
	}
	// 没指定分类就落到「其他」，不留未分组（否则首页那几个站没法管理）
	if strings.TrimSpace(in.GroupID) == "" {
		gid, err := otherGroupID(c)
		if err != nil {
			return err
		}
		in.GroupID = gid
	}
	now := time.Now().Format(time.RFC3339)
	if in.ID == "" {
		id := newID()
		_, err := c.DB.Exec(`insert into bookmarks(uid,id,group_id,title,url,note,account,pwd_hint,sort,created_at,updated_at)
			values(?,?,?,?,?,?,?,?, (select coalesce(max(sort),0)+10 from bookmarks where uid=? and coalesce(group_id,'')=coalesce(?,'')), ?, ?)`,
			c.User, id, nullable(in.GroupID), title, u, in.Descr, in.Account, in.PwdHint, c.User, nullable(in.GroupID), now, now)
		if err != nil {
			return err
		}
		appkit.JSON(w, http.StatusOK, map[string]any{"id": id})
		return nil
	}
	res, err := c.DB.Exec("update bookmarks set group_id=?, title=?, url=?, note=?, account=?, pwd_hint=?, updated_at=? where uid=? and id=?",
		nullable(in.GroupID), title, u, in.Descr, in.Account, in.PwdHint, now, c.User, in.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return appkit.Errorf(http.StatusNotFound, "书签不存在")
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"id": in.ID})
	return nil
}

func deleteBookmark(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil || in.ID == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id")
	}
	if _, err := c.DB.Exec("delete from bookmarks where uid=? and id=?", c.User, in.ID); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// moveBookmark 拖拽落位：把书签放进目标分类的第 index 位（0 开始），
// 然后把该分类的顺序整体重排成 10/20/30…，避免 sort 出现小数或空洞。
func moveBookmark(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID      string `json:"id"`
		GroupID string `json:"groupId"`
		Index   int    `json:"index"`
	}
	if err := appkit.Decode(r, &in); err != nil || in.ID == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id")
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	rows, err := tx.Query("select id from bookmarks where uid=? and coalesce(group_id,'')=? and id<>? order by sort, title",
		c.User, in.GroupID, in.ID)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	ids := make([]string, 0, 64)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			_ = tx.Rollback()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		_ = tx.Rollback()
		return err
	}
	_ = rows.Close()

	if in.Index < 0 {
		in.Index = 0
	}
	if in.Index > len(ids) {
		in.Index = len(ids)
	}
	ids = append(ids, "")
	copy(ids[in.Index+1:], ids[in.Index:])
	ids[in.Index] = in.ID

	now := time.Now().Format(time.RFC3339)
	for i, id := range ids {
		if _, err := tx.Exec("update bookmarks set group_id=?, sort=?, updated_at=? where uid=? and id=?",
			nullable(in.GroupID), (i+1)*10, now, c.User, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func saveGroup(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return appkit.Errorf(http.StatusBadRequest, "分组名不能为空")
	}
	now := time.Now().Format(time.RFC3339)
	if in.ID == "" {
		id := newID()
		if _, err := c.DB.Exec(`insert into bookmark_groups(uid,id,title,sort,created_at)
			values(?,?,?,(select coalesce(max(sort),0)+10 from bookmark_groups where uid=?),?)`,
			c.User, id, title, c.User, now); err != nil {
			return err
		}
		appkit.JSON(w, http.StatusOK, map[string]any{"id": id})
		return nil
	}
	res, err := c.DB.Exec("update bookmark_groups set title=? where uid=? and id=?", title, c.User, in.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return appkit.Errorf(http.StatusNotFound, "分组不存在")
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"id": in.ID})
	return nil
}

// moveGroup 按拖拽后的顺序重排分类（index 从 0 开始）。
func moveGroup(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID    string `json:"id"`
		Index int    `json:"index"`
	}
	if err := appkit.Decode(r, &in); err != nil || in.ID == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id")
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	rows, err := tx.Query("select id from bookmark_groups where uid=? and id<>? order by sort, title", c.User, in.ID)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	ids := make([]string, 0, 16)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			_ = tx.Rollback()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		_ = tx.Rollback()
		return err
	}
	_ = rows.Close()

	if in.Index < 0 {
		in.Index = 0
	}
	if in.Index > len(ids) {
		in.Index = len(ids)
	}
	ids = append(ids, "")
	copy(ids[in.Index+1:], ids[in.Index:])
	ids[in.Index] = in.ID

	for i, id := range ids {
		if _, err := tx.Exec("update bookmark_groups set sort=? where uid=? and id=?", (i+1)*10, c.User, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func deleteGroup(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil || in.ID == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id")
	}
	// 分组删除时书签不删，收到「其他」分类（不留未分组）；「其他」本身是兜底分类，不允许删
	otherID, err := otherGroupID(c)
	if err != nil {
		return err
	}
	if in.ID == otherID {
		return appkit.Errorf(http.StatusBadRequest, "「其他」是兜底分类，不能删除")
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec("update bookmarks set group_id=? where uid=? and group_id=?", otherID, c.User, in.ID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec("delete from bookmark_groups where uid=? and id=?", c.User, in.ID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// importLinks 批量导入：浏览器导出的书签 HTML，或每行「标题,网址 / 网址」。
func importLinks(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Text string `json:"text"`
	}
	if err := appkit.Decode(r, &in); err != nil || strings.TrimSpace(in.Text) == "" {
		return appkit.Errorf(http.StatusBadRequest, "没有可导入的内容")
	}
	items := parseImport(in.Text)
	if len(items) == 0 {
		return appkit.Errorf(http.StatusBadRequest, "没解析到网址")
	}
	// 已存在的网址跳过（同账号内去重）
	exists := map[string]bool{}
	rows, err := c.DB.Query("select url from bookmarks where uid=?", c.User)
	if err != nil {
		return err
	}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err == nil {
			exists[strings.TrimRight(u, "/")] = true
		}
	}
	_ = rows.Close()

	otherID, err := otherGroupID(c)
	if err != nil {
		return err
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	now := time.Now().Format(time.RFC3339)
	added, skipped := 0, 0
	for _, it := range items {
		if exists[strings.TrimRight(it.URL, "/")] {
			skipped++
			continue
		}
		if _, err := tx.Exec(`insert into bookmarks(uid,id,group_id,title,url,note,sort,created_at,updated_at)
			values(?,?,?,?,?,'', (select coalesce(max(sort),0)+10 from bookmarks where uid=? and group_id=?), ?, ?)`,
			c.User, newID(), otherID, it.Title, it.URL, c.User, otherID, now, now); err != nil {
			_ = tx.Rollback()
			return err
		}
		exists[strings.TrimRight(it.URL, "/")] = true
		added++
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"added": added, "skipped": skipped})
	return nil
}

// ── 工具 ──

// otherGroupID 返回「其他」分类；没有就建一个。未分组的数据统一收到这里。
func otherGroupID(c appkit.Context) (string, error) {
	var id string
	err := c.DB.QueryRow(`select id from bookmark_groups where uid=? and (title='其他' or title='其它') order by sort, title limit 1`, c.User).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	id = newID()
	now := time.Now().Format(time.RFC3339)
	if _, err := c.DB.Exec(`insert into bookmark_groups(uid,id,title,sort,created_at)
		values(?,?,?,(select coalesce(max(sort),0)+10 from bookmark_groups where uid=?),?)`,
		c.User, id, "其他", c.User, now); err != nil {
		return "", err
	}
	return id, nil
}

// foldUngrouped 把 group_id 为空的收藏并入「其他」（老数据/删分类留下的）。
func foldUngrouped(c appkit.Context) error {
	var n int
	if err := c.DB.QueryRow(`select count(*) from bookmarks where uid=? and coalesce(group_id,'')=''`, c.User).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	gid, err := otherGroupID(c)
	if err != nil {
		return err
	}
	_, err = c.DB.Exec(`update bookmarks set group_id=? where uid=? and coalesce(group_id,'')=''`, gid, c.User)
	return err
}

var anchorRe = regexp.MustCompile(`(?is)<a[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
var tagRe = regexp.MustCompile(`(?s)<[^>]+>`)

type linkItem struct{ Title, URL string }

func parseImport(text string) []linkItem {
	var out []linkItem
	if strings.Contains(strings.ToLower(text), "<a ") {
		for _, m := range anchorRe.FindAllStringSubmatch(text, -1) {
			raw := strings.TrimSpace(m[1])
			title := strings.TrimSpace(tagRe.ReplaceAllString(m[2], ""))
			if u, err := normalizeURL(raw); err == nil {
				if title == "" {
					title = hostOf(u)
				}
				out = append(out, linkItem{Title: title, URL: u})
			}
		}
		return out
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		title, raw := "", line
		if i := strings.IndexAny(line, ",，\t"); i > 0 {
			title = strings.TrimSpace(line[:i])
			raw = strings.TrimSpace(line[i+1:])
		}
		u, err := normalizeURL(raw)
		if err != nil {
			continue
		}
		if title == "" {
			title = hostOf(u)
		}
		out = append(out, linkItem{Title: title, URL: u})
	}
	return out
}

func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errText("网址不能为空")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errText("网址格式不正确")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errText("只支持 http/https 网址")
	}
	return u.String(), nil
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return strings.TrimPrefix(u.Host, "www.")
	}
	return raw
}

func nullable(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type errText string

func (e errText) Error() string { return string(e) }
