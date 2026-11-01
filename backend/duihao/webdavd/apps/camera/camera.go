// Package camera 视频监控：把局域网里 IP 摄像机的画面接进对号，实时看、按天回放。
//
// 接口约定（/-api/camera/<action>，POST JSON；live/snap/seg.file/comp.status 走 GET）：
//
//	list          {}                                  → {items:[…],comp:{…},disk:{…}}
//	presets       {}                                  → {vendors:[…]}
//	cam.save      {id?,vendor,host,urlMain,urlSub,transport,record,retainDays,enabled,
//	               credUser?,credPass?,plan?,ptz?,
//	               tpl:{vendor,host,user,pass,channel,port}}  → {id,hasCred,plan,ptz}
//	cam.delete    {ids:[…]}                           → {ok,n}
//	rec.start     {id} / rec.stop {id}                 → {ok,state}
//	plan.save     {id,plan:"[{days,from,to}]"}         → {ok,plan,planText,state}
//	ptz.move      {id,dir,speed}                       → {ok}
//	ptz.preset    {id,act:goto|set,n}                  → {ok}
//	live?cam=<id>&w=960                               → multipart/x-mixed-replace（MJPEG）
//	snap          {id}                                → {path,at}（抓一张存网盘）
//	seg.list      {cam,date}                          → {items:[…],days:[…]}
//	seg.file?cam=<id>&f=<file>                        → mp4 字节流（GET，支持 Range）
//	seg.delete    {cam,file}                          → {ok}
//	seg.mark      {cam,file,mark}                      → {ok}
//	comp.status   {}                                  → {id,state,size,url,…}
//	comp.install  {id}                                → {ok,state}（后台下载，轮询 status）
//	comp.import   {id,zip}                             → {ok,dir}（离线导入 zip）
//	discover      {}                                  → {items:[…]}（ONVIF WS-Discovery）
//	log.list      {cam?}                              → {items:[…]}
//
// 口径（2026-10-30 定稿，V2 加凭证/计划/PTZ）：
//   - 组件用到才下载：ffmpeg 不进安装包，首次启用时经五级取包获取（LGPL 构建）；
//   - 录像用 `-c copy -f segment`，**不转码**，单路 1080p 约 1~3% CPU，5 分钟一片；
//   - 磁盘是硬约束：保留天数 + 卷可用空间低于阈值自动停录，绝不让监控写死网盘；
//   - H.265 相机网页放不了（浏览器不解），页面明确提示改子码流为 H.264；
//   - **凭证不进库明文**：地址入库前剥掉 user:pass@，密码走 credentials 保险箱，
//     只在给 ffmpeg 拼地址时解密，日志一律脱敏；
//   - **计划为空 = 全天录**（与 v1 数据兼容），手动开的录像不被计划抢停。
package camera

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/webui"
)

const (
	camRoot    = "_appfiles/camera" // 网盘根下的监控目录
	timeLayout = "2006-01-02 15:04:05"
	dateLayout = "2006-01-02"
	segDur     = 5 * time.Minute // 分片时长（与 ffmpeg -segment_time 300 对齐）
)

type App struct{}

func New() *App { return &App{} }

func (a *App) ID() string    { return "camera" }
func (a *App) Title() string { return "监控" }

func (a *App) Page(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	return webui.Render(w, webui.Page{App: a.ID(), Title: "监控", User: c.User, Writable: c.Writable, Role: c.Role, Perms: c.Perms})
}

// Cam 一台相机（存 cameras 表）。
type Cam struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Vendor     string `json:"vendor"`
	Host       string `json:"host"`
	URLMain    string `json:"urlMain"`
	URLSub     string `json:"urlSub"`
	Transport  string `json:"transport"`
	Record     int    `json:"record"`
	RetainDays int    `json:"retainDays"`
	Enabled    int    `json:"enabled"`
	Sort       int    `json:"sort"`
	State      string `json:"state"`
	LastError  string `json:"lastError"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`

	// 凭证：用户名明文，密码密文（credentials 包），密文绝不回给前端
	CredUser string `json:"credUser"`
	CredSeal string `json:"-"`
	HasCred  bool   `json:"hasCred"`

	Plan string `json:"plan"` // 录像计划 JSON（空 = 全天）
	Ptz  string `json:"ptz"`  // 云台能力：hik:1 / dahua:1 / 空
}

