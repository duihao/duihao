// Package compkit 可选运行组件的取包与状态管理。
//
// 对号的两个「大件」能力都靠外部命令行程序：监控要 ffmpeg，下载要 aria2c。
// 它们都不该塞进安装包（几十上百 MB，绝大多数用户用不到），所以统一走
// 既有的「五级取包」：
//
//	1 runtimes/<id>/<ver>/ 已存在     → 直接用（之前装过的）
//	2 打包版 payload\                 → 内置优先
//	3 离线镜像 mirror\manifest.json   → 拷 zip 进来即可无网安装
//	4 在线下载（官方地址 + 镜像加速逐个试）
//	5 都不行 → 明确报错并给出官方地址，页面显示「导入本地包」
//
// 本包只做「一个组件」的通用部分，不关心组件怎么被使用。之前这段代码长在
// camera 包里（只有 ffmpeg 一个组件），下载模块要第二个组件时抽出来共用；
// 再来第三个（比如 go2rtc）就只需往调用方的清单里加一条 Spec。
//
// 许可证立场：**GPL 组件只以「用户自己获取」的方式存在**。ffmpeg 用 LGPL
// 构建可以随包发；aria2c 是 GPLv2，对号不打包、不分发二进制，只调用用户
// 机器上已有的副本 —— 跟 MySQL 的处理方式一致。
package compkit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	duihao "github.com/ys-ll/uniterm/backend/duihao"
	"github.com/ys-ll/uniterm/backend/duihao/runtimepkg"
)

// Spec 一个可选运行组件。Version 只作为落点目录名 —— latest 会漂移，
// 所以固定一个语义化标签，升级时改这里。
type Spec struct {
	Tag     string   // 日志前缀：camera / download
	ID      string   // 落点目录名与清单键：ffmpeg / aria2
	Name    string   // 展示名
	Version string   // 如 7.1-lgpl / 1.37.0
	Bin     string   // 解压后（单层已剥离）的相对路径，斜杠分隔
	Size    int64    // 参考体积，仅用于 UI 提示
	Note    string   // 一句话说明这组件干什么
	URLs    []string // 第 0 个是官方地址，其余是加速镜像
}

// Status 给页面用的组件状态。
type Status struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	State   string `json:"state"` // missing | downloading | ready | error
	Version string `json:"version"`
	Size    int64  `json:"size"`
	Bin     string `json:"bin"`
	Dir     string `json:"dir"`
	Note    string `json:"note"`
	Error   string `json:"error"`
	URL     string `json:"url"` // 官方地址，手动下载时用
}

type state struct {
	mu      sync.Mutex
	state   string
	err     string
	started time.Time
}

var (
	mu       sync.Mutex
	stateMap = map[string]*state{}
)

func stateOf(id string) *state {
	mu.Lock()
	defer mu.Unlock()
	if s, ok := stateMap[id]; ok {
		return s
	}
	s := &state{state: "missing"}
	stateMap[id] = s
	return s
}

// Bin 已安装组件的可执行文件路径；未安装返回空串。
func Bin(s Spec) string {
	if s.ID == "" || s.Bin == "" {
		return ""
	}
	p := filepath.Join(runtimepkg.RuntimeDir(s.ID, s.Version), filepath.FromSlash(s.Bin))
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p
	}
	return ""
}

// Ready 组件是否可用。
func Ready(s Spec) bool { return Bin(s) != "" }

// StatusOf 汇总磁盘与内存状态：**磁盘是真相**，装好了就是 ready，
// 内存里的 downloading/error 只用来把下载过程画给用户看。
func StatusOf(s Spec) Status {
	out := Status{ID: s.ID, Name: s.Name, Version: s.Version, Size: s.Size, Note: s.Note}
	st := stateOf(s.ID)
	st.mu.Lock()
	out.State, out.Error = st.state, st.err
	st.mu.Unlock()

	// 展示官方地址：镜像只用于程序内部回退，别让用户去记加速域名。
	for _, u := range s.URLs {
		if strings.Contains(u, "ghproxy") || strings.Contains(u, "gh-proxy") {
			continue
		}
		out.URL = u
		break
	}

	if p := Bin(s); p != "" {
		out.State = "ready"
		out.Bin = p
		out.Dir = filepath.Dir(filepath.Dir(p))
		out.Error = ""
	}
	return out
}

// auditShim runtimepkg 要一个审计口，这里落到标准日志（取包过程本来也进应用日志）。
type auditShim struct{ tag string }

func (a auditShim) Ok(actor, action, target string, detail map[string]any) {
	log.Printf("%s: %s %s %s %v", a.tag, actor, action, target, detail)
}

