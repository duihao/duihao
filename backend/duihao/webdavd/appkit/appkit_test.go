package appkit

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"
)

// fakeApp 抓住收到的 Context，验证权限收口后的账号视角（as= 代看等）。
type fakeApp struct {
	id  string // 空 = "foo"
	got *Context
}

func (f *fakeApp) ID() string {
	if f.id != "" {
		return f.id
	}
	return "foo"
}
func (f *fakeApp) Title() string { return "测试" }
func (f *fakeApp) Page(c Context, w http.ResponseWriter, r *http.Request) error {
	*f.got = c
	w.WriteHeader(http.StatusOK)
	return nil
}
func (f *fakeApp) API(c Context, w http.ResponseWriter, r *http.Request, action string) error {
	*f.got = c
	w.WriteHeader(http.StatusOK)
	return nil
}

func testHubDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`create table hub_users(uid text primary key)`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPermsGateOnAPI(t *testing.T) {
	var got, gotHome Context
	reg := New(&fakeApp{got: &got}, &fakeApp{id: "home", got: &gotHome})
	c := Context{
		User: "u1", Role: "member", DB: testHubDB(t), Writable: true,
		Perms: map[string]string{"foo": "r"},
	}

	// 读动作：有 r → 放行
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/-api/foo/list", nil)
	if !reg.Handle(c, w, req) || w.Code != http.StatusOK {
		t.Fatalf("有读权限应放行, code=%d", w.Code)
	}
	// 写动作：只有 r → 403
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/-api/foo/item.save", nil)
	reg.Handle(c, w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("只读成员写动作应 403, code=%d", w.Code)
	}
	// 无权限模块 → 403
	c2 := c
	c2.Perms = map[string]string{}
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/-api/foo/list", nil)
	reg.Handle(c2, w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("无权限应 403, code=%d", w.Code)
	}
	// home（收藏夹）与其他模块同口径：缺键 = 无权限 → 403，显式给了 r → 放行
	c3 := c2
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/-api/home/x", nil)
	reg.Handle(c3, w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("home 缺键应 403, code=%d", w.Code)
	}
	c4 := c2
	c4.Perms = map[string]string{"home": "r"}
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/-api/home/x", nil)
	reg.Handle(c4, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("home 有 r 应放行, code=%d", w.Code)
	}
}

func TestPageGateAndActingAs(t *testing.T) {
	var got Context
	app := &fakeApp{got: &got}
	reg := New(app)
	db := testHubDB(t)
	if _, err := db.Exec(`insert into hub_users(uid) values('m1')`); err != nil {
		t.Fatal(err)
	}

	// member 有 r：页面放行
	member := Context{User: "m1", Role: "member", DB: db, Writable: true,
		Perms: map[string]string{"foo": "r"}}
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/-foo", nil)
	if !reg.Handle(member, w, req) || w.Code != http.StatusOK {
		t.Fatalf("成员页面应放行, code=%d", w.Code)
	}

	// 管理员 as= 代看：目标存在 → User 换成 m1；目标不存在 → 维持自己。
	admin := Context{User: "admin", Role: "admin", DB: db, Writable: true}
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/-foo?as=m1", nil)
	reg.Handle(admin, w, req)
	if got.User != "m1" {
		t.Fatalf("admin as= 应切到 m1, got %q", got.User)
	}
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/-foo?as=ghost", nil)
	reg.Handle(admin, w, req)
	if got.User != "admin" {
		t.Fatalf("as= 不存在应维持自己, got %q", got.User)
	}
	// member 带 as= 无效（只有 admin 能代看）。
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/-foo?as=admin", nil)
	reg.Handle(member, w, req)
	if got.User != "m1" {
		t.Fatalf("member 的 as= 应被忽略, got %q", got.User)
	}
}
