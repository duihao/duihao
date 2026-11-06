package apiv1

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 测试夹具：一个像模像样的共享根目录
//
//	照片/2026/a.jpg  b.png
//	文档/说明.txt
//	_appfiles/video/电影.mp4 旧片.avi
//	.DS_Store（应被隐藏）
func newFixture(t *testing.T) (root, dataDir string, h *Handler) {
	t.Helper()
	root = t.TempDir()
	dataDir = t.TempDir()

	mk := func(rel string, content []byte) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("照片/2026/a.jpg", jpegBytes(t, 800, 600))
	mk("照片/2026/b.png", jpegBytes(t, 120, 90)) // 内容无所谓，扩展名才是关键
	mk("文档/说明.txt", []byte("hello"))
	mk("_appfiles/video/电影.mp4", []byte("not a real mp4"))
	mk("_appfiles/video/旧片.avi", []byte("not a real avi"))
	mk(".DS_Store", []byte("junk"))
	mk("hub.db", []byte("SQLite format 3")) // 内部库误落共享根时不能外露

	// 让「最近」有确定的顺序
	old := time.Now().Add(-90 * 24 * time.Hour)
	_ = os.Chtimes(filepath.Join(root, "照片", "2026", "b.png"), old, old)

	return root, dataDir, New()
}

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 255), uint8(y % 255), 90, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func call(t *testing.T, h *Handler, root, dataDir, method, target string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	rr := httptest.NewRecorder()
	h.Serve(rr, req, Ctx{User: "admin", Role: "admin", Root: root, DataDir: dataDir, Writable: true, Version: "1.0.1"})
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	return rr, body
}

func dataOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	d, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 data：%v", body)
	}
	return d
}

func TestMe(t *testing.T) {
	root, dataDir, h := newFixture(t)
	rr, body := call(t, h, root, dataDir, "GET", Prefix+"/me")
	if rr.Code != 200 {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	d := dataOf(t, body)
	if d["user"] != "admin" || d["api"].(float64) != 1 {
		t.Fatalf("unexpected me: %v", d)
	}
	caps, _ := d["caps"].([]any)
	if len(caps) < 4 {
		t.Fatalf("caps 不足: %v", caps)
	}
}

func TestFSListPagingAndJunk(t *testing.T) {
	root, dataDir, h := newFixture(t)

	rr, body := call(t, h, root, dataDir, "GET", Prefix+"/fs/list?path=/&limit=2")
	if rr.Code != 200 {
		t.Fatalf("code=%d %s", rr.Code, rr.Body.String())
	}
	d := dataOf(t, body)
	items, _ := d["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("limit=2 应回 2 条，得到 %d（%v）", len(items), items)
	}
	first, _ := items[0].(map[string]any)
	if first["dir"] != true {
		t.Fatalf("目录应排在前面：%v", first)
	}
	next, _ := d["next"].(string)
	if next == "" {
		t.Fatalf("应给出下一页游标：%v", d)
	}
	// 垃圾文件不能被列出来
	for _, raw := range items {
		m := raw.(map[string]any)
		switch m["name"] {
		case ".DS_Store":
			t.Fatalf(".DS_Store 应被隐藏")
		case "hub.db", "session.key":
			t.Fatalf("服务端内部文件不能出现在列表里：%v", m["name"])
		}
	}

	rr2, body2 := call(t, h, root, dataDir, "GET", Prefix+"/fs/list?path=/&limit=2&cursor="+next)
	if rr2.Code != 200 {
		t.Fatalf("第二页 code=%d %s", rr2.Code, rr2.Body.String())
	}
	d2 := dataOf(t, body2)
	if d2["total"].(float64) != d["total"].(float64) {
		t.Fatalf("两页 total 应一致 %v vs %v", d["total"], d2["total"])
	}
	second, _ := d2["items"].([]any)
	if len(second) == 0 {
		t.Fatalf("第二页不该为空")
	}
	if second[0].(map[string]any)["name"] == first["name"] {
		t.Fatalf("第二页重复了第一页的内容")
	}
}

func TestFSListTraversal(t *testing.T) {
	root, dataDir, h := newFixture(t)
	for _, target := range []string{
		Prefix + "/fs/list?path=/../etc",
		Prefix + "/thumb?path=/../secret.jpg",
		Prefix + "/videos/play?path=/../a.mp4",
	} {
		rr, _ := call(t, h, root, dataDir, "GET", target)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("%s 应拒绝，得到 %d", target, rr.Code)
		}
	}
}

