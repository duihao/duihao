// Package dbadmin MySQL 管理面（M11）：建库/建用户/改密码/授权/备份还原。
//
// 为什么单独一个包：实例层只管「把 mysqld 跑起来」，数据库对象是 mysqld 内部
// 的 SQL 世界，必须走客户端连接。这里只认「主机:端口 + root 凭证 + bin 目录」，
// 不认识实例/站点（铁律 1），路径与凭证由服务层拼好传进来。
//
// 凭证：root 密码存 instances/<id>/admin.json（0600）。首次装配
// mysqld --initialize-insecure 得到的是空密码 root，所以缺省凭据就是 root/"",
// 用户设过密码后在「连接设置」里填一次即可。
package dbadmin

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Credential 管理连接凭证（root 账号）。
type Credential struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

// DefaultUser 首次装配后的 root 账号。
const DefaultUser = "root"

// CredentialPath 凭证文件路径。
func CredentialPath(instDir string) string {
	return filepath.Join(instDir, "admin.json")
}

// LoadCredential 读凭证；文件不存在时返回 root + 空密码（--initialize-insecure 的初始态）。
func LoadCredential(instDir string) Credential {
	data, err := os.ReadFile(CredentialPath(instDir))
	if err != nil {
		return Credential{User: DefaultUser}
	}
	var c Credential
	if err := json.Unmarshal(data, &c); err != nil {
		return Credential{User: DefaultUser}
	}
	if c.User == "" {
		c.User = DefaultUser
	}
	return c
}

// SaveCredential 原子落盘（0600：里面有 root 密码）。
func SaveCredential(instDir string, c Credential) error {
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := CredentialPath(instDir) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, CredentialPath(instDir))
}

// Params 打开一个管理连接所需的全部外部信息。
type Params struct {
	InstanceID string
	Port       int
	Credential Credential
	InstDir    string // 实例数据目录（放凭证/临时 defaults 文件）
	MyIni      string // my.ini（mysqldump / mysql 客户端读端口与字符集）
	BinDir     string // 运行时 bin（mysqldump.exe / mysql.exe）
	BackupRoot string // 备份根目录（通常是 <dataRoot>/backup）
}

// Client 一个管理连接（每个操作建立，用完关闭：管理操作频率低，长连接反而占 mysqld 连接数）。
type Client struct {
	db *sql.DB
	p  Params
}

// Open 打开管理连接并 ping 一次（5s 超时，失败给出可操作的中文提示）。
func Open(p Params) (*Client, error) {
	if p.Port < 1 || p.Port > 65535 {
		return nil, fmt.Errorf("端口非法: %d", p.Port)
	}
	cfg := mysql.NewConfig()
	cfg.User = p.Credential.User
	if cfg.User == "" {
		cfg.User = DefaultUser
	}
	cfg.Passwd = p.Credential.Password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort("127.0.0.1", fmt.Sprint(p.Port))
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 60 * time.Second
	cfg.WriteTimeout = 60 * time.Second
	cfg.AllowNativePasswords = true
	cfg.InterpolateParams = false

	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("MySQL 连接参数错误: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(2 * time.Minute)

	c := &Client{db: db, p: p}
	if err := c.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return c, nil
}

// Ping 探活；认证失败时给出「去连接设置填 root 密码」的引导。
func (c *Client) Ping() error {
	if err := c.db.Ping(); err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "Access denied"):
			return fmt.Errorf("MySQL 拒绝登录（root 密码可能已修改）：请在「连接设置」里填写当前 root 密码")
		case strings.Contains(msg, "connection refused"), strings.Contains(msg, "dial tcp"):
			return fmt.Errorf("无法连接 MySQL（127.0.0.1:%d），请先启动实例", c.p.Port)
		}
		return fmt.Errorf("连接 MySQL 失败: %w", err)
	}
	return nil
}

// Close 释放连接池。
func (c *Client) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}

// Version 服务端版本（面板顶部展示）。
func (c *Client) Version() string {
	var v string
	if err := c.db.QueryRow("SELECT VERSION()").Scan(&v); err != nil {
		return ""
	}
	return v
}

// quoteIdent 反引号包裹标识符（调用前必须已过 validateIdent 白名单）。
func quoteIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

// quoteString 单引号字符串转义（密码等自由文本；标识符一律走白名单不走这里）。
func quoteString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	return "'" + r.Replace(s) + "'"
}
