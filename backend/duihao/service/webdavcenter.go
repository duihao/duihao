// WebDAV 共享管理服务（M5）：多共享增删改 + 连接信息 + 从站点目录选根目录。
//
// 与 FTPService 同构：共享表是唯一真相（instances/<id>/webdavshares.json），
// 改动后重启实例让内置服务重新加载（WebDAV 长连接为主，重启前提示前端）。
package service

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/audit"
	"github.com/ys-ll/uniterm/backend/duihao/instance"
	"github.com/ys-ll/uniterm/backend/duihao/processd"
	"github.com/ys-ll/uniterm/backend/duihao/site"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd"
)

// WebDavService 共享管理。
type WebDavService struct {
	mgr   *instance.Manager
	sites *site.Manager // 可选：「从站点选择根目录」用
	audit *audit.Logger
}

// NewWebDavService 构建服务。
func NewWebDavService(mgr *instance.Manager, a *audit.Logger) *WebDavService {
	return &WebDavService{mgr: mgr, audit: a}
}

// SetSiteManager 注入站点管理器（可选能力）。
func (s *WebDavService) SetSiteManager(sites *site.Manager) { s.sites = sites }

// WebDavShareView 共享列表一行（不含密码与哈希）。
type WebDavShareView struct {
	ID        string `json:"id"`
	User      string `json:"user"`
	Name      string `json:"name,omitempty"`
	Root      string `json:"root"`
	ReadOnly  bool   `json:"readonly"`
	Enabled   bool   `json:"enabled"`
	Note      string `json:"note,omitempty"`
	CreatedAt string `json:"createdAt"`
}

