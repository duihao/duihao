package apiv1

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	// 解码器注册：手机端最常见的几种格式（HEIC 除外，见下方说明）。
	_ "image/gif"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	thumbMaxSource = 96 << 20 // 原图上限 96MB：再大就是 TIFF/PSD 之类，手机端不值得解
	thumbQuality   = 82
	thumbCacheCap  = 500 << 20 // 缓存目录上限 500MB，超了按最旧删
)

// handleThumb GET /-api/v1/thumb?path=/照片/a.jpg&w=320
//
// 返回 JPEG 字节。缓存键含 (绝对路径, 大小, mtime, 宽度)，所以原图一改
// 键就变，天然不用失效；ETag 同键，客户端可 304 省流量。
// HEIC/ HEIF 走不通：Go 侧没有纯 Go 解码器（要 CGO 或 ffmpeg），
// 这里如实回 415，客户端显示占位图 —— 桌面端上传时转 JPEG 才是正解。
func (h *Handler) handleThumb(w http.ResponseWriter, r *http.Request, c Ctx) {
	rel := r.URL.Query().Get("path")
	if rel == "" {
		fail(w, http.StatusBadRequest, "bad_path", "缺少 path")
		return
	}
	abs, _, err := resolve(c.Root, rel)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_path", "路径不合法")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.Mode().IsRegular() {
		fail(w, http.StatusNotFound, "not_found", "文件不存在")
		return
	}
	if fi.Size() > thumbMaxSource {
		fail(w, http.StatusRequestEntityTooLarge, "too_large", "图片过大，无法生成缩略图")
		return
	}
	wpx := queryInt(r, "w", 320, 64, 1280)

	key := thumbKey(abs, fi, wpx)
	etag := `"` + key + `"`
	w.Header().Set("ETag", etag)
	if match := r.Header.Get("If-None-Match"); strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	cacheDir := thumbDir(c.DataDir)
	cachePath := filepath.Join(cacheDir, key[:2], key+".jpg")
	if data, rerr := os.ReadFile(cachePath); rerr == nil && len(data) > 0 {
		serveThumb(w, r, data, etag)
		return
	}

	select {
	case h.sem <- struct{}{}:
	case <-time.After(15 * time.Second):
		fail(w, http.StatusServiceUnavailable, "busy", "缩略图生成排队超时，请重试")
		return
	}
	data, gerr := renderThumb(abs, wpx)
	<-h.sem
	if gerr != nil {
		if gerr == errUnsupportedImage {
			fail(w, http.StatusUnsupportedMediaType, "unsupported_format",
				"这个格式暂不支持预览（HEIC 需在桌面端转成 JPEG）")
			return
		}
		fail(w, http.StatusInternalServerError, "thumb_failed", "缩略图生成失败")
		return
	}
	_ = os.MkdirAll(filepath.Dir(cachePath), 0o755)
	tmp := cachePath + ".tmp"
	if werr := os.WriteFile(tmp, data, 0o644); werr == nil {
		_ = os.Rename(tmp, cachePath)
	}
	if cacheDir != "" && h.thumbWrites.Add(1)%500 == 0 {
		trimThumbCache(cacheDir)
	}
	serveThumb(w, r, data, etag)
}

func serveThumb(w http.ResponseWriter, r *http.Request, data []byte, etag string) {
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	w.Header().Set("ETag", etag)
	// 键里带 mtime+size，内容不可变，放心长缓存；私有是因为要带 Cookie。
	w.Header().Set("Cache-Control", "private, max-age=604800, immutable")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(data)
}

func thumbDir(dataDir string) string {
	if dataDir == "" {
		return filepath.Join(os.TempDir(), "duihao-thumbs")
	}
	return filepath.Join(dataDir, "cache", "thumbs")
}

func thumbKey(abs string, fi os.FileInfo, w int) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d|%d", abs, fi.Size(), fi.ModTime().UnixNano(), w)))
	return hex.EncodeToString(sum[:])
}

var errUnsupportedImage = fmt.Errorf("unsupported image")

