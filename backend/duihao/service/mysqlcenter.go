// MySQL 管理服务（M11）：把 dbadmin 的能力暴露给前端。
//
// DTO 不直接复用 dbadmin 的类型：前端 bindings 只认 service 包的导出类型，
// 这样也方便面板字段演进时不牵动内核。
package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	duihao "github.com/ys-ll/uniterm/backend/duihao"
	"github.com/ys-ll/uniterm/backend/duihao/audit"
	"github.com/ys-ll/uniterm/backend/duihao/dbadmin"
	"github.com/ys-ll/uniterm/backend/duihao/instance"
)

// MySQLService 数据库管理。
type MySQLService struct {
	mgr   *instance.Manager
	audit *audit.Logger
}

// NewMySQLService 构建服务。
func NewMySQLService(mgr *instance.Manager, a *audit.Logger) *MySQLService {
	return &MySQLService{mgr: mgr, audit: a}
}

// DatabaseView 面板一行。
type DatabaseView struct {
	Name    string   `json:"name"`
	Charset string   `json:"charset"`
	Tables  int      `json:"tables"`
	Bytes   int64    `json:"bytes"`
	Users   []string `json:"users"`
}

// MySQLStatusView 面板顶部连接状态。
type MySQLStatusView struct {
	Connected     bool   `json:"connected"`
	Version       string `json:"version"`
	Port          int    `json:"port"`
	User          string `json:"user"`
	PasswordSaved bool   `json:"passwordSaved"`
	// RootPasswordSet root 是否已设置密码。false 时管理面全部禁用：
	// 空密码的 root 等于把整台 MySQL 对本机任意程序敞开（用户要求：必须
	// 先设置 root 密码才能进行管理）。
	RootPasswordSet bool   `json:"rootPasswordSet"`
	Error           string `json:"error,omitempty"`
}

// MySQLConnectionView 「连接数据库」交给内置数据库客户端的参数。
// 只在对号进程内部流转（前端仅用于创建一次会话，不写入连接列表）。
type MySQLConnectionView struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Database string `json:"database"`
	// Version 服务端版本（客户端标签页标题/握手提示用，可为空）。
	Version string `json:"version,omitempty"`
	// UsingRoot 本次用的是实例的 root 凭证而非该库自己的账号：
	// 该库还没有记下账号密码（外部建的库），或者记下的密码已经失效。
	UsingRoot bool `json:"usingRoot"`
	// StaleUser 记过密码但连不上（在别处改过密码）的那个账号名，空 = 没这情况。
	StaleUser string `json:"staleUser,omitempty"`
}

// CreateDatabaseOptions 建库参数。
type CreateDatabaseOptions struct {
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password"`
	Charset  string `json:"charset"`
}

// BackupView 备份文件。
type BackupView struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Database  string `json:"database"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"createdAt"`
}

// params 拼出连接参数（凭证 + 路径由实例层提供）。
func (s *MySQLService) params(instanceID string) (dbadmin.Params, error) {
	if s.mgr == nil {
		return dbadmin.Params{}, fmt.Errorf("数据库管理尚未就绪")
	}
	rec := s.mgr.GetRecord(instanceID)
	if rec == nil {
		return dbadmin.Params{}, fmt.Errorf("实例不存在: %s", instanceID)
	}
	if rec.Plugin != "mysql" {
		return dbadmin.Params{}, fmt.Errorf("实例 %s 不是 MySQL（%s）", instanceID, rec.Plugin)
	}
	instDir := s.mgr.InstanceDirPath(instanceID)
	binDir, err := s.mgr.RuntimeBinDir(instanceID)
	if err != nil {
		return dbadmin.Params{}, err
	}
	return dbadmin.Params{
		InstanceID: instanceID,
		Port:       rec.Ports["db"],
		Credential: dbadmin.LoadCredential(instDir),
		InstDir:    instDir,
		MyIni:      filepath.Join(instDir, "my.ini"),
		BinDir:     binDir,
		BackupRoot: duihao.DirBackup(),
	}, nil
}

// open 打开一个管理连接（调用方负责 Close）。不做 root 密码检查——
// 设置密码/凭证本身必须能在空密码状态下工作。
func (s *MySQLService) open(instanceID string) (*dbadmin.Client, dbadmin.Params, error) {
	p, err := s.params(instanceID)
	if err != nil {
		return nil, p, err
	}
	c, err := dbadmin.Open(p)
	if err != nil {
		return nil, p, err
	}
	return c, p, nil
}

