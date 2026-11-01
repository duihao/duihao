// 报告附件（H4）：照片 / PDF 存网盘 _appfiles/health/<YYYY-MM>/，库里只存路径。
//
// 接口：
//
//	attach.upload multipart(file) → {name,path,url,size}
//	attach.delete {path}          → {ok}（只删 _appfiles/health/ 下的文件）
//
// 口径：
//   - 附件永远不进 SQLite（体检报告扫描件动辄几 MB，塞进去库就废了）；
//   - 文件落在网盘文件树里，用户在「网盘」能看见、能整理、能同步，和笔记图片同一套规矩；
//   - 删除只允许 _appfiles/health/ 前缀内的路径，越界一律 403。
package health

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
)

const (
	attachDir  = "_appfiles/health"
	attachMax  = 200 << 20 // 单文件 200MB（与笔记一致）
	attachName = 80
)

func attachUpload(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	if strings.TrimSpace(c.Root) == "" {
		return appkit.Errorf(http.StatusInternalServerError, "网盘目录不可用")
	}
	r.Body = http.MaxBytesReader(w, r.Body, attachMax+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "上传失败：文件过大（上限 200MB）或格式错误")
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		return appkit.Errorf(http.StatusBadRequest, "缺少文件字段 file")
	}
	defer func() { _ = f.Close() }()

	name := safeAttachName(hdr.Filename)
	relDir := path.Join("/", attachDir, time.Now().Format("2006-01"))
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
		"name": name, "path": rel, "url": (&url.URL{Path: rel}).EscapedPath(), "size": hdr.Size,
	})
	return nil
}

// attachDelete 删附件：先校验路径在 _appfiles/health/ 里，再删文件。
// 文件已不存在也算成功（用户可能在网盘里先删了，报告里的引用还没清）。
func attachDelete(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Path string `json:"path"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	rel := strings.TrimSpace(in.Path)
	if rel == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少附件路径")
	}
	clean := path.Clean("/" + rel)
	if !strings.HasPrefix(clean, "/"+attachDir+"/") {
		return appkit.Errorf(http.StatusForbidden, "只能删除健康附件目录里的文件")
	}
	full := filepath.Join(c.Root, filepath.FromSlash(clean))
	if absRoot, err := filepath.Abs(c.Root); err == nil {
		if absFull, err2 := filepath.Abs(full); err2 == nil &&
			!strings.HasPrefix(absFull, absRoot+string(filepath.Separator)) {
			return appkit.Errorf(http.StatusForbidden, "路径越界")
		}
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return appkit.Errorf(http.StatusInternalServerError, "删除附件失败：%v", err)
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// attachSave 只更新报告的附件清单（不动指标）。
// 单独一个动作是为了让「加一张照片」不必把整份报告连指标一起重发重校验。
// 动作名以 save 结尾 → 只读账号会被 appkit 拦成 405。
func attachSave(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ReportID    string   `json:"reportId"`
		Attachments []string `json:"attachments"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	id := strings.TrimSpace(in.ReportID)
	if id == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少报告 id")
	}
	// 只收 _appfiles/health/ 下的路径，避免把任意网盘文件写进报告附件
	clean := make([]string, 0, len(in.Attachments))
	for _, p := range in.Attachments {
		if strings.TrimSpace(p) == "" {
			continue
		}
		v := path.Clean("/" + strings.TrimSpace(p))
		if !strings.HasPrefix(v, "/"+attachDir+"/") {
			return appkit.Errorf(http.StatusBadRequest, "附件必须放在 _appfiles/health/ 下")
		}
		clean = append(clean, v)
	}
	atts, _ := json.Marshal(clean)
	res, err := c.DB.Exec(`update health_reports set attachments=?, updated_at=? where uid=? and id=?`,
		string(atts), time.Now().Format(time.RFC3339), c.User, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return appkit.Errorf(http.StatusNotFound, "报告不存在")
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "attachments": clean})
	return nil
}

// safeAttachName 清洗文件名（去掉路径分隔符与控制字符，限长保留扩展名）。
func safeAttachName(s string) string {
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
	if len(r) > attachName {
		ext := filepath.Ext(s)
		be := []rune(ext)
		if len(be) > 12 {
			be = be[:12]
		}
		s = string(r[:attachName-len(be)]) + string(be)
	}
	return s
}

func randHex(n int) string { return newID()[:n] }
