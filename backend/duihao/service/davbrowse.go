// 网盘「共享内容」浏览/预览/上传（M24）。
//
// 定位：WebDAV 是给外部客户端（资源管理器 / RaiDrive）挂载用的；这里补的是
// **对号自己的轻量文件浏览**——不挂载也能看一眼共享里有什么、改一改、传几个文件。
//
// 安全边界与 WebDAV 完全一致：所有路径都相对共享根，逐段校验（webdavd.CheckRelPath）
// 之后再 Join，并二次确认落在根目录之内——前端传来的相对路径一律不可信。
package service

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd"
)

// DavEntry 共享里的一个条目。
type DavEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"` // 相对共享根（"/" 分隔，不含前导斜杠）
	Dir     bool   `json:"dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
	Ext     string `json:"ext,omitempty"`
}

// DavPreview 文件预览结果。
type DavPreview struct {
	Kind    string `json:"kind"` // text | image | pdf | binary | toolarge
	Mime    string `json:"mime,omitempty"`
	Text    string `json:"text,omitempty"`
	Base64  string `json:"base64,omitempty"`
	Size    int64  `json:"size"`
	Message string `json:"message,omitempty"`
}

// DavUploadResult 上传结果。
type DavUploadResult struct {
	Files       int      `json:"files"`
	Bytes       int64    `json:"bytes"`
	Overwritten int      `json:"overwritten"`
	Skipped     []string `json:"skipped,omitempty"`
}

// shareRoot 解析共享根（绝对路径）+ 共享记录。
func (s *WebDavService) shareRoot(instanceID, shareID string) (string, *webdavd.Share, error) {
	instDir, err := s.dir(instanceID)
	if err != nil {
		return "", nil, err
	}
	shares, err := webdavd.LoadShares(instDir)
	if err != nil {
		return "", nil, err
	}
	for i := range shares {
		if shares[i].ID == shareID {
			root := webdavd.ShareRoot(shares[i], instDir)
			if st, err := os.Stat(root); err != nil || !st.IsDir() {
				return "", nil, fmt.Errorf("共享根目录不存在或不是目录：%s", root)
			}
			return root, &shares[i], nil
		}
	}
	return "", nil, fmt.Errorf("共享不存在: %s", shareID)
}

// resolveIn 把相对路径解析成根目录内的绝对路径（越界一律拒绝）。
func resolveIn(root, rel string) (string, error) {
	rel = strings.Trim(strings.ReplaceAll(rel, `\`, "/"), "/")
	if rel != "" {
		if err := webdavd.CheckRelPath(rel); err != nil {
			return "", err
		}
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := filepath.Join(rootAbs, filepath.FromSlash(rel))
	tAbs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if tAbs != rootAbs && !strings.HasPrefix(tAbs, rootAbs+string(os.PathSeparator)) {
		return "", fmt.Errorf("路径越界: %s", rel)
	}
	return tAbs, nil
}

// ListWebDavDir 列出共享内某目录（dirs 在前，按名排序）。
func (s *WebDavService) ListWebDavDir(instanceID, shareID, rel string) ([]DavEntry, error) {
	root, _, err := s.shareRoot(instanceID, shareID)
	if err != nil {
		return nil, err
	}
	abs, err := resolveIn(root, rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("读取目录失败: %w", err)
	}
	out := make([]DavEntry, 0, len(entries))
	for _, e := range entries {
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		p := strings.Trim(strings.TrimPrefix(filepath.ToSlash(filepath.Join(abs, e.Name())), filepath.ToSlash(root)), "/")
		item := DavEntry{
			Name: e.Name(), Path: p, Dir: e.IsDir(), Size: info.Size(),
			ModTime: info.ModTime().Format("2006-01-02 15:04"),
		}
		if !e.IsDir() {
			item.Ext = strings.ToLower(strings.TrimPrefix(filepath.Ext(e.Name()), "."))
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// 文本类扩展名（可直接预览）。
var textExts = map[string]bool{
	"txt": true, "md": true, "json": true, "js": true, "ts": true, "css": true,
	"html": true, "htm": true, "xml": true, "yml": true, "yaml": true, "ini": true,
	"conf": true, "log": true, "php": true, "go": true, "py": true, "sh": true,
	"bat": true, "ps1": true, "sql": true, "csv": true, "env": true, "gitignore": true,
}

var imageExts = map[string]string{
	"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg",
	"gif": "image/gif", "webp": "image/webp", "bmp": "image/bmp",
	"svg": "image/svg+xml", "ico": "image/x-icon", "avif": "image/avif",
}

// PreviewWebDavFile 预览文件：文本 / 图片 / PDF 内联返回，其余给「不支持」。
// maxKB 限制内联大小（超出不读，避免把 500MB 视频读进内存）。
func (s *WebDavService) PreviewWebDavFile(instanceID, shareID, rel string, maxKB int) (*DavPreview, error) {
	root, _, err := s.shareRoot(instanceID, shareID)
	if err != nil {
		return nil, err
	}
	abs, err := resolveIn(root, rel)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("文件不存在: %w", err)
	}
	if st.IsDir() {
		return nil, fmt.Errorf("这是一个目录，不能预览")
	}
	if maxKB <= 0 {
		maxKB = 512
	}
	limit := int64(maxKB) * 1024
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(abs), "."))
	out := &DavPreview{Size: st.Size()}

	switch {
	case textExts[ext]:
		if st.Size() > limit {
			out.Kind = "toolarge"
			out.Message = fmt.Sprintf("文本文件 %.1f MB，超过预览上限 %d KB", float64(st.Size())/1048576, maxKB)
			return out, nil
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		out.Kind = "text"
		out.Mime = "text/plain; charset=utf-8"
		out.Text = string(data)
	case imageExts[ext] != "":
		if st.Size() > limit {
			out.Kind = "toolarge"
			out.Message = fmt.Sprintf("图片 %.1f MB，超过预览上限 %d KB", float64(st.Size())/1048576, maxKB)
			return out, nil
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		out.Kind = "image"
		out.Mime = imageExts[ext]
		out.Base64 = base64.StdEncoding.EncodeToString(data)
	case ext == "pdf":
		if st.Size() > 20*1024*1024 {
			out.Kind = "toolarge"
			out.Message = "PDF 超过 20 MB，请用「打开所在目录」用本地阅读器打开"
			return out, nil
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		out.Kind = "pdf"
		out.Mime = "application/pdf"
		out.Base64 = base64.StdEncoding.EncodeToString(data)
	default:
		out.Kind = "binary"
		out.Message = fmt.Sprintf("「.%s」不支持内联预览，可直接下载或在本地打开", ext)
	}
	return out, nil
}

// UploadToWebDav 把本地文件/文件夹复制进共享目录（目录递归）。
// 返回写入数量；同名文件会被覆盖并计入 Overwritten。
func (s *WebDavService) UploadToWebDav(instanceID, shareID, rel string, localPaths []string) (*DavUploadResult, error) {
	root, sh, err := s.shareRoot(instanceID, shareID)
	if err != nil {
		return nil, err
	}
	if sh.ReadOnly {
		return nil, fmt.Errorf("该共享是只读的（%s），请先关闭只读再上传", sh.User)
	}
	if len(localPaths) == 0 {
		return nil, fmt.Errorf("没有选择要上传的文件")
	}
	dstDir, err := resolveIn(root, rel)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(dstDir); err != nil || !st.IsDir() {
		if err := os.MkdirAll(dstDir, 0o755); err != nil {
			return nil, fmt.Errorf("目标目录不可用: %w", err)
		}
	}
	res := &DavUploadResult{}
	for _, src := range localPaths {
		src = strings.TrimSpace(src)
		if src == "" {
			continue
		}
		st, err := os.Stat(src)
		if err != nil {
			res.Skipped = append(res.Skipped, filepath.Base(src)+"（源文件不可读）")
			continue
		}
		if st.IsDir() {
			base := filepath.Join(dstDir, filepath.Base(src))
			if err := copyDirInto(src, base, res); err != nil {
				return res, err
			}
			continue
		}
		target := filepath.Join(dstDir, filepath.Base(src))
		if _, err := os.Stat(target); err == nil {
			res.Overwritten++
		}
		n, err := copyFileN(src, target)
		if err != nil {
			return res, err
		}
		res.Files++
		res.Bytes += n
	}
	return res, nil
}

func copyDirInto(src, dst string, res *DavUploadResult) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			res.Skipped = append(res.Skipped, path+"（读取失败）")
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if _, serr := os.Stat(target); serr == nil {
			res.Overwritten++
		}
		n, cerr := copyFileN(path, target)
		if cerr != nil {
			res.Skipped = append(res.Skipped, path+"（写入失败）")
			return nil
		}
		res.Files++
		res.Bytes += n
		return nil
	})
}

func copyFileN(src, dst string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); cerr != nil && err == nil {
		err = cerr
	}
	return n, err
}

// DeleteWebDavEntry 删除共享里的文件/目录（目录递归）。
func (s *WebDavService) DeleteWebDavEntry(instanceID, shareID, rel string) error {
	root, sh, err := s.shareRoot(instanceID, shareID)
	if err != nil {
		return err
	}
	if sh.ReadOnly {
		return fmt.Errorf("该共享是只读的，不能删除")
	}
	rel = strings.Trim(strings.ReplaceAll(rel, `\`, "/"), "/")
	if rel == "" {
		return fmt.Errorf("不能删除共享根目录")
	}
	abs, err := resolveIn(root, rel)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(abs); err != nil {
		return fmt.Errorf("删除失败: %w", err)
	}
	s.audit.Ok("ui", "webdav.delete", instanceID, map[string]any{"share": shareID, "path": rel})
	return nil
}

// CreateWebDavDir 在共享里新建文件夹。
func (s *WebDavService) CreateWebDavDir(instanceID, shareID, rel, name string) error {
	root, sh, err := s.shareRoot(instanceID, shareID)
	if err != nil {
		return err
	}
	if sh.ReadOnly {
		return fmt.Errorf("该共享是只读的，不能新建文件夹")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("文件夹名不能为空")
	}
	if strings.ContainsAny(name, `/\:*?"<>|`) {
		return fmt.Errorf("文件夹名包含非法字符")
	}
	base, err := resolveIn(root, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(base, name), 0o755); err != nil {
		return fmt.Errorf("新建文件夹失败: %w", err)
	}
	return nil
}

// OpenWebDavEntryDir 在资源管理器中打开某个条目所在目录。
func (s *WebDavService) OpenWebDavEntryDir(instanceID, shareID, rel string) error {
	root, _, err := s.shareRoot(instanceID, shareID)
	if err != nil {
		return err
	}
	abs, err := resolveIn(root, rel)
	if err != nil {
		return err
	}
	if st, err := os.Stat(abs); err == nil && !st.IsDir() {
		abs = filepath.Dir(abs)
	}
	return openPath(abs)
}

// 让 time 保留（预览里 ModTime 用字符串，保留导入一致性）
var _ = time.Now