func TestThumbAndETag(t *testing.T) {
	root, dataDir, h := newFixture(t)
	target := Prefix + "/thumb?path=/照片/2026/a.jpg&w=320"

	rr, _ := call(t, h, root, dataDir, "GET", target)
	if rr.Code != 200 {
		t.Fatalf("code=%d %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("Content-Type=%q", ct)
	}
	img, err := jpeg.Decode(bytes.NewReader(rr.Body.Bytes()))
	if err != nil {
		t.Fatalf("返回的不是 JPEG: %v", err)
	}
	if got := img.Bounds().Dx(); got != 320 {
		t.Fatalf("宽度应缩到 320，得到 %d", got)
	}
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("缺 ETag")
	}

	// 同键应命中缓存并回 304
	req := httptest.NewRequest("GET", target, nil)
	req.Header.Set("If-None-Match", etag)
	rr2 := httptest.NewRecorder()
	h.Serve(rr2, req, Ctx{User: "admin", Root: root, DataDir: dataDir})
	if rr2.Code != http.StatusNotModified {
		t.Fatalf("应回 304，得到 %d", rr2.Code)
	}

	// 缓存文件确实落盘了
	key := thumbKey(filepath.Join(root, "照片", "2026", "a.jpg"), mustStat(t, filepath.Join(root, "照片", "2026", "a.jpg")), 320)
	if _, err := os.Stat(filepath.Join(thumbDir(dataDir), key[:2], key+".jpg")); err != nil {
		t.Fatalf("缓存文件不存在: %v", err)
	}
}

func mustStat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func TestPhotosTimeline(t *testing.T) {
	root, dataDir, h := newFixture(t)
	rr, body := call(t, h, root, dataDir, "GET", Prefix+"/photos?dir=/照片&limit=10")
	if rr.Code != 200 {
		t.Fatalf("code=%d %s", rr.Code, rr.Body.String())
	}
	d := dataOf(t, body)
	if d["total"].(float64) != 2 {
		t.Fatalf("应扫到 2 张，得到 %v", d["total"])
	}
	months, _ := d["months"].([]any)
	if len(months) == 0 {
		t.Fatalf("应按月分组")
	}
	first := months[0].(map[string]any)
	items, _ := first["items"].([]any)
	it := items[0].(map[string]any)
	p, _ := it["path"].(string)
	if !strings.HasPrefix(p, "/照片/") {
		t.Fatalf("path 应是共享内绝对路径（能直接回传），得到 %q", p)
	}
	if th, _ := it["thumb"].(string); !strings.HasPrefix(th, Prefix+"/thumb?") {
		t.Fatalf("thumb 应是相对 URL，得到 %q", th)
	}
	// 新图排在前面：a.jpg 是刚写的，b.png 是 90 天前
	if !strings.HasSuffix(p, "a.jpg") {
		t.Fatalf("应按时间倒序，首项 = %q", p)
	}
}

func TestVideosAndPlay(t *testing.T) {
	root, dataDir, h := newFixture(t)
	rr, body := call(t, h, root, dataDir, "GET", Prefix+"/videos")
	if rr.Code != 200 {
		t.Fatalf("code=%d %s", rr.Code, rr.Body.String())
	}
	d := dataOf(t, body)
	items, _ := d["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("应有 2 个视频，得到 %d", len(items))
	}
	var mp4, avi map[string]any
	for _, raw := range items {
		m := raw.(map[string]any)
		switch m["ext"] {
		case "mp4":
			mp4 = m
		case "avi":
			avi = m
		}
	}
	if mp4 == nil || avi == nil {
		t.Fatalf("缺 mp4 或 avi：%v", items)
	}
	if mp4["playable"] != true {
		t.Fatalf("mp4 应可直放：%v", mp4)
	}
	if avi["playable"] != false || avi["hint"] == "" {
		t.Fatalf("avi 应标不可直放并给提示：%v", avi)
	}

	rr2, body2 := call(t, h, root, dataDir, "GET", Prefix+"/videos/play?path="+url.QueryEscape("/_appfiles/video/电影.mp4"))
	if rr2.Code != 200 {
		t.Fatalf("play code=%d %s", rr2.Code, rr2.Body.String())
	}
	pd := dataOf(t, body2)
	if pd["mime"] != "video/mp4" || pd["range"] != true {
		t.Fatalf("play 返回不对：%v", pd)
	}
	if u, _ := pd["url"].(string); !strings.HasSuffix(u, ".mp4") {
		t.Fatalf("url 应是可直接 GET 的路径，得到 %q", u)
	}
}

func TestVideosMissingDir(t *testing.T) {
	root := t.TempDir()
	h := New()
	rr, body := call(t, h, root, t.TempDir(), "GET", Prefix+"/videos")
	if rr.Code != 200 {
		t.Fatalf("目录不存在不该报错，得到 %d %s", rr.Code, rr.Body.String())
	}
	if dataOf(t, body)["missing"] != true {
		t.Fatalf("应标 missing")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	root, dataDir, h := newFixture(t)
	rr, _ := call(t, h, root, dataDir, "POST", Prefix+"/me")
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST 应 405，得到 %d", rr.Code)
	}
}

func TestUnknownRoute(t *testing.T) {
	root, dataDir, h := newFixture(t)
	rr, _ := call(t, h, root, dataDir, "GET", Prefix+"/nope")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("未知路由应 404，得到 %d", rr.Code)
	}
}
