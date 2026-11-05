// 实例管理器：装配（Create）与生命周期（Start/Stop/Remove）。
package instance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	duihao "github.com/duihao/duihao/backend/duihao"
	"github.com/duihao/duihao/backend/duihao/audit"
	"github.com/duihao/duihao/backend/duihao/clirun"
	"github.com/duihao/duihao/backend/duihao/hostreq"
	"github.com/duihao/duihao/backend/duihao/pluginc"
	"github.com/duihao/duihao/backend/duihao/processd"
	"github.com/duihao/duihao/backend/duihao/runtimepkg"
)

// Manager 实例管理器。
type Manager struct {
	mu       sync.Mutex
	reg      *Registry
	plugins  *pluginc.Registry
	proc     *processd.Manager
	settings *duihao.Settings
	auditLog *audit.Logger
	// portGuard 端口占用外部检查（由 main 注入站点层）：true 表示该端口已被
	// 站点监听，实例不应抢占。保持 L2 不认识站点的边界——只回一个布尔。
	portGuard func(instanceID string, port int) bool

	// embedded 内置服务（编译进本体的实现，如 FTPServer）：工厂表 + 运行中句柄。
	// 与 processd 平行：一个托管子进程，一个托管本进程内的 listener。
	embeddedMu      sync.Mutex
	embeddedFn      map[string]EmbeddedFactory
	embeddedServers map[string]EmbeddedServer
}

// SetPortGuard 注入端口占用检查（站点端口）。必须在 Start 前设置。
func (m *Manager) SetPortGuard(fn func(instanceID string, port int) bool) { m.portGuard = fn }

// NewManager 构建实例管理器（进程层委托 processd）。
func NewManager(settings *duihao.Settings, auditLog *audit.Logger) (*Manager, error) {
	reg, err := LoadRegistry()
	if err != nil {
		return nil, err
	}
	plugins, err := pluginc.Load()
	if err != nil {
		return nil, err
	}
	proc, err := processd.NewManager(settings, auditLog)
	if err != nil {
		return nil, err
	}
	return &Manager{
		reg:      reg,
		plugins:  plugins,
		proc:     proc,
		settings: settings,
		auditLog: auditLog,
	}, nil
}

// Plugins 暴露清单注册表（服务层列表用）。
func (m *Manager) Plugins() *pluginc.Registry { return m.plugins }

// ManifestWarnings 清单加载期告警。
func (m *Manager) ManifestWarnings() []string { return m.plugins.Warns() }

// InstalledVersions 探测某插件已安装的版本（runtimes/<id>/<ver> 存在）。
func (m *Manager) InstalledVersions(pluginID string) []string {
	var out []string
	base := duihao.DirRuntimes()
	vers, err := os.ReadDir(filepath.Join(base, pluginID))
	if err != nil {
		return nil
	}
	for _, v := range vers {
		if v.IsDir() {
			out = append(out, v.Name())
		}
	}
	sort.Strings(out)
	return out
}

