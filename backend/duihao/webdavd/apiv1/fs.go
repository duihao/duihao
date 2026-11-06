package apiv1

import (
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
)

// internalNames 服务端自己的文件：正常部署时它们在实例数据目录、不在共享里，
// 但用户把共享根指到实例目录（或调试程序这么干）时会露出来 —— 一律不列。
var internalNames = map[string]bool{
	"hub.db": true, "hub.db-wal": true, "hub.db-shm": true,
	"session.key": true, "webdavshares.json": true,
}

// junkNames 系统垃圾文件/目录：摄影机与同步软件留下的，不该出现在手机列表里。
var junkNames = map[string]bool{
	".ds_store": true, "thumbs.db": true, "desktop.ini": true,
	"@eadir": true, "#recycle": true, "$recycle.bin": true,
	"system volume information": true,
}

func isJunk(name string) bool { return junkNames[strings.ToLower(name)] }

// fsEntry 一条目录项。字段只加不改（契约）。
type fsEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"` // 共享内路径，客户端原样回传给 thumb / videos/play
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"` // Unix 秒；0 = 未知
	Ext   string `json:"ext,omitempty"`
}

// handleFSList GET /-api/v1/fs/list?path=/&limit=200&cursor=
//
// 目录优先、再按名称排序（大小写不敏感）；游标 = 上一页最后一项的路径，
// 客户端原样回传即可。分页为了「一个文件夹几千张照片」时首屏不卡。
func (h *Handler) handleFSList(w http.ResponseWriter, r *http.Request, c Ctx) {
	rel := r.URL.Query().Get("path")
	abs, clean, err := resolve(c.Root, rel)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_path", "路径不合法")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "目录不存在")
		return
	}
	if !fi.IsDir() {
		fail(w, http.StatusBadRequest, "not_dir", "不是目录")
		return
	}
	ents, err := os.ReadDir(abs)
	if err != nil {
		fail(w, http.StatusForbidden, "read_denied", "读不了这个目录")
		return
	}

	items := make([]fsEntry, 0, len(ents))
	for _, e := range ents {
		if e.Type()&os.ModeSymlink != 0 { // 符号链接跳过：防跑到共享根之外
			continue
		}
		name := e.Name()
		if isJunk(name) || (clean == "/" && internalNames[strings.ToLower(name)]) {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		it := fsEntry{
			Name:  name,
			Path:  path.Join(clean, name),
			Dir:   e.IsDir(),
			Size:  info.Size(),
			MTime: modTime(info),
		}
		if !it.Dir {
			it.Ext = strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Dir != items[j].Dir {
			return items[i].Dir
		}
		li, lj := strings.ToLower(items[i].Name), strings.ToLower(items[j].Name)
		if li != lj {
			return li < lj
		}
		return items[i].Name < items[j].Name
	})

	limit := queryInt(r, "limit", 200, 1, 1000)
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
		page = []fsEntry{}
	}

	parent := "/"
	if clean != "/" {
		parent = path.Dir(clean)
	}
	okJSON(w, map[string]any{
		"path":     clean,
		"parent":   parent,
		"items":    page,
		"next":     next, // 空 = 没有下一页
		"total":    len(items),
		"writable": c.Writable,
	})
}
