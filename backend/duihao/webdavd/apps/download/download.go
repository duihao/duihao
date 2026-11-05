// Package download 离线下载：丢个链接进去，对号自己取，取完直接躺在网盘里。
//
// 接口约定（/-api/download/<action>，POST JSON）：
//
//	list          {}                                  → {items:[…],setting:{…},comp:{…},disk:{…},engine:bool}
//	add           {urls:[…],dir?,name?}               → {ok,n,ids:[…],warn?}
//	pause         {id}                                → {ok,state}
//	resume        {id}                                → {ok,state}
//	remove        {id,deleteFile?}                    → {ok}
//	setting.save  {maxConc,downLimit,upLimit,btNet,seed,autoDir} → {ok,setting}
//	comp.status   {}                                  → {id,state,size,url,…}
//	comp.install  {id}                                → {ok,state}（后台下载，轮询 status）
//	comp.import   {id,zip}                            → {ok,dir}（离线导入 zip）
//	engine.stop   {}                                  → {ok}（停引擎，任务留记录）
//
// 口径（2026-10-30 定稿）：
//   - **不做搜索、不做索引、不提供任何种子源**：只接受用户自己粘的链接。
//     加能力不扩责任 —— 跟健康不做 AI 解读、监控不做 PTZ 巡航是同一种克制。
//   - **引擎不打包**：aria2c 是 GPLv2，对号以「下载器模式」让用户自己获取，
//     跟 MySQL 一致；不进安装包、不分发二进制。
//   - **落点即入库**：下到 _appfiles/video → 影视页自动出海报墙；下到
//     _appfiles/music → 音乐页自动解析标签。这是下载做在对号里而不是
//     单独装个工具的唯一理由：省掉一次「下完再搬」。
//   - **磁盘是硬约束**：可用空间低于 2GB 拒绝新任务，绝不让下载把网盘塞爆。
//   - **下完即停不做种**（除非用户在设置里主动开）；BT 网络可在设置里关闭，
//     关掉后对号不往公网暴露任何监听端口。
package download

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
	"github.com/duihao/duihao/backend/duihao/webdavd/sysinfo"
	"github.com/duihao/duihao/backend/duihao/webdavd/webui"
)

// timeLayout 与其它模块一致（本地时间字符串，便于直接看库）。
const timeLayout = "2006-01-02 15:04:05"

// minFree 剩余空间红线：低于这个值拒绝新任务（下载的中途也会停）。
const minFree = 2 << 30 // 2GB

// Resync 由 webdavd 注册时注入：下载完成后让影视/音乐重扫一次。
// 用注入而不是直接 import，是为了保持 apps/* 之间的单向依赖。
var Resync func(c appkit.Context, dir string)

// App 下载模块。
type App struct{}

// New 构造应用实例。
func New() *App { return &App{} }

// ID 路径前缀。
func (a *App) ID() string { return "download" }

// Title 顶栏文案。
func (a *App) Title() string { return "下载" }

// Page 渲染页面。
func (a *App) Page(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	return webui.Render(w, webui.Page{App: a.ID(), Title: "下载", User: c.User, Writable: c.Writable, Role: c.Role, Perms: c.Perms})
}

// Task 一个下载任务（存 download_tasks 表）。
type Task struct {
	ID         string `json:"id"`
	GID        string `json:"gid"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	Kind       string `json:"kind"`   // http | magnet
	Dir        string `json:"dir"`    // download | video | music
	Status     string `json:"status"` // waiting | active | paused | done | error
	Total      int64  `json:"total"`
	Done       int64  `json:"done"`
	Speed      int64  `json:"speed"`
	Seeders    int64  `json:"seeders"`
	Err        string `json:"err"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
	FinishedAt string `json:"finishedAt"`
}

const taskCols = `id,gid,name,url,kind,dir,status,total,done,speed,seeders,err,created_at,updated_at,finished_at`

