// FTP 账号管理服务（M11）：多账号增删改 + 从站点目录选根目录。
//
// 账号表是唯一真相（instances/<id>/ftpaccounts.json），改动后需要让内置 FTP
// 服务重新加载：这里直接重启实例（FTP 是短连接、重启 200ms，代价远小于给
// instance 层加一套「热更新具体实现」的接口）。
package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/audit"
	"github.com/ys-ll/uniterm/backend/duihao/ftpd"
	"github.com/ys-ll/uniterm/backend/duihao/instance"
	"github.com/ys-ll/uniterm/backend/duihao/processd"
	"github.com/ys-ll/uniterm/backend/duihao/site"
)

// FTPService FTP 账号管理。
type FTPService struct {
	mgr   *instance.Manager
	sites *site.Manager // 可选：「从站点选择根目录」用
	audit *audit.Logger
}

// NewFTPService 构建服务。
func NewFTPService(mgr *instance.Manager, a *audit.Logger) *FTPService {
	return &FTPService{mgr: mgr, audit: a}
}

// SetSiteManager 注入站点管理器（可选能力）。
func (s *FTPService) SetSiteManager(sites *site.Manager) { s.sites = sites }

// FTPAccountView 账号列表一行（不含密码与哈希）。
type FTPAccountView struct {
	ID        string `json:"id"`
	User      string `json:"user"`
	Root      string `json:"root"`
	ReadOnly  bool   `json:"readonly"`
	Enabled   bool   `json:"enabled"`
	Note      string `json:"note,omitempty"`
	CreatedAt string `json:"createdAt"`
}

// SiteRootView 「从站点选择」下拉项。
type SiteRootView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Root string `json:"root"`
}

func (s *FTPService) dir(instanceID string) (string, error) {
	if s.mgr == nil {
		return "", fmt.Errorf("FTP 管理尚未就绪")
	}
	rec := s.mgr.GetRecord(instanceID)
	if rec == nil {
		return "", fmt.Errorf("实例不存在: %s", instanceID)
	}
	if rec.Plugin != "ftpserver" {
		return "", fmt.Errorf("实例 %s 不是 FTPServer（%s）", instanceID, rec.Plugin)
	}
	return s.mgr.InstanceDirPath(instanceID), nil
}

// ListFTPAccounts 账号列表。
// 账号表为空时，返回设置里那个「默认账号」的只读视图——让用户知道当前
// 用哪套凭据在登录，同时引导他建第一个正式账号（建完这条就不再生效）。
func (s *FTPService) ListFTPAccounts(instanceID string) ([]FTPAccountView, error) {
	dir, err := s.dir(instanceID)
	if err != nil {
		return nil, err
	}
	accounts, err := ftpd.LoadAccounts(dir)
	if err != nil {
		return nil, err
	}
	out := make([]FTPAccountView, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, FTPAccountView{
			ID: a.ID, User: a.User, Root: a.Root, ReadOnly: a.ReadOnly,
			Enabled: a.Enabled, Note: a.Note, CreatedAt: a.CreatedAt,
		})
	}
	if len(out) == 0 {
		values, verr := s.mgr.EffectiveSettings(instanceID)
		if verr == nil {
			out = append(out, FTPAccountView{
				ID:       "legacy",
				User:     firstNonEmpty(values["ftp.user"], "duihao"),
				Root:     firstNonEmpty(values["ftp.root"], "{dir}/www"),
				ReadOnly: values["ftp.readonly"] == "1",
				Enabled:  true,
				Note:     "来自实例设置（旧版单账号）；新增账号后此账号由账号列表接管",
			})
			return out, nil
		}
	}
	return out, nil
}

// CreateFTPAccount 新增账号。
func (s *FTPService) CreateFTPAccount(instanceID, user, password, root string, readonly bool, note string) error {
	dir, err := s.dir(instanceID)
	if err != nil {
		return err
	}
	acc, err := ftpd.NewAccount(user, password, root, readonly, note)
	if err != nil {
		return err
	}
	accounts, err := ftpd.LoadAccounts(dir)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		if strings.EqualFold(a.User, acc.User) {
			return fmt.Errorf("用户名 %s 已存在", acc.User)
		}
	}
	accounts = append(accounts, acc)
	if err := ftpd.SaveAccounts(dir, accounts); err != nil {
		return err
	}
	s.audit.Ok("ui", "ftp.create-account", instanceID, map[string]any{"user": acc.User, "root": acc.Root, "readonly": readonly})
	return s.restartIfRunning(instanceID)
}