// previewURL 库里存的子码流地址（不含账号密码，仅用于展示与排障）。
func (c Cam) previewURL() string {
	if strings.TrimSpace(c.URLSub) != "" {
		return c.URLSub
	}
	return c.URLMain
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

// ---------- API ----------

func (a *App) API(c appkit.Context, w http.ResponseWriter, r *http.Request, action string) error {
	switch action {
	// 读
	case "list", "ping":
		return a.list(c, w)
	case "presets":
		appkit.JSON(w, 200, map[string]any{"vendors": vendorPresets()})
		return nil
	case "comp.status":
		appkit.JSON(w, 200, statusOf("ffmpeg"))
		return nil
	case "seg.list":
		return a.segList(c, w, r)
	case "seg.file":
		return a.segFile(c, w, r)
	case "log.list":
		return a.logList(c, w, r)
	case "live":
		return a.live(c, w, r)
	case "discover":
		return a.discover(c, w, r)

	// 写
	case "cam.save":
		return a.save(c, w, r)
	case "cam.delete":
		return a.remove(c, w, r)
	case "rec.start":
		return a.recToggle(c, w, r, true)
	case "rec.stop":
		return a.recToggle(c, w, r, false)
	case "snap":
		return a.snap(c, w, r)
	case "seg.delete":
		return a.segDelete(c, w, r)
	case "seg.mark":
		return a.segMark(c, w, r)
	case "retention.run":
		return a.retentionRun(c, w, r)
	case "comp.install":
		return a.compInstall(c, w, r)
	case "comp.import":
		return a.compImport(c, w, r)
	case "plan.save":
		return a.planSave(c, w, r)
	case "ptz.move":
		return a.ptzMoveAPI(c, w, r)
	case "ptz.preset":
		return a.ptzPresetAPI(c, w, r)
	}
	return appkit.Errorf(404, "未知接口 %s", action)
}

// camCols 相机列顺序（list 与 loadCam 共用，避免两处漂移）。
const camCols = `id,name,vendor,host,url_main,url_sub,transport,record,retain_days,
	enabled,sort,state,last_error,created_at,updated_at,cred_user,cred_seal,plan,ptz`

// scanner 同时适配 *sql.Row 与 *sql.Rows。
type scanner interface{ Scan(dest ...any) error }

func scanCam(s scanner) (Cam, error) {
	var m Cam
	err := s.Scan(&m.ID, &m.Name, &m.Vendor, &m.Host, &m.URLMain, &m.URLSub, &m.Transport,
		&m.Record, &m.RetainDays, &m.Enabled, &m.Sort, &m.State, &m.LastError,
		&m.CreatedAt, &m.UpdatedAt, &m.CredUser, &m.CredSeal, &m.Plan, &m.Ptz)
	m.HasCred = m.CredSeal != ""
	return m, err
}

func (a *App) list(c appkit.Context, w http.ResponseWriter) error {
	rows, err := c.DB.Query(`select `+camCols+` from cameras where uid=? order by sort, name`, c.User)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []Cam{}
	for rows.Next() {
		m, err := scanCam(rows)
		if err != nil {
			return err
		}
		items = append(items, m)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// 运行态以引擎为准（DB 里的 state 可能是上次退出时留下的）
	for i := range items {
		items[i].State = engineState(items[i].ID)
	}
	// 打开页面 = 恢复「本来就该在录」的相机（组件没装时什么都不做）
	a.kickPending(c, items)
	a.startRetentionLoop(c)
	a.startPlanLoop(c)

	total, used := camUsage(filepath.Join(c.Root, camRoot))
	appkit.JSON(w, 200, map[string]any{
		"items": items,
		"comp":  statusOf("ffmpeg"),
		"disk": map[string]any{
			"path":     filepath.Join(c.Root, camRoot),
			"used":     used,
			"segments": total,
			"free":     diskFree(c.Root),
		},
		"now": time.Now().Format(timeLayout),
	})
	return nil
}

// save 新建/修改相机。既支持直接给 rtsp 地址，也支持「品牌 + 主机 + 账号密码」拼出来。
//
// 凭证口径：地址入库前一定剥掉账号密码（splitCred），密码进 credentials 保险箱。
// 修改时不传 credPass 就沿用原密码 —— 用户改个名字不该被迫重输密码。
func (a *App) save(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID         string  `json:"id"`
		Name       string  `json:"name"`
		Vendor     string  `json:"vendor"`
		Host       string  `json:"host"`
		URLMain    string  `json:"urlMain"`
		URLSub     string  `json:"urlSub"`
		Transport  string  `json:"transport"`
		Record     *int    `json:"record"`
		RetainDays *int    `json:"retainDays"`
		Enabled    *int    `json:"enabled"`
		CredUser   string  `json:"credUser"`
		CredPass   string  `json:"credPass"`
		Plan       *string `json:"plan"`
		Ptz        *string `json:"ptz"`
		Tpl        *struct {
			Vendor  string `json:"vendor"`
			Host    string `json:"host"`
			User    string `json:"user"`
			Pass    string `json:"pass"`
			Channel int    `json:"channel"`
			Port    int    `json:"port"`
		} `json:"tpl"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(400, "参数解析失败：%v", err)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return appkit.Errorf(400, "请给相机起个名字")
	}

	id := strings.TrimSpace(in.ID)
	// 老值兜底：改配置时没重填密码就沿用原来的
	credUser, credSeal, plan, ptz := "", "", "", ""
	if id != "" {
		if old, err := loadCam(c, id); err == nil {
			credUser, credSeal, plan, ptz = old.CredUser, old.CredSeal, old.Plan, old.Ptz
		}
	}

	urlMain, urlSub := strings.TrimSpace(in.URLMain), strings.TrimSpace(in.URLSub)
	vendor, host := strings.TrimSpace(in.Vendor), strings.TrimSpace(in.Host)
	if in.Tpl != nil && strings.TrimSpace(in.Tpl.Host) != "" {
		v := in.Tpl.Vendor
		if v == "" {
			v = "custom"
		}
		p := presetOf(v)
		if p.Key == "" {
			// 未知品牌：当成自定义
			p = presetOf("custom")
		}
		ch := in.Tpl.Channel
		if ch <= 0 {
			ch = 1
		}
		port := in.Tpl.Port
		if port <= 0 {
			port = 554
		}
		// 模板里的 {user}:{pass}@ 不填真值：地址保持干净，凭据另存
		urlMain = p.build(in.Tpl.Host, port, "", "", ch, false)
		urlSub = p.build(in.Tpl.Host, port, "", "", ch, true)
		vendor, host = v, strings.TrimSpace(in.Tpl.Host)

		if u := strings.TrimSpace(in.Tpl.User); u != "" {
			credUser = u
		}
		if pw := in.Tpl.Pass; strings.TrimSpace(pw) != "" {
			s, err := sealPass(pw)
			if err != nil {
				return appkit.Errorf(500, "密码加密失败：%v", err)
			}
			credSeal = s
		}
	}
	if u := strings.TrimSpace(in.CredUser); u != "" {
		credUser = u
	}
	if strings.TrimSpace(in.CredPass) != "" {
		s, err := sealPass(in.CredPass)
		if err != nil {
			return appkit.Errorf(500, "密码加密失败：%v", err)
		}
		credSeal = s
	}

	// 用户直接贴了带账号密码的地址 → 拆出来单独存
	if clean, u, pw := splitCred(urlMain); u != "" || pw != "" {
		urlMain = clean
		if credUser == "" {
			credUser = u
		}
		if pw != "" && strings.TrimSpace(in.CredPass) == "" {
			if s, err := sealPass(pw); err == nil {
				credSeal = s
			}
		}
	}
	if clean, u, pw := splitCred(urlSub); u != "" || pw != "" {
		urlSub = clean
		if credUser == "" {
			credUser = u
		}
		if pw != "" && strings.TrimSpace(in.CredPass) == "" {
			if s, err := sealPass(pw); err == nil {
				credSeal = s
			}
		}
	}

	if urlMain == "" && urlSub == "" {
		return appkit.Errorf(400, "请填写 RTSP 地址（或选品牌后填主机与账号）")
	}
	for _, u := range []string{urlMain, urlSub} {
		if u == "" {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(u), "rtsp://") && !strings.HasPrefix(strings.ToLower(u), "rtsps://") {
			return appkit.Errorf(400, "只接受 rtsp:// 地址：%s", u)
		}
	}
	if urlSub == "" {
		urlSub = urlMain
	}
	transport := in.Transport
	if transport != "udp" {
		transport = "tcp"
	}
	rec, retain, enabled := 1, 7, 1
	if in.Record != nil {
		rec = clampInt(*in.Record, 0, 1)
	}
	if in.RetainDays != nil {
		retain = clampInt(*in.RetainDays, 0, 365)
	}
	if in.Enabled != nil {
		enabled = clampInt(*in.Enabled, 0, 1)
	}

	if in.Plan != nil {
		p, err := normPlan(*in.Plan)
		if err != nil {
			return appkit.Errorf(400, "%v", err)
		}
		plan = p
	}
	if in.Ptz != nil {
		ptz = strings.TrimSpace(*in.Ptz)
		if strings.EqualFold(ptz, "none") {
			ptz = "" // 明确表示「不要云台」
		}
	}
	if ptz == "" && !(in.Ptz != nil && strings.EqualFold(strings.TrimSpace(*in.Ptz), "none")) {
		switch strings.ToLower(vendor) {
		case "hikvision":
			ptz = "hik:1"
		case "dahua":
			ptz = "dahua:1"
		}
	}

	now := time.Now().Format(timeLayout)
	if id == "" {
		id = newID()
		var mx sql.NullInt64
		_ = c.DB.QueryRow(`select max(sort) from cameras where uid=?`, c.User).Scan(&mx)
		_, err := c.DB.Exec(`insert into cameras(uid,id,name,vendor,host,url_main,url_sub,transport,
			record,retain_days,enabled,sort,state,last_error,created_at,updated_at,
			cred_user,cred_seal,plan,ptz)
			values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.User, id, name, vendor, host, urlMain, urlSub, transport,
			rec, retain, enabled, int(mx.Int64)+10, "stopped", "", now, now,
			credUser, credSeal, plan, ptz)
		if err != nil {
			return err
		}
	} else {
		res, err := c.DB.Exec(`update cameras set name=?,vendor=?,host=?,url_main=?,url_sub=?,transport=?,
			record=?,retain_days=?,enabled=?,updated_at=?,cred_user=?,cred_seal=?,plan=?,ptz=?
			where uid=? and id=?`,
			name, vendor, host, urlMain, urlSub, transport, rec, retain, enabled, now,
			credUser, credSeal, plan, ptz, c.User, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return appkit.Errorf(404, "相机不存在")
		}
	}
	cam, err := loadCam(c, id)
	if err != nil {
		return err
	}
	// 改了地址/开关，引擎要跟着调整
	applyCam(c, *cam)
	appkit.JSON(w, 200, map[string]any{
		"id": id, "state": engineState(id),
		"hasCred": credSeal != "", "plan": plan, "planText": planSummary(plan), "ptz": ptz,
	})
	return nil
}

