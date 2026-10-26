package processd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	duihao "github.com/ys-ll/uniterm/backend/duihao"
	"github.com/ys-ll/uniterm/backend/duihao/audit"
)

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

func jsonMarshal(v any) ([]byte, error) { return json.MarshalIndent(v, "", "  ") }

// Manager 守护所有实例。并发安全；实例注册表落盘 config\instances.json。
type Manager struct {
	mu       sync.Mutex
	settings *duihao.Settings
	audit    *audit.Logger
	inst     map[string]*instance
	registry RegistryFile
}

type instance struct {
	spec InstanceSpec
	// 运行态
	mu         sync.Mutex
	state      string
	procs      []*launchedProc
	desiredRun bool
	restarts   int
	stopGrace  chan struct{} // 关闭=请求停止（watchdog 收到后不再重启）
}

// NewManager 加载注册表并创建管理器。所有实例启动时视为 stopped
// （不自动拉起；自动恢复交给上层"随应用启动实例"的显式配置）。
func NewManager(settings *duihao.Settings, auditLog *audit.Logger) (*Manager, error) {
	if settings == nil {
		var err error
		settings, err = duihao.LoadSettings()
		if err != nil {
			return nil, err
		}
	}
	if err := duihao.EnsureDirs(); err != nil {
		return nil, err
	}
	m := &Manager{settings: settings, audit: auditLog, inst: map[string]*instance{}}
	m.loadRegistry()
	return m, nil
}

func (m *Manager) registryPath() string {
	return filepath.Join(duihao.DirConfig(), "instances.json")
}

func (m *Manager) loadRegistry() {
	data, err := os.ReadFile(m.registryPath())
	if err != nil {
		return
	}
	var reg RegistryFile
	if err := jsonUnmarshal(data, &reg); err != nil {
		return
	}
	m.registry = reg
	for _, spec := range reg.Instances {
		m.inst[spec.ID] = &instance{spec: spec, state: StateStopped}
	}
}

func (m *Manager) saveRegistryLocked() {
	m.registry = RegistryFile{SchemaVersion: 1}
	for _, inst := range m.inst {
		m.registry.Instances = append(m.registry.Instances, inst.spec)
	}
	data, err := jsonMarshal(m.registry)
	if err != nil {
		return
	}
	_ = os.WriteFile(m.registryPath(), data, 0o644)
}

// Add 注册实例（declared）；同名覆盖。ports 由调用方先用 AssignPort 分好。
func (m *Manager) Add(spec InstanceSpec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if spec.ID == "" {
		return errors.New("实例 id 不能为空")
	}
	inst := &instance{spec: spec, state: StateDeclared}
	// 只有「真正在跑」的实例不能覆盖（stopped / error / declared 都可以重注册，
	// 改端口后重建 spec 依赖这一点）。
	if old, ok := m.inst[spec.ID]; ok {
		switch old.state {
		case StateRunning, StateStarting, StateStopping:
			return fmt.Errorf("实例 %s 正在运行，先停止再修改", spec.ID)
		}
	}
	m.inst[spec.ID] = inst
	m.saveRegistryLocked()
	m.audit.Ok("processd", "instance.add", spec.ID, map[string]any{"plugin": spec.Plugin, "version": spec.Version})
	return nil
}

// Remove 注销实例（必须已停止）。
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.inst[id]
	if !ok {
		return fmt.Errorf("实例 %s 不存在", id)
	}
	if s := inst.stateString(); s != StateStopped && s != StateError && s != StateDeclared {
		return fmt.Errorf("实例 %s 状态为 %s，先停止再删除", id, s)
	}
	delete(m.inst, id)
	m.saveRegistryLocked()
	m.audit.Ok("processd", "instance.remove", id, nil)
	return nil
}

// Status 返回实例状态；不存在返回空串。
func (m *Manager) Status(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inst, ok := m.inst[id]; ok {
		return inst.stateString()
	}
	return ""
}

// List 返回全部实例 id 与状态。
func (m *Manager) List() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for id, inst := range m.inst {
		out[id] = inst.stateString()
	}
	return out
}

// PIDs 返回运行中实例的 OS 进程号（排障与测试用）。
func (m *Manager) PIDs(id string) []int {
	m.mu.Lock()
	inst, ok := m.inst[id]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	inst.mu.Lock()
	defer inst.mu.Unlock()
	pids := make([]int, 0, len(inst.procs))
	for _, p := range inst.procs {
		pids = append(pids, p.pid)
	}
	return pids
}

// Start 拉起实例：stopped/error → starting →（健康）→ running。
// 启动前逐端口 bind 探测，占用时报占用进程。
func (m *Manager) Start(id string) error {
	m.mu.Lock()
	inst, ok := m.inst[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("实例 %s 不存在", id)
	}
	return m.startInstance(inst, true)
}

