// Package voice 语音提醒：写一句话 + 选个说法 + 定个时间，到点由本机替你念出来。
//
// 接口约定（/-api/voice/<action>，POST JSON；audio 例外走 GET 取 wav）：
//
//	list          {period?}                          → {items:[…],counts:{…}}
//	save          {id?,title,content,voice,tone,times,period,weekdays,
//	               intervalMin,dayFrom,dayTo,startDate,atTime,action,actionArg}
//	                                               → {id,nextAt,audio,audioMs,warn?}
//	update        {ids:[…],enabled}                  → {ok,n}（启停）
//	delete        {ids:[…]}                          → {ok,n}
//	preview       {voice,tone,content}               → {audio,audioMs,url,warn?}（试听，不入库）
//	play          {id}                               → {ok}（立刻念一遍，用来确认音色）
//	audio?f=xxx.wav                                  → wav 字节流（GET，只认 _appfiles/voice 下的文件名）
//	log.list      {}                                 → {items:[…]}（最近 50 次触发）
//	voices        {}                                 → {items:[…]}（本机 SAPI 语音）
//	presets       {}                                 → {voices,tones,periods,system,engine}
//	settings      {}                                 → {engine:{kind,base,model,voice,speed,tokenSet,voices}}
//	settings.save {kind,base,model,voice,speed,token?,clearToken?} → {ok,engine}
//	engine.test   {base,model,token?}                 → {ok,ms,voices:[…]}（连不通时 502 + 人话）
//	notify.test   {title,content}                     → {ok,shown}（弹一张预览卡；shown=false 表示没开桌面端）
//	ping
//
// 口径（2026-10-30 定稿，V2 加 GPU 引擎）：
//   - 出声发生在**本机后端**（Windows SAPI5），不由页面负责：窗口最小化、
//     浏览器关掉、断网，到点照样响 —— 这是「提醒」和「网页提醒」的分界；
//   - 音色 = 本机系统语音 × 韵律档，语气 = 语速/音量/句前停顿；
//   - V2 增加 engine=http：局域网里那台 GPU 主机（OpenAI 兼容 /v1/audio/speech），
//     音色好得多；**但它只是可选增强** —— 合成失败一律降级回 SAPI，
//     绝不出现「到点了但没声音」。到点前 30 秒预合成，省掉那 1~3 秒等待；
//   - 到点动作白名单：none|url|path，不执行任意命令（加字段不扩权）。
package voice

import (
	"crypto/rand"
	"crypto/sha1"
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

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
	"github.com/duihao/duihao/backend/duihao/webdavd/webui"
)

const (
	voiceRoot  = "_appfiles/voice" // 网盘根下的合成缓存目录
	timeLayout = "2006-01-02 15:04"
	dateLayout = "2006-01-02"
)

type App struct{}

func New() *App { return &App{} }

func (a *App) ID() string    { return "voice" }
func (a *App) Title() string { return "语音提醒" }

func (a *App) Page(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	return webui.Render(w, webui.Page{App: a.ID(), Title: "语音提醒", User: c.User, Writable: c.Writable, Role: c.Role, Perms: c.Perms})
}

func (a *App) API(c appkit.Context, w http.ResponseWriter, r *http.Request, action string) error {
	switch action {
	case "ping":
		appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "app": a.ID(), "user": c.User})
		return nil
	case "list":
		return listEvents(c, w, r)
	case "save":
		return saveEvent(c, w, r)
	case "update":
		return updateEvents(c, w, r)
	case "delete":
		return deleteEvents(c, w, r)
	case "preview":
		return previewAudio(c, w, r)
	case "play":
		return playNow(c, w, r)
	case "audio":
		return serveAudio(c, w, r)
	case "log.list":
		return listLogs(c, w)
	case "voices":
		appkit.JSON(w, http.StatusOK, map[string]any{"items": systemVoices()})
		return nil
	case "presets":
		eng := loadEngine(c.DB, c.User)
		appkit.JSON(w, http.StatusOK, map[string]any{
			"voices": voicesFor(eng), "tones": tonePresets, "periods": periodChoices,
			"system": systemVoices(), "engine": engineView(eng),
		})
		return nil
	case "settings":
		appkit.JSON(w, http.StatusOK, map[string]any{"engine": engineView(loadEngine(c.DB, c.User))})
		return nil
	case "settings.save":
		return saveSettings(c, w, r)
	case "engine.test":
		return testEngine(c, w, r)
	case "notify.test":
		return testNotice(c, w, r)
	}
	return appkit.Errorf(http.StatusNotFound, "未知操作")
}