func scanTask(rows interface{ Scan(...any) error }) (Task, error) {
	var t Task
	err := rows.Scan(&t.ID, &t.GID, &t.Name, &t.URL, &t.Kind, &t.Dir, &t.Status,
		&t.Total, &t.Done, &t.Speed, &t.Seeders, &t.Err, &t.CreatedAt, &t.UpdatedAt, &t.FinishedAt)
	return t, err
}

// listTasks 按创建时间倒序（新的在上，符合「刚加的最想看」）。
func listTasks(c appkit.Context) ([]Task, error) {
	if c.DB == nil {
		return nil, appkit.Errorf(503, "个人空间数据库不可用")
	}
	rows, err := c.DB.Query(`select `+taskCols+` from download_tasks where uid=? order by created_at desc, id desc`, c.User)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	return items, rows.Err()
}

func loadTask(c appkit.Context, id string) (*Task, error) {
	if id == "" {
		return nil, appkit.Errorf(400, "缺少任务 id")
	}
	row := c.DB.QueryRow(`select `+taskCols+` from download_tasks where uid=? and id=?`, c.User, id)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, appkit.Errorf(404, "任务不存在")
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// setTask 局部更新（只改传进来的字段由调用方拼 SQL 决定）。
func setTask(c appkit.Context, id string, sets string, args ...any) error {
	q := `update download_tasks set ` + sets + `, updated_at=? where uid=? and id=?`
	args = append(args, time.Now().Format(timeLayout), c.User, id)
	_, err := c.DB.Exec(q, args...)
	return err
}

// taskDir 目标目录的绝对路径。只认三个落点，别的一律按默认处理
// —— 前端是下拉框，但接口要能挡住手改请求。
func taskDir(c appkit.Context, dir string) string {
	switch dir {
	case "video":
		return filepath.Join(c.Root, "_appfiles/video")
	case "music":
		return filepath.Join(c.Root, "_appfiles/music")
	default:
		return filepath.Join(c.Root, "_appfiles/download")
	}
}

// normDir 规范化落点。
func normDir(dir string) string {
	switch dir {
	case "video", "music":
		return dir
	default:
		return "download"
	}
}

// kindOf 从地址判断类型。只认四种前缀，别的一律拒（挡住 file:// 之类的本地读）。
func kindOf(raw string) (string, bool) {
	u := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.HasPrefix(u, "magnet:"):
		return "magnet", true
	case strings.HasPrefix(u, "http://"), strings.HasPrefix(u, "https://"), strings.HasPrefix(u, "ftp://"):
		return "http", true
	}
	return "", false
}

// guessName 从地址猜一个显示名（引擎回填真实名字之前先有个东西看）。
func guessName(raw string) string {
	if strings.HasPrefix(strings.ToLower(raw), "magnet:") {
		// magnet:?xt=urn:btih:XXXX&dn=名字
		if i := strings.Index(raw, "dn="); i >= 0 {
			s := raw[i+3:]
			if j := strings.IndexByte(s, '&'); j >= 0 {
				s = s[:j]
			}
			if s != "" {
				return s
			}
		}
		return "磁力任务"
	}
	p := raw
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	if p == "" {
		return "下载任务"
	}
	return p
}

// newID 任务 id：12 位十六进制（时间无关，避免并发时撞号）。
func newID() string { return randSecret()[:12] }

// ---------- HTTP 层 ----------

// API 动作分发。
func (a *App) API(c appkit.Context, w http.ResponseWriter, r *http.Request, action string) error {
	switch action {
	// 读
	case "list", "ping":
		return a.list(c, w)
	case "comp.status":
		appkit.JSON(w, 200, compStatus())
		return nil

	// 写
	case "add":
		return a.add(c, w, r)
	case "pause":
		return a.toggle(c, w, r, false)
	case "resume":
		return a.toggle(c, w, r, true)
	case "remove":
		return a.remove(c, w, r)
	case "setting.save":
		return a.saveSetting(c, w, r)
	case "comp.install":
		if err := installComp(); err != nil {
			return err
		}
		appkit.JSON(w, 200, map[string]any{"ok": true, "state": compStatus().State})
		return nil
	case "comp.import":
		return a.compImport(c, w, r)
	case "engine.stop":
		stopEngine()
		appkit.JSON(w, 200, map[string]any{"ok": true})
		return nil
	}
	return appkit.Errorf(404, "未知动作 %s", action)
}

