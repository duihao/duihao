// Package service 对号站点管理（M4）的 Wails 服务绑定。
package service

import (
	"fmt"

	"github.com/ys-ll/uniterm/backend/duihao/site"
)

// SiteService 站点管理服务。
type SiteService struct {
	mgr *site.Manager
}

// NewSiteService 构建站点服务（实例/站点管理器在 main 中统一创建后注入）。
func NewSiteService(mgr *site.Manager) *SiteService { return &SiteService{mgr: mgr} }

func (s *SiteService) ready() error {
	if s.mgr == nil {
		return fmt.Errorf("站点管理尚未就绪")
	}
	return nil
}

// ListSites 全部站点视图。
func (s *SiteService) ListSites() []site.View {
	if s.mgr == nil {
		return nil
	}
	return s.mgr.List()
}

// CreateSite 新建站点。port=0 时 http 默认 80 / https 默认 443（被占用自动顺延）；
// domains 留空自动用 <站点id>.test。tls=true 会签发自签名证书（浏览器会提示不安全）。
func (s *SiteService) CreateSite(name string, domains []string, port int, root, web, php, db, database string, tls bool, options site.Options) (*site.View, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	rec, err := s.mgr.Create(site.CreateOpts{
		Name: name, Domains: domains, Port: port, Root: root,
		Web: web, PHP: php, DB: db, Database: database,
		TLS: tls, Options: options,
	})
	if err != nil {
		return nil, err
	}
	return s.mgr.ViewOf(rec.ID)
}

// SiteDefaultRoot 新建站点的默认根目录（数据目录下的 www），前端用来预填。
func (s *SiteService) SiteDefaultRoot() string { return site.RootDir() }

// SiteDefaultPorts 两种协议的默认端口（前端切 http/https 时填）。
func (s *SiteService) SiteDefaultPorts() map[string]int {
	return map[string]int{"http": site.DefaultSitePort, "https": site.DefaultSiteTLSPort}
}

// SetSitePort 修改站点监听端口（重渲染片段 + reload；失败自动回滚）。
func (s *SiteService) SetSitePort(id string, port int) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.mgr.SetPort(id, port)
}

// DeleteSite 删除站点（只删 nginx 片段与记录，根目录保留）。
func (s *SiteService) DeleteSite(id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.mgr.Delete(id)
}

// StartSite 启动站点（按序拉起引用实例）。
func (s *SiteService) StartSite(id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.mgr.Start(id)
}

// StopSite 停止站点。
func (s *SiteService) StopSite(id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.mgr.Stop(id)
}

// SiteLogs 站点日志（web 实例进程日志尾部）。
func (s *SiteService) SiteLogs(id string, tail int) (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	return s.mgr.Logs(id, tail)
}

// SiteURL 站点访问地址（前端 window.open 用）。
func (s *SiteService) SiteURL(id string) (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	v, err := s.mgr.ViewOf(id)
	if err != nil {
		return "", err
	}
	if v.URL == "" {
		return "", fmt.Errorf("站点 %s 还没有可用地址（Web 实例未分配端口）", id)
	}
	return v.URL, nil
}

// OpenSite 用系统默认浏览器打开站点地址。
func (s *SiteService) OpenSite(id string) error {
	url, err := s.SiteURL(id)
	if err != nil {
		return err
	}
	return openExternal(url)
}

// HostsLine 站点需要的 hosts 行（复制粘贴路径）。
func (s *SiteService) HostsLine(id string) (string, error) {
	if err := s.ready(); err != nil {
		return "", err
	}
	for _, v := range s.mgr.List() {
		if v.ID == id {
			return site.HostsLine(&v.Record), nil
		}
	}
	return "", fmt.Errorf("站点不存在: %s", id)
}

// WriteHosts 提权写入 hosts（会弹一次 UAC）。
func (s *SiteService) WriteHosts(id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.mgr.WriteHosts(id)
}

// RebuildSite 重新渲染片段（域名/根目录/栈变更后）。
func (s *SiteService) RebuildSite(id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.mgr.Rebuild(id)
}

// UpdateSite 修改站点配置（域名/端口/根目录/栈/https/各开关）。
// options 传站点完整配置（前端表单整份提交；未改的项保持原值）。
func (s *SiteService) UpdateSite(id string, name string, domains []string, port int, root, php, db, database string, tls bool, options site.Options) (*site.View, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.mgr.Update(id, site.UpdateOpts{
		Name: name, Domains: domains, Port: port, Root: root,
		PHP: php, DB: db, Database: database, TLS: tls, Options: options,
	}); err != nil {
		return nil, err
	}
	return s.mgr.ViewOf(id)
}

// OpenSiteRoot 在文件管理器中打开站点根目录。
func (s *SiteService) OpenSiteRoot(id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.mgr.OpenRoot(id)
}

// CreateSiteErrorPages 按「错误页面」配置在根目录生成静态错误页，返回生成的文件。
func (s *SiteService) CreateSiteErrorPages(id string) ([]string, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	return s.mgr.CreateErrorPages(id)
}

// SitesReferencingInstance 引用了该实例的站点（前端删除实例前提示）。
func (s *SiteService) SitesReferencingInstance(instanceID string) []string {
	if s.mgr == nil {
		return nil
	}
	return s.mgr.ReferencesInstance(instanceID)
}

// LocalCAStatus 本地根 CA 状态（https「绿锁」的前提）。
func (s *SiteService) LocalCAStatus() site.CAStatus { return site.LocalCAStatus() }

// LocalCACertPath 根证书文件路径（给 Firefox 手动导入用）。
func (s *SiteService) LocalCACertPath() string {
	cert, _ := site.CAPaths()
	return cert
}

// InstallLocalCA 把根证书装进当前用户的受信任根存储（免 UAC），
// 并把已有 https 站点证书全部重签一遍（否则老的自签证书装完也不变绿）。
func (s *SiteService) InstallLocalCA() (int, error) {
	if err := site.InstallCA(); err != nil {
		return 0, err
	}
	if s.mgr == nil {
		return 0, nil
	}
	return s.mgr.ReissueTLSCerts()
}

// UninstallLocalCA 撤销根证书（站点 https 会回到「不受信任」状态）。
func (s *SiteService) UninstallLocalCA() error { return site.UninstallCA() }
