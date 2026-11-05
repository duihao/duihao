// 声明式设置（M8）：实例级设置值持久化 + schema 值合并。
//
// 数据流：清单 settings.groups 声明字段（schema）→ 用户在设置对话框改值 →
// 写 instances/<id>/settings.json（override）→ renderConfigOnly 重渲染时
// {set:key} token 取 override（缺省用字段 Default）→ 配置文件生效。
// 端口类字段（PortRole）不进 settings.json：走 SetPort 校验链路。
package instance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/duihao/duihao/backend/duihao/pluginc"
	"github.com/duihao/duihao/backend/duihao/processd"
)

func settingsPath(id string) string {
	return filepath.Join(instanceDir(id), "settings.json")
}

// loadOverride 读用户设置覆盖表。文件不存在 / 损坏 → 空 map（用清单默认值）。
func loadOverride(id string) map[string]string {
	data, err := os.ReadFile(settingsPath(id))
	if err != nil {
		return map[string]string{}
	}
	out := map[string]string{}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]string{}
	}
	return out
}

// saveOverride 原子落盘用户设置。
func saveOverride(id string, vals map[string]string) error {
	data, err := json.Marshal(vals)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(instanceDir(id), 0o755); err != nil {
		return err
	}
	tmp := settingsPath(id) + ".tmp"
	// 0600：设置里可能含 FTP/Redis 密码，不给其他用户读。
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, settingsPath(id))
}

