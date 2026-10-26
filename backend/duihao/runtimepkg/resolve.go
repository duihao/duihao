// Package runtimepkg 实现运行时取包的唯一入口（方案 §5.2 五级取包）。
//
//	取包(pluginId, version, platform)
//	 ├─1 runtimes/<id>/<version>/ 已存在              → source=installed
//	 ├─2 editions payload（exe 同级 payload\）命中    → source=bundled
//	 ├─3 离线镜像（mirror\manifest.json）命中         → source=mirror
//	 ├─4 allowDownload 且在线                         → source=download（官方源）
//	 └─5 都不行 → 明确报错（含官方地址）
//
// 四条硬约束：每条路径过 SHA256；内置≠锁定；同版本只存一份（落点 runtimes/<id>/<version>/）；
// 内置优先于在线。来源记 .source.json。
package runtimepkg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	duihao "github.com/ys-ll/uniterm/backend/duihao"
)

// VersionSpec 描述一个待取包的运行时版本（由上层插件注册表从清单 versions[] 提供）。
type VersionSpec struct {
	PluginID string `json:"pluginId"`
	Version  string `json:"version"`
	Platform string `json:"platform"` // 如 windows-amd64
	// Source：url | github-release | mirror-only（mirror-only 禁走在线下载）。
	Source string `json:"source"`
	// URL 官方下载地址（仅 Source=url 时有效；github-release 由 M7-d 解析后回填）。
	URL string `json:"url,omitempty"`
	// URLs 备用下载源（镜像/加速），按序回退；与 URL 合并去重后逐个尝试。
	URLs   []string `json:"urls,omitempty"`
	SHA256 string   `json:"sha256,omitempty"` // 官方包校验值（64 hex）
	Size   int64    `json:"size,omitempty"`
	// Bin 二进制相对路径（如 nginx.exe / bin/mysqld.exe）。
	// 用于 installed 健康检查：目录在但二进制缺失视为残次安装，重装自愈。
	Bin string `json:"bin,omitempty"`

	// AllowDownload 覆盖全局设置（缺省用 settings.allowDownload）。
	AllowDownload *bool `json:"allowDownload,omitempty"`
}

// SourceInfo 写入 runtimes/<id>/<version>/.source.json（方案 §3.2）。
type SourceInfo struct {
	Source string `json:"source"` // installed | bundled | mirror | download
	SHA256 string `json:"sha256"`
	At     string `json:"at"`
}

// Result 取包结果：Dir 为运行时根目录（只读，多版本共存）。
type Result struct {
	Dir    string
	Source string
}

// 错误：第 5 级明确失败，ErrNoSource 携带官方地址供 UI 复制。
type ErrNoSource struct {
	PluginID string
	Version  string
	URL      string
	Reason   string
}

func (e *ErrNoSource) Error() string {
	msg := fmt.Sprintf("无法取包 %s@%s：%s", e.PluginID, e.Version, e.Reason)
	if e.URL != "" {
		msg += fmt.Sprintf("（需联网下载，或导入离线镜像；官方地址：%s）", e.URL)
	} else {
		msg += "（请导入离线镜像）"
	}
	return msg
}

var ErrMirrorOnly = errors.New("该版本声明为 mirror-only，只允许离线镜像导入")

// RuntimeDir 返回唯一落点 runtimes/<id>/<version>/。
func RuntimeDir(id, version string) string {
	return filepath.Join(duihao.DirRuntimes(), id, version)
}

// Installed 检查第 1 级：目录存在且带 .source.json（即来源已登记）。
// 仅凭 .source.json 无法发现“解压残缺”（例如官方 zip 多套一层目录导致 bin 找不到），
// 需要二进制校验时用 installedHealthy。
func Installed(id, version string) (Result, bool) {
	return installedHealthy(id, version, "")
}

// installedHealthy 在 Installed 基础上校验二进制是否存在。
//   - 有 .source.json 且 bin 在 → 健康
//   - 无 .source.json 但 bin 在 → 手工放入的运行时（source=manual），同样可用
//   - bin 缺失 → 残次安装，调用方应清理后重装
func installedHealthy(id, version, binRel string) (Result, bool) {
	dir := RuntimeDir(id, version)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return Result{}, false
	}
	if binRel != "" {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(binRel))); err != nil {
			return Result{}, false
		}
	}
	if info, ok := readSourceInfo(dir); ok {
		return Result{Dir: dir, Source: info.Source}, true
	}
	if binRel != "" {
		return Result{Dir: dir, Source: "manual"}, true
	}
	return Result{}, false
}