func (a *App) remove(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		IDs []string `json:"ids"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(400, "参数解析失败：%v", err)
	}
	n := 0
	for _, id := range in.IDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		stopAll(id) // 先停进程，再删数据
		res, err := c.DB.Exec(`delete from cameras where uid=? and id=?`, c.User, id)
		if err != nil {
			return err
		}
		if k, _ := res.RowsAffected(); k > 0 {
			n++
		}
		_, _ = c.DB.Exec(`delete from camera_segments where uid=? and cam_id=?`, c.User, id)
		// 录像文件保留（用户删相机不等于删证据），目录留给「清理」按钮
	}
	appkit.JSON(w, 200, map[string]any{"ok": true, "n": n})
	return nil
}

func (a *App) recToggle(c appkit.Context, w http.ResponseWriter, r *http.Request, on bool) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(400, "参数解析失败：%v", err)
	}
	cam, err := loadCam(c, strings.TrimSpace(in.ID))
	if err != nil {
		return err
	}
	if on {
		if err := startRecord(c, *cam, true); err != nil {
			return err
		}
	} else {
		stopRecord(cam.ID)
	}
	appkit.JSON(w, 200, map[string]any{"ok": true, "state": engineState(cam.ID)})
	return nil
}

func (a *App) logList(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	camID := r.URL.Query().Get("cam")
	q := `select id,cam_id,level,msg,at from camera_logs where uid=?`
	args := []any{c.User}
	if camID != "" {
		q += ` and cam_id=?`
		args = append(args, camID)
	}
	q += ` order by at desc limit 100`
	rows, err := c.DB.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, cam, lv, msg, at string
		if err := rows.Scan(&id, &cam, &lv, &msg, &at); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": id, "cam": cam, "level": lv, "msg": msg, "at": at})
	}
	appkit.JSON(w, 200, map[string]any{"items": items})
	return nil
}

// ---------- 录像计划 / 云台 ----------

// planSave 只改计划，不动其它配置（前端弹窗里点「保存计划」走这条）。
func (a *App) planSave(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID   string `json:"id"`
		Plan string `json:"plan"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(400, "参数解析失败：%v", err)
	}
	p, err := normPlan(in.Plan)
	if err != nil {
		return appkit.Errorf(400, "%v", err)
	}
	cam, err := loadCam(c, strings.TrimSpace(in.ID))
	if err != nil {
		return err
	}
	if _, err := c.DB.Exec(`update cameras set plan=?, updated_at=? where uid=? and id=?`,
		p, time.Now().Format(timeLayout), c.User, cam.ID); err != nil {
		return err
	}
	cam.Plan = p
	// 立刻按新计划调整一次：进了窗口就开录（出窗口的停录交给 planTick，避免抢停手动录像）
	if p != "" && planActive(p, time.Now()) {
		if err := startRecord(c, *cam, false); err != nil {
			addLog(c, cam.ID, "warn", "新计划立即开录失败："+err.Error())
		}
	}
	appkit.JSON(w, 200, map[string]any{
		"ok": true, "plan": p, "planText": planSummary(p), "state": engineState(cam.ID),
	})
	return nil
}

