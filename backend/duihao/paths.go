// Package duihao 提供对号的运行时基础：数据根目录、目录布局与全局设置。
//
// 目录布局见方案 §3.2。**默认便携**：数据根 = exe 同级 data\，
// 即软件自带的一切（运行时、实例、站点、日志）都在安装目录下的一个文件夹里，
// 拷走整个目录即可迁移/备份。
//
// 仅当 exe 所在目录不可写（例如 machine scope 装进 Program Files\）时，
// 才回退到 %LOCALAPPDATA%\Duihao。
package duihao

import (
	"os"
	"path/filepath"
	"sync"
)

// exeDir 返回主程序所在目录。
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

var (
	rootOnce   sync.Once
	cachedRoot string
)

// DataRoot 返回数据根目录。判定顺序：
//  1. DUIHAO_DATA_ROOT 显式指定（测试/高级排障钩子）
//  2. DUIHAO_PORTABLE=1 或 =0 —— 强制便携 / 强制非便携
//  3. exe 同级 data\ 已存在       → 用它（便携，且不因目录暂时不可写而漂移）
//  4. exe 目录可写                → exe 同级 data\（**默认行为**）
//  5. %LOCALAPPDATA%\Duihao（缺失时回退 %APPDATA%\Duihao，再回退 Temp）
func DataRoot() string {
	if root := os.Getenv("DUIHAO_DATA_ROOT"); root != "" {
		return root
	}
	rootOnce.Do(func() {
		local := filepath.Join(exeDir(), "data")
		switch os.Getenv("DUIHAO_PORTABLE") {
		case "1":
			cachedRoot = local
			return
		case "0":
			cachedRoot = osDataDir()
			return
		}
		if st, err := os.Stat(local); err == nil && st.IsDir() {
			cachedRoot = local
			return
		}
		if dirWritable(exeDir()) {
			cachedRoot = local
			return
		}
		cachedRoot = osDataDir()
	})
	return cachedRoot
}

// osDataDir 系统级回退目录（exe 目录不可写时）。
func osDataDir() string {
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		return filepath.Join(local, "Duihao")
	}
	if appdata := os.Getenv("APPDATA"); appdata != "" {
		return filepath.Join(appdata, "Duihao")
	}
	return filepath.Join(os.TempDir(), "Duihao")
}

// dirWritable 探测目录是否可写（写一个临时文件再删）。
func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".duihao-wtest-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return true
}

// Portable 报告当前是否运行在便携模式（数据在 exe 同级 data\）。
func Portable() bool {
	root := DataRoot()
	rel, err := filepath.Rel(exeDir(), root)
	return err == nil && rel == "data"
}

// 各子目录（§3.2）。
func DirConfig() string    { return filepath.Join(DataRoot(), "config") }
func DirRuntimes() string  { return filepath.Join(DataRoot(), "runtimes") }
func DirInstances() string { return filepath.Join(DataRoot(), "instances") }
func DirSites() string     { return filepath.Join(DataRoot(), "sites") }
func DirLogs() string      { return filepath.Join(DataRoot(), "logs") }
func DirMirror() string    { return filepath.Join(DataRoot(), "mirror") }
func DirBackup() string    { return filepath.Join(DataRoot(), "backup") }

// DirProcessLogs 返回子进程 stdout/stderr 日志目录 logs\process\。
func DirProcessLogs() string { return filepath.Join(DirLogs(), "process") }

// EnsureDirs 创建全部目录布局，幂等。
func EnsureDirs() error {
	for _, d := range []string{
		DirConfig(), DirRuntimes(), DirInstances(), DirSites(),
		DirLogs(), DirProcessLogs(), DirMirror(), DirBackup(),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// PayloadDir 返回打包版内置运行时目录（exe 同级 payload\）。
// 仅打包版存在；Core 版不存在则 bundled 级自然跳过。
// DUIHAO_PAYLOAD_DIR 可显式覆盖（测试/排障钩子）。
func PayloadDir() string {
	if d := os.Getenv("DUIHAO_PAYLOAD_DIR"); d != "" {
		return d
	}
	return filepath.Join(exeDir(), "payload")
}
