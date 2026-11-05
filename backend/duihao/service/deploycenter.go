// 一键部署服务（M20）：把站点层 + 部署器 + 数据库凭证 串起来。
package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	duihao "github.com/duihao/duihao/backend/duihao"
	"github.com/duihao/duihao/backend/duihao/audit"
	"github.com/duihao/duihao/backend/duihao/deploy"
	"github.com/duihao/duihao/backend/duihao/site"
)

// DeployEventName 部署进度事件名（前端 EventsOn 监听）。
const DeployEventName = "duihao:deploy-progress"

// DeployAppView 程序模板 + 该站点的部署状态（供列表直接渲染）。
type DeployAppView struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	Category        string   `json:"category"`
	Summary         string   `json:"summary"`
	Homepage        string   `json:"homepage,omitempty"`
	License         string   `json:"license,omitempty"`
	RequiresPHP     string   `json:"requiresPhp,omitempty"`
	RequiresDB      string   `json:"requiresDb,omitempty"`
	RequiresExt     []string `json:"requiresExt,omitempty"`
	Entry           string   `json:"entry"`
	Note            string   `json:"note,omitempty"`
	Deployed        bool     `json:"deployed"`
	DeployedVersion string   `json:"deployedVersion,omitempty"`
	DeployedAt      string   `json:"deployedAt,omitempty"`
	// Blocked 非空 = 这个站点当前不能装（说明原因，前端置灰按钮）。
	Blocked string `json:"blocked,omitempty"`
}

// DeployService 一键部署服务。
type DeployService struct {
	mgr   *site.Manager
	mysql *MySQLService
	audit *audit.Logger
	emit  func(event string, data any)

	mu      sync.Mutex
	running map[string]bool // 正在部署的站点（同一站点不并发部署）
}

// NewDeployService 构建部署服务。
// emit 为 nil 时静默（测试/无界面场景）。
func NewDeployService(mgr *site.Manager, mysql *MySQLService, a *audit.Logger, emit func(event string, data any)) *DeployService {
	return &DeployService{mgr: mgr, mysql: mysql, audit: a, emit: emit, running: map[string]bool{}}
}

// DeployPkgDir 本地安装包存放目录（放进去即可离线部署）。
func DeployPkgDir() string { return filepath.Join(duihao.DataRoot(), "deploy", "pkgs") }

// OpenDeployPkgDir 打开本地安装包目录（不存在则创建）。
func (s *DeployService) OpenDeployPkgDir() (string, error) {
	dir := DeployPkgDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, openPath(dir)
}

// ListDeployApps 程序模板列表 + 该站点部署状态。
func (s *DeployService) ListDeployApps(siteID string) ([]DeployAppView, error) {
	if s.mgr == nil {
		return nil, fmt.Errorf("站点管理尚未就绪")
	}
	view, err := s.mgr.ViewOf(siteID)
	if err != nil {
		return nil, err
	}
	marker, deployed := deploy.ReadMarker(view.Root)
	out := make([]DeployAppView, 0, len(deploy.Catalog()))
	for _, a := range deploy.Catalog() {
		v := DeployAppView{
			ID: a.ID, Name: a.Name, Version: a.Version, Category: a.Category,
			Summary: a.Summary, Homepage: a.Homepage, License: a.License,
			RequiresPHP: a.Requires.PHP, RequiresDB: a.Requires.DB,
			RequiresExt: a.Requires.Extensions,
			Entry: a.Install.Entry, Note: a.Install.Note,
		}
		if deployed && marker.AppID == a.ID {
			v.Deployed = true
			v.DeployedVersion = marker.AppVersion
			v.DeployedAt = marker.DeployedAt
		}
		if a.Requires.PHP != "" && view.PHP == "" {
			v.Blocked = "该程序需要 PHP：请先在站点设置里选择一个 PHP 版本"
		} else if a.Requires.DB != "" && view.DB == "" {
			// 不阻断：装完再绑库也常见，只是安装向导要手填
			v.Blocked = ""
		}
		out = append(out, v)
	}
	return out, nil
}

