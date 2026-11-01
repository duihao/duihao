// 笔记媒体：正文/封面里的图片、视频、文件全部落在网盘文件树
// （/_appfiles/notes/<YYYY-MM>/），笔记库里只存路径引用。
//
// 口径（2026-10-29 定稿）：
//   - 网盘根目录预留 _appfiles/ 作为各模块附件区，按模块再分子目录（notes/ledger/...）；
//   - 大文件（视频等）不进 SQLite，网盘里看得见、可整理、同步客户端照常工作；
//   - pick.list 允许从网盘任意目录选已有文件插入，不局限于 _appfiles。
package notes

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
)

const (
	appFilesDir = "_appfiles" // 网盘根下的模块附件区
	notesSub    = "notes"     // 笔记模块子目录
	uploadMax   = 200 << 20   // 单文件 200MB 上限
	nameMax     = 80          // 保留的原始文件名长度（含扩展名）
)

// upload 处理 multipart 上传（字段名 file）：
// 存到 <网盘根>/_appfiles/notes/<YYYY-MM>/<原文件名>，重名自动加随机前缀。
// 返回的 url 已做路径转义，可直接放进 src/href。
func upload(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	if strings.TrimSpace(c.Root) == "" {
		return appkit.Errorf(http.StatusInternalServerError, "网盘目录不可用")
	}
	r.Body = http.MaxBytesReader(w, r.Body, uploadMax+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "上传失败：文件过大（上限 200MB）或格式错误")
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		return appkit.Errorf(http.StatusBadRequest, "缺少文件字段 file")
	}
	defer func() { _ = f.Close() }()

	name := safeName(hdr.Filename)
	month := time.Now().Format("2006-01")
	relDir := path.Join("/", appFilesDir, notesSub, month)
	dstDir := filepath.Join(c.Root, filepath.FromSlash(relDir))
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	rel := path.Join(relDir, name)
	if _, err := os.Stat(filepath.Join(c.Root, filepath.FromSlash(rel))); err == nil {
		rel = path.Join(relDir, randHex(4)+"-"+name)
	}
	dst := filepath.Join(c.Root, filepath.FromSlash(rel))
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, f)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return closeErr
	}
	appkit.JSON(w, http.StatusOK, map[string]any{
		"name": name, "path": rel,
		"url": fileURL(rel), "size": hdr.Size,
	})
	return nil
}

// pickItem 网盘浏览条目。
type pickItem struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size"`
	Mod  string `json:"mod"`
	URL  string `json:"url"` // 已转义；目录为空
}

// pickList 浏览网盘目录（pick 面板用）：{dir:"/子目录"} → {dir, items}。
// 路径安全口径与 /-search 相同：path.Clean 绝对化 + Abs 前缀双重校验。
func pickList(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Dir string `json:"dir"`
	}
	if err := decodeOpt(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	rel := path.Clean("/" + in.Dir)
	if rel != "/" {
		rel = strings.TrimSuffix(rel, "/")
	}
	full := filepath.Join(c.Root, filepath.FromSlash(rel))
	if absRoot, err := filepath.Abs(c.Root); err == nil {
		if absFull, err2 := filepath.Abs(full); err2 == nil &&
			absFull != absRoot && !strings.HasPrefix(absFull, absRoot+string(filepath.Separator)) {
			return appkit.Errorf(http.StatusForbidden, "路径越界")
		}
	}
	ents, err := os.ReadDir(full)
	if err != nil {
		return appkit.Errorf(http.StatusNotFound, "目录不存在或不可读")
	}
	items := make([]pickItem, 0, len(ents))
	for _, e := range ents {
		if e.Type()&os.ModeSymlink != 0 {
			continue // 不跟随符号链接（防环、防逃逸）
		}
		it := pickItem{Name: e.Name(), Dir: e.IsDir()}
		if !e.IsDir() {
			if info, ierr := e.Info(); ierr == nil {
				it.Size = info.Size()
				if info.ModTime().Year() > 1980 {
					it.Mod = info.ModTime().Format("2006-01-02 15:04")
				}
			}
			it.URL = fileURL(path.Join(rel, e.Name()))
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Dir != items[j].Dir {
			return items[i].Dir
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	appkit.JSON(w, http.StatusOK, map[string]any{"dir": rel, "items": items})
	return nil
}

// fileURL 把共享内斜杠路径转成可直接放 src/href 的 URL（逐段转义）。
func fileURL(rel string) string {
	return (&url.URL{Path: rel}).EscapedPath()
}

// safeName 清洗原始文件名：去掉路径分隔符与控制字符，保留扩展名，限长。
func safeName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\\', '/', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
	s = strings.Trim(s, ". ")
	if s == "" {
		return "file"
	}
	r := []rune(s)
	if len(r) > nameMax {
		ext := filepath.Ext(s)
		be := []rune(ext)
		if len(be) > 12 {
			be = be[:12]
		}
		s = string(r[:nameMax-len(be)]) + string(be)
	}
	return s
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