// UpdateFTPAccount 改账号基本信息（不改密码）。
func (s *FTPService) UpdateFTPAccount(instanceID, accountID, user, root, note string, readonly, enabled bool) error {
	dir, err := s.dir(instanceID)
	if err != nil {
		return err
	}
	accounts, err := ftpd.LoadAccounts(dir)
	if err != nil {
		return err
	}
	idx := -1
	for i := range accounts {
		if accounts[i].ID == accountID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("账号不存在: %s", accountID)
	}
	user = strings.TrimSpace(user)
	for i := range accounts {
		if i != idx && strings.EqualFold(accounts[i].User, user) {
			return fmt.Errorf("用户名 %s 已被其它账号使用", user)
		}
	}
	accounts[idx].User = user
	accounts[idx].Root = strings.TrimSpace(root)
	accounts[idx].Note = strings.TrimSpace(note)
	accounts[idx].ReadOnly = readonly
	accounts[idx].Enabled = enabled
	if err := accounts[idx].Validate(); err != nil {
		return err
	}
	if err := ftpd.SaveAccounts(dir, accounts); err != nil {
		return err
	}
	s.audit.Ok("ui", "ftp.update-account", instanceID, map[string]any{
		"user": accounts[idx].User, "root": accounts[idx].Root,
		"readonly": readonly, "enabled": enabled,
	})
	return s.restartIfRunning(instanceID)
}

// SetFTPAccountPassword 改密码。
func (s *FTPService) SetFTPAccountPassword(instanceID, accountID, password string) error {
	dir, err := s.dir(instanceID)
	if err != nil {
		return err
	}
	if password == "" {
		return fmt.Errorf("密码不能为空")
	}
	accounts, err := ftpd.LoadAccounts(dir)
	if err != nil {
		return err
	}
	ok := false
	for i := range accounts {
		if accounts[i].ID == accountID {
			hash, herr := ftpd.HashPassword(password)
			if herr != nil {
				return herr
			}
			accounts[i].PassHash = hash
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("账号不存在: %s", accountID)
	}
	if err := ftpd.SaveAccounts(dir, accounts); err != nil {
		return err
	}
	s.audit.Ok("ui", "ftp.set-password", instanceID, map[string]any{"id": accountID})
	return s.restartIfRunning(instanceID)
}

// DeleteFTPAccount 删账号（至少保留一个启用账号，否则服务起不来）。
func (s *FTPService) DeleteFTPAccount(instanceID, accountID string) error {
	dir, err := s.dir(instanceID)
	if err != nil {
		return err
	}
	accounts, err := ftpd.LoadAccounts(dir)
	if err != nil {
		return err
	}
	out := make([]ftpd.Account, 0, len(accounts))
	found := false
	for _, a := range accounts {
		if a.ID == accountID {
			found = true
			continue
		}
		out = append(out, a)
	}
	if !found {
		return fmt.Errorf("账号不存在: %s", accountID)
	}
	enabled := 0
	for _, a := range out {
		if a.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return fmt.Errorf("至少要保留一个启用的账号")
	}
	if err := ftpd.SaveAccounts(dir, out); err != nil {
		return err
	}
	s.audit.Ok("ui", "ftp.delete-account", instanceID, map[string]any{"id": accountID})
	return s.restartIfRunning(instanceID)
}

// ListFTPSiteRoots 站点根目录候选（「浏览」旁的下拉）。
func (s *FTPService) ListFTPSiteRoots() []SiteRootView {
	if s.sites == nil {
		return []SiteRootView{}
	}
	out := []SiteRootView{}
	for _, v := range s.sites.List() {
		out = append(out, SiteRootView{ID: v.ID, Name: v.Name, Root: v.Root})
	}
	return out
}

// restartIfRunning 账号表变化后让内置服务重新加载（停 → 等端口释放 → 启）。
func (s *FTPService) restartIfRunning(instanceID string) error {
	if s.mgr == nil || s.mgr.Status(instanceID) != processd.StateRunning {
		return nil
	}
	port := 0
	if rec := s.mgr.GetRecord(instanceID); rec != nil {
		port = rec.Ports["ftp"]
	}
	if err := s.mgr.Stop(instanceID); err != nil {
		return fmt.Errorf("账号已保存，但停止 FTP 服务失败: %w", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for port > 0 && !processd.ProbeFree(port) {
		if time.Now().After(deadline) {
			return fmt.Errorf("账号已保存，但端口 %d 释放超时，请手动重启 FTP 实例", port)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := s.mgr.Start(instanceID); err != nil {
		return fmt.Errorf("账号已保存，但重启 FTP 服务失败（请手动启动）: %w", err)
	}
	return nil
}
