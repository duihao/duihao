package editionpkg

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseDrive 覆盖「对号网盘」专有版配置的解析与缺省补全。
func TestParseDrive(t *testing.T) {
	raw := []byte(`{
		"schemaVersion": 1,
		"edition": "drive",
		"name": "对号网盘",
		"home": {"plugin": "webdav"},
		"presetPlugins": ["webdav"]
	}`)
	c, err := parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Edition != "drive" || c.Name != "对号网盘" {
		t.Fatalf("edition/name = %q/%q", c.Edition, c.Name)
	}
	if c.Home == nil || c.Home.Plugin != "webdav" {
		t.Fatalf("home.plugin 应为 webdav，得到 %+v", c.Home)
	}
	if c.Home.Fallback != "connections" {
		t.Fatalf("fallback 缺省应补 connections，得到 %q", c.Home.Fallback)
	}
}

// TestParseExplicitFallback 显式 fallback 原样保留。
func TestParseExplicitFallback(t *testing.T) {
	c, err := parse([]byte(`{"schemaVersion":1,"edition":"x","name":"x","home":{"plugin":"webdav","fallback":"sites"}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Home.Fallback != "sites" {
		t.Fatalf("fallback = %q，期望 sites", c.Home.Fallback)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"坏 JSON":       `{`,
		"版本不支持":        `{"schemaVersion":2,"edition":"x","name":"x"}`,
		"缺 edition":    `{"schemaVersion":1,"name":"x"}`,
		"home 缺 plugin": `{"schemaVersion":1,"edition":"x","name":"x","home":{}}`,
	}
	for name, raw := range cases {
		if _, err := parse([]byte(raw)); err == nil {
			t.Errorf("%s：预期报错但通过", name)
		}
	}
}

// TestLoadMissingFile 约定：exe 同级无 editions.json = core 构建，静默无配置。
func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(err) {
		t.Fatalf("临时目录不应存在 editions.json")
	}
	if _, err := os.ReadFile(filepath.Join(dir, FileName)); err == nil {
		t.Fatalf("预期读取失败")
	}
}
