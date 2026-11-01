// Package appkit 个人空间的应用框架：单端口下按路径分发到各应用（导航/笔记/账本）。
//
// 设计约束（2026-10-28 定稿）：
//   - 一个进程、一个端口、一套登录：本包不碰认证，只拿认证后的 Context；
//   - 一个 SQLite 库：所有应用共用 *sql.DB，租户隔离靠每张表的 uid 列；
//   - 显式注册（server.go 里 New(...)），不用 init() 自动注册——便于裁剪与测试；
//   - 路径全部走保留命名空间 /-<app> 与 /-api/<app>/<action>，绝不与用户目录撞名。
package appkit

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Context 一次请求的账号上下文（webdavd.guard 认证后填充）。
type Context struct {
	User  string            // NAS 账号 uid（写进各表 uid 列；= 共享账号名或 hub: 前缀解析出的内部账号）
	Role  string            // "admin" | "member"；空 = 直连 Context 的单测（不做权限判定）
	Perms map[string]string // member 的模块权限：模块 → "r"|"rw"；admin 忽略（nil = 全权）
	Root     string  // WebDAV 根目录（默认共享的根；文件类资源）
	DB       *sql.DB // 单库连接（数据类资源）；不可用时为 nil
	Writable bool    // 只读共享：所有写接口一律拒绝（磁盘层开关，叠加在 perms 之上）
}

// IsAdmin 管理员（或未接权限层的直连调用）。
func (c Context) IsAdmin() bool { return c.Role == "" || c.Role == "admin" }

// canRead member 是否对模块有读权限；admin 恒真。
func (c Context) canRead(app string) bool {
	if c.IsAdmin() {
		return true
	}
	p, ok := c.Perms[app]
	return ok && strings.Contains(p, "r")
}

// canWrite member 是否对模块有写权限；admin 恒真。
func (c Context) canWrite(app string) bool {
	if c.IsAdmin() {
		return true
	}
	p, ok := c.Perms[app]
	return ok && strings.Contains(p, "w")
}

// App 一个子应用（导航 / 笔记 / 账本）。
type App interface {
	ID() string    // 路径前缀：home / notes / ledger
	Title() string // 顶栏文案
	Page(c Context, w http.ResponseWriter, r *http.Request) error
	API(c Context, w http.ResponseWriter, r *http.Request, action string) error
}

// Registry 应用注册表（页面路径 + API 前缀两张表）。
type Registry struct {
	pages map[string]App // "/-home" → app
	apis  map[string]App // "home"    → app
}

func New(apps ...App) *Registry {
	r := &Registry{pages: map[string]App{}, apis: map[string]App{}}
	for _, a := range apps {
		r.pages["/-"+a.ID()] = a
		r.apis[a.ID()] = a
	}
	return r
}

// Apps 返回注册的应用（顶栏渲染用）。
func (r *Registry) Apps() []App {
	out := make([]App, 0, len(r.apis))
	for _, id := range r.order() {
		out = append(out, r.apis[id])
	}
	return out
}

// order 保持注册顺序（map 迭代无序，顶栏不能跳）。
func (r *Registry) order() []string {
	ids := make([]string, 0, len(r.apis))
	for _, a := range r.pages {
		ids = append(ids, a.ID())
	}
	// 固定顺序：导航 → 笔记 → 账本 → 其他
	rank := func(id string) int {
		switch id {
		case "home":
			return 0
		case "notes":
			return 1
		case "ledger":
			return 2
		}
		return 9
	}
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if rank(ids[j]) < rank(ids[i]) || (rank(ids[j]) == rank(ids[i]) && ids[j] < ids[i]) {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	return ids
}

// Handle 处理 /-<app> 与 /-api/<app>/<action>；未命中返回 false（交回 WebDAV）。
func (r *Registry) Handle(c Context, w http.ResponseWriter, req *http.Request) bool {
	c = c.withActingAs(req)
	p := req.URL.Path

	if strings.HasPrefix(p, "/-api/") {
		rest := strings.TrimPrefix(p, "/-api/")
		id, action, _ := strings.Cut(rest, "/")
		app, ok := r.apis[id]
		if !ok {
			return false
		}
		if req.Method != http.MethodGet && req.Method != http.MethodPost {
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return true
		}
		// 同源校验：Cookie 会话 + POST 时挡住跨站请求（SameSite=Lax 之外的兜底）。
		if req.Method == http.MethodPost && !sameOrigin(req) {
			Error(w, Errorf(http.StatusForbidden, "跨站请求被拒绝"))
			return true
		}
		if c.DB == nil {
			Error(w, Errorf(http.StatusServiceUnavailable, "个人空间数据库不可用"))
			return true
		}
		// 模块权限（NAS 多用户）：users 应用自带授权逻辑（admin-only + 自改密），
		// 放行进应用；其余（含 home=收藏夹）按 perms 收口 —— 无读权限 403，
		// 有读无写碰上写动作 403。共享层只读（Writable=false）再叠一道。
		if id != "users" && !c.canRead(id) {
			Error(w, Errorf(http.StatusForbidden, "没有「%s」的访问权限", id))
			return true
		}
		run(w, func() error {
			if id != "users" && isWriteAction(action) && !c.canWrite(id) {
				return Errorf(http.StatusForbidden, "没有「%s」的修改权限", id)
			}
			if !c.Writable && isWriteAction(action) {
				return ErrReadOnly
			}
			return app.API(c, w, req, action)
		})
		return true
	}

	if app, ok := r.pages[p]; ok {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return true
		}
		// 页面同口径：没读权限的应用页直接 403（导航上也看不到入口）。
		if !c.canRead(app.ID()) {
			Error(w, Errorf(http.StatusForbidden, "没有「%s」的访问权限", app.ID()))
			return true
		}
		run(w, func() error { return app.Page(c, w, req) })
		return true
	}
	return false
}