// ---------- 事件读 ----------

type event struct {
	ID          string
	Title       string
	Content     string
	Voice       string
	Tone        string
	Times       int
	Period      string
	Weekdays    string
	IntervalMin int
	DayFrom     string
	DayTo       string
	StartDate   string
	AtTime      string
	NextAt      string
	Enabled     bool
	Audio       string
	AudioMs     int
	Action      string
	ActionArg   string
	LastAt      string
}

const eventCols = `id,title,content,voice,tone,times,period,weekdays,interval_min,
	day_from,day_to,start_date,at_time,next_at,enabled,audio,audio_ms,action,action_arg,last_at`

func scanEvent(sc interface{ Scan(...any) error }) (event, error) {
	var ev event
	var enabled int
	err := sc.Scan(&ev.ID, &ev.Title, &ev.Content, &ev.Voice, &ev.Tone, &ev.Times, &ev.Period,
		&ev.Weekdays, &ev.IntervalMin, &ev.DayFrom, &ev.DayTo, &ev.StartDate, &ev.AtTime,
		&ev.NextAt, &enabled, &ev.Audio, &ev.AudioMs, &ev.Action, &ev.ActionArg, &ev.LastAt)
	ev.Enabled = enabled != 0
	return ev, err
}

// 列表附带状态（前端药丸直接用）：即将到点 / 已停用 / 已结束。

func listEvents(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Period string `json:"period"`
	}
	_ = appkit.Decode(r, &in) // 空 body / {} 都允许

	q := `select ` + eventCols + ` from voice_events where uid=?`
	args := []any{c.User}
	if p := strings.TrimSpace(in.Period); p != "" && p != "all" {
		q += ` and period=?`
		args = append(args, p)
	}
	// 排序：先按下次触发升序（没下次的沉底），再按创建时间倒序
	q += ` order by case when next_at='' then 1 else 0 end, next_at, created_at desc`

	rows, err := c.DB.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		ev, err := scanEvent(rows)
		if err != nil {
			return err
		}
		items = append(items, eventJSON(ev))
	}

	counts := map[string]int{"all": 0}
	for _, p := range periodChoices {
		counts[p.Key] = 0
	}
	cr, err := c.DB.Query(`select period, count(*) from voice_events where uid=? group by period`, c.User)
	if err != nil {
		return err
	}
	defer cr.Close()
	for cr.Next() {
		var p string
		var n int
		if err := cr.Scan(&p, &n); err != nil {
			return err
		}
		counts[p] += n
		counts["all"] += n
	}

	appkit.JSON(w, http.StatusOK, map[string]any{"items": items, "counts": counts})
	return nil
}

func eventJSON(ev event) map[string]any {
	status := "pending"
	switch {
	case !ev.Enabled:
		status = "paused"
	case ev.NextAt == "":
		status = "done"
	}
	return map[string]any{
		"id": ev.ID, "title": ev.Title, "content": ev.Content,
		"voice": ev.Voice, "tone": ev.Tone, "times": ev.Times,
		"period": ev.Period, "weekdays": ev.Weekdays, "intervalMin": ev.IntervalMin,
		"dayFrom": ev.DayFrom, "dayTo": ev.DayTo,
		"startDate": ev.StartDate, "atTime": ev.AtTime,
		"nextAt": ev.NextAt, "enabled": ev.Enabled, "status": status,
		"audio": ev.Audio, "audioMs": ev.AudioMs,
		"action": ev.Action, "actionArg": ev.ActionArg, "lastAt": ev.LastAt,
	}
}