// list 任务列表 + 设置 + 组件 + 磁盘。
func (a *App) list(c appkit.Context, w http.ResponseWriter) error {
	if c.DB == nil {
		return appkit.Errorf(503, "个人空间数据库不可用")
	}
	items, err := listTasks(c)
	if err != nil {
		return err
	}
	running := false
	if e := engineUp(); e != nil {
		running = true
		// 组件装好后第一次进页面：把排队中的任务推给引擎
		kickWaiting(c, e, items)
		syncFromEngine(c, e, &items)
		reapIdle()
	}
	st := loadSetting(c)
	dir := taskDir(c, "download")
	_ = os.MkdirAll(dir, 0o755)
	appkit.JSON(w, 200, map[string]any{
		"items":   items,
		"setting": st,
		"comp":    compStatus(),
		"engine":  running,
		"disk": map[string]any{
			"path": dir,
			"free": sysinfo.DiskFree(c.Root),
		},
	})
	return nil
}

// addReq 新建任务。urls 支持一次粘多条（前端按行拆）。
type addReq struct {
	URLs []string `json:"urls"`
	Dir  string   `json:"dir"`
	Name string   `json:"name"`
}

func (a *App) add(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in addReq
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(400, "参数解析失败：%v", err)
	}
	// 前端可能把整块文本塞进 urls[0]，这里再按行/空格兜一次
	raw := []string{}
	for _, u := range in.URLs {
		if strings.ContainsAny(u, "\r\n") {
			raw = append(raw, strings.Fields(u)...)
			continue
		}
		raw = append(raw, u)
	}

	dir := normDir(in.Dir)
	if in.Dir == "" {
		dir = loadSetting(c).AutoDir
	}
	// 磁盘先看：加进去再失败，用户要多点一次「删掉」
	if free := sysinfo.DiskFree(c.Root); free > 0 && free < minFree {
		return appkit.Errorf(409, "网盘所在卷只剩 %s，先清理再下（低于 2GB 不接受新任务）", humanSize(free))
	}

	seen := map[string]bool{}
	existing, _ := listTasks(c)
	for _, t := range existing {
		if t.Status != "done" {
			seen[t.URL] = true
		}
	}

	now := time.Now().Format(timeLayout)
	ids := []string{}
	var firstBad string
	for _, u := range raw {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			continue
		}
		kind, ok := kindOf(u)
		if !ok {
			if firstBad == "" {
				firstBad = u
			}
			continue
		}
		seen[u] = true
		id := newID()
		name := strings.TrimSpace(in.Name)
		if name == "" {
			name = guessName(u)
		}
		if _, err := c.DB.Exec(`insert into download_tasks(uid,id,gid,name,url,kind,dir,status,total,done,speed,seeders,err,created_at,updated_at,finished_at)
			values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.User, id, "", name, u, kind, dir, "waiting", 0, 0, 0, 0, "", now, now, ""); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		if firstBad != "" {
			return appkit.Errorf(400, "不认识的地址：%s（只支持 http/https/ftp/magnet）", truncate(firstBad, 60))
		}
		return appkit.Errorf(400, "没有可下载的地址")
	}

	// 组件没装时任务照样存下（用户先排好队，装完组件自动开跑）
	res := map[string]any{"ok": true, "n": len(ids), "ids": ids}
	if e, err := ensureEngine(c); err != nil {
		res["warn"] = "任务已排队：" + err.Error()
	} else {
		for _, id := range ids {
			_, _ = pushTask(c, e, id)
		}
	}
	if firstBad != "" {
		res["skipped"] = "已跳过不认识的地址：" + truncate(firstBad, 40)
	}
	appkit.JSON(w, 200, res)
	return nil
}

// toggle 暂停 / 继续。
func (a *App) toggle(c appkit.Context, w http.ResponseWriter, r *http.Request, resume bool) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(400, "参数解析失败：%v", err)
	}
	t, err := loadTask(c, in.ID)
	if err != nil {
		return err
	}
	if resume {
		if t.Status == "done" {
			return appkit.Errorf(409, "这个任务已经下完了")
		}
		e, err := ensureEngine(c)
		if err != nil {
			return err
		}
		// gid 还在引擎里就 unpause，否则重新入队（对号重启后 gid 会失效）
		if t.GID != "" {
			if st, err := e.rpc.status(t.GID); err == nil && st.Status == "paused" {
				if err := e.rpc.unpause(t.GID); err == nil {
					_ = setTask(c, t.ID, "status=?,err=?", "active", "")
					appkit.JSON(w, 200, map[string]any{"ok": true, "state": "active"})
					return nil
				}
			}
		}
		_ = setTask(c, t.ID, "status=?,gid=?,err=?", "waiting", "", "")
		if _, err := pushTask(c, e, t.ID); err != nil {
			return err
		}
		appkit.JSON(w, 200, map[string]any{"ok": true, "state": "waiting"})
		return nil
	}

	if e := engineUp(); e != nil && t.GID != "" {
		_ = e.rpc.pause(t.GID)
	}
	_ = setTask(c, t.ID, "status=?,speed=?", "paused", 0)
	appkit.JSON(w, 200, map[string]any{"ok": true, "state": "paused"})
	return nil
}

// remove 删任务（可选连文件一起删）。
func (a *App) remove(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID         string `json:"id"`
		DeleteFile bool   `json:"deleteFile"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(400, "参数解析失败：%v", err)
	}
	t, err := loadTask(c, in.ID)
	if err != nil {
		return err
	}
	if e := engineUp(); e != nil && t.GID != "" {
		_ = e.rpc.remove(t.GID)
		_ = e.rpc.removeResult(t.GID)
	}
	if in.DeleteFile {
		// 只删目标目录下这一个文件，basename 收敛，绝不递归
		base := filepath.Base(strings.TrimSpace(t.Name))
		if base != "" && base != "." && base != ".." {
			full := filepath.Join(taskDir(c, t.Dir), base)
			if st, err := os.Stat(full); err == nil && !st.IsDir() {
				_ = os.Remove(full)
			}
		}
	}
	if _, err := c.DB.Exec(`delete from download_tasks where uid=? and id=?`, c.User, t.ID); err != nil {
		return err
	}
	appkit.JSON(w, 200, map[string]any{"ok": true})
	return nil
}