// renderThumb 解码 → 按 EXIF 方向摆正 → 缩到 w 宽 → JPEG。
// 原图比目标窄时不放大（避免糊）。
func renderThumb(abs string, w int) ([]byte, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	orient := jpegOrientation(f)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, errUnsupportedImage
	}

	// 高分辨率 JPEG 解码后可能是 4000×3000，先按需缩，再摆正（摆正在小图上做更省）。
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return nil, errUnsupportedImage
	}
	tw := w
	if sw <= w {
		tw = sw
	}
	th := sh * tw / sw
	if th < 1 {
		th = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)

	var out image.Image = dst
	switch orient {
	case 3:
		out = rotate180(dst)
	case 6:
		out = rotate90(dst)
	case 8:
		out = rotate270(dst)
	}

	var buf bytes.Buffer
	buf.Grow(tw * th / 3)
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: thumbQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func rotate90(src *image.RGBA) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// 顺时针 90°：(x, y) → (h-1-y, x)
			dst.Set(h-1-y, x, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func rotate180(src *image.RGBA) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(w-1-x, h-1-y, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func rotate270(src *image.RGBA) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// 逆时针 90°：(x, y) → (y, w-1-x)
			dst.Set(y, w-1-x, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// jpegOrientation 读 EXIF 方向（1..8，读不到给 1）。
// 只处理 JPEG：手机相册里绝大多数是「像素横放 + EXIF 标记旋转」，
// 不摆正的话缩略图会躺着。
func jpegOrientation(r io.Reader) int {
	br := bufio.NewReaderSize(r, 64<<10)
	var hdr [2]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil || hdr[0] != 0xFF || hdr[1] != 0xD8 {
		return 1
	}
	for i := 0; i < 32; i++ { // 最多扫 32 个段，够覆盖 APPn
		var m [2]byte
		if _, err := io.ReadFull(br, m[:]); err != nil || m[0] != 0xFF {
			return 1
		}
		marker := m[1]
		if marker == 0xD8 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 {
			continue
		}
		var lb [2]byte
		if _, err := io.ReadFull(br, lb[:]); err != nil {
			return 1
		}
		seglen := int(binary.BigEndian.Uint16(lb[:])) - 2
		if seglen < 0 {
			return 1
		}
		if marker != 0xE1 { // 不是 APP1，跳过
			if _, err := br.Discard(seglen); err != nil {
				return 1
			}
			continue
		}
		buf := make([]byte, seglen)
		if _, err := io.ReadFull(br, buf); err != nil {
			return 1
		}
		if o := exifOrientation(buf); o >= 1 && o <= 8 {
			return o
		}
		return 1
	}
	return 1
}

// exifOrientation 在 APP1 段里找 IFD0 的 0x0112（Orientation）。
func exifOrientation(b []byte) int {
	if len(b) < 14 || !bytes.HasPrefix(b, []byte("Exif\x00\x00")) {
		return 0
	}
	t := b[6:]
	var bo binary.ByteOrder
	switch {
	case bytes.HasPrefix(t, []byte("II")):
		bo = binary.LittleEndian
	case bytes.HasPrefix(t, []byte("MM")):
		bo = binary.BigEndian
	default:
		return 0
	}
	if len(t) < 8 {
		return 0
	}
	off := int(bo.Uint32(t[4:8]))
	if off < 8 || off+2 > len(t) {
		return 0
	}
	n := int(bo.Uint16(t[off : off+2]))
	p := off + 2
	for i := 0; i < n; i++ {
		if p+12 > len(t) {
			return 0
		}
		tag := bo.Uint16(t[p : p+2])
		typ := bo.Uint16(t[p+2 : p+4])
		cnt := bo.Uint32(t[p+4 : p+8])
		if tag == 0x0112 && typ == 3 && cnt >= 1 {
			v := int(bo.Uint16(t[p+8 : p+10]))
			if v >= 1 && v <= 8 {
				return v
			}
			return 0
		}
		p += 12
	}
	return 0
}

// trimThumbCache 缓存超上限时按最旧删到 80% —— 只在每 500 次生成后跑一次，
// 不做常驻扫描（NAS 上 IO 比 CPU 更金贵）。
func trimThumbCache(dir string) {
	type ent struct {
		path string
		size int64
		mod  time.Time
	}
	var all []ent
	var total int64
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		all = append(all, ent{p, info.Size(), info.ModTime()})
		total += info.Size()
		if len(all) > 200000 {
			return filepath.SkipAll
		}
		return nil
	})
	if total <= thumbCacheCap {
		return
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mod.Before(all[j].mod) })
	target := int64(thumbCacheCap) * 8 / 10
	for _, e := range all {
		if total <= target {
			break
		}
		if os.Remove(e.path) == nil {
			total -= e.size
		}
	}
}
