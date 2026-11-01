package hubusers

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`create table hub_users(
		uid text primary key, name text not null unique, pass_hash text not null,
		role text not null default 'member', perms text not null default '{}',
		enabled integer not null default 1, created_at text not null default (datetime('now','localtime')));
	create table hub_secrets(k text primary key, v text not null,
		updated_at text not null default (datetime('now','localtime')));`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestCreateVerifyAndPerms(t *testing.T) {
	db := testDB(t)

	if err := Create(db, "张三", "pass66", RoleMember, map[string]string{"notes": "rw", "bogus": "rw"}); err == nil {
		t.Fatal("未知模块的 perms 应被拒")
	}
	if err := Create(db, "张三", "pass66", RoleMember, map[string]string{"notes": "rw", "ledger": "r"}); err != nil {
		t.Fatalf("Create member: %v", err)
	}
	if err := Create(db, "张三", "x2", RoleMember, nil); err == nil {
		t.Fatal("重名应被拒")
	}

	u, err := Verify(db, "张三", "pass66")
	if err != nil || u == nil {
		t.Fatalf("Verify 正确密码: %v %v", u, err)
	}
	if u.Role != RoleMember || u.Perms["notes"] != "rw" || u.Perms["ledger"] != "r" {
		t.Fatalf("角色/权限不符: %+v", u)
	}
	if u, _ = Verify(db, "张三", "wrong!"); u != nil {
		t.Fatal("错密码不应通过")
	}
	if u, _ = Verify(db, "nobody", "x"); u != nil {
		t.Fatal("不存在账号不应通过")
	}

	// 停用后 Get 仍能查到（guard 靠 Enabled 位拦），Verify 不看启用位（登录页另拦）。
	if err := UpdatePerm(db, "张三", RoleMember, map[string]string{"notes": "r"}, false); err != nil {
		t.Fatalf("UpdatePerm: %v", err)
	}
	u, _ = Get(db, "张三")
	if u.Enabled {
		t.Fatal("应已停用")
	}
	if err := UpdatePerm(db, "张三", RoleMember, map[string]string{"nope": "r"}, true); err == nil {
		t.Fatal("非法 perms 应被拒")
	}
}

func TestEnsureAdminAndSecrets(t *testing.T) {
	db := testDB(t)

	uid, pass, created, err := EnsureAdmin(db, "tester")
	if err != nil || !created || uid != "tester" || len(pass) < 8 {
		t.Fatalf("EnsureAdmin 首次: uid=%q created=%v err=%v", uid, created, err)
	}
	if sealed := GetSecret(db, SecretAdminPass); sealed == "" || OpenSecret(sealed) != pass {
		t.Fatal("管理员密码密文应可解回明文")
	}
	// uid 兜底：既有共享名带空格/冒号时退回 "admin"。
	uid2, _, created2, err := EnsureAdmin(db, "")
	if err != nil || created2 || uid2 != "" {
		t.Fatalf("EnsureAdmin 幂等: uid=%q created=%v err=%v", uid2, created2, err)
	}

	// 改密同步密文（secretKey 非空路径）。
	if err := SetPassword(db, uid, "newpass-7", SecretAdminPass); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if got := OpenSecret(GetSecret(db, SecretAdminPass)); got != "newpass-7" {
		t.Fatalf("密文应同步更新, got %q", got)
	}
	if u, _ := Verify(db, uid, "newpass-7"); u == nil {
		t.Fatal("新密码应可登录")
	}
}

func TestDeleteLastAdminGuard(t *testing.T) {
	db := testDB(t)
	uid, _, _, err := EnsureAdmin(db, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if err := Create(db, "member1", "pass66", RoleMember, nil); err != nil {
		t.Fatal(err)
	}
	if err := Delete(db, "member1"); err != nil {
		t.Fatalf("删成员应成功: %v", err)
	}
	if n, _ := CountAdmins(db); n != 1 {
		t.Fatalf("admin 数应为 1, got %d", n)
	}
	// 删掉唯一 admin：服务层负责拦，这里验证删完确实归零。
	if err := Delete(db, uid); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountAdmins(db); n != 0 {
		t.Fatalf("admin 数应为 0, got %d", n)
	}
}

// 改名：账号行 + 所有带 uid 列的业务表一起迁；规则校验、占用拒绝、同名幂等。
func TestRename(t *testing.T) {
	db := testDB(t)
	// 两个带 uid 的"业务表"（一个还带别的列，模拟真实模块表）+ 一个无 uid 表。
	if _, err := db.Exec(`create table notes(uid text, title text);
		create table ledger(uid text, cents integer);
		create table kv(k text primary key, v text);`); err != nil {
		t.Fatal(err)
	}
	if err := Create(db, "admin", "pass-11", RoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	if err := Create(db, "xiaomei", "pass-22", RoleMember, map[string]string{"notes": "rw"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into notes(uid,title) values('admin','备忘'),('xiaomei','小美笔记');
		insert into ledger(uid,cents) values('admin',-990);
		insert into kv(k,v) values('a','b');`); err != nil {
		t.Fatal(err)
	}

	// 1) 第一个 admin 也能改名，业务数据跟随迁移。
	if err := Rename(db, "admin", "boss"); err != nil {
		t.Fatalf("admin 改名: %v", err)
	}
	if u, _ := Get(db, "boss"); u == nil || u.Role != RoleAdmin {
		t.Fatal("改名后应能按新名取到 admin 账号")
	}
	var n int
	if err := db.QueryRow(`select count(*) from notes where uid='boss'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("notes 应迁到 boss, got %d (%v)", n, err)
	}
	if err := db.QueryRow(`select count(*) from ledger where uid='boss'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("ledger 应迁到 boss, got %d (%v)", n, err)
	}

	// 2) 改成已存在的名字 → 拒绝。
	if err := Rename(db, "boss", "xiaomei"); err == nil {
		t.Fatal("改成已存在的用户名应报错")
	}

	// 3) 非法名字（空格 / 太短）→ 拒绝。
	if err := Rename(db, "boss", "a b"); err == nil {
		t.Fatal("含空格用户名应报错")
	}
	if err := Rename(db, "boss", "a"); err == nil {
		t.Fatal("过短用户名应报错")
	}

	// 4) 改成自己的名字 → 幂等无错。
	if err := Rename(db, "boss", "boss"); err != nil {
		t.Fatalf("同名应幂等: %v", err)
	}

	// 5) 不存在的账号 → 报错。
	if err := Rename(db, "ghost", "who"); err == nil {
		t.Fatal("改不存在的账号应报错")
	}

	// 6) 成员改名后旧名取不到、新名能登录。
	if err := Rename(db, "xiaomei", "mei"); err != nil {
		t.Fatal(err)
	}
	if u, _ := Get(db, "xiaomei"); u != nil {
		t.Fatal("旧名应取不到账号")
	}
	if u, err := Verify(db, "mei", "pass-22"); err != nil || u == nil {
		t.Fatalf("新名应能登录: %v", err)
	}
}
