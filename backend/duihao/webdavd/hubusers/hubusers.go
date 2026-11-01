// Package hubusers NAS 内部多用户（迁移 019）。
//
// 口径（2026-10-31 定稿）：
//   - NAS 账号与 WebDAV 共享账号解耦：挂载客户端仍走共享账号（全盘读写），
//     这里的账号只管 NAS 网页（/-<app> 与 /-api/*）；
//   - role=admin 全权（忽略 perms）；member 按 perms 走，每模块 "r"|"rw"，
//     缺条目 = 无权限；users 应用不在 perms 里（仅 admin，自己改密除外）；
//   - 第一个账号由 EnsureAdmin 引导生成（admin + 随机密码），uid 固定
//     "admin"（要别的名字改完再改名，改名会连业务数据一起迁）；
//   - 明文密码只在两处出现：生成/改密的那一刻（写 hub_secrets 密文，
//     credentials.Seal），以及连接管理页查看——库里永远只有 bcrypt 哈希与密文。
package hubusers

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/credentials"
)

// Modules 可授权的业务模块（顺序跟顶栏一致，授权面板照此展开）。
// users 模块不在列：仅 admin，成员只有「改自己密码」。
// files = 网盘（文件管理器页面 + 网页文件读写）；home = 收藏夹（/-home）；
// WebDAV 挂载客户端走共享凭据不受此权限约束（口径：模块权限只管 NAS 网页操作）。
var Modules = []string{"files", "video", "music", "download", "notes", "ledger", "health", "home", "voice", "monitor", "remote"}

// User 一行账号。
type User struct {
	UID       string `json:"uid"`
	Name      string `json:"name"`
	PassHash  string `json:"-"`
	Role      string `json:"role"` // admin | member
	Perms     map[string]string `json:"perms"` // 模块 → "r"|"rw"；admin 不看这个
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"createdAt"`
}

const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

func parsePerms(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	_ = json.Unmarshal([]byte(s), &out)
	for k, v := range out {
		// "" = 显式无权限（区别于「没存过」），与 r|rw 一起保留
		if v != "r" && v != "rw" && v != "" {
			delete(out, k)
		}
	}
	return out
}

// ValidPerms 校验提交的权限表：键必须在 Modules 内，值可以是 r|rw，
// 也可以是 ""（显式无权限——用户管理里选「无权限」要落库，不能丢）。
func ValidPerms(perms map[string]string) bool {
	ok := map[string]bool{"": true, "r": true, "rw": true}
	for k, v := range perms {
		if !ok[v] {
			return false
		}
		found := false
		for _, m := range Modules {
			if m == k {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Can 角色 role 对 module 的 act（"r"|"w"）权限；admin 恒真。
// 与 appkit 的 canRead/canWrite 同口径，供 guard 在文件层（非 /- 路径）判断。
// 老成员（019 时代建的）权限表里没有 files 键：维持改动前的「可看文件」，
// 管理员在用户管理里保存一次后就会显式落键——显式 ""（无权限）与
// 缺键是两回事："" 一律拒绝，缺键才走 files 的 "r" 兜底。
func Can(role string, perms map[string]string, module, act string) bool {
	if role == RoleAdmin {
		return true
	}
	p, ok := perms[module]
	if !ok && module == "files" {
		p = "r"
	}
	if act == "w" {
		return strings.Contains(p, "w")
	}
	return strings.Contains(p, "r")
}

func scan(src interface{ Scan(...any) error }) (*User, error) {
	var u User
	var perms string
	var enabled int
	if err := src.Scan(&u.UID, &u.Name, &u.PassHash, &u.Role, &perms, &enabled, &u.CreatedAt); err != nil {
		return nil, err
	}
	u.Perms = parsePerms(perms)
	u.Enabled = enabled == 1
	return &u, nil
}

const cols = `uid, name, pass_hash, role, perms, enabled, created_at`

// Get 按 uid 取账号；不存在返回 nil（无错误）。
func Get(db *sql.DB, uid string) (*User, error) {
	u, err := scan(db.QueryRow(`select `+cols+` from hub_users where uid=?`, uid))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

// List 全部账号（按创建序）。admin 页用。
func List(db *sql.DB) ([]User, error) {
	rows, err := db.Query(`select ` + cols + ` from hub_users order by created_at, uid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// Count 账号数（引导判断用）。
func Count(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`select count(*) from hub_users`).Scan(&n)
	return n, err
}

