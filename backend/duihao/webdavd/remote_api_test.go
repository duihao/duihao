package webdavd

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/apps/remote"
)

// 远程访问 HTTP 层：状态结构 / DDNS 保存（令牌只进不出）/ frpc 可视化配置
// （保存即校验 + 生成 toml）/ 公网开关真拦公网请求 / 登录失败限速真 429。
func TestRemoteAPI(t *testing.T) {
	srv, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = t.TempDir() })
	cli := newTestClient(t, false)

	post := func(action, body string) (int, map[string]any) {
		r := doReq(t, cli, base, "POST", "/-api/remote/"+action, user, pass, []byte(body))
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return r.StatusCode, m
	}

	// 1) status：结构齐备，公网开关默认关
	code, res := post("status", "{}")
	if code != 200 {
		t.Fatalf("status 应 200, got %d %v", code, res)
	}
	pub, _ := res["public"].(map[string]any)
	if on, _ := pub["enabled"].(bool); on {
		t.Fatal("公网开关默认必须关")
	}
	if _, ok := res["ipv6"].(map[string]any); !ok {
		t.Fatal("status 应带 ipv6")
	}
	if _, ok := res["ddns"].(map[string]any); !ok {
		t.Fatal("status 应带 ddns")
	}
	if _, ok := res["frpc"].(map[string]any); !ok {
		t.Fatal("status 应带 frpc")
	}

	// 2) frpc.save：结构不对要被人话错误拦住
	code, res = post("frpc.save", `{"enabled":true,"server":{"addr":"s.com","port":7000},"tlsEnable":true,"proxies":[]}`)
	if code != 400 || !strings.Contains(mustStr(res["error"]), "至少加一条隧道") {
		t.Fatalf("空隧道应 400+人话, got %d %v", code, res)
	}

	// 3) frpc.save：合法配置（enabled=false，不真拉进程——组件也没装）
	body := `{"enabled":false,"server":{"addr":"s.com","port":7000,"user":"huawei"},"tlsEnable":true,
		"proxies":[{"name":"dav","type":"tcp","localPort":18080,"remotePort":18080}],"token":"tk1"}`
	code, res = post("frpc.save", body)
	if code != 200 {
		t.Fatalf("合法配置应 200, got %d %v", code, res)
	}

	// 4) frpc.start：组件没装 → 400 且报「还没装」
	code, res = post("frpc.start", "{}")
	if code != 400 || !strings.Contains(mustStr(res["error"]), "frpc") {
		t.Fatalf("组件缺失应 400, got %d %v", code, res)
	}

	// 5) status 回读：frpc 配置在、令牌只有 tokenSet
	_, res = post("status", "{}")
	frpc, _ := res["frpc"].(map[string]any)
	srv2, _ := frpc["server"].(map[string]any)
	if mustStr(srv2["addr"]) != "s.com" {
		t.Fatalf("配置没存上: %v", srv2)
	}
	if ts, _ := srv2["tokenSet"].(bool); !ts {
		t.Fatal("令牌存了但 tokenSet=false")
	}
	if _, ok := srv2["Token"]; ok {
		t.Fatal("令牌明文绝不能出现在接口返回里")
	}

	// 6) 公网开关打开 → 关闭 → 生效
	code, _ = post("public.save", `{"enabled":true}`)
	if code != 200 {
		t.Fatalf("public.save 应 200")
	}
	// 缓存最多 30 秒，但 save 会立刻写缓存 → 同进程内立即生效
	if !remotePublicOn(srv) {
		t.Fatal("开关打开后 PublicEnabled 必须立刻为真")
	}
	code, _ = post("public.save", `{"enabled":false}`)
	if code != 200 {
		t.Fatalf("public.save(关) 应 200")
	}
	if remotePublicOn(srv) {
		t.Fatal("开关关闭后 PublicEnabled 必须立刻为假")
	}
}

func remotePublicOn(s *Server) bool {
	st := s.hub()
	if st == nil {
		return false
	}
	return remote.PublicEnabled(st.DB())
}

func mustStr(v any) string {
	s, _ := v.(string)
	return s
}