// healBrokenRuntime 清理残次安装目录（上次解压不完整、或官方包多套一层）。
// 只在确实不可用时动手：含 .source.json 的残次品直接删（可安全重装）；
// 无登记且非空又缺 bin 的目录不明来源，报错交用户处理，绝不擅自删。
func healBrokenRuntime(id, version, binRel string) error {
	dir := RuntimeDir(id, version)
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return nil
	}
	if binRel != "" {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(binRel))); err == nil {
			return nil // 二进制在，不动
		}
	}
	if _, ok := readSourceInfo(dir); ok {
		return os.RemoveAll(dir)
	}
	if entries, _ := os.ReadDir(dir); len(entries) == 0 {
		return os.RemoveAll(dir)
	}
	return fmt.Errorf("目标目录已存在但不完整（缺 %s），请手动清理后重试: %s", binRel, dir)
}

func readSourceInfo(dir string) (SourceInfo, bool) {
	var info SourceInfo
	data, err := os.ReadFile(filepath.Join(dir, ".source.json"))
	if err != nil || jsonUnmarshal(data, &info) != nil || info.Source == "" {
		return SourceInfo{}, false
	}
	return info, true
}

// Resolve 取包唯一入口。audit 可为 nil。
func Resolve(ctx context.Context, spec VersionSpec, auditLog interface {
	Ok(actor, action, target string, detail map[string]any)
	Fail(actor, action, target string, detail map[string]any)
}) (Result, error) {
	actor := "runtimepkg"

	// 第 1 级：installed（带二进制健康校验，残缺目录不算已安装）
	if r, ok := installedHealthy(spec.PluginID, spec.Version, spec.Bin); ok {
		auditLog.Ok(actor, "resolve.runtime", spec.PluginID+"@"+spec.Version,
			map[string]any{"source": r.Source})
		return r, nil
	}
	// 目录在但不可用 → 自愈清理，避免后续 rename 撞车。
	if err := healBrokenRuntime(spec.PluginID, spec.Version, spec.Bin); err != nil {
		auditLog.Fail(actor, "resolve.runtime", spec.PluginID+"@"+spec.Version,
			map[string]any{"level": "installed", "err": err.Error()})
		return Result{}, err
	}

	// 第 2 级：bundled（打包版 payload）
	if z, lock := findBundled(spec); lock != "" {
		if z != "" {
			r, err := extractFromZip(z, spec, "bundled")
			if err != nil {
				auditLog.Fail(actor, "resolve.runtime", spec.PluginID+"@"+spec.Version,
					map[string]any{"level": "bundled", "err": err.Error()})
				return Result{}, err
			}
			auditLog.Ok(actor, "resolve.runtime", spec.PluginID+"@"+spec.Version,
				map[string]any{"source": "bundled"})
			return r, nil
		}
		// lock 命中但 zip 缺失 → 安装包损坏，明确报错（防静默错误）
		err := &ErrNoSource{PluginID: spec.PluginID, Version: spec.Version,
			Reason: "bundle.lock.json 声明了内置包但 payload 文件缺失（安装包损坏？）"}
		auditLog.Fail(actor, "resolve.runtime", spec.PluginID+"@"+spec.Version,
			map[string]any{"level": "bundled", "err": err.Error()})
		return Result{}, err
	}

	// 第 3 级：离线镜像
	if z := findMirror(spec); z != "" {
		r, err := extractFromZip(z, spec, "mirror")
		if err != nil {
			auditLog.Fail(actor, "resolve.runtime", spec.PluginID+"@"+spec.Version,
				map[string]any{"level": "mirror", "err": err.Error()})
			return Result{}, err
		}
		auditLog.Ok(actor, "resolve.runtime", spec.PluginID+"@"+spec.Version,
			map[string]any{"source": "mirror"})
		return r, nil
	}

	// 第 4 级：在线下载
	allowDownload := duihao.DefaultSettings().AllowDownload
	if s, err := duihao.LoadSettings(); err == nil {
		allowDownload = s.AllowDownload
	}
	if spec.AllowDownload != nil {
		allowDownload = *spec.AllowDownload
	}
	if allowDownload && spec.Source != "mirror-only" && spec.URL != "" {
		r, err := downloadAndExtract(ctx, spec)
		if err != nil {
			auditLog.Fail(actor, "resolve.runtime", spec.PluginID+"@"+spec.Version,
				map[string]any{"level": "download", "err": err.Error()})
			return Result{}, err
		}
		auditLog.Ok(actor, "resolve.runtime", spec.PluginID+"@"+spec.Version,
			map[string]any{"source": "download", "url": spec.URL})
		return r, nil
	}

	// 第 5 级：明确失败
	reason := "无可用来源"
	if spec.Source == "mirror-only" {
		reason = ErrMirrorOnly.Error()
	} else if !allowDownload {
		reason = "设置关闭了在线下载（allowDownload=false）"
	} else if spec.URL == "" {
		reason = "清单未提供下载地址"
	}
	err := &ErrNoSource{PluginID: spec.PluginID, Version: spec.Version, URL: spec.URL, Reason: reason}
	auditLog.Fail(actor, "resolve.runtime", spec.PluginID+"@"+spec.Version,
		map[string]any{"err": err.Error()})
	return Result{}, err
}

