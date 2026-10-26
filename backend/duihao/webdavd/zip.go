package webdavd

import (
	"archive/zip"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// streamZip 把 URL 路径对应的目录打成 zip 流式下载（浏览器「打包下载」）。
// 调用前提：guard 已做过路径安全校验与认证；root 为该共享的本地根目录，
// urlPath 为已解码的请求路径（r.URL.Path）。目录 walk 时对每个条目再做一次
// 段校验（防御纵深）。
func streamZip(w http.ResponseWriter, root, urlPath string) {
	rel := strings.Trim(urlPath, "/")
	if rel == "" {
		rel = "."
	}
	local := filepath.Join(root, filepath.FromSlash(rel))

	st, err := os.Stat(local)
	if err != nil || !st.IsDir() {
		http.Error(w, "not a directory", http.StatusNotFound)
		return
	}

	name := "dav"
	if rel != "." {
		name = filepath.Base(local)
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+zipSafeName(name)+`.zip"; filename*=UTF-8''`+url.PathEscape(name)+".zip")

	zw := zip.NewWriter(w)
	defer zw.Close()

	_ = filepath.WalkDir(local, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 单个条目失败不终止整包
		}
		relInZip, rerr := filepath.Rel(local, p)
		if rerr != nil || relInZip == "." {
			return nil
		}
		for _, seg := range strings.Split(filepath.ToSlash(relInZip), "/") {
			if checkSegment(seg) != nil {
				return nil // 可疑名字整枝跳过
			}
		}
		if d.IsDir() {
			return nil // zip 里目录由文件路径隐式产生
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		hdr := &zip.FileHeader{
			Name:     filepath.ToSlash(relInZip),
			Method:   zip.Deflate,
			Modified: info.ModTime(),
		}
		if m := info.ModTime(); m.Year() > 1980 {
			hdr.Modified = m
		} else {
			hdr.Modified = time.Unix(0, 0)
		}
		fw, zerr := zw.CreateHeader(hdr)
		if zerr != nil {
			return fs.SkipAll // 客户端断开等：尽快收尾
		}
		src, oerr := os.Open(p)
		if oerr != nil {
			return nil
		}
		defer src.Close()
		_, _ = io.Copy(fw, src)
		return nil
	})
}

// zipSafeName 附件名里的引号/控制符替换成下划线（filename= 部分用）。
func zipSafeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == '"' || r == '\\' {
			b.WriteByte('_')
		} else {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "dav"
	}
	return b.String()
}
