package webdavd

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// P0 验收：/-api/v1/* 必须挂在真实管线上（认证 → 权限 → 分流），
// 而不是只在单测里直接调用 Handler。这里走完整的 HTTP 栈。
func TestAPIV1OverRealPipeline(t *testing.T) {
	srv, base, user, pass := startTestServer(t, func(c *Config) {
		c.InstanceDir = t.TempDir()
		c.Version = "1.0.1-test"
	})
	root := srv.cfg.Shares[0].Root

	// 造一点内容：两张照片 + 一个视频
	mkDir := func(p string) string {
		d := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		return d
	}
	photoDir := mkDir("照片/2026")
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for y := 0; y < 480; y++ {
		for x := 0; x < 640; x++ {
			img.Set(x, y, color.RGBA{200, uint8(x % 255), 80, 255})
		}
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(photoDir, "海边.jpg"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	videoDir := mkDir("_appfiles/video")
	if err := os.WriteFile(filepath.Join(videoDir, "样片.mp4"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	cli := newTestClient(t, false)
	get := func(path string) (int, string, http.Header) {
		r := doReq(t, cli, base, "GET", path, user, pass, nil)
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b), r.Header
	}

	// 1) me：探活 + 版本 + 能力清单
	code, body, _ := get("/-api/v1/me")
	if code != 200 {
		t.Fatalf("me 应 200，得到 %d：%s", code, body)
	}
	if !strings.Contains(body, `"api":1`) || !strings.Contains(body, "1.0.1-test") {
		t.Fatalf("me 内容不对：%s", body)
	}

	// 2) fs/list：根目录里能看到「照片」，垃圾文件被过滤
	code, body, _ = get("/-api/v1/fs/list?path=/")
	if code != 200 {
		t.Fatalf("fs/list 应 200，得到 %d：%s", code, body)
	}
	if !strings.Contains(body, `"name":"照片"`) || !strings.Contains(body, `"dir":true`) {
		t.Fatalf("列表内容不对：%s", body)
	}

	// 3) photos：按 share 内绝对路径回传，thumb 是可用的相对 URL
	code, body, _ = get("/-api/v1/photos?dir=/照片")
	if code != 200 {
		t.Fatalf("photos 应 200，得到 %d：%s", code, body)
	}
	var pr struct {
		Data struct {
			Total  int `json:"total"`
			Months []struct {
				Month string `json:"month"`
				Items []struct {
					Path  string `json:"path"`
					Thumb string `json:"thumb"`
				} `json:"items"`
			} `json:"months"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &pr); err != nil {
		t.Fatalf("photos JSON: %v / %s", err, body)
	}
	if pr.Data.Total != 1 || len(pr.Data.Months) == 0 || len(pr.Data.Months[0].Items) != 1 {
		t.Fatalf("photos 应 1 张、1 个月：%s", body)
	}
	item := pr.Data.Months[0].Items[0]
	if item.Path != "/照片/2026/海边.jpg" {
		t.Fatalf("path 不对：%q", item.Path)
	}

	// 4) thumb：真的返回 JPEG
	code, _, hdr := get(item.Thumb)
	if code != 200 {
		t.Fatalf("thumb 应 200，得到 %d", code)
	}
	if hdr.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumb Content-Type=%q", hdr.Get("Content-Type"))
	}

	// 5) videos + play
	code, body, _ = get("/-api/v1/videos")
	if code != 200 || !strings.Contains(body, "样片") {
		t.Fatalf("videos 应含样片，得到 %d：%s", code, body)
	}
	code, body, _ = get("/-api/v1/videos/play?path=" + url.QueryEscape("/_appfiles/video/不存在.mp4"))
	if code == 200 {
		t.Fatalf("不存在的视频不该 200：%s", body)
	}

	// 6) 未认证：/api/v1 不能比别的路径松
	r := doReq(t, cli, base, "GET", "/-api/v1/me", "", "", nil)
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未认证应 401，得到 %d", r.StatusCode)
	}

	// 7) 只读共享下 writable 必须是 false（客户端据此隐藏上传/删除）
	srv2, base2, user2, pass2 := startTestServer(t, func(c *Config) {
		c.InstanceDir = t.TempDir()
		c.ReadOnly = true
	})
	_ = srv2
	r2 := doReq(t, cli, base2, "GET", "/-api/v1/me", user2, pass2, nil)
	b2, _ := io.ReadAll(r2.Body)
	if !strings.Contains(string(b2), `"writable":false`) {
		t.Fatalf("只读共享应 writable=false：%s", b2)
	}
}
