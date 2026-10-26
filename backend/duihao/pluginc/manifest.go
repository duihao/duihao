// Package pluginc 实现插件清单（schema v2）的加载与注册表（方案 §4.3/§4.5）。
//
// 兼容策略：v1 清单缺省值兜底可加载；未知顶层字段忽略并打 warn 日志，
// 保证"加字段不破坏存量清单"。清单只声明数据，永不携带可执行逻辑（铁律 3）。
package pluginc

import (
	"fmt"
)

// kind 四类（方案 §4.1）。
const (
	KindProcess  = "process"
	KindCLI      = "cli"
	KindEmbedded = "embedded"
	KindExternal = "external"
)

// PlatformTarget 当前构建的目标平台（与清单 platforms 键一致）。
const PlatformTarget = "windows-amd64"

// PlatformBinary 描述平台 → 二进制相对路径映射（清单 bin 字段）。
type PlatformBinary map[string]string

// VersionEntry 清单 versions[] 元素：一个可安装的版本。
type VersionEntry struct {
	Version string `json:"version"`
	// Source: url | github-release | mirror-only（只能离线导入）。
	Source string `json:"source"`
	// Platforms 平台 → 取包信息。
	Platforms map[string]PlatformAsset `json:"platforms,omitempty"`
}

// PlatformAsset 单平台取包信息（url + 校验值）。
// URLs 为可选备用下载源：官方源在国内常不可达（GitHub Releases），
// 按 urls 顺序回退，全部失败才报错。
type PlatformAsset struct {
	URL    string   `json:"url,omitempty"`
	URLs   []string `json:"urls,omitempty"`
	SHA256 string   `json:"sha256,omitempty"`
	Size   int64    `json:"size,omitempty"`
}

// Requires 前置条件（runlib P1 后置，host 已由 M2 hostreq 实现）。
type Requires struct {
	Runlib []RunlibReq `json:"runlib,omitempty"`
	Host   []HostReq   `json:"host,omitempty"`
}

// RunlibReq 系统运行库前置（VC++ 等）——字段保留，M3 加载时忽略并 warn。
type RunlibReq struct {
	Check string `json:"check"`
	Msg   string `json:"msg,omitempty"`
}

// HostReq 宿主机条件：check 如 "disk:free>=2G"、"port:free:8080"。
type HostReq struct {
	Check string `json:"check"`
	Msg   string `json:"msg,omitempty"`
}

// PortRole 端口角色声明：{name, pool}，池名对应方案 §3.3 端口池。
// Default 是「首选端口」：可在池外（如 nginx 的 80）。空闲则用它，
// 被占用或没权限（<1024 非管理员）时自动回落到 pool 池内分配。
type PortRole struct {
	Name    string `json:"name"`
	Pool    string `json:"pool"`              // web | fcgi | db | ftp
	Default int    `json:"default,omitempty"` // 首选端口（0 = 直接用池）
}

// Lifecycle 生命周期动作白名单（§4.4）。upgrade 属 P2，字段保留。
type Lifecycle struct {
	Install []Action `json:"install,omitempty"`
	Upgrade []Action `json:"upgrade,omitempty"` // P2：加载时忽略
}

// Action 白名单原子动作：mkdir / copy / write / render / exec-once / backup。
type Action struct {
	// Action 动作名（白名单之外的直接报错，不静默忽略）。
	Action string `json:"action"`
	// Path mkdir 目标。
	Path string `json:"path,omitempty"`
	// From copy/backup 源；write 亦用 from 表示内联文本（清单内）。
	From string `json:"from,omitempty"`
	// To copy 目标；Dst 为 write/render 目标（两者等价，兼容两种写法）。
	To  string `json:"to,omitempty"`
	Dst string `json:"dst,omitempty"`
	// Src render 模板（运行时内相对路径）或 write 内联文本。
	Src string `json:"src,omitempty"`
	// Vars render 允许的变量名白名单（声明式约束）。
	Vars []string `json:"vars,omitempty"`
	// Cmd/Args exec-once：只能执行 {bin} 或清单声明的二进制。
	Cmd  string   `json:"cmd,omitempty"`
	Args []string `json:"args,omitempty"`
	// On exec-once 时机：firstInit（幂等）。
	On string `json:"on,omitempty"`
	// IdempotentBy 幂等标记路径：存在即跳过。
	IdempotentBy string `json:"idempotentBy,omitempty"`
	// Confirm true 时首次执行需用户确认。
	Confirm bool `json:"confirm,omitempty"`
}