// Verify 登录校验：用户名（=uid）+ 明文密码。账号不存在也做一次 bcrypt
// 时序对齐（与 shareByCreds 同思路，防账号枚举）。
func Verify(db *sql.DB, name, pass string) (*User, error) {
	u, err := Get(db, name)
	if err != nil {
		return nil, err
	}
	if u == nil {
		_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(pass))
		return nil, nil
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PassHash), []byte(pass)) != nil {
		return nil, nil
	}
	return u, nil
}

// dummyHash 真实 bcrypt 哈希（成本一致），只为时序对齐。
var dummyHash = func() string {
	h, _ := bcrypt.GenerateFromPassword([]byte("duihao-timing-align"), bcrypt.DefaultCost)
	return string(h)
}()

// Create 建账号（uid = name，见 019 注释）。用户名限 2-32 位可见字符，
// 不许空格与冒号（冒号是会话 token 的保留分隔）。
func Create(db *sql.DB, name, pass, role string, perms map[string]string) error {
	name = strings.TrimSpace(name)
	if err := validUID(name); err != nil {
		return err
	}
	if role != RoleAdmin && role != RoleMember {
		return fmt.Errorf("角色必须是 admin 或 member")
	}
	if role == RoleMember && !ValidPerms(perms) {
		return fmt.Errorf("权限表不合法（模块名或档位不对）")
	}
	if len([]rune(pass)) < 6 {
		return fmt.Errorf("密码至少 6 位")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	pj, _ := json.Marshal(perms)
	_, err = db.Exec(`insert into hub_users(uid,name,pass_hash,role,perms,enabled) values(?,?,?,?,?,1)`,
		name, name, string(hash), role, string(pj))
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("用户名 %s 已存在", name)
	}
	return err
}

func validUID(name string) error {
	if l := len([]rune(name)); l < 2 || l > 32 || strings.ContainsAny(name, " :") {
		return fmt.Errorf("用户名需 2-32 个字符，不能含空格或冒号")
	}
	return nil
}

