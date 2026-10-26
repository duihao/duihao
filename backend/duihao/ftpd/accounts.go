// FTP 账号表（多账号）。
//
// 为什么单独一个文件而不塞进 settings.json：设置是「一份配置渲染进配置文件」，
// 而账号是「列表型资源」（增删改各自独立、每个账号有自己的根目录），
// 塞进 {set:key} 的键值模型里只能拼成一段字符串，改一个账号要重写整段。
//
// 存 instances/<id>/ftpaccounts.json，密码只存 bcrypt 哈希（不回传前端）。
// 与设置的关系：账号表为空时回退到设置里的 ftp.user / ftp.password（旧版兼容），
// 一旦有了账号表，设置里那两项即失效（UI 上已注明）。
package ftpd

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

// Account 一个 FTP 账号。
type Account struct {
	ID        string `json:"id"`
	User      string `json:"user"`
	PassHash  string `json:"passHash"`
	Root      string `json:"root"` // 支持 {dir} token
	ReadOnly  bool   `json:"readonly"`
	Enabled   bool   `json:"enabled"`
	Note      string `json:"note,omitempty"`
	CreatedAt string `json:"createdAt"`
}

type accountFile struct {
	SchemaVersion int       `json:"schemaVersion"`
	Accounts      []Account `json:"accounts"`
}

// 用户名白名单：FTP 协议里用户名会进日志与 USER 命令，限制字符避免注入与空白歧义。
var userPattern = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,32}$`)

// AccountsPath 账号表路径。
func AccountsPath(dir string) string { return filepath.Join(dir, "ftpaccounts.json") }

// LoadAccounts 读账号表；文件不存在返回 nil（= 尚未启用多账号）。
func LoadAccounts(dir string) ([]Account, error) {
	data, err := os.ReadFile(AccountsPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var f accountFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("ftpaccounts.json 解析失败: %w", err)
	}
	return f.Accounts, nil
}

// SaveAccounts 原子落盘（0600：里面有密码哈希）。
func SaveAccounts(dir string, list []Account) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(accountFile{SchemaVersion: 1, Accounts: list}, "", "  ")
	if err != nil {
		return err
	}
	tmp := AccountsPath(dir) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, AccountsPath(dir))
}

// HashPassword bcrypt 哈希（cost 10：一次校验约 50ms，暴力猜密码成本足够）。
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// NewAccount 构造账号（校验 + 哈希）。密码为空直接拒绝：匿名 FTP 会把根目录
// 暴露给同网段任何人，这个底线不能靠 UI 保证。
func NewAccount(user, password, root string, readonly bool, note string) (Account, error) {
	user = strings.TrimSpace(user)
	if !userPattern.MatchString(user) {
		return Account{}, fmt.Errorf("用户名只能包含字母、数字、点、下划线、@ 和短横线，长度 1-32")
	}
	if password == "" {
		return Account{}, fmt.Errorf("密码不能为空")
	}
	if strings.TrimSpace(root) == "" {
		return Account{}, fmt.Errorf("根目录不能为空")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return Account{}, err
	}
	return Account{
		ID:        newAccountID(),
		User:      user,
		PassHash:  hash,
		Root:      filepath.FromSlash(strings.TrimSpace(root)),
		ReadOnly:  readonly,
		Enabled:   true,
		Note:      strings.TrimSpace(note),
		CreatedAt: time.Now().Format(time.RFC3339),
	}, nil
}

func newAccountID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("acc-%d", time.Now().UnixNano())
	}
	return "acc-" + hex.EncodeToString(b[:])
}

// Validate 账号基本校验（保存前跑一遍，挡住空密码/非法用户名）。
func (a *Account) Validate() error {
	a.User = strings.TrimSpace(a.User)
	if !userPattern.MatchString(a.User) {
		return fmt.Errorf("用户名只能包含字母、数字、点、下划线、@ 和短横线，长度 1-32")
	}
	if a.PassHash == "" {
		return fmt.Errorf("密码不能为空")
	}
	if strings.TrimSpace(a.Root) == "" {
		return fmt.Errorf("根目录不能为空")
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
