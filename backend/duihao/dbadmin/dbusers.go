// 每库业务账号的密码簿。
//
// 为什么要存：MySQL 里只有密码哈希（authentication_string），无法反推明文，
// 所以「连接数据库」想直接用该库自己的账号登录，就必须在**对号创建/修改密码
// 的那一刻**把明文记下来。没记过的库（外部工具建的、或对号旧版本建的）只能
// 退回 root，由面板提示用户在「修改密码」里补一次。
//
// 落盘位置与 admin.json 同级：instances/<id>/dbusers.json，0600。
// 这是本机开发环境的凭证文件（和 phpStudy 在配置里放明文密码同一性质），
// 不出对号进程、不上传、不写日志。
package dbadmin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// DbUser 一个库的业务账号（明文，见文件头说明）。
type DbUser struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

// dbUsersMu 串行化读写：UI 的建库/改密可能连着点，避免读-改-写丢更新。
var dbUsersMu sync.Mutex

// DbUsersPath 密码簿路径。
func DbUsersPath(instDir string) string {
	return filepath.Join(instDir, "dbusers.json")
}

// LoadDbUsers 读整本密码簿；文件不存在/损坏时返回空表（不影响连接回退）。
func LoadDbUsers(instDir string) map[string]DbUser {
	if instDir == "" {
		return map[string]DbUser{}
	}
	data, err := os.ReadFile(DbUsersPath(instDir))
	if err != nil {
		return map[string]DbUser{}
	}
	var m map[string]DbUser
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]DbUser{}
	}
	if m == nil {
		m = map[string]DbUser{}
	}
	return m
}

// LookupDbUser 取某库的业务账号。
func LookupDbUser(instDir, db string) (DbUser, bool) {
	u, ok := LoadDbUsers(instDir)[db]
	if !ok || u.User == "" {
		return DbUser{}, false
	}
	return u, true
}

// SaveDbUser 记下/更新某库的业务账号密码（原子写）。
func SaveDbUser(instDir, db string, u DbUser) error {
	if instDir == "" || db == "" || u.User == "" {
		return nil
	}
	dbUsersMu.Lock()
	defer dbUsersMu.Unlock()
	m := LoadDbUsers(instDir)
	m[db] = u
	return writeDbUsers(instDir, m)
}

// RemoveDbUser 删库时清掉对应记录。
func RemoveDbUser(instDir, db string) error {
	if instDir == "" || db == "" {
		return nil
	}
	dbUsersMu.Lock()
	defer dbUsersMu.Unlock()
	m := LoadDbUsers(instDir)
	if _, ok := m[db]; !ok {
		return nil
	}
	delete(m, db)
	return writeDbUsers(instDir, m)
}

func writeDbUsers(instDir string, m map[string]DbUser) error {
	if err := os.MkdirAll(instDir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := DbUsersPath(instDir) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, DbUsersPath(instDir))
}