// openManaged 管理操作专用：root 必须已设置密码。
// 首次装配的 root 是空密码，这一步把「设置密码」变成进入管理面的硬性前提；
// 不满足时给出明确引导，而不是放任空密码 root 执行建库/授权。
func (s *MySQLService) openManaged(instanceID string) (*dbadmin.Client, error) {
	p, err := s.params(instanceID)
	if err != nil {
		return nil, err
	}
	if p.Credential.Password == "" {
		return nil, fmt.Errorf("请先设置 root 密码（面板顶部的「首次使用」卡片），设置后才能管理数据库")
	}
	c, err := dbadmin.Open(p)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// MySQLStatus 连接状态 + 版本（面板打开时先调它）。
// 失败不返回 error：前端要显示「未连接」和原因，而不是弹一个错误框。
func (s *MySQLService) MySQLStatus(instanceID string) *MySQLStatusView {
	p, err := s.params(instanceID)
	if err != nil {
		return &MySQLStatusView{Error: err.Error()}
	}
	view := &MySQLStatusView{Port: p.Port, User: p.Credential.User, RootPasswordSet: p.Credential.Password != ""}
	if p.Credential.Password != "" {
		view.PasswordSaved = true
	}
	c, err := dbadmin.Open(p)
	if err != nil {
		view.Error = err.Error()
		return view
	}
	defer c.Close()
	view.Connected = true
	view.Version = c.Version()
	return view
}

// MySQLConnectionInfo 返回「连接数据库」所需的连接参数。
//
// 优先用**该库自己的账号密码**（对号建库/改密时记在 instances/<id>/dbusers.json）：
// 这样连进去就是那个库的身份，权限范围也对得上（不是 root 那种全库视角）。
// 该库没有记录（外部工具建的、老版本建的）或记下的密码已失效时，退回实例的
// root 凭证并把 usingRoot / staleUser 告诉前端，由面板提示用户补一次密码。
//
// 两种情况都需要 root 已设密码：空密码 root 等于把整台 MySQL 对本机任意程序
// 敞开（与 openManaged 同一硬性前提）。
func (s *MySQLService) MySQLConnectionInfo(instanceID, db string) (*MySQLConnectionView, error) {
	p, err := s.params(instanceID)
	if err != nil {
		return nil, err
	}
	if p.Credential.Password == "" {
		return nil, fmt.Errorf("请先设置 root 密码（面板顶部的「首次使用」卡片），设置后才能连接数据库")
	}
	// db 会进入 DSN：只接受 MySQL 标识符里安全的字符，避免拼接注入。
	if db != "" && strings.ContainsAny(db, "`'\";\\ \n\r\t") {
		return nil, fmt.Errorf("库名不合法: %s", db)
	}
	view := &MySQLConnectionView{Host: "127.0.0.1", Port: p.Port, Database: db}

	// 1) 该库自己的账号：连得上就用它
	if u, ok := dbadmin.LookupDbUser(p.InstDir, db); ok {
		up := p
		up.Credential = dbadmin.Credential{User: u.User, Password: u.Password}
		if c, err := dbadmin.Open(up); err == nil {
			view.User, view.Password = u.User, u.Password
			view.Version = c.Version()
			c.Close()
			s.audit.Ok("ui", "mysql.open-client", instanceID, map[string]any{"database": db, "user": u.User})
			return view, nil
		}
		// 记下的密码连不上（外部改过密码）：记住账号名，回退 root 让用户仍能进去改
		view.StaleUser = u.User
	}

	// 2) 回退：实例 root 凭证
	view.UsingRoot = true
	view.User, view.Password = p.Credential.User, p.Credential.Password
	if c, err := dbadmin.Open(p); err == nil {
		view.Version = c.Version()
		c.Close()
	}
	s.audit.Ok("ui", "mysql.open-client", instanceID, map[string]any{"database": db, "user": view.User, "fallback": true})
	return view, nil
}

// SaveMySQLDbUser 记住某库的账号密码（供「连接数据库」直接用该账号登录）。
//
// 先真连一次再落盘：这样密码簿里只有确定能用的组合，连接时不必试错回退。
// 主要给外部建的库用——对号不知道它原来的密码，让用户填一次就记住。
func (s *MySQLService) SaveMySQLDbUser(instanceID, db, user, password string) error {
	user = strings.TrimSpace(user)
	if user == "" {
		return fmt.Errorf("账号不能为空")
	}
	if password == "" {
		return fmt.Errorf("密码不能为空")
	}
	p, err := s.params(instanceID)
	if err != nil {
		return err
	}
	up := p
	up.Credential = dbadmin.Credential{User: user, Password: password}
	c, err := dbadmin.Open(up)
	if err != nil {
		// dbadmin 的认证失败文案是面向 root 的（「去连接设置填 root 密码」），
		// 这里换成用户此刻真正需要的信息：账号或密码不对。
		if strings.Contains(err.Error(), "拒绝登录") {
			return fmt.Errorf("账号 %s 或密码不正确", user)
		}
		return fmt.Errorf("连接失败: %w", err)
	}
	_ = c.Close()
	if err := dbadmin.SaveDbUser(p.InstDir, db, dbadmin.DbUser{User: user, Password: password}); err != nil {
		return fmt.Errorf("连接正常，但保存失败: %w", err)
	}
	s.audit.Ok("ui", "mysql.save-db-user", instanceID, map[string]any{"db": db, "user": user})
	return nil
}

// TestMySQLCredential 试连指定 root 账号（不落盘）。
func (s *MySQLService) TestMySQLCredential(instanceID, user, password string) error {
	p, err := s.params(instanceID)
	if err != nil {
		return err
	}
	p.Credential = dbadmin.Credential{User: strings.TrimSpace(user), Password: password}
	c, err := dbadmin.Open(p)
	if err != nil {
		return err
	}
	return c.Close()
}

// SaveMySQLCredential 测试通过后保存 root 凭证（下次打开面板自动连上）。
func (s *MySQLService) SaveMySQLCredential(instanceID, user, password string) error {
	p, err := s.params(instanceID)
	if err != nil {
		return err
	}
	user = strings.TrimSpace(user)
	if user == "" {
		user = dbadmin.DefaultUser
	}
	p.Credential = dbadmin.Credential{User: user, Password: password}
	c, err := dbadmin.Open(p)
	if err != nil {
		return err
	}
	_ = c.Close()
	if err := dbadmin.SaveCredential(p.InstDir, p.Credential); err != nil {
		return err
	}
	s.audit.Ok("ui", "mysql.save-credential", instanceID, map[string]any{"user": user})
	return nil
}

// ListMySQLDatabases 业务库列表。
func (s *MySQLService) ListMySQLDatabases(instanceID string) ([]DatabaseView, error) {
	c, err := s.openManaged(instanceID)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	list, err := c.ListDatabases()
	if err != nil {
		return nil, err
	}
	out := make([]DatabaseView, 0, len(list))
	for _, d := range list {
		out = append(out, DatabaseView{
			Name: d.Name, Charset: d.Charset, Tables: d.Tables, Bytes: d.Bytes, Users: d.Users,
		})
	}
	return out, nil
}

// CreateMySQLDatabase 建库 + 建用户 + 授权。
func (s *MySQLService) CreateMySQLDatabase(instanceID string, opts CreateDatabaseOptions) error {
	c, err := s.openManaged(instanceID)
	if err != nil {
		return err
	}
	defer c.Close()
	user := firstNonEmpty(opts.User, opts.Name)
	// 账号已存在时 CreateDatabase 不会重置它的密码（多库共用账号），
	// 所以只有「新账号」才把密码记进密码簿，否则会记下一个用不了的密码。
	existed := false
	if user != "" {
		if ok, e := c.UserExists(user); e == nil && ok {
			existed = true
		}
	}
	err = c.CreateDatabase(dbadmin.CreateOptions{
		Name: opts.Name, User: opts.User, Password: opts.Password, Charset: opts.Charset,
	})
	if err != nil {
		s.audit.Fail("ui", "mysql.create-db", instanceID, map[string]any{"db": opts.Name, "err": err.Error()})
		return err
	}
	// 记下该库账号密码：「连接数据库」以后直接用这个账号登录，不再借用 root。
	if !existed && opts.Password != "" && user != "" {
		if p, e := s.params(instanceID); e == nil {
			_ = dbadmin.SaveDbUser(p.InstDir, strings.TrimSpace(opts.Name), dbadmin.DbUser{User: user, Password: opts.Password})
		}
	}
	s.audit.Ok("ui", "mysql.create-db", instanceID, map[string]any{
		"db": opts.Name, "user": user,
	})
	return nil
}

// SetMySQLDatabasePassword 改某个库账号的密码（改完同步密码簿）。
func (s *MySQLService) SetMySQLDatabasePassword(instanceID, db, user, newPassword string) error {
	c, err := s.openManaged(instanceID)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.SetUserPassword(user, newPassword); err != nil {
		return err
	}
	// 改成功了才记：密码簿里就是当前的真实密码，连接时不必再试错回退。
	if p, e := s.params(instanceID); e == nil {
		_ = dbadmin.SaveDbUser(p.InstDir, db, dbadmin.DbUser{User: user, Password: newPassword})
	}
	s.audit.Ok("ui", "mysql.set-password", instanceID, map[string]any{"db": db, "user": user})
	return nil
}

// SetMySQLRootPassword 改 root（管理连接自身）密码，并同步凭证文件。
func (s *MySQLService) SetMySQLRootPassword(instanceID, newPassword string) error {
	c, p, err := s.open(instanceID)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.SetRootPassword(newPassword); err != nil {
		return err
	}
	s.audit.Ok("ui", "mysql.set-root-password", instanceID, map[string]any{"user": p.Credential.User})
	return nil
}

// GetMySQLPrivileges 某账号在某库上的权限。
func (s *MySQLService) GetMySQLPrivileges(instanceID, db, user string) ([]string, error) {
	c, err := s.openManaged(instanceID)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.GetPrivileges(db, user)
}

// MySQLPrivilegeNames 可勾选的权限项（UI 按它渲染勾选框）。
func (s *MySQLService) MySQLPrivilegeNames() []string {
	return append([]string(nil), dbadmin.PrivilegeNames...)
}

// SetMySQLPrivileges 重设某库上的权限（只影响这一个库）。
func (s *MySQLService) SetMySQLPrivileges(instanceID, db, user string, privs []string) error {
	c, err := s.openManaged(instanceID)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.SetPrivileges(db, user, privs); err != nil {
		return err
	}
	s.audit.Ok("ui", "mysql.set-privileges", instanceID, map[string]any{"db": db, "user": user, "privs": privs})
	return nil
}

// DropMySQLDatabase 删库（dropUser=true 时连带清理只属于该库的账号）。
func (s *MySQLService) DropMySQLDatabase(instanceID, db string, dropUser bool) error {
	c, err := s.openManaged(instanceID)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.DropDatabase(db, dropUser); err != nil {
		s.audit.Fail("ui", "mysql.drop-db", instanceID, map[string]any{"db": db, "err": err.Error()})
		return err
	}
	// 库没了，密码簿里的记录也一并清掉（否则同名重建会拿到过期密码）
	if p, e := s.params(instanceID); e == nil {
		_ = dbadmin.RemoveDbUser(p.InstDir, db)
	}
	s.audit.Ok("ui", "mysql.drop-db", instanceID, map[string]any{"db": db, "dropUser": dropUser})
	return nil
}

// BackupMySQLDatabase 导出 .sql 到数据目录的 backup/mysql/<实例>/<库>/。
func (s *MySQLService) BackupMySQLDatabase(instanceID, db string) (*BackupView, error) {
	c, err := s.openManaged(instanceID)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	b, err := c.BackupDatabase(ctx, db)
	if err != nil {
		s.audit.Fail("ui", "mysql.backup", instanceID, map[string]any{"db": db, "err": err.Error()})
		return nil, err
	}
	s.audit.Ok("ui", "mysql.backup", instanceID, map[string]any{"db": db, "file": b.Name, "size": b.Size})
	return toBackupView(b), nil
}

// ListMySQLBackups 全部备份文件。
func (s *MySQLService) ListMySQLBackups(instanceID string) ([]BackupView, error) {
	c, err := s.openManaged(instanceID)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	list, err := c.ListBackups()
	if err != nil {
		return nil, err
	}
	out := make([]BackupView, 0, len(list))
	for i := range list {
		out = append(out, *toBackupView(&list[i]))
	}
	return out, nil
}

// DeleteMySQLBackup 删除备份（路径限制在本实例备份目录内）。
func (s *MySQLService) DeleteMySQLBackup(instanceID, path string) error {
	c, err := s.openManaged(instanceID)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.DeleteBackup(path); err != nil {
		return err
	}
	s.audit.Ok("ui", "mysql.delete-backup", instanceID, map[string]any{"path": path})
	return nil
}

// RestoreMySQLBackup 用备份还原（备份内含 CREATE DATABASE，覆盖同名库）。
func (s *MySQLService) RestoreMySQLBackup(instanceID, db, path string) error {
	c, err := s.openManaged(instanceID)
	if err != nil {
		return err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := c.RestoreBackup(ctx, db, path); err != nil {
		s.audit.Fail("ui", "mysql.restore", instanceID, map[string]any{"db": db, "path": path, "err": err.Error()})
		return err
	}
	s.audit.Ok("ui", "mysql.restore", instanceID, map[string]any{"db": db, "path": path})
	return nil
}

// ImportMySQLSQL 把外部 .sql 导入指定库。
func (s *MySQLService) ImportMySQLSQL(instanceID, db, file string) error {
	c, err := s.openManaged(instanceID)
	if err != nil {
		return err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := c.ImportSQL(ctx, db, file); err != nil {
		s.audit.Fail("ui", "mysql.import", instanceID, map[string]any{"db": db, "file": file, "err": err.Error()})
		return err
	}
	s.audit.Ok("ui", "mysql.import", instanceID, map[string]any{"db": db, "file": file})
	return nil
}

// OpenMySQLBackupDir 在资源管理器中打开某库的备份目录。
func (s *MySQLService) OpenMySQLBackupDir(instanceID, db string) error {
	c, err := s.openManaged(instanceID)
	if err != nil {
		return err
	}
	defer c.Close()
	return openPath(c.BackupDir(db))
}

func toBackupView(b *dbadmin.Backup) *BackupView {
	return &BackupView{
		Name: b.Name, Path: b.Path, Database: b.Database, Size: b.Size, CreatedAt: b.CreatedAt,
	}
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
