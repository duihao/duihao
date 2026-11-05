// Package service 对号环境工具的 Wails 服务绑定。
// 前端经由生成的 bindings 调用；全部写操作走审计。
package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	duihao "github.com/duihao/duihao/backend/duihao"
	"github.com/duihao/duihao/backend/duihao/audit"
	"github.com/duihao/duihao/backend/duihao/clirun"
	"github.com/duihao/duihao/backend/duihao/instance"
	"github.com/duihao/duihao/backend/duihao/pluginc"
	"github.com/duihao/duihao/backend/duihao/runtimepkg"
	"github.com/duihao/duihao/backend/duihao/site"
)

// PluginView 插件卡片视图（清单 + 安装状态）。
type PluginView struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	License     string   `json:"license,omitempty"`
	Versions    []string `json:"versions"`
	// InstalledVersions 已安装（runtimes 落盘）的版本。
	InstalledVersions []string `json:"installedVersions"`
	// DefaultVersion 清单首个版本。
	DefaultVersion string `json:"defaultVersion"`
	Workers        int    `json:"workers,omitempty"`
	HasInstance    bool   `json:"hasInstance"` // 已有实例（process）
	// MultiRun 同插件多实例可同时运行（PHP）；false = 启动时自动停其他版本。
	MultiRun bool `json:"multiRun"`
	// VersionInfos 各版本的取包信息（软件市场展示下载地址、是否已装）。
	VersionInfos []VersionView `json:"versionInfos,omitempty"`
	// Manage 内置管理面（连接管理）。nil = 该软件没有「管理」按钮。
	Manage *ManageView `json:"manage,omitempty"`
}

// ManageView 管理面视图：panel 决定「管理」按钮直连哪个内置连接管理界面
// （mysql 库管理 / redis 键浏览 / ftp 账号）。
type ManageView struct {
	Panel string `json:"panel"`
}