// Create 装配新实例：requires.host → resolve runtime → 分配端口 →
// lifecycle.install → 注册（state=installed）。
func (m *Manager) Create(pluginID, version, name string, confirmed map[string]bool) (*Record, error) {
	man, ok := m.plugins.Get(pluginID)
	if !ok {
		return nil, fmt.Errorf("未知插件: %s", pluginID)
	}
	if man.Kind != pluginc.KindProcess && man.Kind != pluginc.KindEmbedded {
		return nil, fmt.Errorf("插件 %s 是 %s 类型，无实例（cli 用 InvokeCli）", pluginID, man.Kind)
	}
	// embedded：编译进本体的服务（FTPServer 等），没有运行时版本可装，
	// 但仍需要实例承载端口 / 启停 / 设置 / 审计。
	embedded := man.Kind == pluginc.KindEmbedded
	var ve *pluginc.VersionEntry
	if !embedded {
		v0, err := man.Version(version)
		if err != nil {
			return nil, err
		}
		if _, err := v0.Asset(); err != nil {
			return nil, err
		}
		ve = v0
	}

	// 实例 id：插件 id + 序号，避免用户输入。
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.uniqueIDLocked(pluginID)

	rec := &Record{
		ID: id, Plugin: pluginID, Version: versionOrBuiltin(ve, embedded),
		Name:       firstNonEmpty(name, man.Name),
		Ports:      map[string]int{},
		WorkerPorts: map[string][]int{},
		Workers:    man.Workers,
		State:      processd.StateInstalling,
	}

	// 1) requires.host 前置检查。
	if man.Requires != nil && len(man.Requires.Host) > 0 {
		checks := make([]string, len(man.Requires.Host))
		msgs := map[string]string{}
		for i, h := range man.Requires.Host {
			checks[i] = h.Check
			msgs[h.Check] = h.Msg
		}
		dataDir := instanceDir(id)
		for _, f := range hostreq.Check(checks, dataDir) {
			msg := msgs[f.Check]
			if msg == "" {
				msg = f.Msg
			}
			return nil, fmt.Errorf("前置条件不满足 %s: %s", f.Check, msg)
		}
	}

	// 2) 取包（五级链，runtimepkg 唯一入口）。内置服务无包可装，跳过。
	binRel := ""
	resDir := ""
	if !embedded {
		binRel0, _ := man.BinaryPath()
		asset, _ := ve.Asset()
		res, err := runtimepkg.Resolve(context.Background(), runtimepkg.VersionSpec{
			PluginID: pluginID,
			Version:  ve.Version,
			Platform: pluginc.PlatformTarget,
			Source:   ve.Source,
			URL:      asset.URL,
			URLs:     asset.URLs,
			SHA256:   asset.SHA256,
			Size:     asset.Size,
			Bin:      binRel0,
		}, m.auditLog)
		if err != nil {
			return nil, fmt.Errorf("取包失败: %w", err)
		}
		resDir = res.Dir
		binRel, err = man.BinaryPath()
		if err != nil {
			return nil, err
		}
	}
	rec.RuntimeDir = resDir

	// 3) 端口分配（bind 探测 + 落盘在 processd.AssignPort 内）。
	n := man.Workers
	if n <= 0 {
		n = 1
	}
	v := Vars{
		ID: id, Dir: instanceDir(id), RuntimeDir: resDir,
		// embedded 无版本条目（ve==nil），必须走 versionOrBuiltin（记 builtin）。
		Version: versionOrBuiltin(ve, embedded),
		Bin:     filepath.Join(resDir, filepath.FromSlash(binRel)),
	}
	// 已被其他实例记录占用的端口（进程可能都还没起，不能只看 bind）。
	// 单运行插件豁免同插件实例 —— 第二个 MySQL 也默认拿首选 3306，
	// 互斥由 Start 的「启动前停掉同插件兄弟」保证（phpStudy 模式）。
	single := ""
	if !man.MultiRun {
		single = man.ID
	}
	used := m.assignedPorts("", single)
	for _, role := range man.Ports {
		var ports []int
		for i := 0; i < n; i++ {
			// 首选端口（如 nginx 的 80）只在第 1 个 worker 上尝试，其余走池。
			want := 0
			if i == 0 {
				want = role.Default
			}
			p, err := processd.AssignPortPreferredFree(role.Pool, want, used, m.settings)
			if err != nil {
				return nil, fmt.Errorf("分配 %s 端口失败: %w", role.Pool, err)
			}
			used[p] = true // 同一实例内多个 worker 也不能撞
			ports = append(ports, p)
		}
		rec.Ports[role.Name] = ports[0]
		if n > 1 {
			rec.WorkerPorts[role.Name] = ports
		}
		v.Ports = rec.Ports
	}

	// 4) lifecycle.install 白名单动作。
	if man.Lifecycle != nil && len(man.Lifecycle.Install) > 0 {
		if err := os.MkdirAll(v.Dir, 0o755); err != nil {
			return nil, err
		}
		v.Set = settingValues(man, rec.Ports, nil) // 首装：清单默认值 + 分配到的端口
		if err := RunActions(man.Lifecycle.Install, v, confirmed, m.auditLog); err != nil {
			if _, isConfirm := asConfirmNeeded(err); isConfirm {
				// 不注册记录，把确认需求抛给调用方，确认后重新 Create。
				return nil, err
			}
			return nil, err
		}
	}

	// 5) 注册：process 类落进程层规格；embedded 类由本体托管，只需实例记录。
	if !embedded {
		spec := m.buildProcessSpec(rec, man, v)
		if err := m.proc.Add(spec); err != nil {
			return nil, err
		}
	}

	rec.State = processd.StateInstalled
	if err := m.reg.Add(rec); err != nil {
		if !embedded {
			_ = m.proc.Remove(id)
		}
		return nil, err
	}
	if m.auditLog != nil {
		m.auditLog.Ok("instance", "create", id, map[string]any{
			"plugin": pluginID, "version": rec.Version, "ports": rec.Ports,
			"source": map[bool]string{true: "embedded", false: "runtime"}[embedded],
		})
	}
	return rec, nil
}

// versionOrBuiltin embedded 实例没有可装版本，统一记 builtin（UI 显示「内置」）。
func versionOrBuiltin(ve *pluginc.VersionEntry, embedded bool) string {
	if embedded || ve == nil {
		return "builtin"
	}
	return ve.Version
}

// assignedPorts 收集其他实例记录占用的端口（主端口 + worker 端口；进程可能都
// 还没起，不能只看 bind）。exceptID 跳过自己。
// singlePlugin 非空时跳过该插件的其他实例 —— 单运行（multiRun=false）插件的
// 不同实例允许共用端口：同一时刻只有一个在跑（phpStudy 模式，两个 MySQL
// 实例都默认 3306），真正互斥由 Start 的「启动前停掉同插件兄弟」保证。
func (m *Manager) assignedPorts(exceptID string, singlePlugin string) map[int]bool {
	used := map[int]bool{}
	for _, r := range m.reg.List() {
		if r.ID == exceptID {
			continue
		}
		if singlePlugin != "" && r.Plugin == singlePlugin {
			continue
		}
		for _, p := range r.Ports {
			if p > 0 {
				used[p] = true
			}
		}
		for _, ps := range r.WorkerPorts {
			for _, p := range ps {
				if p > 0 {
					used[p] = true
				}
			}
		}
	}
	return used
}

