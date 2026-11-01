// 到点调度：每 15 秒扫一次「该响的」，交给本机语音念出来。
//
// 三条口径：
//   - 播报由后端负责（不是页面轮询），所以窗口最小化 / 浏览器关掉 / 断网都照响；
//   - 播报走串行队列：两件事同时到点不会叠在一起打架，SAPI 也不并发；
//   - 关机/停用期间错过的点**不补播**，只把 next_at 推到下一个点（否则重启后
//     一开机就连着念十几条旧提醒，那不是秘书是骚扰）。
package voice

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// catchUpLimit 过期多久算「错过」：超过就只推进不播。
const catchUpLimit = 10 * time.Minute

// prewarmAhead 提前多久合成。GPU 合成要 1~3 秒，到点才合成会晚一拍；
// 30 秒够宽（调度器本身 15 秒一扫）。
const prewarmAhead = 30 * time.Second

type speechJob struct {
	text  string
	voice string
	tone  string
	times int
	wav   string // 非空 = 直接放这个文件（GPU 合成结果），放失败再退回 SAPI

	// 归属信息：播报失败时回写一条 voice_logs（页面的「提醒日志」直接能看到
	// 「为什么没声音」）。测试路径没带这些就只进日志文件。
	db    *sql.DB
	uid   string
	evID  string
	title string
}

var speechQ = make(chan speechJob, 32)

// Start 由 webdavd 启动时调用一次；getDB 惰性取库（个人空间首次访问才真正建库），
// getRoot 取网盘根（GPU 引擎的 wav 要落在 _appfiles/voice，调度器自己拿不到 Context）。
func Start(getDB func() *sql.DB, getRoot func() string) {
	go speakLoop()
	go func() {
		time.Sleep(3 * time.Second) // 先让 webdav/实例目录就绪
		tick(getDB, getRoot)
		tk := time.NewTicker(15 * time.Second)
		defer tk.Stop()
		for range tk.C {
			tick(getDB, getRoot)
		}
	}()
}

// tick 一次扫描：先播到点的，再给「马上要响」的预合成。
func tick(getDB func() *sql.DB, getRoot func() string) {
	db := getDB()
	if db == nil {
		return
	}
	root := ""
	if getRoot != nil {
		root = getRoot()
	}
	now := time.Now()
	sweep(db, root, now)
	prewarm(db, root, now)
}

// ---------- 播报队列 ----------

func enqueueSpeech(j speechJob) {
	select {
	case speechQ <- j:
	default:
		Logf("voice: 播报队列已满，丢弃一条（%s）", j.text)
	}
}

func speakLoop() {
	for j := range speechQ {
		n := j.times
		if n > 5 {
			n = 5
		}
		for i := 0; i < n; i++ {
			if err := playOrSpeak(j); err != nil {
				Logf("voice: 播报失败: %v", err)
				logSpeechFail(j, err)
				break
			}
			if i < n-1 {
				time.Sleep(300 * time.Millisecond) // 连播之间喘口气
			}
		}
	}
}

// logSpeechFail 播报失败回写一条 voice_logs（ok=0），页面的「提醒日志」直接可见。
func logSpeechFail(j speechJob, err error) {
	if j.db == nil {
		return
	}
	if _, e := j.db.Exec(`insert into voice_logs(uid,id,event_id,title,at,ok,msg)
		values(?,?,?,?,?,?,?)`,
		j.uid, newID(), j.evID, j.title, time.Now().Format(timeLayout), 0,
		"播报失败："+err.Error()); e != nil {
		Logf("voice: 写播报失败流水出错: %v", e)
	}
}

// playOrSpeak 有 GPU 合成结果就放文件，否则（或放失败）用本机 SAPI 念。
// 「到点一定有声音」全靠这一层兜底：GPU 主机半夜关机也不该让人错过提醒。
func playOrSpeak(j speechJob) error {
	if j.wav != "" {
		if err := playWavFile(j.wav); err == nil {
			return nil
		} else {
			Logf("voice: 合成音频放不出来，改用本机语音: %v", err)
		}
	}
	return speakText(j.text, j.voice, j.tone)
}

