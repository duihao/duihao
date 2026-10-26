package deploy

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractZip 解压到 dest（防 zip slip）。
func extractZip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("不是有效的 zip 包: %w", err)
	}
	defer r.Close()
	// dest 可能是临时目录，允许覆盖
	for _, f := range r.File {
		target := filepath.Join(dest, filepath.FromSlash(f.Name))
		// zip slip：目标必须仍在 dest 之内
		absDest, err := filepath.Abs(dest)
		if err != nil {
			return err
		}
		absTarget, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		if absTarget != absDest && !strings.HasPrefix(absTarget, absDest+string(os.PathSeparator)) {
			return fmt.Errorf("压缩包内含非法路径: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			return err
		}
		out.Close()
		rc.Close()
	}
	return nil
}

// promoteSingleRoot 官方包常见形态是「一个顶层目录包住全部文件」（duihaocms/、
// wordpress/）。这种目录会把站点根变成 /duihaocms/index.php，直接 404 —— 这里把
// 唯一顶层目录的内容提到上一级。
func promoteSingleRoot(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return
	}
	inner := filepath.Join(dir, entries[0].Name())
	sub, err := os.ReadDir(inner)
	if err != nil {
		return
	}
	for _, e := range sub {
		_ = os.Rename(filepath.Join(inner, e.Name()), filepath.Join(dir, e.Name()))
	}
	_ = os.Remove(inner)
}

// copyTree 把 src 下的全部文件复制到 dst（覆盖同名文件，不动 dst 里其它文件）。
func copyTree(src, dst string) (int, int64, error) {
	var files int
	var bytes int64
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil // 符号链接/设备文件一律跳过
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		n, err := io.Copy(out, in)
		if cerr := out.Close(); cerr != nil && err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		files++
		bytes += n
		return nil
	})
	return files, bytes, err
}

// sha256File 计算文件摘要。
func sha256File(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