// DeployApp 把程序装到站点根目录。
//
// localZip 非空 = 用用户挑的本地安装包；entry 非空 = 覆盖安装入口（自定义程序）；
// force = 根目录非空也继续（覆盖同名文件）。
// 进度通过 DeployEventName 事件推送，完成后返回结果（含安装向导地址）。
func (s *DeployService) DeployApp(siteID, appID, localZip, entry string, force bool) (*deploy.Result, error) {
	if s.mgr == nil {
		return nil, fmt.Errorf("站点管理尚未就绪")
	}
	app, err := deploy.Find(appID)
	if err != nil {
		return nil, err
	}
	view, err := s.mgr.ViewOf(siteID)
	if err != nil {
		return nil, err
	}
	if app.Requires.PHP != "" && view.PHP == "" {
		return nil, fmt.Errorf("%s 需要 PHP 运行环境：请先在站点设置里选择 PHP 版本", app.Name)
	}

	s.mu.Lock()
	if s.running[siteID] {
		s.mu.Unlock()
		return nil, fmt.Errorf("该站点正在部署中，请等待完成")
	}
	s.running[siteID] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, siteID)
		s.mu.Unlock()
	}()

	// 数据库凭证：优先「该库自己的账号」，回退 root（与「连接数据库」同一套逻辑）。
	// 拿不到不影响部署——只是安装向导要手填数据库信息。
	var target *deploy.Target
	dbWarning := ""
	if view.DB != "" && view.Database != "" {
		if info, err := s.mysql.MySQLConnectionInfo(view.DB, view.Database); err == nil && info != nil {
			target = &deploy.Target{
				Host: "127.0.0.1", Port: info.Port,
				Database: view.Database, User: info.User, Password: info.Password,
			}
		} else if err != nil {
			dbWarning = "读取数据库凭证失败（" + err.Error() + "），安装向导需手填"
		}
	}

	onStep := func(st deploy.Step) {
		if s.emit == nil {
			return
		}
		s.emit(DeployEventName, map[string]any{
			"siteId": siteID, "appId": appID,
			"phase": st.Phase, "percent": st.Percent, "message": st.Message,
		})
	}

	siteName := view.Name
	if siteName == "" && len(view.Domains) > 0 {
		siteName = view.Domains[0]
	}
	res, err := deploy.Deploy(context.Background(), deploy.Options{
		AppID: appID, Root: view.Root, SiteName: siteName,
		BaseURL: view.URL, DB: target, LocalZip: localZip, Force: force,
		EntryOverride: entry,
		OnStep:        onStep,
	})
	if err != nil {
		if s.audit != nil {
			s.audit.Fail("ui", "site.deploy", siteID, map[string]any{"app": appID, "error": err.Error()})
		}
		return nil, err
	}
	if dbWarning != "" {
		res.Warning = strings.TrimSpace(res.Warning + " " + dbWarning)
	}
	if s.audit != nil {
		s.audit.Ok("ui", "site.deploy", siteID, map[string]any{
			"app": appID, "version": res.Version, "files": res.Files, "source": res.Source,
		})
	}
	return res, nil
}

// OpenDeploySetup 打开安装向导：站点没在跑就先启动（否则浏览器打不开）。
func (s *DeployService) OpenDeploySetup(siteID, entry string) error {
	if s.mgr == nil {
		return fmt.Errorf("站点管理尚未就绪")
	}
	view, err := s.mgr.ViewOf(siteID)
	if err != nil {
		return err
	}
	if view.URL == "" {
		return fmt.Errorf("站点还没有可用地址（Web 实例未分配端口）")
	}
	if view.Live != "running" {
		if err := s.mgr.Start(siteID); err != nil {
			return fmt.Errorf("启动站点失败: %w", err)
		}
	}
	base := view.URL
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return openExternal(base + strings.TrimPrefix(entry, "/"))
}
