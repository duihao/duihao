// 运行时包指纹（TOFU：Trust On First Use）。
//
// 背景：上游对 Windows 二进制包**基本不提供官方摘要**——php.net 只给源码 tar 的
// sha256，nginx.org / Oracle MySQL / Redis(GitHub Release) 的 zip 都没有可机读的
// 校验和文件。清单里 "sha256": "" 不是忘了填，是拿不到。
//
// 那怎么保证「每条过 SHA256」这句承诺不落空？做法：
//  1. 包第一次落地时，**算出的摘要被记进 config/runtime-pins.json**（按 插件@版本）。
//  2. 此后同一插件的同一版本，无论来自下载、镜像还是内置包，摘要必须与记录一致，
//     否则拒绝安装（换过的镜像、被污染的缓存、被中间人替换的包都会被拦下）。
//  3. 清单里显式声明了 sha256 时，仍然以清单为准（清单优先于记录）。
//
// 局限（写在明处）：第一份包的来源仍然要可信。所以对高价值版本，把 zip 放到
// bin/data/runtimes 里用「内置包」通道，并在清单里写死 sha256，是更稳的做法。
package runtimepkg

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	duihao "github.com/duihao/duihao/backend/duihao"
)

type pinFile struct {
	SchemaVersion int               `json:"schemaVersion"`
	Pins          map[string]string `json:"pins"` // "php@8.3.14" → sha256
}

var pinMu sync.Mutex

func pinsPath() string { return filepath.Join(duihao.DirConfig(), "runtime-pins.json") }

func loadPins() pinFile {
	f := pinFile{SchemaVersion: 1, Pins: map[string]string{}}
	data, err := os.ReadFile(pinsPath())
	if err != nil {
		return f
	}
	var out pinFile
	if err := jsonUnmarshal(data, &out); err != nil || out.Pins == nil {
		return f
	}
	return out
}

func savePins(f pinFile) error {
	if err := os.MkdirAll(filepath.Dir(pinsPath()), 0o755); err != nil {
		return err
	}
	tmp := pinsPath() + ".tmp"
	if err := writeJSON(tmp, f); err != nil {
		return err
	}
	return os.Rename(tmp, pinsPath())
}

func pinKey(spec VersionSpec) string { return spec.PluginID + "@" + spec.Version }

// checkPin 校验/登记包指纹。
//   - 有记录且一致 → nil
//   - 有记录但不一致 → 错误（拒绝安装）
//   - 无记录 → 记录并返回 nil（首次信任）
func checkPin(spec VersionSpec, sum, source string) error {
	if sum == "" {
		return nil
	}
	pinMu.Lock()
	defer pinMu.Unlock()
	f := loadPins()
	key := pinKey(spec)
	if prev, ok := f.Pins[key]; ok {
		if prev != sum {
			return fmt.Errorf("安装包指纹与首次记录不一致（%s）：期望 %s 实际 %s —— 可能被替换或损坏，已拒绝安装",
				key, short(prev), short(sum))
		}
		return nil
	}
	f.Pins[key] = sum
	if err := savePins(f); err != nil {
		// 记不下来不阻断安装（首次信任已成立），下次会再记
		return nil
	}
	_ = source
	return nil
}

// PinReport 已锁定的指纹（诊断/界面用）。
func PinReport() map[string]string {
	pinMu.Lock()
	defer pinMu.Unlock()
	return loadPins().Pins
}
