package webdavd

// 递归文件名搜索端点（/-search）：从指定目录向下走 depthMax 层，返回文件名
// 包含关键词的条目（不含内容搜索）。设计口径（2026-10-28 定稿）：
//   - 只匹配文件名（大小写不敏感的子串）；
//   - 深度上限 10 层（start 目录本身算第 0 层）；
//   - 结果上限 1000 条，达到即截断并在响应里标 truncated；
//   - 不跟随符号链接（防环）；跳过拒绝访问的目录（不中断整树）。
// 日常规模（万级文件、SSD）实测毫秒级，无需索引。

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	searchDepthMax = 10
	searchLimitMax = 1000
	searchQueryMin = 1 // 关键词最短长度
)

type searchEnt struct {
	N string `json:"n"` // 文件名
	D bool   `json:"d"` // 是否目录
	S int64  `json:"s"` // 字节
	M string `json:"m"` // 修改时间（渲染用，可为空）
	H string `json:"h"` // 已编码的共享内绝对路径（href 直接用）
	P string `json:"p"` // 所在目录（未编码斜杠路径，展示用）
}

// handleSearch 处理 GET /-search?q=&dir=（dir 为共享内斜杠路径，默认 /）。
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request, root string) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if len([]rune(q)) < searchQueryMin {
		writeSearchJSON(w, nil, false)
		return
	}
	// dir 路径安全：path.Clean 绝对化后拼进 root，天然封死 ../ 逃逸
	// （"/..../.." 会被 Clean 成 "/"），再保险校验一次前缀。
	rel := path.Clean("/" + r.URL.Query().Get("dir"))
	if rel != "/" {
		rel = strings.TrimSuffix(rel, "/")
	}
	start := filepath.Join(root, filepath.FromSlash(rel))
	if absRoot, err := filepath.Abs(root); err == nil {
		if absStart, err2 := filepath.Abs(start); err2 == nil && !strings.HasPrefix(absStart, absRoot+string(filepath.Separator)) && absStart != absRoot {
			http.Error(w, "forbidden path", http.StatusForbidden)
			return
		}
	}

	items := make([]searchEnt, 0, 64)
	truncated := false
	ctx := r.Context()
	var walk func(dir string, relDir string, depth int)
	walk = func(dir string, relDir string, depth int) {
		if truncated || depth > searchDepthMax {
			return
		}
		if err := ctx.Err(); err != nil {
			return
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			return // 无权限/已消失：跳过该层，不影响其他分支
		}
		for _, e := range ents {
			if truncated {
				return
			}
			if e.Type()&os.ModeSymlink != 0 {
				continue
			}
			name := e.Name()
			childRel := path.Join(relDir, name)
			if strings.Contains(strings.ToLower(name), q) {
				info, ierr := e.Info()
				var size int64
				var mod string
				if ierr == nil {
					size = info.Size()
					if info.ModTime().Year() > 1980 {
						mod = info.ModTime().Format("2006-01-02 15:04")
					}
				}
				href := (&url.URL{Path: childRel})
				h := href.EscapedPath()
				if e.IsDir() {
					h += "/"
				}
				items = append(items, searchEnt{N: name, D: e.IsDir(), S: size, M: mod, H: h, P: relDir})
				if len(items) >= searchLimitMax {
					truncated = true
					return
				}
			}
			if e.IsDir() {
				walk(filepath.Join(dir, name), childRel, depth+1)
			}
		}
	}
	walk(start, rel, 0)

	// 目录优先，其后按所在路径排序，展示稳定
	sort.Slice(items, func(i, j int) bool {
		if items[i].D != items[j].D {
			return items[i].D
		}
		if items[i].P != items[j].P {
			return items[i].P < items[j].P
		}
		return strings.ToLower(items[i].N) < strings.ToLower(items[j].N)
	})
	writeSearchJSON(w, items, truncated)
}

func writeSearchJSON(w http.ResponseWriter, items []searchEnt, truncated bool) {
	if items == nil {
		items = []searchEnt{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items": items, "truncated": truncated,
	})
}