// healPortConflicts 修老记录里的端口撞车：早期创建实例只探测 bind，两个实例都还没
// 起进程时会被分到同一个端口（PHP 8.2 / 8.3 都拿到 9000），启动第二个才失败。
// 这里把冲突端口换成池里的空闲端口并落盘；changed=true 时调用方必须重渲染配置
// （ensureConfig force），否则配置文件里的旧端口与健康检查、进程参数脱节。
// 冲突判定：① 其他实例记录占用（单运行插件的同插件实例除外——允许共用端口）；
// ② 外部进程正在监听（phpstudy / 别的软件占了端口，dial 探测）。返回是否改动过。
func (m *Manager) healPortConflicts(rec *Record, man *pluginc.Manifest) (bool, error) {
	single := ""
	if !man.MultiRun {
		single = rec.Plugin
	}
	used := m.assignedPorts(rec.ID, single)
	changed := false
	for _, role := range man.Ports {
		slots := []int{rec.Ports[role.Name]}
		hasWorkers := false
		if ps, ok := rec.WorkerPorts[role.Name]; ok && len(ps) > 0 {
			slots, hasWorkers = append([]int(nil), ps...), true
		}
		for i, cur := range slots {
			if cur > 0 && !used[cur] && processd.ProbeFree(cur) {
				used[cur] = true
				continue
			}
			np, err := processd.AssignPortFree(role.Pool, used, m.settings)
			if err != nil {
				return changed, fmt.Errorf("实例 %s 的 %s 端口与其他实例冲突，且 %w", rec.ID, role.Name, err)
			}
			slots[i] = np
			used[np] = true
			changed = true
		}
		if len(slots) == 0 {
			continue
		}
		if hasWorkers {
			rec.WorkerPorts[role.Name] = slots
		}
		rec.Ports[role.Name] = slots[0]
	}
	if changed {
		_ = m.reg.Update(rec)
	}
	return changed, nil
}

// uniqueIDLocked 生成 <plugin>-N 形式的唯一实例 id。
func (m *Manager) uniqueIDLocked(pluginID string) string {
	for i := 1; ; i++ {
		cand := fmt.Sprintf("%s-%d", pluginID, i)
		if m.reg.Get(cand) == nil {
			return cand
		}
	}
}

// buildProcessSpec 把清单 start/stop/health + workers 展开为进程规格。
func (m *Manager) buildProcessSpec(rec *Record, man *pluginc.Manifest, v Vars) processd.InstanceSpec {
	n := man.Workers
	if n <= 0 {
		n = 1
	}
	spec := processd.InstanceSpec{
		ID: rec.ID, Plugin: rec.Plugin, Version: rec.Version,
		RuntimeDir: rec.RuntimeDir, Ports: rec.Ports,
	}
	workerPortsFor := func(role string, i int) int {
		if n > 1 {
			if ps, ok := rec.WorkerPorts[role]; ok && i < len(ps) {
				return ps[i]
			}
		}
		return rec.Ports[role]
	}
	for i := 0; i < n; i++ {
		// 本副本的 token 环境：{port:role} 换成本副本端口。
		pv := v
		pv.Ports = map[string]int{}
		for _, role := range man.Ports {
			pv.Ports[role.Name] = workerPortsFor(role.Name, i)
		}
		name := rec.Plugin
		if n > 1 {
			name = fmt.Sprintf("%s-worker-%d", rec.Plugin, i)
		}
		ps := processd.ProcessSpec{
			Name: name, Bin: v.Bin,
			Dir: applyTokens(man.Start.Dir, pv),
		}
		for _, a := range man.Start.Args {
			ps.Args = append(ps.Args, applyTokens(a, pv))
		}
		for k, val := range man.Start.Env {
			ps.Env = map[string]string{k: applyTokens(val, pv)}
		}
		if man.Health != nil {
			ps.Health = processd.HealthSpec{
				Type: man.Health.Type,
				Target: applyTokens(man.Health.Target, pv),
				Cmd:    resolveRuntimePath(applyTokens(man.Health.Cmd, pv), v.RuntimeDir),
				Args:   man.Health.Args,
				Interval: man.Health.Interval,
			}
		}
		if man.Stop != nil {
			ps.Stop = processd.StopSpec{
				Type: man.Stop.Type,
				Cmd:  resolveRuntimePath(applyTokens(man.Stop.Cmd, pv), v.RuntimeDir),
				Args: func() []string {
					var out []string
					for _, a := range man.Stop.Args {
						out = append(out, applyTokens(a, pv))
					}
					return out
				}(),
				Graceful: man.Stop.Graceful,
			}
		}
		spec.Processes = append(spec.Processes, ps)
	}
	return spec
}

// resolveRuntimePath 相对命令解析到运行时目录（绝对路径或含 {token} 的先替换过）。
func resolveRuntimePath(cmd, runtimeDir string) string {
	if cmd == "" {
		return ""
	}
	if filepath.IsAbs(cmd) {
		return cmd
	}
	return filepath.Join(runtimeDir, filepath.FromSlash(cmd))
}

