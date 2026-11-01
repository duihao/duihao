package download

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// rpcCall 假服务收到的调用。
type rpcCall struct {
	Method string
	Params []any
}

// fakeAria2 起一个假的 aria2 JSON-RPC 服务。
// handle 收到的 params 已经剥掉第一个 "token:xxx"。
func fakeAria2(t *testing.T, secret string, handle func(method string, params []any) (any, *rpcError)) (*httptest.Server, func() []rpcCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []rpcCall

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		mu.Lock()
		calls = append(calls, rpcCall{Method: req.Method, Params: req.Params})
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		// 鉴权：aria2 要求第一个参数就是 token:<secret>
		if len(req.Params) == 0 || req.Params[0] != "token:"+secret {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": "duihao",
				"error": map[string]any{"code": 1, "message": "Unauthorized"},
			})
			return
		}
		res, e := handle(req.Method, req.Params[1:])
		if e != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": "duihao",
				"error": map[string]any{"code": e.Code, "message": e.Message},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": "duihao", "result": res})
	}))
	t.Cleanup(srv.Close)

	return srv, func() []rpcCall {
		mu.Lock()
		defer mu.Unlock()
		out := make([]rpcCall, len(calls))
		copy(out, calls)
		return out
	}
}

// 测试里不该走系统代理：某些环境会把 127.0.0.1 一起劫持（项目里踩过）。
func newTestRPC(url, secret string) *rpcClient {
	return &rpcClient{
		base:   url,
		secret: secret,
		hc:     &http.Client{Transport: &http.Transport{Proxy: nil}},
	}
}

func TestRPCAddSendsTokenAndOptions(t *testing.T) {
	srv, calls := fakeAria2(t, "s3", func(method string, params []any) (any, *rpcError) {
		if method != "aria2.addUri" {
			t.Errorf("方法应为 aria2.addUri，got %s", method)
		}
		return "g-abc", nil
	})
	c := newTestRPC(srv.URL, "s3")

	gid, err := c.add("https://x/y.iso", map[string]string{"dir": `D:\data`, "out": "y.iso"})
	if err != nil {
		t.Fatalf("add 失败: %v", err)
	}
	if gid != "g-abc" {
		t.Fatalf("gid = %q，期望 g-abc", gid)
	}

	got := calls()
	if len(got) != 1 {
		t.Fatalf("应只发生一次调用，got %d", len(got))
	}
	p := got[0].Params
	if p[0] != "token:s3" {
		t.Errorf("第一个参数必须是 token，got %v", p[0])
	}
	// params[1] 是 uri 数组，params[2] 是 options 对象
	uris, ok := p[1].([]any)
	if !ok || len(uris) != 1 || uris[0] != "https://x/y.iso" {
		t.Errorf("uri 参数不对: %v", p[1])
	}
	opts, ok := p[2].(map[string]any)
	if !ok || opts["out"] != "y.iso" {
		t.Errorf("options 没传进去: %v", p[2])
	}
}

func TestRPCWrongSecretRejected(t *testing.T) {
	srv, _ := fakeAria2(t, "right-secret", func(string, []any) (any, *rpcError) {
		return "never", nil
	})
	c := newTestRPC(srv.URL, "wrong-secret")
	if _, err := c.add("https://x/y.iso", nil); err == nil {
		t.Fatal("secret 不对应当报错")
	} else if !strings.Contains(err.Error(), "aria2 错误") {
		t.Errorf("错误信息应带 aria2 前缀: %v", err)
	}
}

