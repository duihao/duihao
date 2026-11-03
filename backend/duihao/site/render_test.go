package site

import (
	"strings"
	"testing"
)

// https 站点带明文端口时，同一个 server 块里必须有两条 listen：
// 一条 ssl（主端口）+ 一条明文（PlainPort），站点根与 location 共用。
func TestRenderVHostPlainPort(t *testing.T) {
	rec := &Record{
		ID: "site-1", Domains: []string{"demo.test"}, Port: 443,
		PlainPort: 8080, Root: `D:\www\demo`, TLSMode: TLSSelfSigned,
	}
	conf := renderVHost(rec, 0, "", t.TempDir())
	if !strings.Contains(conf, "listen 443 ssl;") {
		t.Fatalf("缺 ssl listen:\n%s", conf)
	}
	if !strings.Contains(conf, "listen 8080;") {
		t.Fatalf("缺明文 listen:\n%s", conf)
	}
	if !strings.Contains(conf, `root "D:/www/demo";`) {
		t.Fatalf("root 缺失或被改写:\n%s", conf)
	}
}

// 纯 http 站点不该出现 ssl 相关的 listen / 证书指令。
func TestRenderVHostPlainOnly(t *testing.T) {
	rec := &Record{ID: "site-2", Domains: []string{"demo.test"}, Port: 8081, Root: t.TempDir()}
	conf := renderVHost(rec, 0, "", "")
	if !strings.Contains(conf, "listen 8081;") {
		t.Fatalf("缺 listen:\n%s", conf)
	}
	if strings.Contains(conf, "ssl") {
		t.Fatalf("纯 http 站点不该出现 ssl:\n%s", conf)
	}
}

// 主端口与明文端口相同（异常记录）时只渲染一条，避免 nginx duplicate listen。
func TestRenderVHostNoDuplicateListen(t *testing.T) {
	rec := &Record{
		ID: "site-3", Domains: []string{"demo.test"}, Port: 443,
		PlainPort: 443, Root: t.TempDir(), TLSMode: TLSSelfSigned,
	}
	conf := renderVHost(rec, 0, "", t.TempDir())
	if n := strings.Count(conf, "listen 443"); n != 1 {
		t.Fatalf("443 出现 %d 次，应只 1 次:\n%s", n, conf)
	}
}
