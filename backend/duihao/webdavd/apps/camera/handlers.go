package camera

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
)

// 组件安装 / 离线导入 / 手动清理——这三个动作都在页面按钮后面，不在启动路径上。

func (a *App) compInstall(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	_ = appkit.Decode(r, &in)
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = "ffmpeg"
	}
	if err := installAsync(id); err != nil {
		return err
	}
	appkit.JSON(w, 200, map[string]any{"ok": true, "comp": statusOf(id)})
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
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = "ffmpeg"
	}
	dir, err := importZip(id, in.Zip)
	if err != nil {
		return appkit.Errorf(400, "导入失败：%v", err)
	}
	appkit.JSON(w, 200, map[string]any{"ok": true, "dir": dir, "comp": statusOf(id)})
	return nil
}

// retentionRun 手动清理旧录像（保留天数由每台相机自己定）。
func (a *App) retentionRun(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	files, freed := a.runRetention(c)
	appkit.JSON(w, 200, map[string]any{"ok": true, "files": files, "freed": freed})
	return nil
}

// kickPending 打开页面时恢复「本来就该在录」的相机。
// 有 30s 冷却，失败不会每 3 秒重试一次打满日志。
func (a *App) kickPending(c appkit.Context, cams []Cam) {
	if _, err := ffmpeg(); err != nil {
		return // 组件还没装，等用户点下载
	}
	for _, m := range cams {
		if m.Enabled != 1 || m.Record != 1 {
			continue
		}
		if !planActive(m.Plan, time.Now()) {
			continue // 计划外：现在是「不该录」的时间，别硬拉起来
		}
		if !shouldAutoStart(m.ID) {
			continue
		}
		m2 := m
		go func() { _ = startRecord(c, m2, false) }()
	}
}

// retentionLoop 保留清理的后台节拍（每 10 分钟一次）。
// 由页面首次打开时启动，进程内只跑一份（sync.Once 兜并发）。
var retentionOnce sync.Once

func (a *App) startRetentionLoop(c appkit.Context) {
	retentionOnce.Do(func() { go a.retentionLoop(c) })
}

func (a *App) retentionLoop(c appkit.Context) {
	for {
		time.Sleep(10 * time.Minute)
		if c.DB == nil {
			continue
		}
		a.runRetention(c)
	}
}

// ---------- 录像计划循环 ----------

// planLoop 每 30 秒看一次：进窗口就开录，出窗口就停录。
// 手动开的录像（recState.manual）不受计划约束 —— 用户点了「开始」就是明确意图。
var planOnce sync.Once

func (a *App) startPlanLoop(c appkit.Context) {
	planOnce.Do(func() { go a.planLoop(c) })
}

func (a *App) planLoop(c appkit.Context) {
	tk := time.NewTicker(30 * time.Second)
	defer tk.Stop()
	for range tk.C {
		if c.DB == nil {
			continue
		}
		a.planTick(c, time.Now())
	}
}

// planTick 一次计划巡检（抽出来便于单测把时间钉死）。
func (a *App) planTick(c appkit.Context, now time.Time) {
	if _, err := ffmpeg(); err != nil {
		return // 组件没装，计划也录不了
	}
	rows, err := c.DB.Query(`select `+camCols+` from cameras where uid=? and enabled=1 and record=1`, c.User)
	if err != nil {
		return
	}
	var cams []Cam
	for rows.Next() {
		m, err := scanCam(rows)
		if err != nil {
			break
		}
		cams = append(cams, m)
	}
	rows.Close()

	for _, cam := range cams {
		if strings.TrimSpace(cam.Plan) == "" {
			continue // 全天录的相机不用管
		}
		want := planActive(cam.Plan, now)
		recMu.Lock()
		st := recs[cam.ID]
		running := st != nil && st.proc != nil && st.proc.running()
		manual := st != nil && st.manual
		recMu.Unlock()

		switch {
		case want && !running:
			if shouldAutoStart(cam.ID) {
				if err := startRecord(c, cam, false); err != nil {
					addLog(c, cam.ID, "warn", "计划到点开录失败："+err.Error())
				} else {
					addLog(c, cam.ID, "info", "计划生效，开始录像")
				}
			}
		case !want && running && !manual:
			stopRecord(cam.ID)
			addLog(c, cam.ID, "info", "计划时段结束，停止录像")
		}
	}
}
