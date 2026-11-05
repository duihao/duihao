package runtimepkg

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	duihao "github.com/duihao/duihao/backend/duihao"
)

// ---- 通用 JSON / 哈希工具 ----

func jsonUnmarshal(data []byte, v any) error {
	// 容错：外部工具（如 PowerShell 5.1 Set-Content -Encoding UTF8）写出的
	// JSON 可能带 UTF-8 BOM，encoding/json 会直接报错，剥掉再解。
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	return json.Unmarshal(data, v)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

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

// ---- zip 解压（含 zip-slip 防护）----

// unzip 解压 zip 到 dest，拒绝穿越条目（..\ 与绝对路径）。
func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	destAbs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	// 官方包常带单层顶层目录（nginx-1.15.11/、mysql-8.0.40-winx64/）。
	// 不剥离会导致清单 bin（nginx.exe / bin/mysqld.exe）在运行时根目录找不到。
	strip := singleRootPrefix(r.File)
	for _, f := range r.File {
		name := filepath.FromSlash(f.Name)
		if strip != "" {
			name = filepath.FromSlash(strings.TrimPrefix(strings.ReplaceAll(f.Name, `\`, `/`), strip))
			if name == "." || name == "" {
				continue // 顶层目录自身条目
			}
		}
		if strings.Contains(name, "..") || filepath.IsAbs(name) ||
			strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) ||
			(len(name) >= 2 && name[1] == ':') {
			return fmt.Errorf("zip 条目越界，已拒绝: %s", f.Name)
		}
		target := filepath.Join(destAbs, name)
		if !strings.HasPrefix(target, destAbs+string(filepath.Separator)) && target != destAbs {
			return fmt.Errorf("zip 条目越界，已拒绝: %s", f.Name)
		}
		// 目录判定：除 IsDir 标志外，还要看名字以 / 或 \ 结尾 ——
		// .NET Compress-Archive 等工具会把目录条目存成不带目录标志的
		// "path/" 条目（实测踩坑），只认 IsDir 会把它当文件创建，
		// 导致后续子路径 mkdir 全部失败。
		isDir := f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") || strings.HasSuffix(f.Name, `\`)
		if isDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		// 容错：同名路径已作为文件存在（前一条目被误判），先删再建目录。
		if st, err := os.Lstat(filepath.Dir(target)); err == nil && !st.IsDir() {
			if err := os.Remove(filepath.Dir(target)); err != nil {
				return err
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := extractFile(f, target); err != nil {
			return err
		}
	}
	return nil
}

// singleRootPrefix 返回整包唯一顶层目录前缀（"nginx-1.15.11/"），
// 无法安全判定时返回空串（根上有文件、有多个顶层目录、条目过少）。
func singleRootPrefix(files []*zip.File) string {
	if len(files) < 2 {
		return ""
	}
	root := ""
	for _, f := range files {
		n := strings.TrimSuffix(strings.ReplaceAll(f.Name, `\`, `/`), "/")
		if n == "" {
			continue
		}
		i := strings.Index(n, "/")
		if i <= 0 {
			return "" // 根目录直接有文件 → 不是单层包装
		}
		seg := n[:i+1]
		if root == "" {
			root = seg
		} else if root != seg {
			return ""
		}
	}
	return root
}

func extractFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

// ---- 第 2 级：bundled（payload/bundle.lock.json）----

// bundleLock 对应 payload/bundle.lock.json（方案 §5.3）。
type bundleLock struct {
	SchemaVersion int              `json:"schemaVersion"`
	Edition       string           `json:"edition"`
	BuiltAt       string           `json:"builtAt"`
	Runtimes      []bundleLockItem `json:"runtimes"`
}

type bundleLockItem struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	File     string `json:"file"`
	SHA256   string `json:"sha256"`
}

// findBundled 返回（zip 绝对路径, lock 文件路径）。zip 为空串表示 lock 命中但文件缺失。
// lock 为空串表示本机不是打包版。
func findBundled(spec VersionSpec) (zipPath, lockPath string) {
	payloadDir := duihao.PayloadDir()
	lockPath = filepath.Join(payloadDir, "bundle.lock.json")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return "", ""
	}
	var lock bundleLock
	if jsonUnmarshal(data, &lock) != nil {
		return "", ""
	}
	for _, it := range lock.Runtimes {
		if it.ID == spec.PluginID && it.Version == spec.Version &&
			(it.Platform == "" || it.Platform == spec.Platform) {
			zp := filepath.Join(payloadDir, filepath.FromSlash(it.File))
			if st, err := os.Stat(zp); err != nil || st.IsDir() {
				return "", lockPath
			}
			return zp, lockPath
		}
	}
	return "", ""
}

func lockEntrySHA(lockPath string, spec VersionSpec) string {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return ""
	}
	var lock bundleLock
	if jsonUnmarshal(data, &lock) != nil {
		return ""
	}
	for _, it := range lock.Runtimes {
		if it.ID == spec.PluginID && it.Version == spec.Version {
			return it.SHA256
		}
	}
	return ""
}

// ---- 第 3 级：离线镜像（mirror/manifest.json，方案 §5.6）----

type mirrorManifest struct {
	SchemaVersion int              `json:"schemaVersion"`
	CreatedAt     string           `json:"createdAt"`
	Runtimes      []mirrorManifestItem `json:"runtimes"`
}

type mirrorManifestItem struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
}

// ImportMirror 将外部镜像 zip 的清单导入 mirror\ 目录（环境工具「导入离线包」的后端）。
// manifest 是镜像附带的 manifest.json 内容；files 目录里放 zip。
func ImportMirror(manifestData []byte, filesDir string) error {
	var m mirrorManifest
	if err := jsonUnmarshal(manifestData, &m); err != nil {
		return fmt.Errorf("镜像 manifest.json 解析失败: %w", err)
	}
	if m.SchemaVersion != 1 {
		return fmt.Errorf("镜像 schemaVersion=%d 不支持", m.SchemaVersion)
	}
	if err := os.MkdirAll(duihao.DirMirror(), 0o755); err != nil {
		return err
	}
	// 校验所有 zip 在源目录存在（整批校验通过才落 manifest），并逐个复核摘要：
	// 镜像清单声明了 sha256 就必须匹配；没声明则走 TOFU 指纹记录（pins.go）。
	for _, it := range m.Runtimes {
		src := filepath.Join(filesDir, filepath.FromSlash(it.File))
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("镜像文件缺失: %s", it.File)
		}
		sum, _, err := sha256File(src)
		if err != nil {
			return fmt.Errorf("计算 %s 摘要失败: %w", it.File, err)
		}
		if it.SHA256 != "" {
			if !strings.EqualFold(it.SHA256, sum) {
				return fmt.Errorf("镜像文件 %s 的 SHA256 不匹配（期望 %s 实际 %s），整批拒绝导入",
					it.File, short(it.SHA256), short(sum))
			}
		} else if err := checkPin(VersionSpec{PluginID: it.ID, Version: it.Version}, sum, "mirror-import"); err != nil {
			return err
		}
	}
	for _, it := range m.Runtimes {
		dst := filepath.Join(duihao.DirMirror(), filepath.Base(filepath.FromSlash(it.File)))
		src := filepath.Join(filesDir, filepath.FromSlash(it.File))
		if err := copyFile(src, dst); err != nil {
			return err
		}
	}
	return writeJSON(filepath.Join(duihao.DirMirror(), "manifest.json"), m)
}

// findMirror 返回命中的 zip 绝对路径（未命中返回空）。
func findMirror(spec VersionSpec) string {
	data, err := os.ReadFile(filepath.Join(duihao.DirMirror(), "manifest.json"))
	if err != nil {
		return ""
	}
	var m mirrorManifest
	if jsonUnmarshal(data, &m) != nil {
		return ""
	}
	for _, it := range m.Runtimes {
		if it.ID == spec.PluginID && it.Version == spec.Version {
			return filepath.Join(duihao.DirMirror(), filepath.FromSlash(it.File))
		}
	}
	return ""
}

func mirrorEntrySHA(spec VersionSpec) string {
	data, err := os.ReadFile(filepath.Join(duihao.DirMirror(), "manifest.json"))
	if err != nil {
		return ""
	}
	var m mirrorManifest
	if jsonUnmarshal(data, &m) != nil {
		return ""
	}
	for _, it := range m.Runtimes {
		if it.ID == spec.PluginID && it.Version == spec.Version {
			return it.SHA256
		}
	}
	return ""
}

// ---- 第 4 级：在线下载 ----

var httpClient = &http.Client{Timeout: 0} // 大包下载不设总超时，由 ctx 控制

func newRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Duihao/1.0 (+https://duihao.cn)")
	return req, nil
}

// ---- 小工具 ----

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// 供 paths 之外独立使用的时间戳（保留以备排障）。
var _ = time.Now
