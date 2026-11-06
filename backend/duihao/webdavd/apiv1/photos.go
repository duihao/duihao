package apiv1

import (
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 照片时间线：不做索引表，直接扫共享目录。理由：
//   - 索引要处理新增/改名/删除/云端同步的所有边界，P0 阶段不值得；
//   - 手机端只翻最近几千张，扫一次带缓存就够快。
// 代价：目录很大时首次请求慢（有上限），所以给了 visited/collected 双上限
// 与 90 秒缓存；真到十万张的规模，P2 再上后台索引。

const (
	photoDepthMax     = 8
	photoVisitMax     = 30000
	photoCollectMax   = 3000
	photoCacheTTL     = 90 * time.Second
	photoThumbDefault = 320
)

var imageExts = map[string]bool{
	"jpg": true, "jpeg": true, "png": true, "webp": true, "gif": true,
	"bmp": true, "heic": true, "heif": true, "tif": true, "tiff": true,
}

type photoItem struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
	Thumb string `json:"thumb"`
	Kind  string `json:"kind"` // image / heic（客户端对 heic 先显示占位）
}

type photoCache struct {
	at        time.Time
	items     []photoItem
	truncated bool
}

// handlePhotos GET /-api/v1/photos?dir=/&limit=120&cursor=&w=320&appfiles=0
//
// 返回按月分组的列表（新的在前）。游标 = 上一页最后一张的路径。
func (h *Handler) handlePhotos(w http.ResponseWriter, r *http.Request, c Ctx) {
	dir := r.URL.Query().Get("dir")
	abs, clean, err := resolve(c.Root, dir)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_path", "路径不合法")
		return
	}
	if fi, serr := os.Stat(abs); serr != nil || !fi.IsDir() {
		fail(w, http.StatusNotFound, "not_found", "目录不存在")
		return
	}
	includeApp := r.URL.Query().Get("appfiles") == "1"
	wpx := queryInt(r, "w", photoThumbDefault, 64, 1280)
	limit := queryInt(r, "limit", 120, 1, 500)

	items, truncated := h.scanPhotos(abs, clean, includeApp)

	start := 0
	if cur := decodeCursor(r.URL.Query().Get("cursor")); cur != "" {
		for i := range items {
			if items[i].Path == cur {
				start = i + 1
				break
			}
		}
	}
	if start > len(items) {
		start = len(items)
	}
	end := start + limit
	next := ""
	if end < len(items) {
		next = encodeCursor(items[end-1].Path)
	} else {
		end = len(items)
	}
	page := make([]photoItem, 0, limit)
	for _, it := range items[start:end] {
		cp := it
		cp.Thumb = thumbURL(cp.Path, wpx)
		page = append(page, cp)
	}

	okJSON(w, map[string]any{
		"dir":       clean,
		"total":     len(items),
		"months":    groupByMonth(page),
		"next":      next,
		"truncated": truncated,
	})
}

func thumbURL(p string, w int) string {
	q := url.Values{}
	q.Set("path", p)
	q.Set("w", strconv.Itoa(w))
	return Prefix + "/thumb?" + q.Encode()
}

func groupByMonth(items []photoItem) []map[string]any {
	out := make([]map[string]any, 0, 8)
	cur := ""
	var bucket []photoItem
	flush := func() {
		if cur == "" {
			return
		}
		out = append(out, map[string]any{
			"month": cur,
			"count": len(bucket),
			"items": bucket,
		})
	}
	for _, it := range items {
		m := "未知"
		if it.MTime > 0 {
			m = time.Unix(it.MTime, 0).Format("2006-01")
		}
		if m != cur {
			flush()
			cur = m
			bucket = make([]photoItem, 0, 16)
		}
		bucket = append(bucket, it)
	}
	flush()
	return out
}

// scanPhotos 带缓存地扫一遍目录（新的在前）。base 是这次扫描的共享内路径，
// 用来把相对路径还原成客户端可直接回传的绝对逻辑路径。
func (h *Handler) scanPhotos(abs, base string, includeApp bool) ([]photoItem, bool) {
	cacheKey := abs
	if includeApp {
		cacheKey += "|app"
	}
	h.mu.Lock()
	if c := h.photos[cacheKey]; c != nil && time.Since(c.at) < photoCacheTTL {
		items, tr := c.items, c.truncated
		h.mu.Unlock()
		return items, tr
	}
	h.mu.Unlock()

	items := make([]photoItem, 0, 256)
	visited := 0
	truncated := false

	_ = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 权限/消失：跳过这一支
		}
		if d.IsDir() {
			if p != abs {
				rel, rerr := filepath.Rel(abs, p)
				if rerr == nil && strings.Count(rel, string(filepath.Separator)) >= photoDepthMax {
					return filepath.SkipDir
				}
			}
			name := strings.ToLower(d.Name())
			if isJunk(name) || name == "$recycle.bin" || name == "node_modules" || name == ".git" {
				return filepath.SkipDir
			}
			if name == "_appfiles" && !includeApp {
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		if visited > photoVisitMax {
			truncated = true
			return filepath.SkipAll
		}
		if len(items) >= photoCollectMax {
			truncated = true
			return filepath.SkipAll
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(d.Name()), "."))
		if !imageExts[ext] {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		rel, rerr := filepath.Rel(abs, p)
		if rerr != nil {
			return nil
		}
		logical := path.Join(base, filepath.ToSlash(rel))
		kind := "image"
		if ext == "heic" || ext == "heif" {
			kind = "heic"
		}
		items = append(items, photoItem{
			Name:  d.Name(),
			Path:  logical,
			Size:  info.Size(),
			MTime: modTime(info),
			Kind:  kind,
		})
		return nil
	})

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].MTime != items[j].MTime {
			return items[i].MTime > items[j].MTime
		}
		return items[i].Path > items[j].Path
	})

	h.mu.Lock()
	h.photos[cacheKey] = &photoCache{at: time.Now(), items: items, truncated: truncated}
	// 缓存表本身也要封顶：共享目录多、来回切的客户端不至于把内存吃光。
	if len(h.photos) > 32 {
		var oldestKey string
		var oldest time.Time
		for k, v := range h.photos {
			if oldestKey == "" || v.at.Before(oldest) {
				oldestKey, oldest = k, v.at
			}
		}
		delete(h.photos, oldestKey)
	}
	h.mu.Unlock()
	return items, truncated
}
