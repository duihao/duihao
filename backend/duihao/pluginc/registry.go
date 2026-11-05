// 插件注册表：内置清单（go:embed）+ 用户导入清单（config\plugins\*.json）。
// 同 id 时用户清单覆盖内置；未知顶层字段 warn 忽略。
package pluginc

import (
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	duihao "github.com/duihao/duihao/backend/duihao"
)

//go:embed embed/builtin/*.json
var builtinFS embed.FS

// Registry 清单注册表。
type Registry struct {
	byID map[string]*Manifest
	// warns 加载期告警（未知字段、v1 兼容降级等），UI 可展示。
	warns []string
}

// Load 扫描全部来源并构建注册表。
func Load() (*Registry, error) {
	r := &Registry{byID: map[string]*Manifest{}}

	// 1) 内置清单（首发 nginx / php / composer）。
	entries, err := builtinFS.ReadDir("embed/builtin")
	if err != nil {
		return nil, fmt.Errorf("读内置清单: %w", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := builtinFS.ReadFile("embed/builtin/" + e.Name())
		if err != nil {
			return nil, err
		}
		if err := r.add(data, "builtin/"+e.Name()); err != nil {
			return nil, err
		}
	}

	// 2) 用户导入清单：config\plugins\*.json（离线导入入口，M3 仅加载）。
	userDir := filepath.Join(duihao.DirConfig(), "plugins")
	files, _ := filepath.Glob(filepath.Join(userDir, "*.json"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			r.warnf("读用户清单 %s 失败: %v", f, err)
			continue
		}
		if err := r.add(data, f); err != nil {
			// 单个用户清单损坏不拖垮整个注册表。
			r.warnf("加载用户清单 %s 失败: %v", f, err)
		}
	}

	return r, nil
}

// add 解析并注册一份清单（同 id 覆盖）。
func (r *Registry) add(data []byte, origin string) error {
	m, warns, err := parseManifest(data)
	if err != nil {
		return fmt.Errorf("%s: %w", origin, err)
	}
	for _, w := range warns {
		r.warnf("%s: %s", origin, w)
	}
	if m.SchemaVersion == 1 {
		r.warnf("%s: schemaVersion=1（v1 兼容加载，建议升级 v2）", origin)
	}
	if prev, ok := r.byID[m.ID]; ok {
		r.warnf("%s: 覆盖同 id 清单 %s（来源优先级：用户 > 内置）", origin, prev.ID)
	}
	r.byID[m.ID] = m
	return nil
}

// parseManifest 解析单份清单：先 map 探测未知字段，再结构化解码。
func parseManifest(data []byte) (*Manifest, []string, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("JSON 解析失败: %w", err)
	}
	var warns []string
	for k := range raw {
		if !knownFields[k] {
			warns = append(warns, fmt.Sprintf("未知字段 %q 忽略（向后兼容）", k))
			slog.Warn("插件清单未知字段", "field", k)
		}
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, warns, err
	}
	// v1 兼容：schemaVersion 缺省按 2 处理缺省值兜底。
	if m.SchemaVersion == 0 {
		m.SchemaVersion = 2
	}
	if err := m.Validate(); err != nil {
		return nil, warns, err
	}
	return &m, warns, nil
}

func (r *Registry) warnf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	r.warns = append(r.warns, msg)
	slog.Warn("插件清单", "warn", msg)
}

// Warns 返回加载期告警快照。
func (r *Registry) Warns() []string { return append([]string(nil), r.warns...) }

// Get 按 id 取清单。
func (r *Registry) Get(id string) (*Manifest, bool) {
	m, ok := r.byID[id]
	return m, ok
}

// List 返回全部清单，按 id 排序。
func (r *Registry) List() []*Manifest {
	out := make([]*Manifest, 0, len(r.byID))
	for _, m := range r.byID {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListByKind 按 kind 过滤。
func (r *Registry) ListByKind(kind string) []*Manifest {
	var out []*Manifest
	for _, m := range r.List() {
		if m.Kind == kind {
			out = append(out, m)
		}
	}
	return out
}
