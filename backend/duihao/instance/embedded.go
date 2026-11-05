// embedded 服务支持：编译进本体的 Go 实现（如 FTPServer）也走「实例」这一层。
//
// 为什么还要实例：端口分配、启停状态、可视化设置、审计都挂在实例上。
// 与 process 类唯一的差别是「谁来跑」——process 委托 processd 起子进程，
// embedded 在本进程内起 goroutine + net.Listener，不落任何子进程。
//
// 铁律 1 不变：本层仍不知道"站点"；铁律 3 不变：清单只声明数据，
// 真正的服务实现由 main 注入的工厂构建（清单不能携带逻辑）。
package instance

import (
	"fmt"
	"path/filepath"

	duihao "github.com/duihao/duihao/backend/duihao"
	"github.com/duihao/duihao/backend/duihao/pluginc"
)

// embeddedLogPath 内置服务日志与 process 类同路径（UI「日志」按钮不用分叉）。
func embeddedLogPath(id string) string {
	return filepath.Join(duihao.DirProcessLogs(), id+".log")
}

// EmbeddedConfig 构建内置服务的入参（实例维度）。
type EmbeddedConfig struct {
	InstanceID string
	Dir        string            // 实例数据目录 instances/<id>
	Ports      map[string]int    // 角色 → 端口
	Values     map[string]string // 声明式设置合并值（默认 + override）
	LogPath    string            // 日志文件（UI「日志」按钮读同一份）
}

// EmbeddedServer 内置服务的运行时句柄。
type EmbeddedServer interface {
	Start() error
	Stop() error
	Running() bool
}

// EmbeddedFactory 由 main 注入：同一插件可有多实例，工厂必须每次返回新对象。
type EmbeddedFactory func(cfg EmbeddedConfig) (EmbeddedServer, error)

// RegisterEmbedded 注册某插件 id 的内置实现（main 启动时调用）。
func (m *Manager) RegisterEmbedded(pluginID string, f EmbeddedFactory) {
	m.embeddedMu.Lock()
	defer m.embeddedMu.Unlock()
	if m.embeddedFn == nil {
		m.embeddedFn = map[string]EmbeddedFactory{}
	}
	m.embeddedFn[pluginID] = f
}

// isEmbedded 该实例是否由内置实现托管。
func (m *Manager) isEmbedded(pluginID string) bool {
	m.embeddedMu.Lock()
	defer m.embeddedMu.Unlock()
	_, ok := m.embeddedFn[pluginID]
	return ok
}

// embeddedFactory 取工厂（未注册返回错误：清单声明了 embedded 但本体没有实现）。
func (m *Manager) embeddedFactory(pluginID string) (EmbeddedFactory, error) {
	m.embeddedMu.Lock()
	defer m.embeddedMu.Unlock()
	f, ok := m.embeddedFn[pluginID]
	if !ok {
		return nil, fmt.Errorf("插件 %s 是内置类型，但当前版本未包含该实现", pluginID)
	}
	return f, nil
}

func (m *Manager) embeddedServer(id string) EmbeddedServer {
	m.embeddedMu.Lock()
	defer m.embeddedMu.Unlock()
	return m.embeddedServers[id]
}

func (m *Manager) setEmbeddedServer(id string, s EmbeddedServer) {
	m.embeddedMu.Lock()
	defer m.embeddedMu.Unlock()
	if m.embeddedServers == nil {
		m.embeddedServers = map[string]EmbeddedServer{}
	}
	if s == nil {
		delete(m.embeddedServers, id)
		return
	}
	m.embeddedServers[id] = s
}

// startEmbedded 按当前记录 + 设置构建并启动（已存在旧句柄先停掉，避免端口占用）。
func (m *Manager) startEmbedded(rec *Record, man *pluginc.Manifest) error {
	f, err := m.embeddedFactory(rec.Plugin)
	if err != nil {
		return err
	}
	if old := m.embeddedServer(rec.ID); old != nil {
		_ = old.Stop()
		m.setEmbeddedServer(rec.ID, nil)
	}
	srv, err := f(EmbeddedConfig{
		InstanceID: rec.ID,
		Dir:        instanceDir(rec.ID),
		Ports:      rec.Ports,
		Values:     settingValues(man, rec.Ports, loadOverride(rec.ID)),
		LogPath:    embeddedLogPath(rec.ID),
	})
	if err != nil {
		return err
	}
	if err := srv.Start(); err != nil {
		return err
	}
	m.setEmbeddedServer(rec.ID, srv)
	return nil
}

// stopEmbedded 停止并丢弃句柄（找不到句柄视为已停）。
func (m *Manager) stopEmbedded(id string) error {
	srv := m.embeddedServer(id)
	if srv == nil {
		return nil
	}
	err := srv.Stop()
	m.setEmbeddedServer(id, nil)
	return err
}

// embeddedRunning 内置服务是否在跑。
func (m *Manager) embeddedRunning(id string) bool {
	srv := m.embeddedServer(id)
	return srv != nil && srv.Running()
}

// embeddedState 供 Status 用：活句柄优先，否则回落记录态。
func (m *Manager) embeddedState(rec *Record) string {
	if m.embeddedRunning(rec.ID) {
		return "running"
	}
	switch rec.State {
	case "running", "starting", "stopping": // 上次宿主退出遗留的「运行中」
		return "stopped"
	}
	if rec.State == "" {
		return "installed"
	}
	return rec.State
}