func listLogs(c appkit.Context, w http.ResponseWriter) error {
	rows, err := c.DB.Query(`select id,event_id,title,at,ok,msg from voice_logs
		where uid=? order by at desc limit 50`, c.User)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, eid, title, at, msg string
		var ok int
		if err := rows.Scan(&id, &eid, &title, &at, &ok, &msg); err != nil {
			return err
		}
		out = append(out, map[string]any{"id": id, "eventId": eid, "title": title,
			"at": at, "ok": ok != 0, "msg": msg})
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"items": out})
	return nil
}

// ---------- 事件写 ----------

type saveIn struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Content     string `json:"content"`
	Voice       string `json:"voice"`
	Tone        string `json:"tone"`
	Times       int    `json:"times"`
	Period      string `json:"period"`
	Weekdays    string `json:"weekdays"`
	IntervalMin int    `json:"intervalMin"`
	DayFrom     string `json:"dayFrom"`
	DayTo       string `json:"dayTo"`
	StartDate   string `json:"startDate"`
	AtTime      string `json:"atTime"`
	Action      string `json:"action"`
	ActionArg   string `json:"actionArg"`
}

func saveEvent(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in saveIn
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Content = strings.TrimSpace(in.Content)
	if in.Title == "" {
		return appkit.Errorf(http.StatusBadRequest, "标题不能为空")
	}
	if len([]rune(in.Title)) > 60 {
		return appkit.Errorf(http.StatusBadRequest, "标题最多 60 字")
	}
	if len([]rune(in.Content)) > 2000 {
		return appkit.Errorf(http.StatusBadRequest, "内容最多 2000 字")
	}
	if strings.TrimSpace(c.Root) == "" {
		return appkit.Errorf(http.StatusInternalServerError, "网盘目录不可用")
	}

	eng := loadEngine(c.DB, c.User)
	ev := event{
		ID: in.ID, Title: in.Title, Content: in.Content,
		Voice:       pickVoice(eng, in.Voice),
		Tone:        pickKey(in.Tone, tonePresets, "normal"),
		Times:       clampInt(in.Times, 0, 5),
		Period:      pickPeriod(in.Period),
		Weekdays:    normWeekdays(in.Weekdays),
		IntervalMin: clampInt(in.IntervalMin, 1, 720),
		DayFrom:     normClock(in.DayFrom, "09:00"),
		DayTo:       normClock(in.DayTo, "18:00"),
		StartDate:   normDate(in.StartDate),
		AtTime:      normClock(in.AtTime, ""),
		Action:      pickAction(in.Action),
		ActionArg:   strings.TrimSpace(in.ActionArg),
		Enabled:     true,
	}
	if ev.StartDate == "" {
		ev.StartDate = time.Now().Format(dateLayout)
	}
	if ev.Period == "repeat" {
		ev.AtTime = ev.DayFrom // 窗口型不需要单独的时刻
	} else if ev.AtTime == "" {
		return appkit.Errorf(http.StatusBadRequest, "请选择具体时间")
	}
	if ev.Action == "url" || ev.Action == "path" {
		if ev.ActionArg == "" {
			return appkit.Errorf(http.StatusBadRequest, "填一下到点要打开什么")
		}
	}

	// 先合成音频（顺带校验文本能念出来），再算下次时间落库
	audio, audioMs, warn, synthErr := ensureAudio(c, ev)
	ev.Audio, ev.AudioMs = audio, audioMs
	ev.NextAt = ""
	if t := nextDue(ev, time.Now()); !t.IsZero() {
		ev.NextAt = t.Format(timeLayout)
	}

	now := time.Now().Format(time.RFC3339)
	if ev.ID != "" {
		res, err := c.DB.Exec(`update voice_events set title=?,content=?,voice=?,tone=?,times=?,
			period=?,weekdays=?,interval_min=?,day_from=?,day_to=?,start_date=?,at_time=?,
			next_at=?,audio=?,audio_ms=?,action=?,action_arg=?,updated_at=? where uid=? and id=?`,
			ev.Title, ev.Content, ev.Voice, ev.Tone, ev.Times, ev.Period, ev.Weekdays, ev.IntervalMin,
			ev.DayFrom, ev.DayTo, ev.StartDate, ev.AtTime, ev.NextAt, ev.Audio, ev.AudioMs,
			ev.Action, ev.ActionArg, now, c.User, ev.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return appkit.Errorf(http.StatusNotFound, "这条提醒不存在")
		}
	} else {
		ev.ID = newID()
		if _, err := c.DB.Exec(`insert into voice_events(uid,id,title,content,voice,tone,times,
			period,weekdays,interval_min,day_from,day_to,start_date,at_time,next_at,enabled,
			audio,audio_ms,action,action_arg,last_at,created_at,updated_at)
			values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,?,?,?,?,'',?,?)`,
			c.User, ev.ID, ev.Title, ev.Content, ev.Voice, ev.Tone, ev.Times, ev.Period, ev.Weekdays,
			ev.IntervalMin, ev.DayFrom, ev.DayTo, ev.StartDate, ev.AtTime, ev.NextAt,
			ev.Audio, ev.AudioMs, ev.Action, ev.ActionArg, now, now); err != nil {
			return err
		}
	}

	res := map[string]any{"id": ev.ID, "nextAt": ev.NextAt, "audio": ev.Audio, "audioMs": ev.AudioMs}
	switch {
	case synthErr != nil:
		// 合不出来不算失败：事件照样存下，到点改用系统默认声音念，前端给个提示
		res["warn"] = "语音合成失败，将用系统默认声音播报：" + synthErr.Error()
	case warn != "":
		res["warn"] = warn
	}
	appkit.JSON(w, http.StatusOK, res)
	return nil
}

