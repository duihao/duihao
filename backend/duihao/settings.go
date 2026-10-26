package duihao

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// PortPool 端口池区间（含端点）。
type PortPool struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// Settings 对应 config\settings.json（方案 §3.2）。
type Settings struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Edition       string               `json:"edition"`                 // core | site-builder | ...
	AllowDownload bool                 `json:"allowDownload"`           // 五级取包第 4 级开关，默认 true
	PortPools     map[string]PortPool  `json:"portPools"`               // web / fcgi / db / ftp / misc
	Extra         map[string]any       `json:"extra,omitempty"`         // 预留，不解释
}

// DefaultSettings 返回默认设置（端口池见方案 §3.3）。
// 本机 80/8080/9000 常被 phpstudy 占用 —— 池内分配会逐个 bind 探测，天然避开。
func DefaultSettings() *Settings {
	return &Settings{
		SchemaVersion: 1,
		Edition:       "core",
		AllowDownload: true,
		PortPools:     defaultPortPools(),
	}
}

// defaultPortPools 默认端口池。单列出来：LoadSettings 里 JSON 会整表覆盖
// PortPools，老版本 settings.json 没有 misc 池时靠这里补齐。
func defaultPortPools() map[string]PortPool {
	return map[string]PortPool{
		"web":  {8080, 8099},
		"fcgi": {9000, 9099},
		"db":   {3306, 3356},
		"ftp":  {2121, 2199},
		"misc": {9100, 9199},
	}
}

func settingsPath() string { return filepath.Join(DirConfig(), "settings.json") }

// LoadSettings 读取 settings.json；文件不存在时返回默认值（不落盘，落盘交给 Save）。
func LoadSettings() (*Settings, error) {
	data, err := os.ReadFile(settingsPath())
	if errors.Is(err, os.ErrNotExist) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return nil, err
	}
	s := DefaultSettings()
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	// 老版本 settings.json 缺新池（如 misc）时补默认值；用户已有的池不动。
	def := defaultPortPools()
	for k, v := range def {
		if _, ok := s.PortPools[k]; !ok {
			if s.PortPools == nil {
				s.PortPools = map[string]PortPool{}
			}
			s.PortPools[k] = v
		}
	}
	return s, nil
}

// Save 落盘 settings.json。
func (s *Settings) Save() error {
	if err := os.MkdirAll(DirConfig(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath(), data, 0o644)
}