// ---- zip 解压落盘 ----

// extractFromZip：校验 zip SHA256 → 解压到临时目录 → 原子改名到唯一落点 → 写 .source.json。
func extractFromZip(zipPath string, spec VersionSpec, source string) (Result, error) {
	// SHA256 每条路径都过（方案硬约束 1）。
	sum, size, err := sha256File(zipPath)
	if err != nil {
		return Result{}, fmt.Errorf("校验 %s 失败: %w", zipPath, err)
	}
	expect := expectedSHA(spec, zipPath, source)
	if expect != "" && expect != sum {
		return Result{}, fmt.Errorf("SHA256 不匹配：期望 %s 实际 %s", short(expect), short(sum))
	}
	// 清单没声明摘要时走 TOFU：首次落地记指纹，之后再装必须一致
	if expect == "" {
		if err := checkPin(spec, sum, source); err != nil {
			return Result{}, err
		}
	}

	finalDir := RuntimeDir(spec.PluginID, spec.Version)
	if err := os.MkdirAll(filepath.Dir(finalDir), 0o755); err != nil {
		return Result{}, err
	}

	// 临时目录解压（同版本只存一份：落点唯一，先解到 .tmp 再改名）。
	tmpDir := finalDir + ".tmp-" + fmt.Sprint(time.Now().UnixNano())
	defer os.RemoveAll(tmpDir)
	if err := unzip(zipPath, tmpDir); err != nil {
		return Result{}, fmt.Errorf("解压失败: %w", err)
	}

	// 竞态兜底：另一路刚好先落盘则直接复用（同版本只存一份）。
	if _, exists := installedHealthy(spec.PluginID, spec.Version, spec.Bin); exists {
		return Result{Dir: finalDir, Source: source}, nil
	}
	if err := os.Rename(tmpDir, finalDir); err != nil {
		return Result{}, fmt.Errorf("落盘失败（%v），请先清理 %s", err, finalDir)
	}
	if err := finalizeRuntime(spec); err != nil {
		return Result{}, err
	}
	info := SourceInfo{Source: source, SHA256: sum, At: time.Now().Format(time.RFC3339)}
	if err := writeSourceInfo(finalDir, info); err != nil {
		return Result{Dir: finalDir, Source: source}, err
	}
	_ = size
	return Result{Dir: finalDir, Source: source}, nil
}

// expectedSHA：bundled 取 lock、mirror 取镜像清单、download 取清单声明。
// zip 内嵌清单里没有的（例如测试手工放 zip）返回空 → 跳过比对（文件本体仍记入 .source.json）。
func expectedSHA(spec VersionSpec, zipPath, source string) string {
	switch source {
	case "bundled":
		_, lock := findBundled(spec)
		return lockEntrySHA(lock, spec)
	case "mirror":
		return mirrorEntrySHA(spec)
	default:
		return spec.SHA256
	}
}

// downloadAndExtract：依次尝试主源与备用镜像，第一个成功的落盘。
func downloadAndExtract(ctx context.Context, spec VersionSpec) (Result, error) {
	urls := candidateURLs(spec)
	if len(urls) == 0 {
		return Result{}, fmt.Errorf("清单未提供下载地址")
	}
	var errs []string
	for _, u := range urls {
		r, err := downloadOne(ctx, spec, u)
		if err == nil {
			return r, nil
		}
		errs = append(errs, fmt.Sprintf("%s → %v", u, err))
	}
	return Result{}, fmt.Errorf("全部 %d 个下载源均失败：%s", len(urls), strings.Join(errs, "；"))
}