func updateEvents(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		IDs     []string `json:"ids"`
		Enabled bool     `json:"enabled"`
	}
	if err := appkit.Decode(r, &in); err != nil || len(in.IDs) == 0 {
		return appkit.Errorf(http.StatusBadRequest, "缺少 ids")
	}
	now := time.Now()
	n := 0
	for _, id := range in.IDs {
		// 启用时重算 next_at（停用期间错过的点不补播，从下一个点开始）
		if in.Enabled {
			rows, err := c.DB.Query(`select `+eventCols+` from voice_events where uid=? and id=?`, c.User, id)
			if err != nil {
				return err
			}
			if rows.Next() {
				ev, err := scanEvent(rows)
				rows.Close()
				if err != nil {
					return err
				}
				next := ""
				if t := nextDue(ev, now); !t.IsZero() {
					next = t.Format(timeLayout)
				}
				if _, err := c.DB.Exec(`update voice_events set enabled=1,next_at=?,updated_at=?
					where uid=? and id=?`, next, now.Format(time.RFC3339), c.User, id); err != nil {
					return err
				}
				n++
				continue
			}
			rows.Close()
			continue
		}
		res, err := c.DB.Exec(`update voice_events set enabled=0,updated_at=? where uid=? and id=?`,
			now.Format(time.RFC3339), c.User, id)
		if err != nil {
			return err
		}
		if m, _ := res.RowsAffected(); m > 0 {
			n++
		}
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "n": n})
	return nil
}

func deleteEvents(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		IDs []string `json:"ids"`
	}
	if err := appkit.Decode(r, &in); err != nil || len(in.IDs) == 0 {
		return appkit.Errorf(http.StatusBadRequest, "缺少 ids")
	}
	n := 0
	for _, id := range in.IDs {
		res, err := c.DB.Exec(`delete from voice_events where uid=? and id=?`, c.User, id)
		if err != nil {
			return err
		}
		if m, _ := res.RowsAffected(); m > 0 {
			n++
		}
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "n": n})
	return nil
}

// ---------- 试听 / 播放 / 取音频 ----------