// removeOverride 删除覆盖（设置默认）：回落清单 Default。
func removeOverride(id string) error {
	err := os.Remove(settingsPath(id))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// settingValues 合并出「配置模板渲染」用的键值表：
// 默认值（清单 Default）→ 实例端口（PortRole 字段取 rec.Ports 实际值）→ 用户 override 覆盖。
// keySchema：key → 字段（用于 PortRole 判断）。
func settingValues(man *pluginc.Manifest, ports map[string]int, override map[string]string) map[string]string {
	if man.Settings == nil {
		return map[string]string{}
	}
	out := map[string]string{}
	for _, g := range man.Settings.Groups {
		for _, f := range g.Fields {
			if f.PortRole != "" {
				if p, ok := ports[f.PortRole]; ok && p != 0 {
					out[f.Key] = fmt.Sprint(p)
				}
				continue // 端口由端口池管理，不进 override
			}
			out[f.Key] = f.Default
		}
	}
	for k, v := range override {
		// override 只允许覆盖 schema 里声明过的 key（防脏数据混入配置）。
		if f, _ := schemaField(man, k); f != nil {
			out[k] = v
		}
	}
	return out
}

// schemaField 按 key 找字段定义。
func schemaField(man *pluginc.Manifest, key string) (*pluginc.SettingField, *pluginc.SettingGroup) {
	if man.Settings == nil {
		return nil, nil
	}
	for i := range man.Settings.Groups {
		for j := range man.Settings.Groups[i].Fields {
			if man.Settings.Groups[i].Fields[j].Key == key {
				return &man.Settings.Groups[i].Fields[j], &man.Settings.Groups[i]
			}
		}
	}
	return nil, nil
}

// validateValues 校验用户提交：key 必须在 schema 内；number 需可解析且在 min/max 内；
// switch 归一为 "0"/"1"。返回归一后的值表（不含端口字段——端口走 SetPort）。
func validateValues(man *pluginc.Manifest, vals map[string]string) (map[string]string, []string, error) {
	out := map[string]string{}
	var portFields []string
	for k, raw := range vals {
		f, _ := schemaField(man, k)
		if f == nil {
			return nil, nil, fmt.Errorf("未知设置项: %s", k)
		}
		v := strings.TrimSpace(raw)
		switch f.Type {
		case "switch":
			if v == "true" || v == "on" || v == "1" {
				v = "1"
			} else {
				v = "0"
			}
		case "number":
			var n float64
			if _, err := fmt.Sscanf(v, "%g", &n); err != nil {
				return nil, nil, fmt.Errorf("%s 需要数字，得到: %q", f.Label, raw)
			}
			if f.Min != nil && n < *f.Min {
				return nil, nil, fmt.Errorf("%s 不能小于 %g", f.Label, *f.Min)
			}
			if f.Max != nil && n > *f.Max {
				return nil, nil, fmt.Errorf("%s 不能大于 %g", f.Label, *f.Max)
			}
		case "select":
			okOpt := false
			for _, o := range f.Options {
				if o == v {
					okOpt = true
					break
				}
			}
			if !okOpt {
				return nil, nil, fmt.Errorf("%s 的值 %q 不在可选项中", f.Label, v)
			}
		}
		if f.PortRole != "" {
			portFields = append(portFields, k)
			continue // 端口不进 override，由 ApplySettings 走 SetPort
		}
		out[k] = v
	}
	return out, portFields, nil
}

// EffectiveSettings 实例的生效设置值（清单默认 + 端口实际值 + 用户 override）。
//
// 给服务层用：数据库/Redis 管理要知道端口、requirepass 之类，但不需要（也不该）
// 知道这些值是怎么合并出来的，更不该自己去读 settings.json。
func (m *Manager) EffectiveSettings(id string) (map[string]string, error) {
	rec := m.reg.Get(id)
	if rec == nil {
		return nil, fmt.Errorf("实例不存在: %s", id)
	}
	man, ok := m.plugins.Get(rec.Plugin)
	if !ok {
		return nil, fmt.Errorf("插件清单不存在: %s", rec.Plugin)
	}
	return settingValues(man, rec.Ports, loadOverride(id)), nil
}

// RuntimeBinDir 运行时 bin 目录（清单 bin 的目录部分）。
// mysqldump.exe / mysql.exe 这类配套工具就在同一个目录里。
func (m *Manager) RuntimeBinDir(id string) (string, error) {
	rec := m.reg.Get(id)
	if rec == nil {
		return "", fmt.Errorf("实例不存在: %s", id)
	}
	man, ok := m.plugins.Get(rec.Plugin)
	if !ok {
		return "", fmt.Errorf("插件清单不存在: %s", rec.Plugin)
	}
	rel, err := man.BinaryPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(rec.RuntimeDir, filepath.FromSlash(filepath.Dir(rel))), nil
}

// ─── Manager 设置 API（服务层直接调用） ───

// SettingFieldView 字段 + 当前值。
type SettingFieldView struct {
	pluginc.SettingField
	Current string `json:"current"`
}

// SettingGroupView 分组 + 字段。
type SettingGroupView struct {
	Title  string             `json:"title"`
	Fields []SettingFieldView `json:"fields"`
}

// SettingsView 设置对话框完整数据。
type SettingsView struct {
	InstanceID string              `json:"instanceId"`
	Plugin     string              `json:"plugin"`
	Groups     []SettingGroupView  `json:"groups"`
	ConfigFile string              `json:"configFile,omitempty"`
	Restart    bool                `json:"restart"`
}

// GetSettingsView 组装设置视图：schema + 当前值（override 优先，端口字段=实际端口）。
func (m *Manager) GetSettingsView(id string) (*SettingsView, error) {
	rec := m.reg.Get(id)
	if rec == nil {
		return nil, fmt.Errorf("实例不存在: %s", id)
	}
	man, ok := m.plugins.Get(rec.Plugin)
	if !ok {
		return nil, fmt.Errorf("插件清单不存在: %s", rec.Plugin)
	}
	view := &SettingsView{InstanceID: id, Plugin: rec.Plugin, Restart: false}
	if man.Settings == nil {
		return view, nil
	}
	view.Restart = man.Settings.Restart
	view.ConfigFile = man.Settings.ConfigFile
	override := loadOverride(id)
	for _, g := range man.Settings.Groups {
		gv := SettingGroupView{Title: g.Title}
		for _, f := range g.Fields {
			cur := f.Default
			if f.PortRole != "" {
				if p, ok := rec.Ports[f.PortRole]; ok {
					cur = fmt.Sprint(p)
				}
			} else if v, ok := override[f.Key]; ok {
				cur = v
			}
			gv.Fields = append(gv.Fields, SettingFieldView{SettingField: f, Current: cur})
		}
		view.Groups = append(view.Groups, gv)
	}
	return view, nil
}

// ApplySettings 保存并应用用户设置。
// 端口字段走 SetPort（占用/站点冲突校验 + refreshSpec + 失败回滚）；
// 其余写 settings.json → 重渲染配置 → 运行中按清单 Restart 决定 重启 / reload。
func (m *Manager) ApplySettings(id string, vals map[string]string) error {
	rec := m.reg.Get(id)
	if rec == nil {
		return fmt.Errorf("实例不存在: %s", id)
	}
	man, ok := m.plugins.Get(rec.Plugin)
	if !ok {
		return fmt.Errorf("插件清单不存在: %s", rec.Plugin)
	}
	if man.Settings == nil {
		return fmt.Errorf("插件 %s 未声明可视化设置", rec.Plugin)
	}

	norm, portKeys, err := validateValues(man, vals)
	if err != nil {
		return err
	}

	wasRunning := m.Status(id) == processd.StateRunning

	// 1) 端口字段逐个走 SetPort（已有完整校验/回滚链路）。
	for _, k := range portKeys {
		f, _ := schemaField(man, k)
		var p int
		if _, err := fmt.Sscanf(norm2(vals[k]), "%d", &p); err != nil || p < 1 || p > 65535 {
			return fmt.Errorf("%s 端口非法: %q", f.Label, vals[k])
		}
		if rec.Ports[f.PortRole] == p {
			continue // 未变
		}
		if err := m.SetPort(id, f.PortRole, p); err != nil {
			return fmt.Errorf("%s: %w", f.Label, err)
		}
	}

	// 2) 其余写 override 并重渲染。
	if err := saveOverride(id, norm); err != nil {
		return err
	}
	if err := m.renderConfigOnly(id); err != nil {
		_ = removeOverride(id)
		return fmt.Errorf("应用设置失败（配置渲染错误，已还原）: %w", err)
	}

	// 3) 生效方式：mysql 类必须重启；nginx 类 reload 足够。
	if wasRunning {
		if man.Settings.Restart {
			if err := m.restartInstance(id, rec.Ports); err != nil {
				return err
			}
		} else {
			if err := m.Reload(id); err != nil {
				return fmt.Errorf("设置已保存，但重载失败: %w", err)
			}
		}
	}
	if m.auditLog != nil {
		changed := make([]string, 0, len(norm)+len(portKeys))
		for k, v := range norm {
			if k != "password" && !strings.Contains(k, "pass") {
				changed = append(changed, k+"="+v)
			} else {
				changed = append(changed, k+"=***")
			}
		}
		for _, k := range portKeys {
			changed = append(changed, k+"="+norm2(vals[k]))
		}
		m.auditLog.Ok("instance", "set-settings", id, map[string]any{"changes": changed})
	}
	return nil
}

// ApplySettingsDiff 只改指定的几个键，其余 keep 当前生效值。
//
// ApplySettings 的语义是「整表覆盖」（saveOverride 直接写 norm），只传一两个键会
// 把别的 override 抹掉；首屏「一键开启局域网」这类只动 1-2 个键的快捷入口走这里，
// 先取生效值合并再交给 ApplySettings，端口字段值未变会自动跳过。
func (m *Manager) ApplySettingsDiff(id string, kv map[string]string) error {
	eff, err := m.EffectiveSettings(id)
	if err != nil {
		return err
	}
	for k, v := range kv {
		eff[k] = v
	}
	return m.ApplySettings(id, eff)
}

// ResetSettings 恢复清单默认值：删 override → 重渲染 → 同 ApplySettings 的生效方式。
// 端口不动（端口由端口池/站点占用管理，不在「默认值」语义内）。
func (m *Manager) ResetSettings(id string) error {
	rec := m.reg.Get(id)
	if rec == nil {
		return fmt.Errorf("实例不存在: %s", id)
	}
	man, ok := m.plugins.Get(rec.Plugin)
	if !ok {
		return fmt.Errorf("插件清单不存在: %s", rec.Plugin)
	}
	wasRunning := m.Status(id) == processd.StateRunning
	if err := removeOverride(id); err != nil {
		return err
	}
	if err := m.renderConfigOnly(id); err != nil {
		return fmt.Errorf("恢复默认失败（配置渲染错误）: %w", err)
	}
	if wasRunning {
		if man.Settings.Restart {
			if err := m.restartInstance(id, rec.Ports); err != nil {
				return err
			}
		} else if err := m.Reload(id); err != nil {
			return fmt.Errorf("默认值已保存，但重载失败: %w", err)
		}
	}
	if m.auditLog != nil {
		m.auditLog.Ok("instance", "reset-settings", id, nil)
	}
	return nil
}

// norm2 数字串去空白（复用场景小工具）。
func norm2(s string) string { return strings.TrimSpace(s) }

// restartInstance 停止 → 等端口全部释放 → 再启动。
// 竞态：mysqld 收到停止信号后端口释放有延迟（Windows TIME_WAIT/关停节奏），
// 立即 Start 会撞上「端口被占用（未知进程）」——必须等 ProbeFree 通过。
func (m *Manager) restartInstance(id string, ports map[string]int) error {
	if err := m.Stop(id); err != nil {
		return fmt.Errorf("设置已保存，但停止实例失败: %w", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for _, p := range ports {
		for p > 0 && !processd.ProbeFree(p) {
			if time.Now().After(deadline) {
				return fmt.Errorf("设置已保存，但端口 %d 释放超时，请手动启动实例", p)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	if err := m.Start(id); err != nil {
		return fmt.Errorf("设置已保存，但重启实例失败（可手动启动）: %w", err)
	}
	return nil
}