// makeJob 组装一次播报：GPU 引擎下先把 wav 备好（缓存命中就秒回），
// 备不出来就留空——播报时退回 SAPI。
func makeJob(root string, db *sql.DB, uid string, eng engineConfig,
	text, sapiVoice, tone string, times int) speechJob {
	j := speechJob{text: text, voice: sapiVoice, tone: tone, times: times}
	if !eng.active() || root == "" {
		return j
	}
	name, _, warn, err := synthToCache(root, db, uid,
		event{Voice: sapiVoice, Tone: tone, Content: text}, text)
	if err != nil {
		Logf("voice: GPU 合成失败，退回本机语音: %v", err)
		return j
	}
	if warn != "" {
		Logf("voice: %s", warn)
	}
	if name != "" {
		j.wav = filepath.Join(root, voiceRoot, name)
	}
	return j
}

// ---------- 扫描与触发 ----------

func sweep(db *sql.DB, root string, now time.Time) {
	// 列顺序必须与 eventCols 一致（多一列 uid 打头）
	rows, err := db.Query(`select uid,`+eventCols+` from voice_events
		where enabled=1 and next_at<>'' and next_at<=? order by next_at limit 20`,
		now.Format(timeLayout))
	if err != nil {
		Logf("voice: 查询到点事件失败: %v", err)
		return
	}
	type due struct {
		uid string
		ev  event
	}
	list := make([]due, 0, 4)
	for rows.Next() {
		var d due
		var enabled int
		if err := rows.Scan(&d.uid, &d.ev.ID, &d.ev.Title, &d.ev.Content, &d.ev.Voice, &d.ev.Tone,
			&d.ev.Times, &d.ev.Period, &d.ev.Weekdays, &d.ev.IntervalMin, &d.ev.DayFrom, &d.ev.DayTo,
			&d.ev.StartDate, &d.ev.AtTime, &d.ev.NextAt, &enabled, &d.ev.Audio, &d.ev.AudioMs,
			&d.ev.Action, &d.ev.ActionArg, &d.ev.LastAt); err != nil {
			rows.Close()
			Logf("voice: 读到点事件失败: %v", err)
			return
		}
		d.ev.Enabled = enabled != 0
		list = append(list, d)
	}
	rows.Close()
	for _, d := range list {
		fire(db, root, d.uid, d.ev, now)
	}
}

// fire 到点处理：播报 + 动作 + 流水 + 推进下次时间。
func fire(db *sql.DB, root, uid string, ev event, now time.Time) {
	ok, msg := true, ""
	late := false
	if at, err := time.ParseInLocation(timeLayout, ev.NextAt, now.Location()); err == nil {
		late = now.Sub(at) > catchUpLimit
	}
	switch {
	case late:
		msg = "错过（关机或停用期间到点），不补播，直接排下一次"
	case ev.Times <= 0:
		msg = "按设置只提醒不出声"
	default:
		eng := loadEngine(db, uid)
		j := makeJob(root, db, uid, eng, composeText(ev.Title, ev.Content),
			pickKey(ev.Voice, voicePresets, "huihui-std"), ev.Tone, ev.Times)
		j.db, j.uid, j.evID, j.title = db, uid, ev.ID, ev.Title
		enqueueSpeech(j)
	}
	if !late {
		// 到点弹卡片：和播报同一个时机、同一个「错过不补」判断。只记录不出声
		// （times<=0）的事件更要弹 —— 那本来就是靠眼睛看的那一类。
		emitNotice(Notice{
			Title:     ev.Title,
			Content:   ev.Content,
			SnoozeMin: SnoozeDefault,
			At:        now,
			Ack: func(action string, minutes int) error {
				return ack(db, uid, ev.ID, action, minutes)
			},
		})
		if err := runAction(ev); err != nil {
			ok = false
			msg = appendMsg(msg, "到点动作失败："+err.Error())
		}
	}

	if _, err := db.Exec(`insert into voice_logs(uid,id,event_id,title,at,ok,msg)
		values(?,?,?,?,?,?,?)`, uid, newID(), ev.ID, ev.Title,
		now.Format(timeLayout), boolInt(ok), msg); err != nil {
		Logf("voice: 写触发流水失败: %v", err)
	}

	next := ""
	if t := nextDue(ev, now); !t.IsZero() {
		next = t.Format(timeLayout)
	}
	// 兜底：算出过去时间就往后推一分钟，避免同一个点每 15 秒重复触发
	if next != "" && next <= now.Format(timeLayout) {
		next = now.Add(time.Minute).Format(timeLayout)
	}
	if _, err := db.Exec(`update voice_events set next_at=?, last_at=?, updated_at=?
		where uid=? and id=?`, next, now.Format(timeLayout), now.Format(time.RFC3339),
		uid, ev.ID); err != nil {
		Logf("voice: 推进下次时间失败: %v", err)
	}
}