func previewAudio(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Voice   string `json:"voice"`
		Tone    string `json:"tone"`
		Content string `json:"content"`
		Title   string `json:"title"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	text := composeText(in.Title, in.Content)
	if text == "" {
		return appkit.Errorf(http.StatusBadRequest, "先写点内容再试听")
	}
	if strings.TrimSpace(c.Root) == "" {
		return appkit.Errorf(http.StatusInternalServerError, "网盘目录不可用")
	}
	vk := pickVoice(loadEngine(c.DB, c.User), in.Voice)
	tk := pickKey(in.Tone, tonePresets, "normal")
	name, ms, warn, err := ensureAudio(c, event{Voice: vk, Tone: tk, Content: text})
	if err != nil {
		return appkit.Errorf(http.StatusInternalServerError, "合成失败：%v", err)
	}
	out := map[string]any{"audio": name, "audioMs": ms,
		"url": "/-api/voice/audio?f=" + name}
	if warn != "" {
		out["warn"] = warn
	}
	appkit.JSON(w, http.StatusOK, out)
	return nil
}

// playNow 让本机立刻念一遍（不写流水，纯确认音色用）。
func playNow(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID      string `json:"id"`
		Voice   string `json:"voice"`
		Tone    string `json:"tone"`
		Content string `json:"content"`
		Title   string `json:"title"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	text, vk, tk := composeText(in.Title, in.Content), in.Voice, in.Tone
	if in.ID != "" {
		rows, err := c.DB.Query(`select `+eventCols+` from voice_events where uid=? and id=?`, c.User, in.ID)
		if err != nil {
			return err
		}
		if !rows.Next() {
			rows.Close()
			return appkit.Errorf(http.StatusNotFound, "这条提醒不存在")
		}
		ev, err := scanEvent(rows)
		rows.Close()
		if err != nil {
			return err
		}
		text, vk, tk = composeText(ev.Title, ev.Content), ev.Voice, ev.Tone
	}
	if text == "" {
		return appkit.Errorf(http.StatusBadRequest, "没有可念的内容")
	}
	eng := loadEngine(c.DB, c.User)
	enqueueSpeech(makeJob(c.Root, c.DB, c.User, eng, text, pickVoice(eng, vk),
		pickKey(tk, tonePresets, "normal"), 1))
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// serveAudio 只认文件名（不带路径），锁死在 _appfiles/voice 下，避免穿越。
func serveAudio(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	f := r.URL.Query().Get("f")
	if !safeAudioName(f) {
		return appkit.Errorf(http.StatusBadRequest, "文件名不合法")
	}
	full := filepath.Join(c.Root, voiceRoot, f)
	fh, err := os.Open(full)
	if err != nil {
		return appkit.Errorf(http.StatusNotFound, "音频不存在")
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, f, st.ModTime(), fh)
	return nil
}

func safeAudioName(f string) bool {
	if f == "" || len(f) > 64 || strings.ContainsAny(f, `/\:`) {
		return false
	}
	for _, r := range f {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-') {
			return false
		}
	}
	return strings.HasSuffix(f, ".wav")
}

// ensureAudio 合成并落盘（内容哈希命名 → 同一段话同一引擎同一音色只合成一次）。
//
// 返回值里 warn 表示「合成成功但降了级」（GPU 主机不通 → 改用本机 SAPI）：
// 文件可用，只是声音不是想要的那个，前端要提示但不算失败。
func ensureAudio(c appkit.Context, ev event) (name string, ms int, warn string, err error) {
	text := composeText(ev.Title, ev.Content)
	if strings.TrimSpace(text) == "" {
		return "", 0, "", nil
	}
	if strings.TrimSpace(c.Root) == "" {
		return "", 0, "", appkit.Errorf(http.StatusInternalServerError, "网盘目录不可用")
	}
	return synthToCache(c.Root, c.DB, c.User, ev, text)
}

