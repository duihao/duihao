package main

// 「清除缓存」（NAS 主窗 → 设置 → 清除缓存）的后端。
//
// 清什么（只清可再生的）：
//   · WebView2 的 HTTP / 代码 / 着色器缓存 —— 页面改版后还看到旧样式的元凶，
//     体积也最大（一台机器上通常几十 MB）；
//   · Service Worker 的 CacheStorage —— 网盘页面的离线副本；
//   · 应用临时目录 <数据根>\tmp。
//
// 不清什么（清了会让用户丢东西）：
//   · Local Storage / IndexedDB —— 主题、语言、NAS 登录令牌都在里面，清了等于
//     把用户踢下线；
//   · instances / sites / runtimes / config / logs —— 实例数据、站点文件、
//     运行环境、设置、日志，都是「数据」不是「缓存」。
//
// 文件被 WebView2 占用时 Windows 不让删：这种情况不报错，只统计「还占着多少」，
// 前端提示重启对号后再清一次（重启后句柄释放，能删干净）。

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ys-ll/uniterm/backend/log"
)

// cacheDirNames 认目录名而不是写死路径：WebView2 的 profile 布局各版本略有
// 差异（EBWebView/Default/Cache vs EBWebView/Cache），按名字找最稳。
var cacheDirNames = map[string]bool{
	"Cache":         true,
	"Code Cache":    true,
	"GPUCache":      true,
	"GrShaderCache": true,
	"ShaderCache":   true,
	"CacheStorage":  true,
}

// CacheResult 清理结果（前端据此拼一句人话）。
type CacheResult struct {
	Freed  int64 `json:"freed"`  // 实际释放的字节数
	Locked int64 `json:"locked"` // 被占用、这次没删掉的字节数
	Files  int   `json:"files"`  // 删掉的文件数
}

// dirUsage 目录里所有普通文件的总字节 + 文件数。
func dirUsage(root string) (int64, int) {
	var size int64
	var n int
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			size += info.Size()
			n++
		}
		return nil
	})
	return size, n
}

// findCacheDirs 在 root 下最多三层里找缓存目录（不往缓存目录内部继续找）。
func findCacheDirs(root string) []string {
	var out []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > 3 {
			return
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if cacheDirNames[e.Name()] {
				out = append(out, p)
				continue
			}
			walk(p, depth+1)
		}
	}
	walk(root, 1)
	return out
}

// ClearAppCache 清掉可再生的缓存，返回释放/占用统计。
func (a *App) ClearAppCache() (*CacheResult, error) {
	res := &CacheResult{}

	type target struct {
		path     string
		recreate bool // 临时目录清空后要建回去（缓存目录不需要，浏览器会自建）
	}
	targets := []target{}
	if a.webviewDataPath != "" {
		for _, d := range findCacheDirs(a.webviewDataPath) {
			targets = append(targets, target{path: d})
		}
		// 应用临时目录（跟着 webview 数据放在 %LOCALAPPDATA%\Duihao\tmp）
		targets = append(targets, target{path: filepath.Join(filepath.Dir(a.webviewDataPath), "tmp"), recreate: true})
	}
	// 便携版的 ./data 下也有一个 tmp
	if a.dataDir != "" {
		targets = append(targets, target{path: filepath.Join(a.dataDir, "tmp"), recreate: true})
	}

	seen := map[string]bool{}
	for _, t := range targets {
		if t.path == "" || seen[t.path] {
			continue
		}
		seen[t.path] = true
		if st, err := os.Stat(t.path); err != nil || !st.IsDir() {
			continue
		}
		size, files := dirUsage(t.path)
		if err := os.RemoveAll(t.path); err != nil {
			// 目录里有一部分被占用：把删不掉的部分记成「占用」
			left, _ := dirUsage(t.path)
			res.Freed += size - left
			res.Locked += left
			continue
		}
		res.Freed += size
		res.Files += files
		// 清完把临时目录建回去：目录不存在时有的下载/转码逻辑会先报错再说
		if t.recreate {
			_ = os.MkdirAll(t.path, 0o755)
		}
	}
	log.Writef("cache: cleared freed=%.1fMB locked=%.1fMB files=%d path=%s",
		float64(res.Freed)/(1<<20), float64(res.Locked)/(1<<20), res.Files, a.webviewDataPath)
	return res, nil
}