// ptzMoveAPI 点一下方向 / 松手停。
func (a *App) ptzMoveAPI(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID    string `json:"id"`
		Dir   string `json:"dir"`
		Speed int    `json:"speed"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(400, "参数解析失败：%v", err)
	}
	cam, err := loadCam(c, strings.TrimSpace(in.ID))
	if err != nil {
		return err
	}
	if err := ptzMove(*cam, strings.TrimSpace(in.Dir), in.Speed); err != nil {
		addLog(c, cam.ID, "warn", "云台："+err.Error())
		return appkit.Errorf(502, "%v", err)
	}
	appkit.JSON(w, 200, map[string]any{"ok": true})
	return nil
}

// ptzPresetAPI 预置位：act=goto 调用 / act=set 保存。
func (a *App) ptzPresetAPI(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID  string `json:"id"`
		Act string `json:"act"`
		N   int    `json:"n"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(400, "参数解析失败：%v", err)
	}
	act := strings.ToLower(strings.TrimSpace(in.Act))
	if act != "goto" && act != "set" {
		return appkit.Errorf(400, "act 只能是 goto 或 set")
	}
	cam, err := loadCam(c, strings.TrimSpace(in.ID))
	if err != nil {
		return err
	}
	if err := ptzPreset(*cam, act, in.N); err != nil {
		addLog(c, cam.ID, "warn", "预置位："+err.Error())
		return appkit.Errorf(502, "%v", err)
	}
	appkit.JSON(w, 200, map[string]any{"ok": true})
	return nil
}

// ---------- 小工具 ----------

func loadCam(c appkit.Context, id string) (*Cam, error) {
	if id == "" {
		return nil, appkit.Errorf(400, "缺少相机 id")
	}
	m, err := scanCam(c.DB.QueryRow(`select `+camCols+` from cameras where uid=? and id=?`, c.User, id))
	if err == sql.ErrNoRows {
		return nil, appkit.Errorf(404, "相机不存在")
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func camDir(root, camID string) string {
	return filepath.Join(root, filepath.FromSlash(camRoot), camID)
}

// camUsage 统计监控目录下有多少片段、占多少字节。
func camUsage(dir string) (segments int, used int64) {
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(p), ".mp4") {
			if info, e := d.Info(); e == nil {
				segments++
				used += info.Size()
			}
		}
		return nil
	})
	return
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func sortedStrings(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fmtErr(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