func (m *Manager) startInstance(inst *instance, manual bool) error {
	inst.mu.Lock()
	switch inst.state {
	case StateRunning, StateStarting:
		inst.mu.Unlock()
		return fmt.Errorf("实例 %s 已在运行", inst.spec.ID)
	case StateStopping:
		inst.mu.Unlock()
		return fmt.Errorf("实例 %s 正在停止，稍后再启动", inst.spec.ID)
	}
	// 端口探测：先于状态迁移，失败不进 starting。
	for role, port := range inst.spec.Ports {
		if !ProbeFree(port) {
			inst.mu.Unlock()
			err := fmt.Errorf("端口 %d 被占用（%s），无法启动实例 %s 的 %s 端口", port, Occupier(port), inst.spec.ID, role)
			m.audit.Fail("processd", "instance.start", inst.spec.ID, map[string]any{"err": err.Error()})
			return err
		}
	}
	inst.state = StateStarting
	inst.desiredRun = true
	inst.restarts = 0
	inst.stopGrace = make(chan struct{})
	spec := inst.spec
	inst.mu.Unlock()

	m.audit.Ok("processd", "instance.start.begin", spec.ID, map[string]any{"ports": spec.Ports})

	if err := m.launchAll(inst, spec); err != nil {
		inst.mu.Lock()
		inst.state = StateError
		inst.desiredRun = false
		inst.mu.Unlock()
		m.audit.Fail("processd", "instance.start", spec.ID, map[string]any{"err": err.Error()})
		return err
	}

	// 健康门：全部进程健康才进 running（以第一个进程的健康检查为准）。
	if err := m.awaitHealthy(inst, spec); err != nil {
		m.killAll(inst)
		inst.mu.Lock()
		inst.state = StateError
		inst.desiredRun = false
		inst.mu.Unlock()
		m.audit.Fail("processd", "instance.start", spec.ID, map[string]any{"err": err.Error()})
		return err
	}

	inst.mu.Lock()
	inst.state = StateRunning
	inst.mu.Unlock()
	m.audit.Ok("processd", "instance.start", spec.ID, map[string]any{"manual": manual})
	return nil
}

// launchAll 逐个拉起进程并挂上看门狗。
func (m *Manager) launchAll(inst *instance, spec InstanceSpec) error {
	inst.mu.Lock()
	inst.procs = nil
	inst.mu.Unlock()

	logPath := filepath.Join(duihao.DirProcessLogs(), spec.ID+".log")
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)

	for i, ps := range spec.Processes {
		p, err := launchWindows(ps.Bin, ps.Args, effectiveDir(ps, spec), envSlice(ps.Env), logPath)
		if err != nil {
			m.killAll(inst)
			return fmt.Errorf("拉起 %s（进程 %d/%d）失败: %w", ps.Name, i+1, len(spec.Processes), err)
		}
		inst.mu.Lock()
		inst.procs = append(inst.procs, p)
		inst.mu.Unlock()

		// 看门狗：进程意外退出 → 退避重启（1s→2s→4s，3 次后置 error）。
		go m.watchProc(inst, ps, i, p)
	}
	return nil
}

func effectiveDir(ps ProcessSpec, spec InstanceSpec) string {
	if ps.Dir != "" {
		return ps.Dir
	}
	if spec.RuntimeDir != "" {
		return spec.RuntimeDir
	}
	return ""
}

func envSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// watchProc 等待进程退出；desiredRun 仍为真时按退避重启整个实例。
func (m *Manager) watchProc(inst *instance, ps ProcessSpec, idx int, p *launchedProc) {
	<-p.waitEvent
	inst.mu.Lock()
	if !inst.desiredRun {
		inst.mu.Unlock()
		return // 计划内停止
	}
	// 周期健康检查兜底之外的"进程死亡"路径：直接进入重启流程。
	attempt := inst.restarts
	inst.restarts++
	inst.state = StateStarting
	inst.mu.Unlock()

	m.audit.Fail("processd", "instance.proc.exit", inst.spec.ID,
		map[string]any{"proc": ps.Name, "pid": p.pid, "attempt": attempt + 1})

	if attempt >= len(restartBackoff) {
		inst.mu.Lock()
		inst.state = StateError
		inst.desiredRun = false
		inst.mu.Unlock()
		m.audit.Fail("processd", "instance.error", inst.spec.ID,
			map[string]any{"reason": "重启次数用尽", "attempts": attempt + 1})
		return
	}

	delay := restartBackoff[attempt]
	select {
	case <-inst.stopGrace: // 退避期间收到停止请求
		inst.mu.Lock()
		inst.state = StateStopped
		inst.mu.Unlock()
		return
	case <-time.After(delay):
	}

	inst.mu.Lock()
	if !inst.desiredRun {
		inst.mu.Unlock()
		return
	}
	spec := inst.spec
	inst.mu.Unlock()

	if err := m.launchAll(inst, spec); err != nil {
		inst.mu.Lock()
		inst.state = StateError
		inst.desiredRun = false
		inst.mu.Unlock()
		m.audit.Fail("processd", "instance.error", inst.spec.ID, map[string]any{"err": err.Error()})
		return
	}
	if err := m.awaitHealthy(inst, spec); err != nil {
		m.killAll(inst)
		inst.mu.Lock()
		inst.state = StateError
		inst.desiredRun = false
		inst.mu.Unlock()
		m.audit.Fail("processd", "instance.error", inst.spec.ID, map[string]any{"err": err.Error()})
		return
	}
	inst.mu.Lock()
	inst.state = StateRunning
	inst.restarts = 0 // 稳定运行后清零
	inst.mu.Unlock()
	m.audit.Ok("processd", "instance.restart", inst.spec.ID, map[string]any{"attempt": attempt + 1})
}

