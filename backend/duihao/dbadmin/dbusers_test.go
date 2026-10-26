package dbadmin

import (
	"os"
	"path/filepath"
	"testing"
)

// 密码簿：写入 → 读回 → 删库清理，且不串库。
func TestDbUsersRoundTrip(t *testing.T) {
	dir := t.TempDir()

	if _, ok := LookupDbUser(dir, "blog"); ok {
		t.Fatalf("空目录不应有记录")
	}

	if err := SaveDbUser(dir, "blog", DbUser{User: "blog", Password: "p1"}); err != nil {
		t.Fatalf("save blog: %v", err)
	}
	if err := SaveDbUser(dir, "shop", DbUser{User: "shop_u", Password: "p2"}); err != nil {
		t.Fatalf("save shop: %v", err)
	}

	u, ok := LookupDbUser(dir, "blog")
	if !ok || u.User != "blog" || u.Password != "p1" {
		t.Fatalf("blog 读回不对: %+v ok=%v", u, ok)
	}
	s, _ := LookupDbUser(dir, "shop")
	if s.User != "shop_u" || s.Password != "p2" {
		t.Fatalf("shop 读回不对: %+v", s)
	}

	// 改密码覆盖同一条记录
	if err := SaveDbUser(dir, "blog", DbUser{User: "blog", Password: "p1b"}); err != nil {
		t.Fatalf("update blog: %v", err)
	}
	if u, _ := LookupDbUser(dir, "blog"); u.Password != "p1b" {
		t.Fatalf("密码未更新: %+v", u)
	}

	// 删库清理
	if err := RemoveDbUser(dir, "blog"); err != nil {
		t.Fatalf("remove blog: %v", err)
	}
	if _, ok := LookupDbUser(dir, "blog"); ok {
		t.Fatalf("blog 记录应已删除")
	}
	if _, ok := LookupDbUser(dir, "shop"); !ok {
		t.Fatalf("不该误删 shop")
	}

	// 删不存在的库不报错
	if err := RemoveDbUser(dir, "nope"); err != nil {
		t.Fatalf("remove missing: %v", err)
	}
}

// 损坏的文件不能让「连接数据库」整个挂掉：读成空表即可（会退回 root）。
func TestDbUsersCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dbusers.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := LookupDbUser(dir, "blog"); ok {
		t.Fatalf("损坏文件应视为无记录")
	}
	// 仍然可写：覆盖成合法内容
	if err := SaveDbUser(dir, "blog", DbUser{User: "blog", Password: "x"}); err != nil {
		t.Fatalf("save over corrupt: %v", err)
	}
	if u, ok := LookupDbUser(dir, "blog"); !ok || u.Password != "x" {
		t.Fatalf("覆盖后读回不对: %+v ok=%v", u, ok)
	}
}

// 空 instDir / 空账号：静默忽略（不写文件、不 panic）。
func TestDbUsersEmptyInput(t *testing.T) {
	if err := SaveDbUser("", "blog", DbUser{User: "blog", Password: "p"}); err != nil {
		t.Fatalf("空目录应静默返回: %v", err)
	}
	dir := t.TempDir()
	if err := SaveDbUser(dir, "blog", DbUser{}); err != nil {
		t.Fatalf("空账号应静默返回: %v", err)
	}
	if _, err := os.Stat(DbUsersPath(dir)); err == nil {
		t.Fatalf("空账号不该落盘")
	}
}
