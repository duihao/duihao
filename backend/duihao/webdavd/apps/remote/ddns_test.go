package remote

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fr(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Fatalf(format, args...)
}

// ---------- TOML 生成与校验 ----------

func validCfg() FrpConfig {
	return FrpConfig{
		Server:    frpServer{Addr: "nas.example.com", Port: 7000, User: "huawei"},
		TLSEnable: true,
		Proxies: []frpProxy{
			{Name: "dav", Type: "tcp", LocalPort: 18080, RemotePort: 18080},
			{Name: "web", Type: "http", LocalPort: 18080, CustomDomain: "a.example.com, b.example.com"},
		},
	}
}

func TestGenTOML(t *testing.T) {
	toml, err := genTOML(validCfg(), "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`serverAddr = "nas.example.com"`, `serverPort = 7000`, `user = "huawei"`,
		`auth.token = "s3cret"`, `transport.tls.enable = true`,
		`name = "dav"`, `remotePort = 18080`, `localIP = "127.0.0.1"`,
		`customDomains = ["a.example.com", "b.example.com"]`,
	} {
		if !strings.Contains(toml, want) {
			t.Errorf("生成的 toml 缺 %q\n%s", want, toml)
		}
	}
	if !strings.HasPrefix(toml, "# 由对号") {
		t.Errorf("头注释没了（用户会以为这是手写文件）")
	}
}

func TestGenTOMLEscape(t *testing.T) {
	toml, err := genTOML(validCfg(), `to"ken`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(toml, `auth.token = "to\"ken"`) {
		t.Errorf("令牌里的引号没转义，生成的 toml 是坏的:\n%s", toml)
	}
}

func TestGenTOMLValidation(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*FrpConfig)
		want string
	}{
		{"没填服务器", func(c *FrpConfig) { c.Server.Addr = "" }, "服务器地址"},
		{"端口离谱", func(c *FrpConfig) { c.Server.Port = 99999 }, "端口"},
		{"没有隧道", func(c *FrpConfig) { c.Proxies = nil }, "至少加一条隧道"},
		{"tcp 缺对外端口", func(c *FrpConfig) { c.Proxies[0].RemotePort = 0 }, "对外端口"},
		{"http 缺域名", func(c *FrpConfig) { c.Proxies[1].CustomDomain = "" }, "访问域名"},
		{"隧道重名", func(c *FrpConfig) { c.Proxies[1].Name = "dav" }, "重复"},
		{"类型不认识", func(c *FrpConfig) { c.Proxies[1].Type = "udp" }, "tcp / http / https"},
		{"本地端口不对", func(c *FrpConfig) { c.Proxies[0].LocalPort = -1 }, "本地端口"},
	}
	for _, tc := range cases {
		cfg := validCfg()
		tc.mut(&cfg)
		if _, err := genTOML(cfg, "t"); err == nil {
			t.Errorf("%s：应该报错（%s）", tc.name, tc.want)
		} else if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s：报错要提「%s」，实际 %q", tc.name, tc.want, err.Error())
		}
	}
}

// ---------- 配置存取（真库） ----------

// ---------- DNSPod 假服务 ----------

func dnspodStub(t *testing.T, listCode, listMsg string, existing []int64, codes map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/Record.List", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("record_type") != "AAAA" {
			t.Errorf("Record.List 要按 AAAA 过滤, got %v", r.Form)
		}
		recs := []map[string]any{}
		for _, id := range existing {
			recs = append(recs, map[string]any{"id": id, "value": "2001:db8::old", "type": "AAAA", "line": "默认", "line_id": "0"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  map[string]any{"statuscode": listCode, "message": listMsg},
			"records": recs,
		})
	})
	mux.HandleFunc("/Record.Modify", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		codes["modify"] = r.Form.Get("value")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"statuscode": "1", "message": "ok"}})
	})
	mux.HandleFunc("/Record.Create", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		codes["create"] = r.Form.Get("value")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"statuscode": "1", "message": "ok"}})
	})
	return httptest.NewServer(mux)
}

func TestDnspodUpdateModifyExisting(t *testing.T) {
	codes := map[string]string{}
	srv := dnspodStub(t, "1", "ok", []int64{42}, codes)
	old := dnspodAPI
	dnspodAPI = srv.URL
	defer func() { dnspodAPI = old; srv.Close() }()

	msg, err := dnspodUpdate("example.com", "duihao", "12345,abcdef", "2408:8207::9")
	if err != nil {
		t.Fatalf("应成功: %v (%s)", err, msg)
	}
	if codes["modify"] != "2408:8207::9" {
		t.Errorf("应走 Modify 且写入新 IP, got %v", codes)
	}
	if codes["create"] != "" {
		t.Errorf("已有记录不该走 Create")
	}
}

func TestDnspodUpdateCreateWhenMissing(t *testing.T) {
	codes := map[string]string{}
	srv := dnspodStub(t, "1", "ok", nil, codes)
	old := dnspodAPI
	dnspodAPI = srv.URL
	defer func() { dnspodAPI = old; srv.Close() }()

	if _, err := dnspodUpdate("example.com", "", "1,t", "::5"); err != nil {
		t.Fatal(err)
	}
	if codes["create"] != "::5" {
		t.Errorf("没有记录要走 Create, got %v", codes)
	}
}

func TestDnspodTokenFormat(t *testing.T) {
	if _, err := dnspodUpdate("e.com", "@", "只有一段", "::1"); err == nil {
		t.Fatal("两段式令牌校验没生效")
	}
}

// ---------- 阿里云签名 ----------

func TestAliyunEncode(t *testing.T) {
	cases := map[string]string{
		"a-b_c.d~e": "a-b_c.d~e", // 保留字符原样
		"a b":       "a%20b",     // 空格是 %20 不是 +
		"a+b":       "a%2Bb",     // + 是普通字符要编码
		"中":         "%E4%B8%AD", // 大写十六进制
	}
	for in, want := range cases {
		if got := aliyunEncode(in); got != want {
			t.Errorf("aliyunEncode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAliyunTokenFormat(t *testing.T) {
	if _, err := aliyunUpdate("e.com", "@", "没有冒号", "::1"); err == nil {
		t.Fatal("两段式令牌校验没生效")
	}
}

// ---------- 工具 ----------

func TestFullDomain(t *testing.T) {
	if got := fullDomain("a.com", "b"); got != "b.a.com" {
		t.Errorf("got %q", got)
	}
	if got := fullDomain("a.com", "@"); got != "a.com" {
		t.Errorf("got %q", got)
	}
	if got := fullDomain("a.com", ""); got != "a.com" {
		t.Errorf("got %q", got)
	}
}