func TestRPCStatusParsesStringNumbers(t *testing.T) {
	// aria2 的数字全是字符串 —— 当 number 解析会静默变 0，进度就永远 0%
	srv, _ := fakeAria2(t, "s3", func(method string, params []any) (any, *rpcError) {
		if method != "aria2.tellStatus" {
			t.Errorf("方法应为 aria2.tellStatus，got %s", method)
		}
		if params[0] != "g-1" {
			t.Errorf("gid 参数不对: %v", params[0])
		}
		return map[string]any{
			"gid":             "g-1",
			"status":          "active",
			"totalLength":     "1073741824",
			"completedLength": "268435456",
			"downloadSpeed":   "1048576",
			"numSeeders":      "12",
			"dir":             `D:\data`,
			"files":           []any{map[string]any{"path": `D:\data\ubuntu.iso`}},
		}, nil
	})
	c := newTestRPC(srv.URL, "s3")

	st, err := c.status("g-1")
	if err != nil {
		t.Fatalf("tellStatus 失败: %v", err)
	}
	if st.Status != "active" {
		t.Errorf("状态应 active，got %q", st.Status)
	}
	if atoi64(st.TotalLength) != 1073741824 {
		t.Errorf("totalLength 解析错: %q", st.TotalLength)
	}
	if atoi64(st.CompletedLength) != 268435456 {
		t.Errorf("completedLength 解析错: %q", st.CompletedLength)
	}
	// 文件名要能从 Windows 反斜杠路径里取出来
	if got := st.fileName(); got != "ubuntu.iso" {
		t.Errorf("fileName = %q，期望 ubuntu.iso", got)
	}
}

func TestRPCPauseUnpauseRemoveMethods(t *testing.T) {
	want := map[string]string{
		"aria2.forcePause":           "pause",
		"aria2.unpause":              "unpause",
		"aria2.forceRemove":          "remove",
		"aria2.removeDownloadResult": "removeResult",
	}
	seen := map[string]bool{}
	srv, _ := fakeAria2(t, "s3", func(method string, _ []any) (any, *rpcError) {
		seen[method] = true
		if method == "aria2.removeDownloadResult" {
			return nil, nil
		}
		return "g-1", nil
	})
	c := newTestRPC(srv.URL, "s3")

	if err := c.pause("g-1"); err != nil {
		t.Errorf("pause: %v", err)
	}
	if err := c.unpause("g-1"); err != nil {
		t.Errorf("unpause: %v", err)
	}
	if err := c.remove("g-1"); err != nil {
		t.Errorf("remove: %v", err)
	}
	if err := c.removeResult("g-1"); err != nil {
		t.Errorf("removeResult: %v", err)
	}
	for m := range want {
		if !seen[m] {
			t.Errorf("没调到 %s", m)
		}
	}
}

func TestRPCGlobalStatAndLimits(t *testing.T) {
	var limitOpt map[string]any
	srv, _ := fakeAria2(t, "s3", func(method string, params []any) (any, *rpcError) {
		switch method {
		case "aria2.getGlobalStat":
			return map[string]any{
				"downloadSpeed": "2048000", "uploadSpeed": "1024",
				"numActive": "2", "numWaiting": "3", "numStoppedTotal": "7",
			}, nil
		case "aria2.changeGlobalOption":
			limitOpt, _ = params[0].(map[string]any)
			return "OK", nil
		}
		return nil, nil
	})
	c := newTestRPC(srv.URL, "s3")

	down, up, active, waiting, err := c.globalStat()
	if err != nil {
		t.Fatalf("getGlobalStat: %v", err)
	}
	if down != 2048000 || up != 1024 || active != 2 || waiting != 3 {
		t.Errorf("全局统计解析错: %d %d %d %d", down, up, active, waiting)
	}

	if err := c.applyLimits(1024, 64, 5); err != nil {
		t.Fatalf("applyLimits: %v", err)
	}
	if limitOpt["max-overall-download-limit"] != "1024K" {
		t.Errorf("下载限速格式应为 1024K，got %v", limitOpt["max-overall-download-limit"])
	}
	if limitOpt["max-concurrent-downloads"] != "5" {
		t.Errorf("并发应为 5，got %v", limitOpt["max-concurrent-downloads"])
	}
}

func TestRPCUnreachableGivesReadableError(t *testing.T) {
	// 端口上没人：错误信息要能让人看懂，而不是 rpc error 之类的黑话
	c := newTestRPC("http://127.0.0.1:1/jsonrpc", "s3")
	_, err := c.version()
	if err == nil {
		t.Fatal("连不上应当报错")
	}
	if !strings.Contains(err.Error(), "连不上下载引擎") {
		t.Errorf("错误信息应可读，got: %v", err)
	}
}