// prewarm 到点前 30 秒预合成。
//
// 为什么值得单独一轮：GPU 合成一次 1~3 秒，到点才合成会「慢半拍」——
// 提醒这种事，晚 3 秒就不像提醒了。提前合成好落成 wav，到点只剩一次播放。
// 只对 GPU 引擎做：SAPI 40ms 出音，预热纯属浪费。
func prewarm(db *sql.DB, root string, now time.Time) {
	if root == "" {
		return
	}
	from := now.Format(timeLayout)
	to := now.Add(prewarmAhead).Format(timeLayout)
	rows, err := db.Query(`select uid,id,title,content,voice,tone,audio from voice_events
		where enabled=1 and next_at<>'' and next_at>? and next_at<=?`, from, to)
	if err != nil {
		Logf("voice: 预合成查询失败: %v", err)
		return
	}
	type pending struct {
		uid, id, title, content, voice, tone, audio string
	}
	var list []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.uid, &p.id, &p.title, &p.content, &p.voice, &p.tone, &p.audio); err != nil {
			rows.Close()
			return
		}
		list = append(list, p)
	}
	rows.Close()

	for _, p := range list {
		eng := loadEngine(db, p.uid)
		if !eng.active() {
			continue
		}
		ev := event{Title: p.title, Content: p.content, Voice: p.voice, Tone: p.tone}
		name, ms, warn, err := synthToCache(root, db, p.uid, ev, composeText(p.title, p.content))
		if err != nil {
			Logf("voice: 预合成失败（到点会降级）: %v", err)
			continue
		}
		if warn != "" {
			Logf("voice: 预合成降级: %s", warn)
		}
		if name == "" || name == p.audio {
			continue
		}
		// 把缓存名写回事件：卡片上的波形与试听按钮都靠它
		if _, err := db.Exec(`update voice_events set audio=?, audio_ms=? where uid=? and id=?`,
			name, ms, p.uid, p.id); err != nil {
			Logf("voice: 回写预合成结果失败: %v", err)
		}
	}
}

func appendMsg(a, b string) string {
	if a == "" {
		return b
	}
	return a + "；" + b
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// runAction 到点动作白名单：播报之外只允许打开网址 / 打开文件，不执行任意命令。
func runAction(ev event) error {
	arg := strings.TrimSpace(ev.ActionArg)
	switch ev.Action {
	case "url":
		low := strings.ToLower(arg)
		if !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://") {
			return errBadAction("网址要以 http:// 或 https:// 开头")
		}
		return openTarget(arg)
	case "path":
		if arg == "" {
			return errBadAction("没填要打开的文件")
		}
		if _, err := os.Stat(arg); err != nil {
			return errBadAction("文件不存在：" + arg)
		}
		return openTarget(arg)
	}
	return nil
}

type actionErr string

func (e actionErr) Error() string { return string(e) }

func errBadAction(s string) error { return actionErr(s) }

// ---------- 下次触发时间 ----------

