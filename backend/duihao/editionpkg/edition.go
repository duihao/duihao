// Package editionpkg 读取发行版配置（editions.json，M19 插件化产品线）。
//
// 约定：editions.json 与 exe 同级（安装包根，与 payload\ 同层）。
// core 构建不带该文件 → 一切走现状；专有版（如「对号网盘」drive）声明
// home.plugin 让主窗口首屏直接渲染该插件的 home 视图。
//
// 铁律：本包只读不写、零副作用；解析失败按「无 editions」处理并打日志，
// 绝不让专有版配置问题阻断启动（回落 connections 首屏）。
package editionpkg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// HomeSpec 首页归属声明（editions.json 的 home 段）。
type HomeSpec struct {
	// Plugin 首页归属插件 id（清单须带 ui.home）。
	Plugin string `json:"plugin"`
	// Fallback 兜底首屏：connections | sites（缺省 connections）。
	Fallback string `json:"fallback,omitempty"`
}

// Config editions.json 解析结果（schemaVersion 1，只取运行时关心的字段）。
type Config struct {
	SchemaVersion int      `json:"schemaVersion"`
	Edition       string   `json:"edition"`
	Name          string   `json:"name"`
	Home          *HomeSpec `json:"home,omitempty"`
	FirstScreen   string   `json:"firstScreen,omitempty"`
	HideViews     []string `json:"hideViews,omitempty"`
	PresetPlugins []string `json:"presetPlugins,omitempty"`
}

var (
	once   sync.Once
	config *Config // nil = 无 editions（core 构建）
	loadEr error
)

// FileName 安装包根的发行版配置文件名。
const FileName = "editions.json"

func load() {
	once.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return // 取不到 exe 路径 ≈ core
		}
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(exe), FileName))
		if err != nil {
			return // 文件不存在 = core 构建，静默
		}
		c, err := parse(raw)
		if err != nil {
			loadEr = err
			return
		}
		config = c
	})
}

// parse 解析并校验 editions.json 内容（与文件定位解耦，便于单测）。
func parse(raw []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("editions.json 解析失败（按无发行版处理）: %w", err)
	}
	if c.SchemaVersion != 1 {
		return nil, fmt.Errorf("editions.json schemaVersion=%d 不支持（仅 1），按无发行版处理", c.SchemaVersion)
	}
	if c.Edition == "" {
		return nil, fmt.Errorf("editions.json 缺少 edition，按无发行版处理")
	}
	if c.Home != nil {
		if c.Home.Plugin == "" {
			return nil, fmt.Errorf("editions.json home.plugin 为空")
		}
		if c.Home.Fallback == "" {
			c.Home.Fallback = "connections"
		}
	}
	return &c, nil
}

// Current 当前发行版配置；core 构建返回 nil。
func Current() (*Config, error) {
	load()
	return config, loadEr
}

// Home 返回首页声明；ok=false 表示无 home（core 或 editions 未声明）。
func Home() (plugin, fallback string, ok bool) {
	c, _ := Current()
	if c == nil || c.Home == nil {
		return "", "", false
	}
	return c.Home.Plugin, c.Home.Fallback, true
}

// HideViews 本 editions 显式隐藏的界面集合；core 返回空集。
func HideViews() map[string]bool {
	c, _ := Current()
	m := map[string]bool{}
	if c != nil {
		for _, v := range c.HideViews {
			m[v] = true
		}
	}
	return m
}