// StartSpec process 启动参数模板（支持 {port:xxx}、{dir}、{bin} 变量）。
type StartSpec struct {
	Args     []string          `json:"args,omitempty"`
	Dir      string            `json:"dir,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

// Detect external 三路探测（P2 纳管；M3 字段保留）。
type Detect struct {
	Type   string `json:"type"` // service | path | port
	Name   string `json:"name,omitempty"`
	Glob   string `json:"glob,omitempty"`
	Target string `json:"target,omitempty"`
}

// DataDir external 数据目录约束。
type DataDir struct {
	Readonly bool `json:"readonly,omitempty"`
}

// Config 配置管理声明：v1 只支持整份渲染 + include 片段，不做结构化 patch。
type Config struct {
	Main       string      `json:"main,omitempty"`
	IncludeDir string      `json:"includeDir,omitempty"`
	Reload     *ReloadSpec `json:"reload,omitempty"`
	// Signature 配置模板版本签名。实例目录里的 .conf-signature 与之不符时，
	// 启动前会用当前端口变量自动重跑 install 里的 write/render 动作
	// （＝模板升级后老实例自动补上新配置，例如 charset utf-8）。
	Signature string `json:"signature,omitempty"`
	// ReloadSettleMs reload 后的稳定等待（毫秒）。nginx/其他 master-worker 型服务
	// 的 reload 是异步的：命令返回成功只代表信号送达，新 worker 接管还要几百毫秒。
	// 默认 400；配 0 表示不等待（老清单行为不变）。
	ReloadSettleMs int `json:"reloadSettleMs,omitempty"`
}

// ReloadSpec 配置重载命令。
type ReloadSpec struct {
	Cmd  string   `json:"cmd"`
	Args []string `json:"args,omitempty"`
}

// ─── 声明式设置（phpStudy 式可视化配置，方案 §4.6） ───

// SettingField 一个可配置项。清单只声明数据（铁律 3）：
// 前端按 Type 动态渲染控件，后端把值注入 {set:key} token 重渲染配置。
type SettingField struct {
	Key      string `json:"key"`      // 配置键（如 mysql.max_connections）；模板里用 {set:mysql.max_connections}
	Label    string `json:"label"`    // 显示名
	Type     string `json:"type"`     // text | number | switch | select | textarea | password
	Default  string `json:"default,omitempty"` // 默认值（switch 用 "1"/"0"）
	Unit     string `json:"unit,omitempty"`    // 显示单位（M / K / 秒 …）
	Options  []string `json:"options,omitempty"` // select 可选值
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	// PortRole 非空时该字段映射到实例端口（如 "http"）：读写走 SetPort
	// 校验链路（占用检查 / 站点冲突 / refreshSpec），不走 settings.json。
	PortRole string `json:"portRole,omitempty"`
	// Hint 字段下方说明文字。
	Hint string `json:"hint,omitempty"`
}

// SettingGroup 设置分组（对应 phpStudy 的 基本配置 / 性能配置 / 参数配置）。
type SettingGroup struct {
	Title  string         `json:"title"`
	Fields []SettingField `json:"fields"`
}

// Settings 插件可视化设置声明。
type Settings struct {
	// Groups 分组字段定义（顺序即界面顺序）。
	Groups []SettingGroup `json:"groups,omitempty"`
	// ConfigFile 「配置文件」Tab 可直接编辑的原文文件（实例目录内相对路径，
	// 如 "my.ini"）。空则不显示该 Tab。
	ConfigFile string `json:"configFile,omitempty"`
	// Restart true = 应用设置需要重启进程（MySQL 等不认 reload 的）；
	// false = 配置渲染后 Reload 即可（nginx）。运行中应用时据此选择。
	Restart bool `json:"restart,omitempty"`
}

// ─── 管理面（连接管理，声明式） ───

// Manage 管理面声明：panel 指明「管理」按钮直连哪个内置连接管理界面。
// 取值与前端内置界面对应：mysql | redis | ftp。
// 无 manage 段的插件不显示「管理」按钮（如 nginx / php，它们的维护走配置与站点）。
type Manage struct {
	Panel string `json:"panel,omitempty"`
}

// UI 前端贡献声明（M19 插件化产品线）。
// 加字段不扩权：ui.home 只换取「首页展示资格」，不带来任何新的执行权；
// 视图随版本编译内置（frontend/src/homeviews 注册表），不做远程动态加载。
type UI struct {
	Home *UIHome `json:"home,omitempty"`
}

// UIHome 插件贡献的首页视图。
type UIHome struct {
	// Component 前端视图注册名（homeviews 注册表 key）。
	Component string `json:"component"`
	// Title 首页标题（i18n key 或字面量）。
	Title string `json:"title,omitempty"`
	// Order 同屏多 home 候选时排序，小者先（可选）。
	Order int `json:"order,omitempty"`
}

// Manifest 插件清单（schema v2）。
type Manifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	ID            string `json:"id"`
	Name          string `json:"name"`
	Category      string `json:"category,omitempty"`
	Description   string `json:"description,omitempty"`
	Kind          string `json:"kind"`
	// MultiRun true = 同插件多实例可同时运行（PHP：各版本并存，站点按需选用）。
	// 缺省 false：Start 时自动停止同插件其他运行实例（phpStudy 式单版本运行）。
	MultiRun bool `json:"multiRun,omitempty"`

	Versions []VersionEntry `json:"versions,omitempty"`
	Requires *Requires      `json:"requires,omitempty"`

	Bin     PlatformBinary `json:"bin,omitempty"` // process/cli
	Entry   []string       `json:"entry,omitempty"` // cli：可执行 + 固定前置参数
	Cwd     string         `json:"cwd,omitempty"`   // cli 默认工作目录模板

	Ports   []PortRole `json:"ports,omitempty"` // process
	Workers int        `json:"workers,omitempty"` // process：副本数，0=1

	Lifecycle *Lifecycle `json:"lifecycle,omitempty"`
	Start     *StartSpec `json:"start,omitempty"` // process
	Stop      *StopSpecShape `json:"stop,omitempty"`
	Health    *HealthSpecShape `json:"health,omitempty"`
	Config    *Config    `json:"config,omitempty"`

	Detect  []Detect `json:"detect,omitempty"`  // external
	DataDir *DataDir `json:"dataDir,omitempty"` // external

	Settings *Settings `json:"settings,omitempty"` // 可视化设置（M8）

	Manage *Manage `json:"manage,omitempty"` // 内置管理面能力（M14）

	UI *UI `json:"ui,omitempty"` // 前端贡献（M19）：ui.home 首页视图
}

// StopSpecShape / HealthSpecShape 与 processd 的 StopSpec/HealthSpec 同构，
// 独立声明避免插件层反向依赖进程层。
type StopSpecShape struct {
	Type     string   `json:"type,omitempty"`
	Cmd      string   `json:"cmd,omitempty"`
	Args     []string `json:"args,omitempty"`
	Graceful int      `json:"graceful,omitempty"`
}

type HealthSpecShape struct {
	Type     string   `json:"type,omitempty"`
	Target   string   `json:"target,omitempty"`
	Cmd      string   `json:"cmd,omitempty"`
	Args     []string `json:"args,omitempty"`
	Interval int      `json:"interval,omitempty"`
}

// knownFields 顶层字段白名单 —— 之外的打 warn（§4.5 清单治理）。
var knownFields = map[string]bool{
	"schemaVersion": true, "id": true, "name": true, "category": true,
	"description": true, "homepage": true, "license": true,
	"multiRun": true,
	"kind": true, "versions": true, "requires": true, "bin": true,
	"entry": true, "cwd": true, "ports": true, "expose": true,
	"workers": true, "processes": true, "lifecycle": true,
	"start": true, "stop": true, "health": true, "config": true,
	"detect": true, "dataDir": true, "settings": true, "manage": true,
	"ui": true,
}

// Validate 结构校验（加载时调用；错误即拒绝注册）。
func (m *Manifest) Validate() error {
	if m.ID == "" {
		return fmt.Errorf("清单缺少 id")
	}
	if m.Name == "" {
		return fmt.Errorf("清单 %s 缺少 name", m.ID)
	}
	switch m.Kind {
	case KindProcess, KindCLI, KindEmbedded, KindExternal:
	default:
		return fmt.Errorf("清单 %s kind 非法: %q", m.ID, m.Kind)
	}
	if m.SchemaVersion != 2 && m.SchemaVersion != 1 {
		return fmt.Errorf("清单 %s schemaVersion=%d 不支持（仅 1/2）", m.ID, m.SchemaVersion)
	}
	if (m.Kind == KindProcess || m.Kind == KindCLI) && len(m.Versions) == 0 {
		return fmt.Errorf("清单 %s（%s）缺少 versions", m.ID, m.Kind)
	}
	if m.UI != nil && m.UI.Home != nil {
		if m.Kind != KindEmbedded && m.Kind != KindProcess {
			return fmt.Errorf("清单 %s：ui.home 仅 embedded/process 可声明（当前 %s）", m.ID, m.Kind)
		}
		if m.UI.Home.Component == "" {
			return fmt.Errorf("清单 %s：ui.home.component 不能为空", m.ID)
		}
	}
	return nil
}

// Version 返回指定版本条目；空串取第一个（默认版本）。
func (m *Manifest) Version(v string) (*VersionEntry, error) {
	if len(m.Versions) == 0 {
		return nil, fmt.Errorf("插件 %s 无可安装版本", m.ID)
	}
	if v == "" {
		return &m.Versions[0], nil
	}
	for i := range m.Versions {
		if m.Versions[i].Version == v {
			return &m.Versions[i], nil
		}
	}
	return nil, fmt.Errorf("插件 %s 无版本 %s", m.ID, v)
}

// Asset 返回当前平台的取包信息。
func (ve *VersionEntry) Asset() (*PlatformAsset, error) {
	a, ok := ve.Platforms[PlatformTarget]
	if !ok {
		return nil, fmt.Errorf("版本 %s 无 %s 平台包", ve.Version, PlatformTarget)
	}
	return &a, nil
}

// BinaryPath 返回当前平台的二进制相对路径。
func (m *Manifest) BinaryPath() (string, error) {
	p, ok := m.Bin[PlatformTarget]
	if !ok {
		// v1 兼容：bin 可能是字符串而非 map —— 由加载层预先归一化，此处兜底。
		return "", fmt.Errorf("插件 %s 无 %s 平台 bin", m.ID, PlatformTarget)
	}
	return p, nil
}