// nextDue 返回严格晚于 from 的下一次触发（本地时区）；零值 = 不再触发。
// 纯函数，所有周期口径都在这里，测试直接喂时间。
func nextDue(ev event, from time.Time) time.Time {
	loc := from.Location()
	start, err := time.ParseInLocation(dateLayout, ev.StartDate, loc)
	if err != nil {
		start = time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	}
	today := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)

	switch ev.Period {
	case "once":
		// 单次就认 start_date 那一天的那个时刻（起始日就是提醒日）
		at, ok := atOn(start, ev.AtTime, loc)
		if !ok || !at.After(from) {
			return time.Time{}
		}
		return at

	case "repeat":
		for i := 0; i < 8; i++ {
			d := today.AddDate(0, 0, i)
			if d.Before(start) {
				continue
			}
			if t := slotInWindow(d, ev, from, loc); !t.IsZero() {
				return t
			}
		}
		return time.Time{}

	case "daily":
		for i := 0; i < 3; i++ {
			d := today.AddDate(0, 0, i)
			if d.Before(start) {
				continue
			}
			if at, ok := atOn(d, ev.AtTime, loc); ok && at.After(from) {
				return at
			}
		}
		return time.Time{}

	case "weekly":
		days := parseWeekdays(ev.Weekdays)
		if len(days) == 0 {
			days = []int{isoWeekday(start)}
		}
		for i := 0; i < 8; i++ {
			d := today.AddDate(0, 0, i)
			if d.Before(start) || !hasInt(days, isoWeekday(d)) {
				continue
			}
			if at, ok := atOn(d, ev.AtTime, loc); ok && at.After(from) {
				return at
			}
		}
		return time.Time{}

	case "monthly", "quarterly", "yearly":
		step := 1
		switch ev.Period {
		case "quarterly":
			step = 3
		case "yearly":
			step = 12
		}
		// 从「起点月」按 step 递进；直接从 from 对应的周期序号开始看，
		// 免得几年前建的提醒要空转几十次（这里最多再往后看 64 个周期）
		k := ((from.Year()-start.Year())*12 + int(from.Month()) - int(start.Month())) / step
		if k < 0 {
			k = 0
		}
		for i := k; i < k+64; i++ {
			y, m := monthAdd(start.Year(), int(start.Month()), step*i)
			d := clampDay(y, time.Month(m), start.Day(), loc)
			if at, ok := atOn(d, ev.AtTime, loc); ok && at.After(from) && !d.Before(start) {
				return at
			}
		}
		return time.Time{}
	}
	return time.Time{}
}

// atOn 某天的 HH:MM。
func atOn(day time.Time, clock string, loc *time.Location) (time.Time, bool) {
	h, m, ok := clockOf(clock)
	if !ok {
		return time.Time{}, false
	}
	return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, loc), true
}

func clockOf(s string) (int, int, bool) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, 0, false
	}
	return t.Hour(), t.Minute(), true
}

// slotInWindow repeat：当天 [day_from, day_to] 内按 interval 均匀排点，取第一个晚于 from 的。
func slotInWindow(day time.Time, ev event, from time.Time, loc *time.Location) time.Time {
	fh, fm, ok1 := clockOf(ev.DayFrom)
	th, tm, ok2 := clockOf(ev.DayTo)
	if !ok1 || !ok2 {
		return time.Time{}
	}
	begin, end := fh*60+fm, th*60+tm
	if begin >= end {
		return time.Time{}
	}
	step := ev.IntervalMin
	if step < 1 {
		step = 60
	}
	for cur := begin; cur <= end; cur += step {
		t := time.Date(day.Year(), day.Month(), day.Day(), cur/60, cur%60, 0, 0, loc)
		if t.After(from) {
			return t
		}
	}
	return time.Time{}
}

func parseWeekdays(s string) []int {
	out := []int{}
	for _, part := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && n >= 1 && n <= 7 {
			out = append(out, n)
		}
	}
	return out
}

func hasInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// isoWeekday 周一=1 … 周日=7。
func isoWeekday(t time.Time) int {
	return (int(t.Weekday())+6)%7 + 1
}

// monthAdd 年月递进（m 从 1 起），m 越界自动进位。
func monthAdd(y, m, delta int) (int, int) {
	t := time.Date(y, time.Month(m)+time.Month(delta), 1, 0, 0, 0, 0, time.UTC)
	return t.Year(), int(t.Month())
}

// clampDay 日大于当月天数时压到月末（1 月 31 日的月度提醒，2 月落在 28/29 号）。
func clampDay(y int, m time.Month, day int, loc *time.Location) time.Time {
	last := time.Date(y, m+1, 0, 0, 0, 0, 0, loc).Day()
	if day > last {
		day = last
	}
	if day < 1 {
		day = 1
	}
	return time.Date(y, m, day, 0, 0, 0, 0, loc)
}
