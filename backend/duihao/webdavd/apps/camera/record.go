package camera

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
)

// 分段录像：每台相机一个 ffmpeg 进程，`-c copy -f segment` 每 5 分钟落一片。
//
// 关键点：
//   - `-c copy` 不解码不转码，单路 1080p 约 1~3% CPU；H.265 也能原样存
//     （网页放不了是浏览器的事，跟录像无关）；
//   - 分片落盘 → 断电最多丢最后 5 分钟，回放可以按片 seek；
//   - 进程挂了自动重连（5s 起步，60s 封顶），连续失败记日志但不放弃；
//   - 磁盘不够 / 用户停用 → 立即停，绝不让监控把网盘写死。

type recState struct {
	camID   string
	root    string
	uid     string
	proc    *childProc
	since   time.Time
	wanted  bool
	manual  bool // 用户手动点的「开始录像」：计划到点也不去停它
	retry   int
	lastTry time.Time
}

var (
	recMu sync.Mutex
	recs  = map[string]*recState{}
)

// recordArgs ffmpeg 分段录像参数（抽出来便于测试断言关键开关）。
func recordArgs(cam Cam, dir string) []string {
	transport := cam.Transport
	if transport == "" {
		transport = "tcp"
	}
	return []string{
		"-hide_banner", "-loglevel", "warning", "-nostdin",
		"-rtsp_transport", transport, "-rw_timeout", "5000000",
		"-i", streamURL(cam, true),
		"-c", "copy",
		"-f", "segment",
		"-segment_time", "300",
		"-segment_format", "mp4",
		"-reset_timestamps", "1",
		"-strftime", "1",
		filepath.Join(dir, "%Y%m%d_%H%M%S.mp4"),
	}
}