// Start 启动实例（委托 processd：Job Object 守护 + 健康检查 + 退避）。
//
// 幂等：已在运行 / 正在启动时直接成功。站点层是「确保在运行」语义 ——
// 两个站点共用同一 PHP 实例、同时点启动时，后到者不该收到「已在运行」报错。
func (m *Manager) Start(id string) error {
	rec := m.reg.Get(id)
	if rec == nil {
		return fmt.Errorf("实例不存在: %s", id)
	}
	switch {
	case m.isEmbedded(rec.Plugin):
		if m.embeddedRunning(id) {
			return nil
		}
	default:
		switch m.proc.Status(id) {
		case processd.StateRunning, processd.StateStarting:
			return nil
		}
	}
	// 单版本运行约束（清单 multiRun 缺省 false）：启动前自动停止同插件
	// 其他运行实例（phpStudy 式切换）。PHP 声明 multiRun=true 豁免 ——
	// 多版本并存由站点按需选用，站点启动时拉起对应 php-cgi。
	if man, _ := m.plugins.Get(rec.Plugin); man != nil && !man.MultiRun {
		for _, other := range m.reg.List() {
			if other.Plugin == rec.Plugin && other.ID != id &&
				m.Status(other.ID) == processd.StateRunning {
				if m.auditLog != nil {
					m.auditLog.Ok("instance", "start-stop-sibling", other.ID,
						map[string]any{"for": id})
				}
				_ = m.Stop(other.ID)
			}
		}
	}
	if m.auditLog != nil {
		m.auditLog.Ok("instance", "start", id, nil)
	}
	// 内置服务：本进程内起 listener，不经 processd。
	if m.isEmbedded(rec.Plugin) {
		if m.embeddedRunning(id) {
			return fmt.Errorf("实例 %s 已在运行", id)
		}
		man, ok := m.plugins.Get(rec.Plugin)
		if !ok {
			return fmt.Errorf("插件清单不存在: %s", rec.Plugin)
		}
		rec.State = processd.StateStarting
		_ = m.reg.Update(rec)
		if err := m.startEmbedded(rec, man); err != nil {
			rec.State = processd.StateError
			_ = m.reg.Update(rec)
			if m.auditLog != nil {
				m.auditLog.Fail("instance", "start", id, map[string]any{"error": err.Error()})
			}
			return err
		}
		rec.State = processd.StateRunning
		return m.reg.Update(rec)
	}
	// 配置模板升级过（清单 signature 变了）就先按当前端口重渲染，避免老实例
	// 带着过时配置运行（例如缺 charset utf-8 → 页面乱码）。
	man, ok := m.plugins.Get(rec.Plugin)
	if !ok {
		return fmt.Errorf("插件清单不存在: %s", rec.Plugin)
	}
	// 端口自愈：老记录可能与其他实例撞同一个端口（创建时都还没 bind），
	// 启动第二个才会报「端口被占用」——正是 PHP 多版本并存的拦路虎。
	healed, err := m.healPortConflicts(rec, man)
	if err != nil {
		return err
	}
	if healed && m.auditLog != nil {
		m.auditLog.Ok("instance", "heal-ports", id, map[string]any{"ports": rec.Ports})
	}
	if err := m.ensureConfig(id, healed); err != nil {
		return err
	}
	// 宿主重启后 processd 从 instances.json 恢复的 spec 不含进程展开信息
	//（该文件是 instance 层记录格式，两包字段不同）—— 直接 Start 会展开出
	// 0 个进程然后报「无进程可探活」。非运行态一律按清单重建 spec 再启动。
	switch m.proc.Status(id) {
	case processd.StateRunning, processd.StateStarting, processd.StateStopping:
		// 运行态不动 spec；proc.Start 随后报「已在运行」。
	default:
		if err := m.refreshSpec(rec, man); err != nil {
			return err
		}
	}
	err = m.proc.Start(id)
	if err != nil {
		rec.State = processd.StateError
		_ = m.reg.Update(rec)
		return err
	}
	rec.State = processd.StateRunning
	return m.reg.Update(rec)
}

// Stop 停止实例。
func (m *Manager) Stop(id string) error {
	rec := m.reg.Get(id)
	if rec == nil {
		return fmt.Errorf("实例不存在: %s", id)
	}
	if m.auditLog != nil {
		m.auditLog.Ok("instance", "stop", id, nil)
	}
	var err error
	if m.isEmbedded(rec.Plugin) {
		err = m.stopEmbedded(id)
	} else {
		err = m.proc.Stop(id)
	}
	rec.State = processd.StateStopped
	if uerr := m.reg.Update(rec); uerr != nil && err == nil {
		err = uerr
	}
	return err
}

