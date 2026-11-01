package camera

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
)

// 实时预览：一路 ffmpeg 转 MJPEG，多个人看同一路只起一个进程（fan-out）。
//
// 为什么用 MJPEG 而不是 MSE/WebRTC：`<img src>` 原生支持 multipart/x-mixed-replace，
// 前端零依赖、断网可用、失败可见；代价是带宽和 8fps 的「够看」画质。
// 真要 200ms 级延迟再引 WebRTC 组件，不动这层。

type liveStream struct {
	camID string
	url   string

	mu    sync.Mutex
	subs  map[int]chan []byte
	seq   int
	proc  *childProc
	done  chan struct{} // 当前 ffmpeg 生命周期的关闭信号
	lastE string
	idle  *time.Timer
}

var (
	liveMu sync.Mutex
	lives  = map[string]*liveStream{}
)

func liveOf(camID string) *liveStream {
	liveMu.Lock()
	defer liveMu.Unlock()
	s, ok := lives[camID]
	if !ok {
		s = &liveStream{camID: camID, subs: map[int]chan []byte{}}
		lives[camID] = s
	}
	return s
}

// subscribe 订阅一路画面；返回数据通道与退订函数。
func (s *liveStream) subscribe(url string) (<-chan []byte, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
	if s.proc == nil || !s.proc.running() || s.url != url {
		s.restartLocked(url)
	}
	id := s.seq
	s.seq++
	ch := make(chan []byte, 48)
	s.subs[id] = ch

	return ch, func() {
		s.mu.Lock()
		delete(s.subs, id)
		empty := len(s.subs) == 0
		s.mu.Unlock()
		if empty {
			// 没人看就 20 秒后关掉，避免长期占一路相机连接
			s.mu.Lock()
			if s.idle == nil && len(s.subs) == 0 {
				s.idle = time.AfterFunc(20*time.Second, func() {
					s.mu.Lock()
					defer s.mu.Unlock()
					if len(s.subs) == 0 {
						s.stopLocked()
					}
				})
			}
			s.mu.Unlock()
		}
	}
}

func (s *liveStream) restartLocked(url string) {
	s.stopLocked()
	bin, err := ffmpeg()
	if err != nil {
		s.lastE = err.Error()
		return
	}
	s.url = url
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-rtsp_transport", "tcp", "-rw_timeout", "5000000",
		"-i", url,
		"-an",
		"-vf", "scale=960:-2",
		"-r", "8",
		"-q:v", "6",
		"-f", "mpjpeg", "-",
	}
	proc, out, err := startChild(bin, args, "", true)
	if err != nil {
		s.lastE = err.Error()
		return
	}
	s.proc = proc
	s.lastE = ""
	done := make(chan struct{})
	s.done = done

	go func() {
		defer close(done)
		buf := make([]byte, 32<<10)
		for {
			n, err := out.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				s.mu.Lock()
				for _, c := range s.subs {
					select {
					case c <- chunk:
					default: // 订阅者太慢就丢帧，绝不拖住整路
					}
				}
				s.mu.Unlock()
			}
			if err != nil {
				if err != io.EOF {
					s.mu.Lock()
					s.lastE = err.Error()
					s.mu.Unlock()
				}
				break
			}
		}
		s.mu.Lock()
		if s.proc == proc {
			s.proc = nil
		}
		s.mu.Unlock()
	}()
}

func (s *liveStream) stopLocked() {
	if s.proc != nil {
		s.proc.stop()
		s.proc = nil
	}
}

func stopLive(camID string) {
	liveMu.Lock()
	s := lives[camID]
	liveMu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stopLocked()
	s.mu.Unlock()
}

// live 输出一路 MJPEG（multipart/x-mixed-replace）。ffmpeg 的 mpjpeg 封装自带
// `--ffmpeg` 边界与每帧 Content-Type/Length，原样转发即可，浏览器能直接显示。
func (a *App) live(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	id := r.URL.Query().Get("cam")
	// 允许「先填地址试看」：不传 cam 但传了 url 时直接试，不落库
	raw := r.URL.Query().Get("url")
	var url string
	if id != "" {
		cam, err := loadCam(c, id)
		if err != nil {
			return err
		}
		url = streamURL(*cam, true)
	} else {
		url = raw
	}
	if url == "" {
		return appkit.Errorf(400, "缺少相机或地址")
	}

	ch, unsub := liveOf(id).subscribe(url)
	defer unsub()

	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=ffmpeg")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Connection", "close")
	flusher, ok := w.(http.Flusher)
	if !ok {
		return appkit.Errorf(500, "当前连接不支持流式输出")
	}
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return nil
		case chunk, ok := <-ch:
			if !ok {
				return nil
			}
			if _, err := w.Write(chunk); err != nil {
				return nil
			}
			flusher.Flush()
		}
	}
}

// snap 抓一张图存进该相机目录（不进片段索引，只当截图用）。
func (a *App) snap(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(400, "参数解析失败：%v", err)
	}
	cam, err := loadCam(c, in.ID)
	if err != nil {
		return err
	}
	bin, err := ffmpeg()
	if err != nil {
		return err
	}
	dir := filepath.Join(camDir(c.Root, cam.ID), "snap")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	now := time.Now()
	name := now.Format("20060102_150405") + ".jpg"
	out := filepath.Join(dir, name)
	args := []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-rtsp_transport", "tcp", "-rw_timeout", "5000000",
		"-i", streamURL(*cam, true),
		"-frames:v", "1", "-q:v", "3", out,
	}
	proc, _, err := startChild(bin, args, "", false)
	if err != nil {
		return err
	}
	// ffmpeg 抓单帧一般 1~3 秒，最多等 10 秒
	for i := 0; i < 100; i++ {
		if !proc.running() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	proc.stop()
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		_ = os.Remove(out)
		return appkit.Errorf(502, "抓图失败：相机没有返回画面（检查地址/账号/编码）")
	}
	rel := filepath.ToSlash(filepath.Join(camRoot, cam.ID, "snap", name))
	appkit.JSON(w, 200, map[string]any{"ok": true, "path": rel, "at": now.Format(timeLayout)})
	return nil
}