// startRecord 幂等：已经在录就直接返回。
// manual=true 表示用户在页面上手动开的，planLoop 不会因为「出了计划窗口」把它停掉。
func startRecord(c appkit.Context, cam Cam, manual bool) error {
	bin, err := ffmpeg()
	if err != nil {
		return err
	}
	if free := diskFree(c.Root); free > 0 && free < 2<<30 {
		msg := "磁盘可用空间不足 2GB，已拒绝开始录像"
		addLog(c, cam.ID, "error", msg)
		return appkit.Errorf(409, "%s", msg)
	}
	dir := camDir(c.Root, cam.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	recMu.Lock()
	st := recs[cam.ID]
	if st != nil && st.proc != nil && st.proc.running() {
		st.wanted = true
		st.manual = st.manual || manual
		st.lastTry = time.Now()
		recMu.Unlock()
		return nil
	}
	st = &recState{camID: cam.ID, root: c.Root, uid: c.User, wanted: true, manual: manual, lastTry: time.Now()}
	recs[cam.ID] = st
	recMu.Unlock()

	proc, _, err := startChild(bin, recordArgs(cam, dir), filepath.Join(logDir(), "rec-"+cam.ID+".log"), false)
	if err != nil {
		st.wanted = false
		addLog(c, cam.ID, "error", "启动录像进程失败："+err.Error())
		return appkit.Errorf(502, "启动录像失败：%v", err)
	}
	recMu.Lock()
	st.proc = proc
	st.since = time.Now()
	st.retry = 0
	recMu.Unlock()

	_, _ = c.DB.Exec(`update cameras set state='recording', last_error='' where uid=? and id=?`, c.User, cam.ID)
	addLog(c, cam.ID, "info", "开始录像 → "+maskURL(streamURL(cam, true)))

	go watchRecord(c, st, cam, bin, dir, proc)
	return nil
}

// watchRecord 进程退出后按退避重连，直到 wanted=false。
func watchRecord(c appkit.Context, st *recState, cam Cam, bin, dir string, cur *childProc) {
	for {
		for cur != nil && cur.running() {
			time.Sleep(2 * time.Second)
		}
		recMu.Lock()
		wanted := st.wanted
		if !wanted {
			st.proc = nil
		}
		st.retry++
		retry := st.retry
		recMu.Unlock()
		if !wanted {
			return
		}

		// 退避：5s → 10s → 20s → 40s → 60s 封顶
		wait := time.Duration(5<<uint(minInt(retry-1, 3))) * time.Second
		if wait > 60*time.Second {
			wait = 60 * time.Second
		}
		addLog(c, cam.ID, "warn", fmt.Sprintf("录像中断（第 %d 次），%s 后重连", retry, wait))
		_, _ = c.DB.Exec(`update cameras set state='error', last_error=? where uid=? and id=?`,
			"录像中断，正在重连", c.User, cam.ID)

		// 磁盘守护：空间不够就别重连了，等清理后再来
		if free := diskFree(st.root); free > 0 && free < 2<<30 {
			addLog(c, cam.ID, "error", "磁盘不足 2GB，停止录像")
			recMu.Lock()
			st.wanted = false
			st.proc = nil
			recMu.Unlock()
			_, _ = c.DB.Exec(`update cameras set state='stopped', last_error=? where uid=? and id=?`,
				"磁盘空间不足，已停止录像", c.User, cam.ID)
			return
		}

		time.Sleep(wait)

		recMu.Lock()
		if !st.wanted {
			recMu.Unlock()
			return
		}
		recMu.Unlock()

		np, _, err := startChild(bin, recordArgs(cam, dir), filepath.Join(logDir(), "rec-"+cam.ID+".log"), false)
		if err != nil {
			addLog(c, cam.ID, "error", "重连失败："+err.Error())
			cur = nil
			continue
		}
		recMu.Lock()
		st.proc = np
		recMu.Unlock()
		cur = np
		addLog(c, cam.ID, "info", "已重新连上相机")
		_, _ = c.DB.Exec(`update cameras set state='recording', last_error='' where uid=? and id=?`, c.User, cam.ID)
	}
}

// stopRecord 停止某台相机的录像（不删文件）。
func stopRecord(camID string) {
	recMu.Lock()
	st := recs[camID]
	if st != nil {
		st.wanted = false
		st.manual = false // 停了就是停了，下次由计划/开关重新决定
		if st.proc != nil {
			st.proc.stop()
			st.proc = nil
		}
	}
	recMu.Unlock()
}

// stopAll 停掉一台相机的所有进程（删相机/停用时用）。
func stopAll(camID string) {
	stopRecord(camID)
	stopLive(camID)
}

// engineState 运行态：以进程为准，不看 DB 里的旧值。
func engineState(camID string) string {
	recMu.Lock()
	st := recs[camID]
	recording := st != nil && st.proc != nil && st.proc.running()
	recMu.Unlock()
	if recording {
		return "recording"
	}
	liveMu.Lock()
	ls := lives[camID]
	liveMu.Unlock()
	if ls != nil {
		ls.mu.Lock()
		preview := ls.proc != nil && ls.proc.running()
		ls.mu.Unlock()
		if preview {
			return "preview"
		}
	}
	return "stopped"
}

// applyCam 相机改完配置后的引擎跟随动作。
func applyCam(c appkit.Context, cam Cam) {
	if cam.Enabled == 1 && cam.Record == 1 {
		// 计划外不主动拉起来：planTick 会在进窗口时接手。
		// 这里也不去停已有录像（用户可能正手动录着，改个名字不该把录像掐断）。
		if planActive(cam.Plan, time.Now()) {
			// 组件没装 / 相机离线都不该让「保存」失败：存下来，等条件满足再录
			if err := startRecord(c, cam, false); err != nil {
				log.Printf("camera: %s 暂未开始录像: %v", cam.ID, err)
			}
		}
		return
	}
	stopRecord(cam.ID)
	if cam.Enabled == 0 {
		stopLive(cam.ID)
	}
}

// shouldAutoStart 判断 list() 是否需要顺手拉起录像（带 30s 冷却，避免每 3s 轮询就重试一次）。
// 语义：打开监控页 = 恢复上级「本来就该在录」的状态（跟音乐空库懒同步同一套路）。
func shouldAutoStart(camID string) bool {
	recMu.Lock()
	defer recMu.Unlock()
	st := recs[camID]
	if st != nil && st.proc != nil && st.proc.running() {
		return false
	}
	if st != nil && time.Since(st.lastTry) < 30*time.Second {
		return false
	}
	if st == nil {
		st = &recState{camID: camID}
		recs[camID] = st
	}
	st.lastTry = time.Now()
	st.wanted = true
	return true
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// logDir ffmpeg 自己的 stdout/stderr（排障用，不算业务日志）。
func logDir() string {
	d := filepath.Join(os.TempDir(), "duihao-camera")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// addLog 写一条监控日志（页面「日志」抽屉与排障都看这张表）。
func addLog(c appkit.Context, camID, level, msg string) {
	if c.DB == nil {
		return
	}
	_, err := c.DB.Exec(`insert into camera_logs(uid,id,cam_id,level,msg,at) values(?,?,?,?,?,?)`,
		c.User, newID(), camID, level, msg, time.Now().Format(timeLayout))
	if err != nil {
		log.Printf("camera: 写日志失败 %v", err)
	}
}
