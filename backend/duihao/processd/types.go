// Package processd 实现对号的进程守护内核（方案 §3.3）。
//
// 职责：实例状态机、Windows Job Object 子进程回收（CREATE_SUSPENDED→Assign→Resume，
// M0 实测顺序不可变）、端口池分配与占用者检测、健康检查（tcp/http/cmd/none）、
// 重启退避（1s→2s→4s 最多 3 次）、优雅停止（signal/cmd/http 超时强杀）、
// stdout/stderr 重定向到 logs\process\<id>.log。
package processd

import "time"

// 实例状态机（方案 §3.4）：declared → installing → installed →
// stopped ⇄ starting → running → stopping → error。UI 状态灯直接映射，不做隐式推断。
const (
	StateDeclared  = "declared"
	StateInstalling = "installing"
	StateInstalled = "installed"
	StateStopped   = "stopped"
	StateStarting  = "starting"
	StateRunning   = "running"
	StateStopping  = "stopping"
	StateError     = "error"
)

// HealthSpec 健康检查配置（清单 schema v2 definitions/health）。
type HealthSpec struct {
	Type     string   `json:"type"` // tcp | http | cmd | none
	Target   string   `json:"target,omitempty"` // tcp: "127.0.0.1:8080"；http: URL
	Cmd      string   `json:"cmd,omitempty"`    // cmd 类型：可执行文件
	Args     []string `json:"args,omitempty"`   // cmd 类型：参数（如 mysqladmin ping）
	Interval int      `json:"interval,omitempty"` // 秒，0=仅启动时探一次
}

// StopSpec 优雅停止配置（definitions/stop）。
type StopSpec struct {
	Type     string   `json:"type,omitempty"` // signal | cmd | http；空=直接强杀兜底
	Cmd      string   `json:"cmd,omitempty"`
	Args     []string `json:"args,omitempty"`
	Graceful int      `json:"graceful,omitempty"` // 优雅超时秒数；0=默认 8s，上限 15s
}

// ProcessSpec 描述一个实际 OS 进程（workers 展开后的单副本）。
type ProcessSpec struct {
	Name string   `json:"name,omitempty"` // 进程名（如 nginx / php-worker-0）
	Bin  string   `json:"bin"`            // 绝对路径
	Args []string `json:"args,omitempty"` // 已渲染的具体参数
	Dir  string   `json:"dir,omitempty"`  // 工作目录（空=继承宿主）
	Env  map[string]string `json:"env,omitempty"` // 追加环境变量

	Health HealthSpec `json:"health,omitempty"`
	Stop   StopSpec   `json:"stop,omitempty"`
}

// InstanceSpec 实例描述：runtime + 端口 + 进程组（可变层，方案 §3.4）。
type InstanceSpec struct {
	ID      string            `json:"id"`      // 实例 id（如 nginx-default）
	Plugin  string            `json:"plugin"`  // 插件 id
	Version string            `json:"version"` // runtime 版本
	RuntimeDir string         `json:"runtimeDir,omitempty"`
	Ports   map[string]int    `json:"ports,omitempty"` // 角色 → 端口（http→18080）
	Processes []ProcessSpec   `json:"processes"`
}

// RegistryFile 对应 config\instances.json 的落盘结构。
type RegistryFile struct {
	SchemaVersion int            `json:"schemaVersion"`
	Instances     []InstanceSpec `json:"instances"`
}

// 退避与超时参数（方案 §3.3）。
var (
	restartBackoff = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	healthTimeout  = 15 * time.Second // 启动后等健康的最长时间
	healthTick     = 500 * time.Millisecond
)

func gracefulTimeout(s StopSpec) time.Duration {
	if s.Graceful <= 0 {
		return 8 * time.Second
	}
	if s.Graceful > 15 {
		return 15 * time.Second
	}
	return time.Duration(s.Graceful) * time.Second
}