// WebDavConnInfo 连接信息卡（前端展示「往哪挂载」）。
type WebDavConnInfo struct {
	Scheme  string `json:"scheme"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	URL     string `json:"url"`     // http://127.0.0.1:9111/ 或 https://局域网IP:9111/
	TLS     bool   `json:"tls"`
	Running bool   `json:"running"`
}

// SiteRootView 复用 ftpcenter.go 里的定义（同包）。

func (s *WebDavService) dir(instanceID string) (string, error) {
	if s.mgr == nil {
		return "", fmt.Errorf("网盘管理尚未就绪")
	}
	rec := s.mgr.GetRecord(instanceID)
	if rec == nil {
		return "", fmt.Errorf("实例不存在: %s", instanceID)
	}
	if rec.Plugin != "webdav" {
		return "", fmt.Errorf("实例 %s 不是 WebDAV 网盘（%s）", instanceID, rec.Plugin)
	}
	return s.mgr.InstanceDirPath(instanceID), nil
}

// ListWebDavShares 共享列表。表为空时返回设置默认提示行（同 FTP 逻辑）。
func (s *WebDavService) ListWebDavShares(instanceID string) ([]WebDavShareView, error) {
	dir, err := s.dir(instanceID)
	if err != nil {
		return nil, err
	}
	shares, err := webdavd.LoadShares(dir)
	if err != nil {
		return nil, err
	}
	out := make([]WebDavShareView, 0, len(shares))
	for _, sh := range shares {
		out = append(out, WebDavShareView{
			ID: sh.ID, User: sh.User, Name: sh.Name, Root: sh.Root,
			ReadOnly: sh.ReadOnly, Enabled: sh.Enabled, Note: sh.Note, CreatedAt: sh.CreatedAt,
		})
	}
	return out, nil
}

// CreateWebDavShare 新增共享。
func (s *WebDavService) CreateWebDavShare(instanceID, user, password, root string, readonly bool, name, note string) error {
	dir, err := s.dir(instanceID)
	if err != nil {
		return err
	}
	shares, err := webdavd.LoadShares(dir)
	if err != nil {
		return err
	}
	sh, err := webdavd.NewShare(user, password, root, readonly, name, note)
	if err != nil {
		return err
	}
	for _, ex := range shares {
		if strings.EqualFold(ex.User, sh.User) {
			return fmt.Errorf("用户名 %s 已存在", sh.User)
		}
	}
	shares = append(shares, sh)
	if err := webdavd.SaveShares(dir, shares); err != nil {
		return err
	}
	s.audit.Ok("ui", "webdav.create-share", instanceID, map[string]any{"user": sh.User, "root": sh.Root, "readonly": readonly})
	return s.restartIfRunning(instanceID)
}

// UpdateWebDavShare 改共享基本信息（不改密码）。
func (s *WebDavService) UpdateWebDavShare(instanceID, shareID, user, root string, readonly, enabled bool, name, note string) error {
	dir, err := s.dir(instanceID)
	if err != nil {
		return err
	}
	shares, err := webdavd.LoadShares(dir)
	if err != nil {
		return err
	}
	idx := -1
	for i := range shares {
		if shares[i].ID == shareID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("共享不存在: %s", shareID)
	}
	user = strings.TrimSpace(user)
	for i := range shares {
		if i != idx && strings.EqualFold(shares[i].User, user) {
			return fmt.Errorf("用户名 %s 已被其它共享使用", user)
		}
	}
	shares[idx].User = user
	shares[idx].Root = strings.TrimSpace(root)
	shares[idx].Name = strings.TrimSpace(name)
	shares[idx].Note = strings.TrimSpace(note)
	shares[idx].ReadOnly = readonly
	shares[idx].Enabled = enabled
	if err := shares[idx].Validate(); err != nil {
		return err
	}
	if err := webdavd.SaveShares(dir, shares); err != nil {
		return err
	}
	s.audit.Ok("ui", "webdav.update-share", instanceID, map[string]any{
		"user": shares[idx].User, "root": shares[idx].Root,
		"readonly": readonly, "enabled": enabled,
	})
	return s.restartIfRunning(instanceID)
}

// SetWebDavSharePassword 改密码。
func (s *WebDavService) SetWebDavSharePassword(instanceID, shareID, password string) error {
	dir, err := s.dir(instanceID)
	if err != nil {
		return err
	}
	if password == "" {
		return fmt.Errorf("密码不能为空")
	}
	shares, err := webdavd.LoadShares(dir)
	if err != nil {
		return err
	}
	ok := false
	for i := range shares {
		if shares[i].ID == shareID {
			hash, herr := webdavd.HashPassword(password)
			if herr != nil {
				return herr
			}
			shares[i].PassHash = hash
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("共享不存在: %s", shareID)
	}
	if err := webdavd.SaveShares(dir, shares); err != nil {
		return err
	}
	s.audit.Ok("ui", "webdav.set-password", instanceID, map[string]any{"id": shareID})
	return s.restartIfRunning(instanceID)
}

// DeleteWebDavShare 删共享。
func (s *WebDavService) DeleteWebDavShare(instanceID, shareID string) error {
	dir, err := s.dir(instanceID)
	if err != nil {
		return err
	}
	shares, err := webdavd.LoadShares(dir)
	if err != nil {
		return err
	}
	out := make([]webdavd.Share, 0, len(shares))
	found := false
	for _, sh := range shares {
		if sh.ID == shareID {
			found = true
			continue
		}
		out = append(out, sh)
	}
	if !found {
		return fmt.Errorf("共享不存在: %s", shareID)
	}
	if err := webdavd.SaveShares(dir, out); err != nil {
		return err
	}
	s.audit.Ok("ui", "webdav.delete-share", instanceID, map[string]any{"id": shareID})
	return s.restartIfRunning(instanceID)
}

// GetWebDavConnInfo 连接信息（URL 拼装给前端展示/复制）。
func (s *WebDavService) GetWebDavConnInfo(instanceID string) (*WebDavConnInfo, error) {
	if s.mgr == nil {
		return nil, fmt.Errorf("网盘管理尚未就绪")
	}
	rec := s.mgr.GetRecord(instanceID)
	if rec == nil {
		return nil, fmt.Errorf("实例不存在: %s", instanceID)
	}
	scheme := "http"
	tls := false
	if values, err := s.mgr.EffectiveSettings(instanceID); err == nil {
		tls = values["dav.tls"] == "1"
		if tls {
			scheme = "https"
		}
	}
	host := "127.0.0.1"
	if values, err := s.mgr.EffectiveSettings(instanceID); err == nil && strings.TrimSpace(values["dav.bind"]) != "" {
		if values["dav.bind"] != "127.0.0.1" {
			// 局域网监听时展示本机 IPv4（127.0.0.1 挂不了手机）。
			if ip := firstLANIPv4(); ip != "" {
				host = ip
			} else {
				host = "本机IP"
			}
		}
	}
	port := rec.Ports["dav"]
	running := s.mgr.Status(instanceID) == processd.StateRunning
	return &WebDavConnInfo{
		Scheme: scheme, Host: host, Port: port,
		URL: fmt.Sprintf("%s://%s:%d/", scheme, host, port),
		TLS: tls, Running: running,
	}, nil
}

// ListWebDavSiteRoots 站点根目录候选（「浏览」旁的下拉）。
func (s *WebDavService) ListWebDavSiteRoots() []SiteRootView {
	if s.sites == nil {
		return []SiteRootView{}
	}
	out := []SiteRootView{}
	for _, v := range s.sites.List() {
		out = append(out, SiteRootView{ID: v.ID, Name: v.Name, Root: v.Root})
	}
	return out
}

// restartIfRunning 共享表变化后让内置服务重新加载（停 → 等端口释放 → 启）。
func (s *WebDavService) restartIfRunning(instanceID string) error {
	if s.mgr == nil || s.mgr.Status(instanceID) != processd.StateRunning {
		return nil
	}
	port := 0
	if rec := s.mgr.GetRecord(instanceID); rec != nil {
		port = rec.Ports["dav"]
	}
	if err := s.mgr.Stop(instanceID); err != nil {
		return fmt.Errorf("共享已保存，但停止网盘服务失败: %w", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for port > 0 && !processd.ProbeFree(port) {
		if time.Now().After(deadline) {
			return fmt.Errorf("共享已保存，但端口 %d 释放超时，请手动重启网盘实例", port)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := s.mgr.Start(instanceID); err != nil {
		return fmt.Errorf("共享已保存，但重启网盘服务失败（请手动启动）: %w", err)
	}
	return nil
}

// firstLANIPv4 本机第一个非回环 IPv4（局域网挂载提示用）。
func firstLANIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
			return ipn.IP.String()
		}
	}
	return ""
}