// Rename 改用户名（uid）。uid 是各业务表的账号键：同一事务里把 hub_users
// 和所有带 uid 列的表一起迁到新名——表清单用 sqlite_master 现扫、列用
// pragma 探测，以后新增模块不用回来改这里。旧会话（hub:<旧uid> 票）会
// 失效，改自己名字等价于被登出，前端改完直接回登录页。
func Rename(db *sql.DB, oldUID, newUID string) error {
	oldUID = strings.TrimSpace(oldUID)
	newUID = strings.TrimSpace(newUID)
	if oldUID == "" {
		return fmt.Errorf("缺少要改名的账号")
	}
	if err := validUID(newUID); err != nil {
		return err
	}
	if oldUID == newUID {
		return nil
	}
	if u, err := Get(db, newUID); err != nil {
		return err
	} else if u != nil {
		return fmt.Errorf("用户名 %s 已存在", newUID)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`update hub_users set uid=? where uid=?`, newUID, oldUID)
	if err != nil {
		return err
	}
	if err := requireRow(res, "账号不存在: "+oldUID); err != nil {
		return err
	}

	// 收集所有带 uid 列的业务表（先取完名再更新，避免边查边写）。
	names, err := func() ([]string, error) {
		rows, err := tx.Query(`select name from sqlite_master
			where type='table' and name not like 'sqlite_%' and name <> 'hub_users'`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				return nil, err
			}
			out = append(out, n)
		}
		return out, rows.Err()
	}()
	if err != nil {
		return err
	}
	for _, t := range names {
		hasUID, err := func() (bool, error) {
			rows, err := tx.Query(`pragma table_info("` + strings.ReplaceAll(t, `"`, `""`) + `")`)
			if err != nil {
				return false, err
			}
			defer rows.Close()
			for rows.Next() {
				var cid, notNull, pk int
				var name, typ string
				var dflt sql.NullString
				if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
					return false, err
				}
				if name == "uid" {
					return true, nil
				}
			}
			return false, rows.Err()
		}()
		if err != nil {
			return err
		}
		if !hasUID {
			continue
		}
		if _, err := tx.Exec(`update "`+t+`" set uid=? where uid=?`, newUID, oldUID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpdatePerm 改成员的角色/权限/启用位（不改密码）。admin 不受 perms 约束，
// 但仍存着（降级 member 时立即生效）。
func UpdatePerm(db *sql.DB, uid, role string, perms map[string]string, enabled bool) error {
	if role != RoleAdmin && role != RoleMember {
		return fmt.Errorf("角色必须是 admin 或 member")
	}
	if role == RoleMember && !ValidPerms(perms) {
		return fmt.Errorf("权限表不合法（模块名或档位不对）")
	}
	pj, _ := json.Marshal(perms)
	en := 0
	if enabled {
		en = 1
	}
	res, err := db.Exec(`update hub_users set role=?, perms=?, enabled=? where uid=?`, role, string(pj), en, uid)
	if err != nil {
		return err
	}
	return requireRow(res, "账号不存在: "+uid)
}

// SetPassword 改密码（管理员重置 / 成员自改都走这里），并同步 hub_secrets
// 里「可查看」的密文 —— 连接管理页展示的永远是当前密码。ownSecret 为空表示
// 该账号不进查看名单（普通成员）。
func SetPassword(db *sql.DB, uid, pass, secretKey string) error {
	if len([]rune(pass)) < 6 {
		return fmt.Errorf("密码至少 6 位")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	res, err := db.Exec(`update hub_users set pass_hash=? where uid=?`, string(hash), uid)
	if err != nil {
		return err
	}
	if err := requireRow(res, "账号不存在: "+uid); err != nil {
		return err
	}
	if secretKey != "" {
		return PutSecret(db, secretKey, pass)
	}
	return nil
}

// Delete 删账号。调用方负责保证不删自己、不删最后一个 admin。
func Delete(db *sql.DB, uid string) error {
	res, err := db.Exec(`delete from hub_users where uid=?`, uid)
	if err != nil {
		return err
	}
	return requireRow(res, "账号不存在: "+uid)
}

// CountAdmins 存活的 admin 数（防删光）。
func CountAdmins(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`select count(*) from hub_users where role='admin' and enabled=1`).Scan(&n)
	return n, err
}

func requireRow(res sql.Result, msg string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// ---------- 引导 ----------

// EnsureAdmin 第一个账号：不存在时创建 admin。fallbackUID 非空且无冲突时
// 用它当 uid（当前调用方固定传 "admin"），否则用 "admin"。
// 返回（uid, 明文密码, 是否新建）。已存在时返回 ("","",false)。
func EnsureAdmin(db *sql.DB, fallbackUID string) (string, string, bool, error) {
	n, err := Count(db)
	if err != nil {
		return "", "", false, err
	}
	if n > 0 {
		return "", "", false, nil
	}
	uid := "admin"
	if fb := strings.TrimSpace(fallbackUID); fb != "" && !strings.ContainsAny(fb, " :") {
		uid = fb
	}
	pass, err := genPassword()
	if err != nil {
		return "", "", false, err
	}
	if err := Create(db, uid, pass, RoleAdmin, nil); err != nil {
		return "", "", false, err
	}
	if err := PutSecret(db, SecretAdminPass, pass); err != nil {
		return uid, "", true, err
	}
	return uid, pass, true, nil
}

// SecretAdminPass hub_secrets 键：管理员密码密文。
const SecretAdminPass = "admin.password"

// SecretSharePass hub_secrets 键前缀：自动创建的共享密码密文（share.<user>.password）。
func SecretSharePass(user string) string { return "share." + user + ".password" }

// GenPassword 生成 12 位可读随机密码（去掉易混的 0/O/1/l/I）。
// 服务层建默认共享也用它。
func GenPassword() (string, error) { return genPassword() }

// genPassword 12 位可读随机密码（去掉易混的 0/O/1/l/I）。
func genPassword() (string, error) {
	const alphabet = "23456789abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ"
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = alphabet[int(v)%len(alphabet)]
	}
	return string(out), nil
}

// ---------- hub_secrets ----------

// GetSecret 取密文；无则空串。
func GetSecret(db *sql.DB, k string) string {
	var v string
	_ = db.QueryRow(`select v from hub_secrets where k=?`, k).Scan(&v)
	return v
}

// PutSecret 存密文（调用方先 credentials.Seal）。
func PutSecret(db *sql.DB, k, sealed string) error {
	_, err := db.Exec(`insert into hub_secrets(k,v,updated_at) values(?,?,?)
		on conflict(k) do update set v=excluded.v, updated_at=excluded.updated_at`,
		k, sealed, time.Now().Format(time.RFC3339))
	return err
}

// DelSecret 删密文（删账号/删共享时清掉，不留死密码）。
func DelSecret(db *sql.DB, k string) error {
	_, err := db.Exec(`delete from hub_secrets where k=?`, k)
	return err
}

// OpenSecret 解密（解不开 = 换过机器/用户，返回空）。
func OpenSecret(sealed string) string {
	if sealed == "" {
		return ""
	}
	p, err := credentials.Open(sealed)
	if err != nil {
		return ""
	}
	return p
}
