// 数据库对象操作：列库、建库建用户、改密码、改权限、删库。
//
// 所有标识符（库名/用户名）先过白名单再进 SQL——DDL 不支持占位符绑定标识符，
// 只能拼字符串，白名单是唯一安全的做法（宁可拒绝奇怪名字，也不放注入进来）。
package dbadmin

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// identPattern 库名/用户名白名单。
var identPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// charsetPattern 字符集白名单（utf8mb4 / utf8 / gbk / latin1 / ascii / utf8mb4_0900_ai_ci 之类不列）。
var charsetPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)

// systemSchemas 不进列表的系统库。
var systemSchemas = map[string]bool{
	"mysql":              true,
	"information_schema": true,
	"performance_schema": true,
	"sys":                true,
}

// Database 面板一行。
type Database struct {
	Name    string   `json:"name"`
	Charset string   `json:"charset"`
	Tables  int      `json:"tables"`
	Bytes   int64    `json:"bytes"`
	Users   []string `json:"users"`
}

// ListDatabases 列出业务库（含表数/体积/已授权用户）。
func (c *Client) ListDatabases() ([]Database, error) {
	const q = `
SELECT s.SCHEMA_NAME, COALESCE(s.DEFAULT_CHARACTER_SET_NAME, ''),
       COALESCE(t.table_count, 0), COALESCE(t.bytes, 0)
FROM information_schema.SCHEMATA s
LEFT JOIN (
  SELECT TABLE_SCHEMA, COUNT(*) AS table_count,
         SUM(COALESCE(DATA_LENGTH,0) + COALESCE(INDEX_LENGTH,0)) AS bytes
  FROM information_schema.TABLES
  GROUP BY TABLE_SCHEMA
) t ON t.TABLE_SCHEMA = s.SCHEMA_NAME
ORDER BY s.SCHEMA_NAME`
	rows, err := c.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("读取数据库列表失败: %w", err)
	}
	defer rows.Close()

	users := c.schemaUsers()
	var out []Database
	for rows.Next() {
		var d Database
		var bytes float64
		if err := rows.Scan(&d.Name, &d.Charset, &d.Tables, &bytes); err != nil {
			return nil, err
		}
		if systemSchemas[strings.ToLower(d.Name)] {
			continue
		}
		d.Bytes = int64(bytes)
		d.Users = users[d.Name]
		if d.Users == nil {
			d.Users = []string{}
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// schemaUsers 每个库的授权用户（一次查完，避免逐库 N+1）。
func (c *Client) schemaUsers() map[string][]string {
	m := map[string][]string{}
	rows, err := c.db.Query(`SELECT TABLE_SCHEMA, GRANTEE FROM information_schema.SCHEMA_PRIVILEGES`)
	if err != nil {
		return m // 权限不足时列表仍可用，只是没用户名
	}
	defer rows.Close()
	for rows.Next() {
		var schema, grantee string
		if err := rows.Scan(&schema, &grantee); err != nil {
			continue
		}
		u := parseGrantee(grantee)
		if u == "" || u == "root" {
			continue
		}
		if !contains(m[schema], u) {
			m[schema] = append(m[schema], u)
		}
	}
	return m
}

// parseGrantee `'user'@'host'` → user。
func parseGrantee(g string) string {
	g = strings.TrimSpace(g)
	if i := strings.Index(g, "@"); i > 0 {
		return strings.Trim(strings.TrimSpace(g[:i]), "'\"")
	}
	return strings.Trim(g, "'\"")
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// CreateOptions 建库参数。
type CreateOptions struct {
	Name     string `json:"name"`
	User     string `json:"user"`     // 留空 = 与库名同名（phpStudy 习惯）
	Password string `json:"password"` // 必填：空密码等于把库敞开
	Charset  string `json:"charset"`  // 留空 = utf8mb4
}

// CreateDatabase 建库 + 建同名用户（localhost 与 127.0.0.1 两条）+ 授权。
// DDL 会隐式提交，不能包在事务里；每一步失败都返回明确的「做到哪一步」。
func (c *Client) CreateDatabase(opts CreateOptions) error {
	name := strings.TrimSpace(opts.Name)
	if !identPattern.MatchString(name) {
		return fmt.Errorf("数据库名只能用字母、数字、下划线（1-64 位）")
	}
	if systemSchemas[strings.ToLower(name)] {
		return fmt.Errorf("%s 是系统库名，请换一个", name)
	}
	user := strings.TrimSpace(opts.User)
	if user == "" {
		user = name
	}
	if !identPattern.MatchString(user) {
		return fmt.Errorf("用户名只能用字母、数字、下划线（1-64 位）")
	}
	if opts.Password == "" {
		return fmt.Errorf("密码不能为空——空密码的数据库账号等于对同机所有程序敞开")
	}
	charset := strings.TrimSpace(opts.Charset)
	if charset == "" {
		charset = "utf8mb4"
	}
	if !charsetPattern.MatchString(charset) {
		return fmt.Errorf("字符集非法: %s", charset)
	}

	exists, err := c.databaseExists(name)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("数据库 %s 已存在", name)
	}

	if _, err := c.db.Exec(fmt.Sprintf(
		"CREATE DATABASE %s DEFAULT CHARACTER SET %s", quoteIdent(name), charset)); err != nil {
		return fmt.Errorf("创建数据库失败: %w", err)
	}

	for _, host := range []string{"localhost", "127.0.0.1"} {
		// IF NOT EXISTS：用户可能已存在（比如同名用户被别的库用过），此时只补授权。
		createUser := fmt.Sprintf("CREATE USER IF NOT EXISTS %s@%s IDENTIFIED BY %s",
			quoteString(user), quoteString(host), quoteString(opts.Password))
		if _, err := c.db.Exec(createUser); err != nil {
			return fmt.Errorf("创建用户 %s@%s 失败（数据库 %s 已建好）: %w", user, host, name, err)
		}
		// 已存在的用户不重置密码：多库共用账号时改密码会静默影响其它库。
		grant := fmt.Sprintf("GRANT ALL PRIVILEGES ON %s.* TO %s@%s",
			quoteIdent(name), quoteString(user), quoteString(host))
		if _, err := c.db.Exec(grant); err != nil {
			return fmt.Errorf("授权 %s@%s 失败（数据库 %s 已建好）: %w", user, host, name, err)
		}
	}
	_, _ = c.db.Exec("FLUSH PRIVILEGES")
	return nil
}

func (c *Client) databaseExists(name string) (bool, error) {
	var n string
	err := c.db.QueryRow("SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", name).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// userHosts 某用户存在的 host 列表。
func (c *Client) userHosts(user string) ([]string, error) {
	rows, err := c.db.Query("SELECT Host FROM mysql.user WHERE User = ?", user)
	if err != nil {
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			continue
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// UserExists 某用户名是否已存在（任一 host）。
// 建库时用来判断密码是否会真的生效：已存在的账号不会被重置密码
// （多库共用账号），所以只有「新建账号」才值得记进密码簿。
func (c *Client) UserExists(user string) (bool, error) {
	hosts, err := c.userHosts(user)
	if err != nil {
		return false, err
	}
	return len(hosts) > 0, nil
}

// SetUserPassword 改用户密码（该用户的所有 host 一起改）。
func (c *Client) SetUserPassword(user, newPass string) error {
	user = strings.TrimSpace(user)
	if !identPattern.MatchString(user) {
		return fmt.Errorf("用户名非法: %s", user)
	}
	if newPass == "" {
		return fmt.Errorf("密码不能为空")
	}
	hosts, err := c.userHosts(user)
	if err != nil {
		return err
	}
	if len(hosts) == 0 {
		return fmt.Errorf("用户 %s 不存在", user)
	}
	for _, h := range hosts {
		stmt := fmt.Sprintf("ALTER USER %s@%s IDENTIFIED BY %s",
			quoteString(user), quoteString(h), quoteString(newPass))
		if _, err := c.db.Exec(stmt); err != nil {
			return fmt.Errorf("修改 %s@%s 密码失败: %w", user, h, err)
		}
	}
	_, _ = c.db.Exec("FLUSH PRIVILEGES")
	return nil
}

// SetRootPassword 改管理连接自身的账号密码，并同步凭证文件（否则下次连不上）。
func (c *Client) SetRootPassword(newPass string) error {
	user := c.p.Credential.User
	if err := c.SetUserPassword(user, newPass); err != nil {
		return err
	}
	c.p.Credential.Password = newPass
	if c.p.InstDir != "" {
		if err := SaveCredential(c.p.InstDir, c.p.Credential); err != nil {
			return fmt.Errorf("密码已改，但保存凭证失败（请重启后手动填一次）: %w", err)
		}
	}
	return nil
}

// PrivilegeNames 可勾选的库级权限（顺序即 UI 顺序）。
var PrivilegeNames = []string{
	"SELECT", "INSERT", "UPDATE", "DELETE",
	"CREATE", "DROP", "ALTER", "INDEX",
	"CREATE TEMPORARY TABLES", "LOCK TABLES",
	"EXECUTE", "CREATE VIEW", "SHOW VIEW", "TRIGGER", "REFERENCES",
}

// normalizePrivileges 只保留白名单内的权限名（去重、大写）。
func normalizePrivileges(privs []string) []string {
	allowed := map[string]bool{}
	for _, p := range PrivilegeNames {
		allowed[p] = true
	}
	var out []string
	for _, p := range privs {
		u := strings.ToUpper(strings.TrimSpace(p))
		if allowed[u] && !contains(out, u) {
			out = append(out, u)
		}
	}
	return out
}

// GetPrivileges 某用户在某库上的权限列表。
// DISTINCT：同一用户通常有 localhost / 127.0.0.1 两条 host 记录，
// 不去重会把每个权限列两遍。
func (c *Client) GetPrivileges(dbName, user string) ([]string, error) {
	rows, err := c.db.Query(
		`SELECT DISTINCT PRIVILEGE_TYPE FROM information_schema.SCHEMA_PRIVILEGES
		 WHERE TABLE_SCHEMA = ? AND GRANTEE LIKE ?`, dbName, "%'"+user+"'@%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			continue
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetPrivileges 重设某用户在某库上的权限：只撤销这个库的授权（其它库不动），
// 再按勾选重新 GRANT。撤销不存在的授权会报 1141，直接忽略。
func (c *Client) SetPrivileges(dbName, user string, privs []string) error {
	if !identPattern.MatchString(dbName) || !identPattern.MatchString(user) {
		return fmt.Errorf("数据库名或用户名非法")
	}
	hosts, err := c.userHosts(user)
	if err != nil {
		return err
	}
	if len(hosts) == 0 {
		return fmt.Errorf("用户 %s 不存在", user)
	}
	list := normalizePrivileges(privs)
	for _, h := range hosts {
		revoke := fmt.Sprintf("REVOKE ALL PRIVILEGES ON %s.* FROM %s@%s",
			quoteIdent(dbName), quoteString(user), quoteString(h))
		if _, err := c.db.Exec(revoke); err != nil {
			// 1141: 本来就没有授权 → 正常，继续 GRANT。
			if !strings.Contains(err.Error(), "1141") && !strings.Contains(err.Error(), "There is no such grant") {
				return fmt.Errorf("撤销 %s@%s 的权限失败: %w", user, h, err)
			}
		}
		if len(list) == 0 {
			continue
		}
		grant := fmt.Sprintf("GRANT %s ON %s.* TO %s@%s",
			strings.Join(list, ", "), quoteIdent(dbName), quoteString(user), quoteString(h))
		if _, err := c.db.Exec(grant); err != nil {
			return fmt.Errorf("授权失败: %w", err)
		}
	}
	_, _ = c.db.Exec("FLUSH PRIVILEGES")
	return nil
}

// DropDatabase 删库；dropUsers=true 时连带删除「只对这个库有权限」的账号。
// 判断依据是 information_schema.SCHEMA_PRIVILEGES：账号在别的库仍有授权就保留，
// 避免删一个库把共用的账号一起带走。
func (c *Client) DropDatabase(name string, dropUsers bool) error {
	if !identPattern.MatchString(name) {
		return fmt.Errorf("数据库名非法: %s", name)
	}
	if systemSchemas[strings.ToLower(name)] {
		return fmt.Errorf("拒绝删除系统库: %s", name)
	}
	exists, err := c.databaseExists(name)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("数据库 %s 不存在", name)
	}

	var orphans []string
	if dropUsers {
		orphans = c.orphanGrantees(name)
	}
	if _, err := c.db.Exec(fmt.Sprintf("DROP DATABASE %s", quoteIdent(name))); err != nil {
		return fmt.Errorf("删除数据库失败: %w", err)
	}
	for _, g := range orphans {
		u := parseGrantee(g)
		hosts := hostsFromGrantee(g)
		if u == "" || u == "root" {
			continue
		}
		for _, h := range hosts {
			_, _ = c.db.Exec(fmt.Sprintf("DROP USER IF EXISTS %s@%s", quoteString(u), quoteString(h)))
		}
	}
	return nil
}

// orphanGrantees 该库的授权对象中，在其它库没有任何授权的那些。
func (c *Client) orphanGrantees(dbName string) []string {
	rows, err := c.db.Query(
		`SELECT DISTINCT GRANTEE FROM information_schema.SCHEMA_PRIVILEGES WHERE TABLE_SCHEMA = ?`, dbName)
	if err != nil {
		return nil
	}
	var grantees []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err == nil {
			grantees = append(grantees, g)
		}
	}
	rows.Close()

	var out []string
	for _, g := range grantees {
		var n int
		if err := c.db.QueryRow(
			`SELECT COUNT(*) FROM information_schema.SCHEMA_PRIVILEGES
			 WHERE GRANTEE = ? AND TABLE_SCHEMA <> ?`, g, dbName).Scan(&n); err != nil {
			continue
		}
		if n == 0 {
			out = append(out, g)
		}
	}
	return out
}

// hostsFromGrantee `'u'@'localhost'` → [localhost]（通常单 host，保留切片便于扩展）。
func hostsFromGrantee(g string) []string {
	i := strings.Index(g, "@")
	if i < 0 {
		return nil
	}
	h := strings.Trim(strings.TrimSpace(g[i+1:]), "'\"")
	if h == "" {
		return nil
	}
	return []string{h}
}
