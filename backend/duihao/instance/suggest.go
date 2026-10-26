package instance

import (
	"fmt"

	"github.com/ys-ll/uniterm/backend/duihao/pluginc"
	"github.com/ys-ll/uniterm/backend/duihao/processd"
)

// SuggestPort 给实例的某个端口角色找一个可用端口（池内 + 未被其他实例占用）。
//
// 用于「端口被别的软件占了」时的一键换端：返回的端口拿 SetPort 一设就能启动。
// 跳过本实例已用的端口（含该角色当前端口），避免建议出「还是原来那个」。
func (m *Manager) SuggestPort(id, role string) (int, error) {
	rec := m.reg.Get(id)
	if rec == nil {
		return 0, fmt.Errorf("实例不存在: %s", id)
	}
	man, ok := m.plugins.Get(rec.Plugin)
	if !ok {
		return 0, fmt.Errorf("插件清单不存在: %s", rec.Plugin)
	}
	var roleDef *pluginc.PortRole
	for i := range man.Ports {
		if man.Ports[i].Name == role {
			roleDef = &man.Ports[i]
			break
		}
	}
	if roleDef == nil {
		return 0, fmt.Errorf("插件 %s 没有端口角色 %q", rec.Plugin, role)
	}
	skip := map[int]bool{}
	for _, other := range m.reg.List() {
		for _, p := range other.Ports {
			if p > 0 {
				skip[p] = true
			}
		}
	}
	return processd.AssignPortFree(roleDef.Pool, skip, m.settings)
}