func (a auditShim) Fail(actor, action, target string, detail map[string]any) {
	log.Printf("%s: %s %s %s FAIL %v", a.tag, actor, action, target, detail)
}

// Install 后台下载组件（几十 MB 级，HTTP 不能挂着等）。幂等：已装或正在装都算成功。
func Install(s Spec) error {
	if s.ID == "" {
		return fmt.Errorf("组件 ID 为空")
	}
	if Bin(s) != "" {
		return nil
	}
	st := stateOf(s.ID)
	st.mu.Lock()
	if st.state == "downloading" {
		st.mu.Unlock()
		return nil
	}
	st.state = "downloading"
	st.err = ""
	st.started = time.Now()
	st.mu.Unlock()

	tag := s.Tag
	if tag == "" {
		tag = s.ID
	}
	go func() {
		vs := runtimepkg.VersionSpec{
			PluginID: s.ID,
			Version:  s.Version,
			Platform: "windows-amd64",
			Source:   "url",
			URL:      s.URLs[0],
			Bin:      s.Bin,
		}
		if len(s.URLs) > 1 {
			vs.URLs = s.URLs[1:]
		}
		res, err := runtimepkg.Resolve(context.Background(), vs, auditShim{tag: tag})

		st.mu.Lock()
		defer st.mu.Unlock()
		if err != nil {
			st.state = "error"
			st.err = err.Error()
			log.Printf("%s: %s 安装失败: %v", tag, s.ID, err)
			return
		}
		st.state = "ready"
		st.err = ""
		log.Printf("%s: %s 就绪 %s（来源 %s）", tag, s.ID, res.Dir, res.Source)
	}()
	return nil
}

// Import 离线导入：把 zip 拷进 mirror/files/ + 算 SHA256 写进 manifest.json，
// 然后走第 3 级取包。用它的场景是「这台机器完全没网，另一台下好 zip 拷过来」。
func Import(s Spec, zipPath string) (string, error) {
	zipPath = strings.TrimSpace(zipPath)
	if zipPath == "" {
		return "", fmt.Errorf("请提供 zip 的完整路径")
	}
	st0, err := os.Stat(zipPath)
	if err != nil || st0.IsDir() {
		return "", fmt.Errorf("找不到文件：%s", zipPath)
	}
	if !strings.EqualFold(filepath.Ext(zipPath), ".zip") {
		return "", fmt.Errorf("只支持 zip 包")
	}

	filesDir := filepath.Join(duihao.DirMirror(), "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		return "", err
	}
	dstName := fmt.Sprintf("%s-%s.zip", s.ID, s.Version)
	dst := filepath.Join(filesDir, dstName)
	if err := CopyFile(zipPath, dst); err != nil {
		return "", err
	}
	sum, err := SHA256File(dst)
	if err != nil {
		return "", err
	}

	m := MirrorManifest{}
	if data, err := os.ReadFile(filepath.Join(duihao.DirMirror(), "manifest.json")); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	entry := MirrorEntry{ID: s.ID, Version: s.Version, File: "files/" + dstName, SHA256: sum, Size: st0.Size()}
	replaced := false
	for i := range m.Runtimes {
		if m.Runtimes[i].ID == s.ID && m.Runtimes[i].Version == s.Version {
			m.Runtimes[i] = entry
			replaced = true
		}
	}
	if !replaced {
		m.Runtimes = append(m.Runtimes, entry)
	}
	data, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(duihao.DirMirror(), "manifest.json"), data, 0o644); err != nil {
		return "", err
	}

	tag := s.Tag
	if tag == "" {
		tag = s.ID
	}
	res, err := runtimepkg.Resolve(context.Background(), runtimepkg.VersionSpec{
		PluginID: s.ID, Version: s.Version, Platform: "windows-amd64",
		Source: "mirror-only", Bin: s.Bin,
	}, auditShim{tag: tag})
	if err != nil {
		return "", err
	}
	st := stateOf(s.ID)
	st.mu.Lock()
	st.state = "ready"
	st.err = ""
	st.mu.Unlock()
	return res.Dir, nil
}

// MirrorManifest 离线镜像清单（与 runtimepkg 认的格式一致）。
type MirrorManifest struct {
	SchemaVersion int           `json:"schemaVersion"`
	Runtimes      []MirrorEntry `json:"runtimes"`
}

// MirrorEntry 清单里的一条。
type MirrorEntry struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

// SHA256File 算文件摘要（导入时锁进 manifest，挡住拷坏的 zip）。
func SHA256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CopyFile 原子拷贝（先写 .part 再 rename），跨盘也能用。
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