// Remove 卸载实例：停止 → 删进程注册 → 删实例数据目录（runtime 共享保留）。
func (m *Manager) Remove(id string) error {
	rec := m.reg.Get(id)
	if rec == nil {
		return fmt.Errorf("实例不存在: %s", id)
	}
	_ = m.Stop(id)
	// 内置服务没有进程层记录：停掉句柄即可。
	if !m.isEmbedded(rec.Plugin) {
		if err := m.proc.Remove(id); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(instanceDir(id)); err != nil {
		return err
	}
	if err := m.reg.Remove(id); err != nil {
		return err
	}
	if m.auditLog != nil {
		m.auditLog.Ok("instance", "remove", id, map[string]any{"plugin": rec.Plugin})
	}
	return nil
}

// View 实例视图：注册记录 + 活状态。
type View struct {
	Record
	Live    string `json:"live"`    // processd 实时状态
	Pids    []int  `json:"pids"`    // 存活进程（空=未运行）
	RuntimeSource string `json:"runtimeSource,omitempty"` // bundled|download|mirror|installed
}

// List 全部实例视图。
func (m *Manager) List() []View {
	recs := m.reg.List()
	out := make([]View, 0, len(recs))
	for _, rec := range recs {
		v := View{Record: *rec, Live: m.Status(rec.ID)}
		// 早期版本装配的实例没有落 name，前端下拉/卡片会渲染成 "undefined · nginx-1"。
		// 视图层按清单名回填（只改内存视图，不写回注册表，避免每次 List 都触发落盘）。
		if v.Name == "" {
			v.Name = m.displayName(rec.Plugin)
		}
		if !m.isEmbedded(rec.Plugin) {
			v.Pids = m.proc.PIDs(rec.ID)
		}
		if si, ok := runtimepkg.Installed(rec.Plugin, rec.Version); ok {
			v.RuntimeSource = si.Source
		}
		out = append(out, v)
	}
	return out
}

// displayName 插件展示名（清单 name），清单缺失时退化为插件 id。
func (m *Manager) displayName(plugin string) string {
	if m.plugins != nil {
		if man, ok := m.plugins.Get(plugin); ok && man != nil && man.Name != "" {
			return man.Name
		}
	}
	return plugin
}

// Status 活状态（error 需 UI 标红）。
func (m *Manager) Status(id string) string {
	rec := m.reg.Get(id)
	if rec == nil {
		return processd.StateError
	}
	if m.isEmbedded(rec.Plugin) {
		return m.embeddedState(rec)
	}
	return m.proc.Status(id)
}

// PIDs 存活进程（内置服务无子进程，返回空）。
func (m *Manager) PIDs(id string) []int {
	rec := m.reg.Get(id)
	if rec == nil || m.isEmbedded(rec.Plugin) {
		return nil
	}
	return m.proc.PIDs(id)
}

// GetRecord 读取实例注册记录（不存在返回 nil）。
func (m *Manager) GetRecord(id string) *Record { return m.reg.Get(id) }

// InstanceDirPath 实例数据目录（L3 站点层需要知道片段该写到哪里）。
func (m *Manager) InstanceDirPath(id string) string { return instanceDir(id) }

// ReadInstanceFile 读实例数据目录内的文件（配置文件原文 Tab 用，越界拒绝）。
func (m *Manager) ReadInstanceFile(id, relPath string) (string, error) {
	if m.reg.Get(id) == nil {
		return "", fmt.Errorf("实例不存在: %s", id)
	}
	dir := instanceDir(id)
	dst := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := guardInside(dir, dst); err != nil {
		return "", err
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteInstanceFile 在实例数据目录内写文件（站点片段等，路径越界拒绝 + 审计）。
// 这是 L3 向 L2 的受控写入通道：目标必须在 instances/<id>/ 之内。
func (m *Manager) WriteInstanceFile(id, relPath, content string) error {
	if m.reg.Get(id) == nil {
		return fmt.Errorf("实例不存在: %s", id)
	}
	dir := instanceDir(id)
	dst := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := guardInside(dir, dst); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
		return err
	}
	if m.auditLog != nil {
		m.auditLog.Ok("instance", "write-file", id, map[string]any{"path": relPath})
	}
	return nil
}

// RemoveInstanceFile 删除实例数据目录内的文件（不存在视为成功）。
func (m *Manager) RemoveInstanceFile(id, relPath string) error {
	if m.reg.Get(id) == nil {
		return fmt.Errorf("实例不存在: %s", id)
	}
	dir := instanceDir(id)
	dst := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := guardInside(dir, dst); err != nil {
		return err
	}
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	if m.auditLog != nil {
		m.auditLog.Ok("instance", "remove-file", id, map[string]any{"path": relPath})
	}
	return nil
}

// configActions 只挑「生成配置文件」类动作（write/render）。
// 重渲染时安全：mkdir 幂等但没必要，exec-once 有副作用绝不能再跑。
func configActions(actions []pluginc.Action) []pluginc.Action {
	var out []pluginc.Action
	for _, a := range actions {
		switch strings.ToLower(a.Action) {
		case "write", "render":
			out = append(out, a)
		}
	}
	return out
}

func (m *Manager) varsFor(rec *Record, man *pluginc.Manifest) (Vars, error) {
	binRel, err := man.BinaryPath()
	if err != nil {
		return Vars{}, err
	}
	return Vars{
		ID: rec.ID, Dir: instanceDir(rec.ID), RuntimeDir: rec.RuntimeDir,
		Version: rec.Version,
		Bin:     filepath.Join(rec.RuntimeDir, filepath.FromSlash(binRel)),
		Ports:   rec.Ports,
		Set:     settingValues(man, rec.Ports, loadOverride(rec.ID)),
	}, nil
}

func signaturePath(id string) string {
	return filepath.Join(instanceDir(id), ".conf-signature")
}

// RenderConfig 用当前端口变量重渲染主配置（只跑 write/render 动作），并落签名。
// 实例运行中会自动 reload；停着则等下次启动生效（不启进程，符合 L2 边界）。
func (m *Manager) RenderConfig(id string) error {
	if err := m.renderConfigOnly(id); err != nil {
		return err
	}
	return m.Reload(id)
}

// renderConfigOnly 只重渲染配置，不 reload（供改端口时先停后启的流程使用）。
func (m *Manager) renderConfigOnly(id string) error {
	rec := m.reg.Get(id)
	if rec == nil {
		return fmt.Errorf("实例不存在: %s", id)
	}
	man, ok := m.plugins.Get(rec.Plugin)
	if !ok {
		return fmt.Errorf("插件清单不存在: %s", rec.Plugin)
	}
	if man.Lifecycle == nil {
		return nil
	}
	acts := configActions(man.Lifecycle.Install)
	if len(acts) == 0 {
		return nil
	}
	v, err := m.varsFor(rec, man)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(v.Dir, 0o755); err != nil {
		return err
	}
	if err := RunActions(acts, v, nil, m.auditLog); err != nil {
		return fmt.Errorf("重渲染配置失败: %w", err)
	}
	if man.Config != nil && man.Config.Signature != "" {
		_ = os.WriteFile(signaturePath(id), []byte(man.Config.Signature), 0o644)
	}
	if m.auditLog != nil {
		m.auditLog.Ok("instance", "render-config", id, map[string]any{"ports": rec.Ports})
	}
	return nil
}

// refreshSpec 把当前记录（端口等）重新注册进 processd。
// 必须在实例停止时调用：processd.Add 拒绝覆盖运行中的实例。
// 内置服务没有进程层规格，端口在 Start 时由工厂读取，故为 no-op。
func (m *Manager) refreshSpec(rec *Record, man *pluginc.Manifest) error {
	if m.isEmbedded(rec.Plugin) {
		return nil
	}
	v, err := m.varsFor(rec, man)
	if err != nil {
		return err
	}
	return m.proc.Add(m.buildProcessSpec(rec, man, v))
}

// ensureConfig 配置模板签名不符（安装包升级过模板）时自动重渲染。
// 例：nginx 模板新加 charset utf-8 后，老实例启动即补上，用户不用删了重装。
// force=true 表示端口刚被 heal 改过 —— 无论签名是否一致都要重渲染，
// 否则配置文件里是旧端口，进程按旧端口监听、健康检查拨新端口，必然超时。
func (m *Manager) ensureConfig(id string, force bool) error {
	rec := m.reg.Get(id)
	if rec == nil {
		return fmt.Errorf("实例不存在: %s", id)
	}
	man, ok := m.plugins.Get(rec.Plugin)
	if !ok || man.Config == nil {
		return nil
	}
	promoted := m.promotePorts(rec, man)
	if promoted {
		// 端口变了必须同步 processd 的 spec（健康检查目标/启动参数都吃它）。
		if err := m.refreshSpec(rec, man); err != nil {
			return err
		}
	}
	if !promoted && !force && man.Config.Signature != "" {
		if b, err := os.ReadFile(signaturePath(id)); err == nil &&
			strings.TrimSpace(string(b)) == man.Config.Signature {
			return nil
		}
	}
	if man.Config.Signature == "" && !promoted && !force {
		return nil
	}
	return m.RenderConfig(id)
}

// promotePorts 把「池内自动分配的端口」升到清单声明的首选端口（如 nginx 的 80）。
// 只动仍是池区间的端口——用户手工指定的端口不碰；首选端口不可用
//（被占用 / 无 <1024 权限 / 被站点占用）则保持原状。
func (m *Manager) promotePorts(rec *Record, man *pluginc.Manifest) bool {
	changed := false
	for _, role := range man.Ports {
		if role.Default < 1 {
			continue
		}
		cur := rec.Ports[role.Name]
		if cur == role.Default {
			continue
		}
		pool, ok := m.settings.PortPools[role.Pool]
		if !ok || cur < pool.Min || cur > pool.Max {
			continue
		}
		if !processd.ProbeFree(role.Default) {
			continue
		}
		if m.portGuard != nil && m.portGuard(rec.ID, role.Default) {
			continue
		}
		rec.Ports[role.Name] = role.Default
		if err := m.reg.Update(rec); err != nil {
			rec.Ports[role.Name] = cur
			continue
		}
		changed = true
		if m.auditLog != nil {
			m.auditLog.Ok("instance", "promote-port", rec.ID,
				map[string]any{"role": role.Name, "from": cur, "to": role.Default})
		}
	}
	return changed
}

// SetPort 修改实例端口：校验 → 改记录 → 重渲染 → （运行中）reload。
// 任一步失败回滚到旧端口，绝不留「界面改了但实际没生效」的状态。
func (m *Manager) SetPort(id, role string, port int) error {
	rec := m.reg.Get(id)
	if rec == nil {
		return fmt.Errorf("实例不存在: %s", id)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("端口 %d 非法（1-65535）", port)
	}
	man, ok := m.plugins.Get(rec.Plugin)
	if !ok {
		return fmt.Errorf("插件清单不存在: %s", rec.Plugin)
	}
	known := false
	for _, r := range man.Ports {
		if r.Name == role {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("插件 %s 没有端口角色 %q", rec.Plugin, role)
	}
	old := rec.Ports[role]
	if old == port {
		return nil
	}
	for name, p := range rec.Ports {
		if name != role && p == port {
			return fmt.Errorf("端口 %d 已被本实例的 %s 占用", port, name)
		}
	}
	if !processd.ProbeFree(port) {
		tip := ""
		if port < 1024 {
			tip = "（Windows 下 1-1023 端口需要以管理员身份运行对号）"
		}
		return fmt.Errorf("端口 %d 不可用：被 %s 占用%s", port, processd.Occupier(port), tip)
	}

	// 改端口要重建进程 spec，而 processd 不允许覆盖运行中的实例 ——
	// 先停，改完再按原状态启动（端口变更本来就要求重启）。
	running := m.Status(id) == processd.StateRunning
	if running {
		if err := m.Stop(id); err != nil {
			return fmt.Errorf("为修改端口停止实例失败: %w", err)
		}
	}
	apply := func(p int) error {
		rec.Ports[role] = p
		if err := m.reg.Update(rec); err != nil {
			return err
		}
		if err := m.renderConfigOnly(id); err != nil {
			return err
		}
		return m.refreshSpec(rec, man)
	}
	rollback := func() {
		rec.Ports[role] = old
		_ = m.reg.Update(rec)
		_ = m.renderConfigOnly(id)
		_ = m.refreshSpec(rec, man)
		if running {
			_ = m.Start(id)
		}
	}

	if err := apply(port); err != nil {
		rollback()
		return fmt.Errorf("改端口失败（已回滚到 %d）: %w", old, err)
	}
	if running {
		if err := m.Start(id); err != nil {
			rollback()
			return fmt.Errorf("新端口 %d 启动失败（已回滚到 %d）: %w", port, old, err)
		}
	}
	if m.auditLog != nil {
		m.auditLog.Ok("instance", "set-port", id,
			map[string]any{"role": role, "from": old, "to": port, "restarted": running})
	}
	return nil
}

// Reload 按清单 config.reload 重载配置（如 nginx -s reload）。
// 实例未运行 / 插件未声明 reload 时直接返回 nil——配置会在下次启动时自然生效。
func (m *Manager) Reload(id string) error {
	rec := m.reg.Get(id)
	if rec == nil {
		return fmt.Errorf("实例不存在: %s", id)
	}
	// 内置服务由本体托管：没有外部 reload 命令，改配置走重启（清单 restart）。
	if m.isEmbedded(rec.Plugin) {
		return nil
	}
	if m.proc.Status(id) != processd.StateRunning {
		return nil
	}
	man, ok := m.plugins.Get(rec.Plugin)
	if !ok || man.Config == nil || man.Config.Reload == nil {
		return nil
	}
	binRel, err := man.BinaryPath()
	if err != nil {
		return err
	}
	v := Vars{
		ID: id, Dir: instanceDir(id), RuntimeDir: rec.RuntimeDir,
		Version: rec.Version,
		Bin:     filepath.Join(rec.RuntimeDir, filepath.FromSlash(binRel)),
		Ports:   rec.Ports,
	}
	cmdPath := applyTokens(firstNonEmpty(man.Config.Reload.Cmd, "{bin}"), v)
	if !filepath.IsAbs(cmdPath) {
		cmdPath = filepath.Join(rec.RuntimeDir, filepath.FromSlash(cmdPath))
	}
	var args []string
	for _, a := range man.Config.Reload.Args {
		args = append(args, applyTokens(a, v))
	}
	cmd := exec.Command(cmdPath, args...)
	cmd.Dir = v.Dir
	out, runErr := cmd.CombinedOutput()
	if m.auditLog != nil {
		if runErr != nil {
			m.auditLog.Fail("instance", "reload", id, map[string]any{"error": runErr.Error(), "out": string(out)})
		} else {
			m.auditLog.Ok("instance", "reload", id, map[string]any{"cmd": cmdPath})
		}
	}
	if runErr != nil {
		return fmt.Errorf("重载失败: %w（%s）", runErr, strings.TrimSpace(string(out)))
	}
	// reload 是异步的：返回 0 只代表信号已送达，master 起新 worker、旧 worker
	// 优雅退出还要几百毫秒。期间旧 worker 仍可能服务部分新连接 → 用户改完配置
	// 立刻刷新会看到旧行为（"我明明开了 gzip，怎么没压？"）。
	// 这里等一个稳定期（清单可配 config.reloadSettleMs，默认 400ms）。
	settle := 400
	if man.Config != nil && man.Config.ReloadSettleMs > 0 {
		settle = man.Config.ReloadSettleMs
	}
	time.Sleep(time.Duration(settle) * time.Millisecond)
	return nil
}

// Logs 读子进程日志尾部（logs/process/<id>.log）。
func (m *Manager) Logs(id string, tail int) (string, error) {
	if tail <= 0 {
		tail = 100
	}
	p := filepath.Join(duihao.DirProcessLogs(), id+".log")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	return strings.Join(lines, "\n"), nil
}

// InstallRuntime 为 cli/embedded 插件取包（无实例；cli 调用前调用）。
func (m *Manager) InstallRuntime(ctx context.Context, pluginID, version string) (string, error) {
	man, ok := m.plugins.Get(pluginID)
	if !ok {
		return "", fmt.Errorf("未知插件: %s", pluginID)
	}
	ve, err := man.Version(version)
	if err != nil {
		return "", err
	}
	asset, err := ve.Asset()
	if err != nil {
		return "", err
	}
	binRel, _ := man.BinaryPath()
	res, err := runtimepkg.Resolve(ctx, runtimepkg.VersionSpec{
		PluginID: pluginID, Version: ve.Version, Platform: pluginc.PlatformTarget,
		Source: ve.Source, URL: asset.URL, URLs: asset.URLs,
		SHA256: asset.SHA256, Size: asset.Size, Bin: binRel,
	}, m.auditLog)
	if err != nil {
		return "", err
	}
	if m.auditLog != nil {
		m.auditLog.Ok("runtime", "install", pluginID+"@"+ve.Version, map[string]any{"source": res.Source})
	}
	return res.Dir, nil
}

// InvokeCli 执行 cli 插件：解析 entry（{runtime:<pid>:<ver>} 借用其他插件运行时）→
// 按需取包 → clirun.Invoke（捕获输出 + 审计）。
func (m *Manager) InvokeCli(ctx context.Context, cliID, version string, args []string, cwd string, timeoutSec int) (clirun.Result, error) {
	man, ok := m.plugins.Get(cliID)
	if !ok {
		return clirun.Result{}, fmt.Errorf("未知插件: %s", cliID)
	}
	if man.Kind != pluginc.KindCLI {
		return clirun.Result{}, fmt.Errorf("插件 %s 不是 cli 类型（%s）", cliID, man.Kind)
	}
	ve, err := man.Version(version)
	if err != nil {
		return clirun.Result{}, err
	}

	// cli 自身运行时（composer.phar 等资源所在）。
	cliDir, err := m.InstallRuntime(ctx, cliID, ve.Version)
	if err != nil {
		return clirun.Result{}, fmt.Errorf("cli 运行时取包失败: %w", err)
	}

	if len(man.Entry) == 0 {
		return clirun.Result{}, fmt.Errorf("cli %s 缺少 entry", cliID)
	}
	// entry[0]：可执行。支持 {runtime:<plugin>:<version>} 借用其他插件运行时。
	bin, err := m.expandRuntimeToken(ctx, man.Entry[0])
	if err != nil {
		return clirun.Result{}, err
	}
	// entry[1:]：固定前置参数；相对路径解析到 cli 运行时目录。
	var prefix []string
	for _, e := range man.Entry[1:] {
		if !strings.Contains(e, "{") && !filepath.IsAbs(e) {
			e = filepath.Join(cliDir, filepath.FromSlash(e))
		}
		prefix = append(prefix, e)
	}

	if timeoutSec <= 0 {
		timeoutSec = 600
	}
	res, err := clirun.Invoke(ctx, clirun.CLISpec{ID: cliID, Bin: bin, Prefix: prefix},
		args, cwd, nil, time.Duration(timeoutSec)*time.Second, m.auditLog)
	if err == nil && m.auditLog != nil {
		m.auditLog.Ok("cli", "invoke", cliID, map[string]any{"args": args, "cwd": cwd, "exit": res.ExitCode})
	}
	return res, err
}

// expandRuntimeToken 解析 "{runtime:<plugin>:<version>}[/subpath]" →
// 该插件运行时目录（可选拼接子路径）。cli 的 entry 用它借用其他插件运行时，
// 如 composer entry[0] = "{runtime:php:8.2.29}/php.exe"（注意 php 清单的 bin
// 是 php-cgi.exe，cli 场景必须显式借 php.exe，否则 CGI 模式输出 HTTP 头）。
func (m *Manager) expandRuntimeToken(ctx context.Context, tok string) (string, error) {
	i := strings.Index(tok, "{runtime:")
	if i < 0 {
		return tok, nil
	}
	j := strings.Index(tok[i:], "}")
	if j < 0 {
		return "", fmt.Errorf("runtime token 未闭合: %s", tok)
	}
	inner := tok[i+len("{runtime:") : i+j]
	parts := strings.SplitN(inner, ":", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("runtime token 非法: %s", tok)
	}
	depID, depVer := parts[0], parts[1]
	if _, ok := m.plugins.Get(depID); !ok {
		return "", fmt.Errorf("entry 依赖未知插件 %s", depID)
	}
	dir, err := m.InstallRuntime(ctx, depID, depVer)
	if err != nil {
		return "", fmt.Errorf("entry 依赖 %s@%s 取包失败: %w", depID, depVer, err)
	}
	return tok[:i] + dir + tok[i+j+1:], nil
}

func instanceDir(id string) string { return filepath.Join(duihao.DirInstances(), id) }