// candidateURLs 合并 URL 与 URLs 并按序去重。
func candidateURLs(spec VersionSpec) []string {
	var out []string
	seen := map[string]bool{}
	for _, u := range append([]string{spec.URL}, spec.URLs...) {
		u = strings.TrimSpace(u)
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// downloadOne：流式下载 → 边下边算 SHA256 → 落临时文件 → 解压。
func downloadOne(ctx context.Context, spec VersionSpec, url string) (Result, error) {
	tmpZip, err := os.CreateTemp("", "duihao-dl-*.zip")
	if err != nil {
		return Result{}, err
	}
	tmpZipPath := tmpZip.Name()
	defer os.Remove(tmpZipPath)

	h := sha256.New()
	n, err := func() (int64, error) {
		defer tmpZip.Close()
		req, err := newRequest(ctx, url)
		if err != nil {
			return 0, err
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return 0, fmt.Errorf("下载返回 %s", resp.Status)
		}
		return io.Copy(io.MultiWriter(tmpZip, h), resp.Body)
	}()
	if err != nil {
		return Result{}, err
	}
	if spec.Size != 0 && spec.Size != n {
		return Result{}, fmt.Errorf("下载大小不符：期望 %d 实际 %d", spec.Size, n)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if spec.SHA256 != "" && spec.SHA256 != sum {
		return Result{}, fmt.Errorf("SHA256 不匹配：期望 %s 实际 %s", short(spec.SHA256), short(sum))
	}
	// 未声明摘要 → TOFU 校验/登记（见 pins.go）
	if spec.SHA256 == "" {
		if err := checkPin(spec, sum, "download"); err != nil {
			return Result{}, err
		}
	}

	finalDir := RuntimeDir(spec.PluginID, spec.Version)
	if err := os.MkdirAll(filepath.Dir(finalDir), 0o755); err != nil {
		return Result{}, err
	}
	tmpDir := finalDir + ".tmp-" + fmt.Sprint(time.Now().UnixNano())
	defer os.RemoveAll(tmpDir)
	if err := unzip(tmpZipPath, tmpDir); err != nil {
		return Result{}, fmt.Errorf("解压失败: %w", err)
	}
	if _, exists := installedHealthy(spec.PluginID, spec.Version, spec.Bin); exists {
		return Result{Dir: finalDir, Source: "download"}, nil
	}
	if err := os.Rename(tmpDir, finalDir); err != nil {
		// 目标已存在（并发或残次目录）→ 明确报错而非静默。
		return Result{}, fmt.Errorf("落盘失败（%v），请先清理 %s", err, finalDir)
	}
	if err := finalizeRuntime(spec); err != nil {
		return Result{}, err
	}
	info := SourceInfo{Source: "download", SHA256: sum, At: time.Now().Format(time.RFC3339)}
	if err := writeSourceInfo(finalDir, info); err != nil {
		return Result{Dir: finalDir, Source: "download"}, err
	}
	return Result{Dir: finalDir, Source: "download"}, nil
}

func writeSourceInfo(dir string, info SourceInfo) error {
	return writeJSON(filepath.Join(dir, ".source.json"), info)
}

// finalizeRuntime 落盘后校验二进制位置；官方包多套一层目录时兜底上提。
// 找不到二进制说明压缩包结构与清单不符 —— 宁可报错删掉，也不要留下
// “目录在、exe 不在”的残次品（下次启动会误判为已安装）。
func finalizeRuntime(spec VersionSpec) error {
	if spec.Bin == "" {
		return nil
	}
	dir := RuntimeDir(spec.PluginID, spec.Version)
	bin := filepath.Join(dir, filepath.FromSlash(spec.Bin))
	if _, err := os.Stat(bin); err == nil {
		return nil
	}
	promoteSingleRoot(dir)
	if _, err := os.Stat(bin); err == nil {
		return nil
	}
	_ = os.RemoveAll(dir)
	return fmt.Errorf("解压后未找到二进制 %s（压缩包结构与清单不符，已回滚）", spec.Bin)
}

// promoteSingleRoot 若目录下只有唯一子目录且无文件，则把该子目录内容上提一层。
// 单层剥离在 unzip 阶段已做，这里是最后一道兜底。
func promoteSingleRoot(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var dirs []string
	for _, e := range entries {
		if e.Name() == ".source.json" {
			continue
		}
		if !e.IsDir() {
			return // 根上有文件 → 不是单层包装
		}
		dirs = append(dirs, e.Name())
	}
	if len(dirs) != 1 {
		return
	}
	base := filepath.Join(dir, dirs[0])
	items, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, it := range items {
		_ = os.Rename(filepath.Join(base, it.Name()), filepath.Join(dir, it.Name()))
	}
	_ = os.Remove(base)
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12] + "…"
	}
	return s
}
