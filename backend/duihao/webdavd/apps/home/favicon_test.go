package home

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
)

// 图标缓存命中路径（不打网络）：正缓存直出字节流、负缓存快速 404、非法 host 400。
func TestServeFaviconCache(t *testing.T) {
	db := iconTestDB(t)
	defer func() { _ = db.Close() }()
	png := []byte("\x89PNG\r\n\x1a\n0123456789012345678901234567890123456789")
	now := time.Now().Format(time.RFC3339)
	if _, err := db.Exec("insert into favicons(host,mime,data,fetched_at) values('example.com','image/png',?,?)", png, now); err != nil {
		t.Fatalf("塞缓存: %v", err)
	}
	if _, err := db.Exec("insert into favicons(host,mime,data,fetched_at) values('noicon.test','none',x'',?)", now); err != nil {
		t.Fatalf("塞负缓存: %v", err)
	}
	c := appkit.Context{User: "u", DB: db}

	call := func(host string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/-api/home/favicon?host="+url.QueryEscape(host), nil)
		if err := serveFavicon(c, w, r); err != nil {
			w.Code = errCode(err)
		}
		return w
	}

	if w := call("example.com"); w.Code != http.StatusOK || w.Body.Len() != len(png) || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("命中缓存应直出图标, got %d len=%d ct=%s", w.Code, w.Body.Len(), w.Header().Get("Content-Type"))
	}
	if w := call("noicon.test"); w.Code != http.StatusNotFound {
		t.Fatalf("负缓存应 404（不重复打外网）, got %d", w.Code)
	}
	if w := call("bad host/bad"); w.Code != http.StatusBadRequest {
		t.Fatalf("非法 host 应 400, got %d", w.Code)
	}
	// 内网地址不许代理
	if w := call("127.0.0.1"); w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
		t.Fatalf("内网地址应被拒, got %d", w.Code)
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"Example.COM.":              "example.com",
		"https://www.bilibili.com/": "www.bilibili.com",
		"127.0.0.1:8080":            "127.0.0.1:8080",
	}
	for in, want := range cases {
		got, err := normalizeHost(in)
		if err != nil || got != want {
			t.Fatalf("normalizeHost(%q)=%q,%v want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "a b", "exa_mple.com", "http://"} {
		if got, err := normalizeHost(bad); err == nil {
			t.Fatalf("normalizeHost(%q) 应报错, got %q", bad, got)
		}
	}
	if !isLocalHost("localhost") || !isLocalHost("192.168.1.9") || isLocalHost("example.com") {
		t.Fatal("内网判定不对")
	}
}

func iconTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("内存库: %v", err)
	}
	if _, err := db.Exec(`create table favicons(host text primary key, mime text not null, data blob not null, fetched_at text not null)`); err != nil {
		t.Fatalf("建表: %v", err)
	}
	return db
}

func errCode(err error) int {
	if he, ok := err.(*appkit.HTTPError); ok {
		return he.Code
	}
	return http.StatusInternalServerError
}