// saveSetting 保存设置；顺便把限速/并发同步给在跑的引擎（不用重启）。
func (a *App) saveSetting(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in Setting
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(400, "参数解析失败：%v", err)
	}
	if err := saveSetting(c, in); err != nil {
		return err
	}
	st := loadSetting(c)
	if e := engineUp(); e != nil {
		_ = e.rpc.applyLimits(st.DownLimit, st.UpLimit, st.MaxConc)
	}
	appkit.JSON(w, 200, map[string]any{"ok": true, "setting": st})
	return nil
}

func (a *App) compImport(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID  string `json:"id"`
		Zip string `json:"zip"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(400, "参数解析失败：%v", err)
	}
	dir, err := importComp(in.Zip)
	if err != nil {
		return err
	}
	appkit.JSON(w, 200, map[string]any{"ok": true, "dir": dir})
	return nil
}

// ---------- 引擎与库的同步 ----------

// pushTask 把一条 waiting 任务交给引擎。
func pushTask(c appkit.Context, e *engine, id string) (string, error) {
	t, err := loadTask(c, id)
	if err != nil {
		return "", err
	}
	target := taskDir(c, t.Dir)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}
	opts := map[string]string{"dir": target}
	if t.Name != "" && !strings.HasPrefix(t.URL, "magnet:") {
		// 磁力是多文件/目录名，out 会被 aria2 忽略，别硬塞
		opts["out"] = t.Name
	}
	gid, err := e.rpc.add(t.URL, opts)
	if err != nil {
		_ = setTask(c, t.ID, "status=?,err=?", "error", err.Error())
		return "", appkit.Errorf(502, "交给下载引擎失败：%v", err)
	}
	return gid, setTask(c, t.ID, "status=?,gid=?,err=?", "active", gid, "")
}

// syncFromEngine 把引擎里的实时进度刷回内存列表 + 库（只在状态变化时写库）。
func syncFromEngine(c appkit.Context, e *engine, items *[]Task) {
	busy := false
	for i := range *items {
		t := &(*items)[i]
		if t.GID == "" || t.Status == "done" {
			continue
		}
		st, err := e.rpc.status(t.GID)
		if err != nil {
			// gid 丢了（引擎重启过）：不是错误，重新排队即可
			if t.Status == "active" || t.Status == "waiting" {
				_ = setTask(c, t.ID, "status=?,gid=?", "waiting", "")
				t.Status, t.GID = "waiting", ""
			}
			continue
		}
		t.Total = atoi64(st.TotalLength)
		t.Done = atoi64(st.CompletedLength)
		t.Speed = atoi64(st.DownloadSpeed)
		t.Seeders = int64(atoiSafe(st.NumSeeders))
		if n := st.fileName(); n != "" && t.Name == "" {
			t.Name = n
		}

		switch st.Status {
		case "active":
			busy = true
			if t.Status != "active" {
				t.Status = "active"
				_ = setTask(c, t.ID, "status=?,name=?", "active", t.Name)
			}
		case "waiting":
			busy = true
			t.Status = "waiting"
		case "paused":
			t.Status = "paused"
			t.Speed = 0
		case "complete":
			t.Status = "done"
			t.Speed = 0
			if t.Total == 0 {
				t.Total = t.Done
			}
			now := time.Now().Format(timeLayout)
			t.FinishedAt = now
			_ = setTask(c, t.ID, "status=?,done=?,total=?,speed=?,name=?,finished_at=?,err=?",
				"done", t.Done, t.Total, 0, t.Name, now, "")
			_ = e.rpc.removeResult(t.GID)
			// 下完就通知影视/音乐重扫 —— 这是「落点即入库」的兑现点
			if Resync != nil && (t.Dir == "video" || t.Dir == "music") {
				go Resync(c, t.Dir)
			}
		case "error":
			t.Status = "error"
			t.Speed = 0
			t.Err = st.ErrorMessage
			_ = setTask(c, t.ID, "status=?,err=?,speed=?", "error", t.Err, 0)
		}
	}
	if busy {
		engMu.Lock()
		if eng != nil {
			eng.lastBusy = time.Now()
		}
		engMu.Unlock()
	}
	// 引擎在跑但一条活任务都没有（全下完了 / 全暂停了）：交给 reapIdle 计时
}

// kickWaiting 引擎就绪后把库里排队的任务推给它（就地更新内存列表）。
func kickWaiting(c appkit.Context, e *engine, items []Task) {
	for i := range items {
		t := &items[i]
		if t.Status != "waiting" || t.URL == "" {
			continue
		}
		gid, err := pushTask(c, e, t.ID)
		if err != nil {
			continue
		}
		t.GID = gid
		t.Status = "active"
	}
}

// ---------- 小工具 ----------

func humanSize(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return strconv.FormatInt(n, 10) + " B"
	}
	return strconv.FormatFloat(f, 'f', 1, 64) + " " + units[i]
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
