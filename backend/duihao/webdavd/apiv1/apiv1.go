// Package apiv1 移动端 / 第三方客户端的稳定契约层。
//
// 为什么单独一层：网页接口 /-api/<app>/<action> 是按页面定制的（字段随页面
// 改、没有版本、错误靠 HTTP 码），一旦发给客户端就没法动。这里另开一份
// 「发布即契约」的接口：字段只加不改，要改名就升 /-api/v2。
//
// 复用面（不重复造）：
//   - 认证：同一套会话 Cookie / Basic（guard 已完成，本包不碰登录）；
//   - 权限：调用方在 guard 里按 NAS 账号的 files 读权限挡过；
//   - 路径安全：本包自己再校验一次（防 query 参数绕过 URL 层校验）。
//
// 挂载点 /-api/v1/<resource>，只读（GET / HEAD）。写接口等 P1 的配对令牌
// 落地后再加 POST，届时同样按动作名判权限。
package apiv1

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Prefix 挂载前缀（guard 里按它分流）。
const Prefix = "/-api/v1"

// APIVer 契约版本；不兼容改动时 +1 并另起前缀。
const APIVer = 1

// Ctx 一次请求的上下文（由 webdavd.guard 认证后填充）。
type Ctx struct {
	User     string            // NAS 账号 uid
	Role     string            // admin / member；空 = 直连单测
	Perms    map[string]string // member 的模块权限
	Root     string            // 共享根目录（绝对路径）
	DataDir  string            // 实例数据目录（缩略图缓存落这里）
	Writable bool              // 只读共享
	Version  string            // 服务端版本（客户端做兼容提示用）
}

// errBadPath 路径非法（穿越 / 越界 / 非法字符）。
var errBadPath = errors.New("路径非法")

// Handler 无状态分发器 + 进程内缓存（缩略图并发生成限流、照片时间线缓存）。
// 一个实例一个 Handler，可并发调用。
type Handler struct {
	sem chan struct{} // 缩略图生成并发上限（解码 + 缩放是纯 CPU）

	thumbWrites atomic.Int64 // 累计写缓存次数，到阈值才做一次清理

	mu     sync.Mutex
	photos map[string]*photoCache
}

// New 构造分发器。
func New() *Handler {
	return &Handler{
		sem:    make(chan struct{}, 4),
		photos: make(map[string]*photoCache),
	}
}

// Match 判断路径是否属于 v1（供 guard 分流）。
func Match(p string) bool {
	return p == Prefix || strings.HasPrefix(p, Prefix+"/")
}

// Serve 处理 /-api/v1/*。
func (h *Handler) Serve(w http.ResponseWriter, r *http.Request, c Ctx) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "客户端接口只读，暂不支持该方法")
		return
	}
	if c.Root == "" {
		fail(w, http.StatusInternalServerError, "no_root", "共享根目录未配置")
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, Prefix), "/")
	switch rest {
	case "", "me":
		h.handleMe(w, r, c)
	case "fs/list":
		h.handleFSList(w, r, c)
	case "thumb":
		h.handleThumb(w, r, c)
	case "photos":
		h.handlePhotos(w, r, c)
	case "videos":
		h.handleVideos(w, r, c)
	case "videos/play":
		h.handleVideoPlay(w, r, c)
	default:
		fail(w, http.StatusNotFound, "not_found", "接口不存在："+rest)
	}
}

// handleMe 探活 + 账号信息。客户端启动第一件事就调它：能通说明地址/凭据/权限都对。
func (h *Handler) handleMe(w http.ResponseWriter, r *http.Request, c Ctx) {
	caps := []string{"fs", "thumb", "photos", "videos"}
	okJSON(w, map[string]any{
		"user":     c.User,
		"role":     c.Role,
		"perms":    c.Perms,
		"writable": c.Writable,
		"version":  c.Version,
		"api":      APIVer,
		"caps":     caps,
		"server":   "duihao",
	})
}

// ─── 路径解析 ───

// resolve 把共享内的逻辑路径（/照片/2026）解析成绝对路径，并做越界校验。
// 与 search.go 同口径：先拒绝 .. 段（不能先 Clean，否则折叠后查不到），
// 再 Clean、Join、前缀比对。
func resolve(root, rel string) (abs string, clean string, err error) {
	if rel == "" {
		rel = "/"
	}
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", "", errBadPath
		}
		for _, r := range seg {
			if r < 0x20 {
				return "", "", errBadPath
			}
		}
	}
	clean = path.Clean(rel)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", errBadPath
	}
	joined, err := filepath.Abs(filepath.Join(absRoot, filepath.FromSlash(clean)))
	if err != nil {
		return "", "", errBadPath
	}
	if joined != absRoot && !strings.HasPrefix(joined, absRoot+string(filepath.Separator)) {
		return "", "", errBadPath
	}
	return joined, clean, nil
}

// ─── 响应 ───

func okJSON(w http.ResponseWriter, v any) {
	writeJSON(w, http.StatusOK, map[string]any{"data": v})
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "msg": msg},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ─── 通用小工具 ───

// encodeCursor / decodeCursor 游标一律对客户端不透明（base64url），
// 客户端只负责原样回传，内部格式随时能改。
func encodeCursor(s string) string {
	if s == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func decodeCursor(s string) string {
	if s == "" {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(b)
}

func queryInt(r *http.Request, key string, def, min, max int) int {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return def
	}
	n := 0
	neg := false
	for i, ch := range v {
		if i == 0 && ch == '-' {
			neg = true
			continue
		}
		if ch < '0' || ch > '9' {
			return def
		}
		n = n*10 + int(ch-'0')
		if n > max*10 {
			break
		}
	}
	if neg {
		n = -n
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

// modTime 取文件修改时间（Unix 秒）；零值/异常时间回落 0，客户端按「未知」处理。
func modTime(fi os.FileInfo) int64 {
	t := fi.ModTime()
	if t.Year() < 1980 {
		return 0
	}
	return t.Unix()
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }
