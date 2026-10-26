// WebDAV 共享表（多共享）。
//
// 与 ftpd/accounts.go 同构：列表型资源独立成文件（instances/<id>/webdavshares.json），
// 不塞进 settings 的键值模型。每个共享 = 一套 Basic Auth 凭据 + 一个根目录，
// WebDAV 客户端（RaiDrive / ES 文件管理器 / Windows 资源管理器）用「哪个用户名登录，
// 就看到哪个根目录」——URL 对所有共享是同一个，凭据决定视图。
//
// 密码只存 bcrypt 哈希，不回传前端。
package webdavd

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Share 一个网盘共享。
type Share struct {
	ID        string `json:"id"`
	User      string `json:"user"`
	PassHash  string `json:"passHash"`
	Name      string `json:"name,omitempty"` // 显示名（中文可）
	Root      string `json:"root"`           // 支持 {dir} token
	ReadOnly  bool   `json:"readonly"`
	Enabled   bool   `json:"enabled"`
	Note      string `json:"note,omitempty"`
	CreatedAt string `json:"createdAt"`
}

type shareFile struct {
	SchemaVersion int     `json:"schemaVersion"`
	Shares        []Share `json:"shares"`
}

// 用户名白名单：Basic Auth 用户名会进日志与 WWW-Authenticate 上下文，
// 限制字符避免注入与歧义（与 ftpd 同规则）。
var shareUserPattern = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,32}$`)

// SharesPath 共享表路径。
func SharesPath(dir string) string { return filepath.Join(dir, "webdavshares.json") }

// LoadShares 读共享表；文件不存在返回 nil（= 尚未建共享）。
func LoadShares(dir string) ([]Share, error) {
	data, err := os.ReadFile(SharesPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var f shareFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("webdavshares.json 解析失败: %w", err)
	}
	return f.Shares, nil
}

// SaveShares 原子落盘（0600：里面有密码哈希；Windows 上 perm 不生效，
// 靠 AppData 目录 ACL 保护，与 ftpd/dbusers 同一事实）。
func SaveShares(dir string, list []Share) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(shareFile{SchemaVersion: 1, Shares: list}, "", "  ")
	if err != nil {
		return err
	}
	tmp := SharesPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, SharesPath(dir))
}

// NewShare 构造共享（校验 + 哈希）。密码为空直接拒绝：匿名网盘会把目录
// 暴露给同网段任何人。
func NewShare(user, password, root string, readonly bool, name, note string) (Share, error) {
	user = strings.TrimSpace(user)
	if !shareUserPattern.MatchString(user) {
		return Share{}, fmt.Errorf("用户名只能包含字母、数字、点、下划线、@ 和短横线，长度 1-32")
	}
	if password == "" {
		return Share{}, fmt.Errorf("密码不能为空")
	}
	if err := CheckRoot(root); err != nil {
		return Share{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return Share{}, err
	}
	return Share{
		ID:        newShareID(),
		User:      user,
		PassHash:  hash,
		Name:      strings.TrimSpace(name),
		Root:      filepath.FromSlash(strings.TrimSpace(root)),
		ReadOnly:  readonly,
		Enabled:   true,
		Note:      strings.TrimSpace(note),
		CreatedAt: time.Now().Format(time.RFC3339),
	}, nil
}

func newShareID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("sh-%d", time.Now().UnixNano())
	}
	return "sh-" + hex.EncodeToString(b[:])
}

// Validate 共享基本校验（保存前跑一遍）。
func (s *Share) Validate() error {
	s.User = strings.TrimSpace(s.User)
	if !shareUserPattern.MatchString(s.User) {
		return fmt.Errorf("用户名只能包含字母、数字、点、下划线、@ 和短横线，长度 1-32")
	}
	if s.PassHash == "" {
		return fmt.Errorf("密码不能为空")
	}
	if err := CheckRoot(s.Root); err != nil {
		return err
	}
	return nil
}

// expandRoot 展开 {dir} 并返回绝对路径。
func expandRoot(root, dir string) string {
	r := strings.ReplaceAll(root, "{dir}", dir)
	if r == "" {
		return ""
	}
	if abs, err := filepath.Abs(filepath.FromSlash(r)); err == nil {
		return abs
	}
	return filepath.FromSlash(r)
}

// ShareRoot 展开共享根目录的绝对路径（管理端浏览共享内容用）。
func ShareRoot(sh Share, instanceDir string) string { return expandRoot(sh.Root, instanceDir) }

// HashPassword bcrypt 哈希（cost 10，与 ftpd 一致）。
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}