// VersionView 单个版本的安装元信息。
type VersionView struct {
	Version   string `json:"version"`
	URL       string `json:"url,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Installed bool   `json:"installed"`
}

// PluginCenterService 环境工具服务。
type PluginCenterService struct {
	mgr   *instance.Manager
	sites *site.Manager // 可选：删除实例前做站点引用检查
}

// NewPluginCenterService 构建服务（mgr 由 main 统一创建；传 nil 时在
// ServiceStartup 自行初始化，保持测试/独立启动兼容）。
func NewPluginCenterService(mgr *instance.Manager) *PluginCenterService {
	return &PluginCenterService{mgr: mgr}
}

// SetSiteManager 注入站点管理器（引用完整性检查用）。
func (s *PluginCenterService) SetSiteManager(sm *site.Manager) { s.sites = sm }

// ServiceStartup Wails 生命周期钩子：仅在未注入管理器时自行初始化。
func (s *PluginCenterService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	if s.mgr != nil {
		return nil
	}
	if err := duihao.EnsureDirs(); err != nil {
		return err
	}
	settings, err := duihao.LoadSettings()
	if err != nil {
		settings = duihao.DefaultSettings()
	}
	mgr, err := instance.NewManager(settings, audit.New(duihao.DirLogs()))
	if err != nil {
		return err
	}
	s.mgr = mgr
	return nil
}

// ListPlugins 全部插件视图。
func (s *PluginCenterService) ListPlugins() []PluginView {
	if s.mgr == nil {
		return nil
	}
	var out []PluginView
	hasInst := map[string]bool{}
	for _, v := range s.mgr.List() {
		hasInst[v.Plugin] = true
	}
	for _, m := range s.mgr.Plugins().List() {
		vers := make([]string, 0, len(m.Versions))
		for _, v := range m.Versions {
			vers = append(vers, v.Version)
		}
		installedVers := s.mgr.InstalledVersions(m.ID)
		installed := make(map[string]bool, len(installedVers))
		for _, v := range installedVers {
			installed[v] = true
		}
		// 版本元信息：软件市场用它显示下载地址、大小与已装状态。
		infos := make([]VersionView, 0, len(m.Versions))
		for _, v := range m.Versions {
			vv := VersionView{Version: v.Version, Installed: installed[v.Version]}
			if a, ok := v.Platforms[pluginc.PlatformTarget]; ok {
				vv.URL = a.URL
				if vv.URL == "" && len(a.URLs) > 0 {
					vv.URL = a.URLs[0]
				}
				vv.SHA256 = a.SHA256
				vv.Size = a.Size
			}
			infos = append(infos, vv)
		}
		pv := PluginView{
			ID: m.ID, Name: m.Name, Kind: m.Kind,
			Category: m.Category, Description: m.Description,
			Versions: vers,
			InstalledVersions: installedVers,
			Workers: m.Workers,
			HasInstance: hasInst[m.ID],
			MultiRun: m.MultiRun,
			VersionInfos: infos,
		}
		if len(m.Versions) > 0 {
			pv.DefaultVersion = m.Versions[0].Version
		}
		pv.Manage = manageView(m.Manage)
		out = append(out, pv)
	}
	return out
}

// manageView 清单 manage 段 → 视图（panel 为空视为无管理面）。
func manageView(m *pluginc.Manage) *ManageView {
	if m == nil || m.Panel == "" {
		return nil
	}
	return &ManageView{Panel: m.Panel}
}

// ManifestWarnings 清单加载告警（未知字段等，UI 可展示）。
func (s *PluginCenterService) ManifestWarnings() []string {
	if s.mgr == nil {
		return nil
	}
	return s.mgr.ManifestWarnings()
}

// DataRoot 数据根目录（运行时/实例/站点/日志全在其下）。
// 默认便携：exe 同级 data\；仅 exe 目录不可写时回退 %LOCALAPPDATA%\Duihao。
func (s *PluginCenterService) DataRoot() string { return duihao.DataRoot() }

// IsPortable 数据是否在 exe 同级 data\（便携模式）。
func (s *PluginCenterService) IsPortable() bool { return duihao.Portable() }

// OpenDataRoot 在资源管理器中打开数据目录。
func (s *PluginCenterService) OpenDataRoot() error {
	if err := duihao.EnsureDirs(); err != nil {
		return err
	}
	return openPath(duihao.DataRoot())
}

// Install 为 cli/embedded 插件取包（process 插件请用 CreateInstance）。
func (s *PluginCenterService) Install(pluginID, version string) (string, error) {
	if s.mgr == nil {
		return "", errNotReady()
	}
	return s.mgr.InstallRuntime(context.Background(), pluginID, version)
}

// RuntimeProgress 取包进度快照（前端在「安装中」时轮询，500ms 一次）。
// 下载 244MB 的 MySQL 包要几分钟，没有进度用户只能猜「是不是卡死了」。
func (s *PluginCenterService) RuntimeProgress() []runtimepkg.Progress {
	return runtimepkg.Snapshot()
}

// CreateInstance 装配并注册实例。
// confirmed：exec-once 用户确认过的动作下标（上一次返回 ErrConfirmNeeded 后重放）。
func (s *PluginCenterService) CreateInstance(pluginID, version, name string, confirmed []int) (*instance.View, error) {
	if s.mgr == nil {
		return nil, errNotReady()
	}
	cf := map[string]bool{}
	for _, i := range confirmed {
		cf[strconv.Itoa(i)] = true
	}
	rec, err := s.mgr.Create(pluginID, version, name, cf)
	if err != nil {
		return nil, err
	}
	return s.view(rec.ID)
}

// ListInstances 全部实例视图。
func (s *PluginCenterService) ListInstances() []instance.View {
	if s.mgr == nil {
		return nil
	}
	return s.mgr.List()
}

// StartInstance 启动实例。
// PHP 实例启动时端口可能被自愈换掉（老记录撞端口），引用它的站点片段要跟着
// 重渲染，否则 nginx 还指向旧端口，页面照样 502。
func (s *PluginCenterService) StartInstance(id string) error {
	if s.mgr == nil {
		return errNotReady()
	}
	if err := s.mgr.Start(id); err != nil {
		return err
	}
	s.rebuildSitesUsing(id)
	return nil
}

// rebuildSitesUsing 重渲染引用了该实例的站点片段（实例端口变化后保持一致）。
func (s *PluginCenterService) rebuildSitesUsing(id string) {
	if s.sites == nil {
		return
	}
	for _, sid := range s.sites.ReferencesInstance(id) {
		_ = s.sites.Rebuild(sid)
	}
}

// StopInstance 停止实例。
func (s *PluginCenterService) StopInstance(id string) error {
	if s.mgr == nil {
		return errNotReady()
	}
	return s.mgr.Stop(id)
}

// RemoveInstance 卸载实例（数据目录删除；runtime 共享保留）。
// 引用完整性：被站点引用时拒绝，并列出站点 id（改绑或先删站点）。
func (s *PluginCenterService) RemoveInstance(id string) error {
	if s.mgr == nil {
		return errNotReady()
	}
	if s.sites != nil {
		if refs := s.sites.ReferencesInstance(id); len(refs) > 0 {
			return fmt.Errorf("实例 %s 仍被站点引用（%s）—— 请先在站点管理中删除或改绑这些站点", id, strings.Join(refs, ", "))
		}
	}
	return s.mgr.Remove(id)
}

// SetInstancePort 修改实例端口（如 nginx 的 http 端口，默认 80）。
// 运行中会自动重渲染配置并 reload，失败回滚；撞上已有站点端口则拒绝。
func (s *PluginCenterService) SetInstancePort(id, role string, port int) (*instance.View, error) {
	if s.mgr == nil {
		return nil, errNotReady()
	}
	if role == "http" && s.sites != nil {
		if sid := s.sites.PortInUseBy(id, port); sid != "" {
			return nil, fmt.Errorf("端口 %d 已被站点 %s 占用 —— 请先改该站点端口", port, sid)
		}
	}
	if err := s.mgr.SetPort(id, role, port); err != nil {
		return nil, err
	}
	// 端口变了：引用它的站点片段按新端口重写（PHP 换 fcgi 端口尤其要）
	s.rebuildSitesUsing(id)
	return s.view(id)
}

// InstanceLogs 子进程日志尾部。
func (s *PluginCenterService) InstanceLogs(id string, tail int) (string, error) {
	if s.mgr == nil {
		return "", errNotReady()
	}
	return s.mgr.Logs(id, tail)
}

// CliResult cli 调用结果（前端展示）。
type CliResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// InvokeCli 执行 cli 插件（如 composer）。
func (s *PluginCenterService) InvokeCli(cliID, version string, args []string, cwd string, timeoutSec int) (*CliResult, error) {
	if s.mgr == nil {
		return nil, errNotReady()
	}
	res, err := s.mgr.InvokeCli(context.Background(), cliID, version, args, cwd, timeoutSec)
	if err != nil {
		return nil, err
	}
	return &CliResult{ExitCode: res.ExitCode, Stdout: res.Stdout, Stderr: res.Stderr}, nil
}

// ---- 内部 ----

func (s *PluginCenterService) view(id string) (*instance.View, error) {
	for _, v := range s.mgr.List() {
		if v.ID == id {
			return &v, nil
		}
	}
	return nil, errNotReady()
}

func errNotReady() error { return &pluginCenterError{"环境工具尚未就绪"} }

type pluginCenterError struct{ msg string }

func (e *pluginCenterError) Error() string { return e.msg }

// 编译期引用校验。
var (
	_ = pluginc.KindProcess
	_ = clirun.Result{}
)
