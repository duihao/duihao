package apiv1

import (
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// 影视/视频列表。默认目录与桌面端「影视」模块一致（_appfiles/video），
// 一级子目录就是分类，所以这里平铺递归，深度限 4。
const (
	videoDepthMax     = 4
	videoCollectMax   = 2000
	videoDefaultDir   = "/_appfiles/video"
	videoDefaultLimit = 60
)

var videoExts = map[string]bool{
	"mp4": true, "m4v": true, "mov": true, "mkv": true, "webm": true,
	"avi": true, "wmv": true, "flv": true, "ts": true, "m2ts": true,
	"rmvb": true, "rm": true, "3gp": true, "mpg": true, "mpeg": true,
}

type videoItem struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	MTime     int64  `json:"mtime"`
	Ext       string `json:"ext"`
	Container string `json:"container"`
	Playable  bool   `json:"playable"`
	Hint      string `json:"hint,omitempty"`
}

// containerInfo 判断这个容器能不能在手机上直接拖进度播放。
//
// 口径（Android 先行的现实）：
//   - mp4/m4v/mov/webm/3gp：系统解码器基本都吃，直接放；
//   - mkv：ExoPlayer 能吃，但编码五花八门（HEVC/AV1/TrueHD），放不出来时
//     客户端要能给出「去桌面端转码」的提示，所以 playable=true + hint；
//   - avi/wmv/flv/rmvb/ts：手机端基本没戏，playable=false。
func containerInfo(ext string) (mime string, playable bool, hint string) {
	switch ext {
	case "mp4":
		return "video/mp4", true, ""
	case "m4v":
		return "video/x-m4v", true, ""
	case "mov":
		return "video/quicktime", true, ""
	case "webm":
		return "video/webm", true, ""
	case "3gp":
		return "video/3gpp", true, ""
	case "mkv":
		return "video/x-matroska", true, "MKV 能否直放取决于内部编码；黑屏或卡顿请在桌面端转成 MP4"
	case "avi":
		return "video/x-msvideo", false, "AVI 手机端不支持，请在桌面端转成 MP4"
	case "wmv":
		return "video/x-ms-wmv", false, "WMV 手机端不支持，请在桌面端转成 MP4"
	case "flv":
		return "video/x-flv", false, "FLV 手机端不支持，请在桌面端转成 MP4"
	case "ts", "m2ts":
		return "video/mp2t", false, "TS 流手机端不支持，请在桌面端转成 MP4"
	case "rm", "rmvb":
		return "application/vnd.rn-realmedia", false, "RM/RMVB 手机端不支持，请在桌面端转成 MP4"
	case "mpg", "mpeg":
		return "video/mpeg", false, "MPEG-PS 手机端不支持，请在桌面端转成 MP4"
	}
	return "application/octet-stream", false, "未知视频格式"
}

// handleVideos GET /-api/v1/videos?dir=/&limit=60&cursor=
func (h *Handler) handleVideos(w http.ResponseWriter, r *http.Request, c Ctx) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir = videoDefaultDir
	}
	abs, clean, err := resolve(c.Root, dir)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_path", "路径不合法")
		return
	}
	fi, serr := os.Stat(abs)
	if serr != nil || !fi.IsDir() {
		// 没建过影视目录不算错：客户端显示引导文案即可。
		okJSON(w, map[string]any{
			"dir":     clean,
			"items":   []videoItem{},
			"next":    "",
			"total":   0,
			"missing": true,
			"hint":    "还没有影视目录，把片子放进共享的 _appfiles/video 就会出现在这里",
		})
		return
	}

	items := make([]videoItem, 0, 128)
	_ = filepath.WalkDir(abs, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		if d.IsDir() {
			if p != abs {
				rel, rerr := filepath.Rel(abs, p)
				if rerr == nil && strings.Count(rel, string(filepath.Separator)) >= videoDepthMax {
					return filepath.SkipDir
				}
			}
			if isJunk(strings.ToLower(d.Name())) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(items) >= videoCollectMax {
			return filepath.SkipAll
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(d.Name()), "."))
		if !videoExts[ext] {
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
		_, playable, hint := containerInfo(ext)
		items = append(items, videoItem{
			Name:      strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())),
			Path:      path.Join(clean, filepath.ToSlash(rel)),
			Size:      info.Size(),
			MTime:     modTime(info),
			Ext:       ext,
			Container: ext,
			Playable:  playable,
			Hint:      hint,
		})
		return nil
	})

	// 新加的排在前面：手机端最常看「最近放的」。
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].MTime != items[j].MTime {
			return items[i].MTime > items[j].MTime
		}
		return items[i].Path < items[j].Path
	})

	limit := queryInt(r, "limit", videoDefaultLimit, 1, 200)
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
	page := items[start:end]
	if page == nil {
		page = []videoItem{}
	}
	okJSON(w, map[string]any{
		"dir":     clean,
		"items":   page,
		"next":    next,
		"total":   len(items),
		"missing": false,
	})
}

// handleVideoPlay GET /-api/v1/videos/play?path=/..mp4
//
// 只回播放所需的信息，不代理字节流：播放直连共享内的原始路径
// （webdav.Handler 已支持 Range/断点续传，4K 拖动靠它）。
func (h *Handler) handleVideoPlay(w http.ResponseWriter, r *http.Request, c Ctx) {
	rel := r.URL.Query().Get("path")
	if rel == "" {
		fail(w, http.StatusBadRequest, "bad_path", "缺少 path")
		return
	}
	abs, clean, err := resolve(c.Root, rel)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_path", "路径不合法")
		return
	}
	fi, serr := os.Stat(abs)
	if serr != nil || !fi.Mode().IsRegular() {
		fail(w, http.StatusNotFound, "not_found", "文件不存在")
		return
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(clean), "."))
	if !videoExts[ext] {
		fail(w, http.StatusBadRequest, "not_video", "不是视频文件")
		return
	}
	mime, playable, hint := containerInfo(ext)
	okJSON(w, map[string]any{
		"path":      clean,
		"url":       (&url.URL{Path: clean}).EscapedPath(),
		"name":      strings.TrimSuffix(filepath.Base(clean), filepath.Ext(clean)),
		"size":      fi.Size(),
		"mtime":     modTime(fi),
		"mime":      mime,
		"container": ext,
		"playable":  playable,
		"hint":      hint,
		"range":     true,
	})
}