// withActingAs 管理员的「代看」：请求带 as=<uid> 且确实是 admin 时，把本次
// 请求的 User 换成目标成员 —— 页面与 API 都生效，管理员能看/管成员数据。
// 目标必须是真实存在的账号（防瞎填）。
func (c Context) withActingAs(req *http.Request) Context {
	if !c.IsAdmin() || c.DB == nil {
		return c
	}
	as := strings.TrimSpace(req.URL.Query().Get("as"))
	if as == "" || as == c.User {
		return c
	}
	var n int
	if err := c.DB.QueryRow(`select count(*) from hub_users where uid=?`, as).Scan(&n); err != nil || n == 0 {
		return c
	}
	c.User = as
	return c
}

// isWriteAction 写接口的动作名约定：create/update/delete/save/import/move/rename/upload。
// 动作名可能带命名空间（cat.save / bulk.delete / group.delete），取最后一段判断，
// 否则只读账号能绕过写保护调用 cat.save。
func isWriteAction(action string) bool {
	if i := strings.LastIndexByte(action, '.'); i >= 0 {
		action = action[i+1:]
	}
	switch action {
	case "create", "update", "delete", "save", "import", "move", "rename", "reorder", "upload", "scan",
		// 监控：装组件、启停录像、抓图/标记片段、跑一次保留清理 —— 都是改状态的动作
		"install", "start", "stop", "mark", "snap", "run",
		// 下载：加任务、暂停/继续、删任务 —— 同样改状态
		"add", "pause", "resume", "remove":
		return true
	}
	return false
}

func sameOrigin(req *http.Request) bool {
	o := req.Header.Get("Origin")
	if o == "" {
		return true // 部分客户端不带 Origin（curl / 脚本），交给 Cookie 认证兜底
	}
	i := strings.Index(o, "://")
	if i < 0 {
		return false
	}
	host := o[i+3:]
	if j := strings.IndexAny(host, "/"); j >= 0 {
		host = host[:j]
	}
	return strings.EqualFold(host, req.Host)
}

// run 统一跑应用 handler：panic 兜底 + 错误映射（不打印敏感信息）。
func run(w http.ResponseWriter, fn func() error) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("appkit: panic: %v", rec)
			Error(w, Errorf(http.StatusInternalServerError, "服务内部错误"))
		}
	}()
	if err := fn(); err != nil {
		Error(w, err)
	}
}

// HTTPError 带状态码的业务错误。
type HTTPError struct {
	Code int
	Msg  string
}

func (e *HTTPError) Error() string { return e.Msg }

func Errorf(code int, format string, a ...any) error {
	return &HTTPError{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// ErrReadOnly 只读账号的写请求。
var ErrReadOnly = &HTTPError{Code: http.StatusMethodNotAllowed, Msg: "只读账号不能修改"}

// Error 把错误写成 JSON（登录页/页面渲染失败时退回纯文本）。
func Error(w http.ResponseWriter, err error) {
	var he *HTTPError
	if errors.As(err, &he) {
		JSON(w, he.Code, map[string]any{"error": he.Msg})
		return
	}
	JSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
}

// JSON 统一 JSON 响应。
func JSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Decode 解析 JSON 请求体（上限 1MB，拒绝未知字段）。
func Decode(req *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(req.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// Period 把时间维度名换算成日期区间（闭区间，YYYY-MM-DD，本地时区）。
// 账本的时间范围 tabs 与笔记的统计卡共用这一套口径，避免两边算法漂移。
// 支持 today / yesterday / week / lastweek / month / lastmonth / year / lastyear，
// 其他值返回两个空串（= 不过滤）。
func Period(name string) (string, string) {
	now := time.Now()
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch name {
	case "today":
		return d.Format(dateLayout), d.Format(dateLayout)
	case "yesterday":
		y := d.AddDate(0, 0, -1)
		return y.Format(dateLayout), y.Format(dateLayout)
	case "week": // 周一为一周开始
		mon := d.AddDate(0, 0, -mondayOffset(d))
		return mon.Format(dateLayout), mon.AddDate(0, 0, 6).Format(dateLayout)
	case "lastweek":
		mon := d.AddDate(0, 0, -mondayOffset(d)-7)
		return mon.Format(dateLayout), mon.AddDate(0, 0, 6).Format(dateLayout)
	case "month":
		return d.Format("2006-01") + "-01", lastDayOfMonth(d)
	case "lastmonth":
		pm := d.AddDate(0, -1, 0)
		return pm.Format("2006-01") + "-01", lastDayOfMonth(pm)
	case "year":
		return d.Format("2006") + "-01-01", d.Format("2006") + "-12-31"
	case "lastyear":
		py := d.AddDate(-1, 0, 0)
		return py.Format("2006") + "-01-01", py.Format("2006") + "-12-31"
	}
	return "", ""
}

const dateLayout = "2006-01-02"

// mondayOffset 距本周一的天数（周日算一周的最后一天）。
func mondayOffset(d time.Time) int {
	return (int(d.Weekday()) + 6) % 7
}

// lastDayOfMonth 当月最后一天。
func lastDayOfMonth(t time.Time) string {
	return time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location()).Format(dateLayout)
}