// synthToCache 引擎无关的合成入口（HTTP 请求与调度器共用）。
func synthToCache(root string, db *sql.DB, uid string, ev event, text string) (string, int, string, error) {
	eng := loadEngine(db, uid)
	dir := filepath.Join(root, voiceRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, "", err
	}
	vk := effVoice(eng, ev.Voice)
	name := audioName(eng.key(), vk, ev.Tone, text)
	full := filepath.Join(dir, name)
	if st, err := os.Stat(full); err == nil && st.Size() > 64 {
		return name, wavDurationMs(full), "", nil
	}

	_, rate, _, _ := resolveSpeech(vk, ev.Tone)

	if eng.active() {
		ms, err := synthHTTPTo(full, eng, text, rate)
		if err == nil {
			return name, ms, "", nil
		}
		_ = os.Remove(full)
		// 降级保命：写到同一个文件名（内容是 SAPI 的），到点至少有声音
		ms2, err2 := synthWAV(full, text, vk, ev.Tone)
		if err2 != nil {
			_ = os.Remove(full)
			return "", 0, "", fmt.Errorf("GPU 主机与降级都失败：%v / %v", err, err2)
		}
		return name, ms2, "GPU 语音主机不可用，已改用本机语音：" + err.Error(), nil
	}

	ms, err := synthWAV(full, text, vk, ev.Tone)
	if err != nil {
		_ = os.Remove(full)
		return "", 0, "", err
	}
	return name, ms, "", nil
}

// composeText 播报文案：标题 + 内容（内容为空就只念标题）。
func composeText(title, content string) string {
	t, ct := strings.TrimSpace(title), strings.TrimSpace(content)
	switch {
	case t != "" && ct != "":
		return t + "。" + ct
	case ct != "":
		return ct
	}
	return t
}

// audioName 缓存名 = 引擎指纹 + 音色 + 语气 + 文本（任一变化都要重新合成）。
func audioName(engineKey, voiceKey, toneKey, text string) string {
	return hash12(engineKey+"\x00"+voiceKey+"\x00"+toneKey+"\x00"+text) + ".wav"
}

// pickVoice 校验音色键：GPU 引擎认 lan:<服务端音色>，SAPI 引擎认预置键。
// 不认识的一律不写进库（返回空 = 用引擎默认），避免把非法值固化下来。
func pickVoice(eng engineConfig, k string) string {
	k = strings.TrimSpace(k)
	if eng.active() {
		if v, ok := strings.CutPrefix(k, "lan:"); ok && v != "" {
			return "lan:" + v
		}
		return ""
	}
	for _, p := range voicePresets {
		if p.Key == k {
			return k
		}
	}
	return "huihui-std"
}

// ---------- 小工具 ----------

// systemVoice 本机一个 SAPI 语音（tts_*.go 里填充）。
type systemVoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Lang string `json:"lang"`
}

// hash12 内容哈希（音频缓存名：同一段话同一音色只合成一次）。
func hash12(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("v%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func pickKey(k string, presets []preset, fallback string) string {
	for _, p := range presets {
		if p.Key == k {
			return k
		}
	}
	return fallback
}

func pickPeriod(p string) string {
	for _, x := range periodChoices {
		if x.Key == p {
			return p
		}
	}
	return "once"
}

func pickAction(a string) string {
	switch a {
	case "url", "path":
		return a
	}
	return "none"
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

// normClock 规整成 HH:MM（非法值回退到 def）。
func normClock(s, def string) string {
	s = strings.TrimSpace(s)
	t, err := time.Parse("15:04", s)
	if err != nil {
		return def
	}
	return t.Format("15:04")
}

func normDate(s string) string {
	s = strings.TrimSpace(s)
	if _, err := time.Parse(dateLayout, s); err != nil {
		return ""
	}
	return s
}

// normWeekdays 只收 1-7，去重升序，逗号分隔。
func normWeekdays(s string) string {
	seen := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 || n > 7 || seen[n] {
			continue
		}
		seen[n] = true
	}
	out := make([]int, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Ints(out)
	parts := make([]string, len(out))
	for i, n := range out {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}