func (m *Manager) awaitHealthy(inst *instance, spec InstanceSpec) error {
	inst.mu.Lock()
	procs := append([]*launchedProc(nil), inst.procs...)
	inst.mu.Unlock()
	if len(procs) == 0 {
		return errors.New("无进程可探活")
	}
	for i, ps := range spec.Processes {
		if i >= len(procs) {
			break
		}
		if ps.Health.Type == "none" || ps.Health.Type == "" {
			continue
		}
		if err := waitHealthy(context.Background(), ps.Health, healthTimeout); err != nil {
			return fmt.Errorf("%s: %w", ps.Name, err)
		}
	}
	return nil
}

// Stop 优雅停止：按 stop 声明（signal/cmd/http），超时强杀兜底。
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	inst, ok := m.inst[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("实例 %s 不存在", id)
	}
	return m.stopInstance(inst)
}

func (m *Manager) stopInstance(inst *instance) error {
	inst.mu.Lock()
	switch inst.state {
	case StateStopped, StateError, StateDeclared, StateInstalled:
		inst.mu.Unlock()
		// 无进程可停；若残留 proc（error 态可能有）仍清一次
		m.killAll(inst)
		inst.mu.Lock()
		inst.state = StateStopped
		inst.mu.Unlock()
		return nil
	}
	inst.state = StateStopping
	inst.desiredRun = false
	if inst.stopGrace != nil {
		close(inst.stopGrace)
	}
	spec := inst.spec
	procs := append([]*launchedProc(nil), inst.procs...)
	inst.mu.Unlock()

	m.audit.Ok("processd", "instance.stop.begin", spec.ID, nil)

	// 1) 优雅信号：以第一个声明了 stop 的进程为准（nginx -s stop 这类全局命令）。
	for i, ps := range spec.Processes {
		if i >= len(procs) {
			break
		}
		switch ps.Stop.Type {
		case "signal":
			_ = sendCtrlBreak(procs[i])
		case "cmd":
			cmd := exec.Command(ps.Stop.Cmd, ps.Stop.Args...)
			cmd.Dir = effectiveDir(ps, spec)
			_ = cmd.Run()
		case "http":
			if ps.Stop.Cmd != "" {
				c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				req, err := http.NewRequestWithContext(c, http.MethodGet, ps.Stop.Cmd, nil)
				if err == nil {
					if resp, err := http.DefaultClient.Do(req); err == nil {
						resp.Body.Close()
					}
				}
				cancel()
			}
		}
	}

	// 2) 等优雅退出，超时强杀。
	grace := 8 * time.Second
	if len(spec.Processes) > 0 {
		grace = gracefulTimeout(spec.Processes[0].Stop)
	}
	deadline := time.Now().Add(grace)
	for _, p := range procs {
		remain := time.Until(deadline)
		if remain <= 0 {
			break
		}
		select {
		case <-p.waitEvent:
		case <-time.After(remain):
		}
	}
	m.killAll(inst)

	inst.mu.Lock()
	inst.state = StateStopped
	inst.procs = nil
	inst.mu.Unlock()
	m.saveRegistry()
	m.audit.Ok("processd", "instance.stop", spec.ID, nil)
	return nil
}

// killAll 强杀全部残留进程并关闭 Job（KILL_ON_JOB_CLOSE 兜底的显式路径）。
func (m *Manager) killAll(inst *instance) {
	inst.mu.Lock()
	procs := append([]*launchedProc(nil), inst.procs...)
	inst.procs = nil
	inst.mu.Unlock()
	for _, p := range procs {
		if procAlive(p) {
			_ = terminate(p)
		}
		<-p.waitEvent // 等内核确认退出
		closeJob(p)
	}
}

func (m *Manager) saveRegistry() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveRegistryLocked()
}

// ---- instance 小工具 ----

func (i *instance) stateString() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.state
}

// RenderArgs 渲染参数模板变量：{port:name} / {dir} / {bin}（方案 §4.3 start）。
func RenderArgs(args []string, vars map[string]string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		for k, v := range vars {
			a = replaceAll(a, "{"+k+"}", v)
		}
		out[i] = a
	}
	return out
}

func replaceAll(s, old, new string) string {
	for {
		i := indexOf(s, old)
		if i < 0 {
			return s
		}
		s = s[:i] + new + s[i+len(old):]
	}
}

func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}
