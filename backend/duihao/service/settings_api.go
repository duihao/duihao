// M8：可视化设置服务 API（schema 视图 / 应用 / 恢复默认 / 配置文件原文读写）。
package service

import (
	"github.com/ys-ll/uniterm/backend/duihao/instance"
)

// GetSettingsSchema 设置对话框数据：分组字段定义 + 当前值 + 生效方式。
func (s *PluginCenterService) GetSettingsSchema(id string) (*instance.SettingsView, error) {
	if s.mgr == nil {
		return nil, errNotReady()
	}
	return s.mgr.GetSettingsView(id)
}

// ApplySettings 保存并应用设置（端口字段走 SetPort 校验链路；
// 运行中按清单 Restart 决定重启或 reload）。
func (s *PluginCenterService) ApplySettings(id string, values map[string]string) error {
	if s.mgr == nil {
		return errNotReady()
	}
	return s.mgr.ApplySettings(id, values)
}

// ResetSettings 恢复清单默认值（端口不动）。
func (s *PluginCenterService) ResetSettings(id string) error {
	if s.mgr == nil {
		return errNotReady()
	}
	return s.mgr.ResetSettings(id)
}

// ReadInstanceFile 读配置文件原文（「配置文件」Tab）。
func (s *PluginCenterService) ReadInstanceFile(id, relPath string) (string, error) {
	if s.mgr == nil {
		return "", errNotReady()
	}
	return s.mgr.ReadInstanceFile(id, relPath)
}

// SaveInstanceFile 保存配置文件原文。运行中自动 reload（nginx 类立即生效；
// mysql 类改完需重启，由设置面板 Restart 标记提示）。
func (s *PluginCenterService) SaveInstanceFile(id, relPath, content string) error {
	if s.mgr == nil {
		return errNotReady()
	}
	if err := s.mgr.WriteInstanceFile(id, relPath, content); err != nil {
		return err
	}
	return s.mgr.Reload(id) // 未运行 / 无 reload 声明时内部 no-op
}
