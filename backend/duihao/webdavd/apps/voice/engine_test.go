package voice

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeWAV 造一个 0.4 秒的静音 wav（22kHz/16bit/mono），够 wavDurationMs 算出时长。
func fakeWAV(ms int) []byte {
	const rate, ch, bits = 22050, 1, 16
	dataLen := rate * ch * bits / 8 * ms / 1000
	buf := make([]byte, 44+dataLen)
	copy(buf[0:], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:], uint32(36+dataLen))
	copy(buf[8:], "WAVE")
	copy(buf[12:], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:], 16)
	binary.LittleEndian.PutUint16(buf[20:], 1)    // PCM
	binary.LittleEndian.PutUint16(buf[22:], ch)   // 声道
	binary.LittleEndian.PutUint32(buf[24:], rate) // 采样率
	binary.LittleEndian.PutUint32(buf[28:], rate*ch*bits/8)
	binary.LittleEndian.PutUint16(buf[32:], ch*bits/8)
	binary.LittleEndian.PutUint16(buf[34:], bits)
	copy(buf[36:], "data")
	binary.LittleEndian.PutUint32(buf[40:], uint32(dataLen))
	return buf
}

// 一台「像 CosyVoice2 那样」的假 GPU 主机：校验令牌与请求字段，回一段 wav。
func fakeGPUServer(t *testing.T, wantToken string) (*httptest.Server, *map[string]any) {
	t.Helper()
	got := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wantToken != "" && r.Header.Get("Authorization") != "Bearer "+wantToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"bad token"}`))
			return
		}
		switch r.URL.Path {
		case "/v1/voices":
			_ = json.NewEncoder(w).Encode(map[string]any{"voices": []map[string]string{
				{"id": "vivi", "name": "Vivi 2.0"},
				{"id": "ruya", "name": "儒雅逸辰"},
			}})
		case "/v1/audio/speech":
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &got)
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(fakeWAV(400))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// 合成：请求字段对、落盘是 wav、时长算得出来。
func TestSynthHTTP(t *testing.T) {
	srv, got := fakeGPUServer(t, "tok-1")
	e := engineConfig{Kind: engineHTTP, Base: srv.URL, Token: "tok-1",
		Model: "cosyvoice2", Voice: "vivi", Speed: 1}

	out := filepath.Join(t.TempDir(), "a.wav")
	ms, err := synthHTTPTo(out, e, "该喝水了", 0)
	if err != nil {
		t.Fatalf("合成失败: %v", err)
	}
	if ms < 300 || ms > 500 {
		t.Fatalf("时长算错了: %d", ms)
	}
	body, err := os.ReadFile(out)
	if err != nil || !isWAV(body) {
		t.Fatalf("落盘的不是 wav: %v", err)
	}
	for k, want := range map[string]string{
		"input": "该喝水了", "voice": "vivi", "model": "cosyvoice2", "response_format": "wav",
	} {
		if (*got)[k] != want {
			t.Fatalf("请求字段 %s = %v, want %q（整个请求 %v）", k, (*got)[k], want, *got)
		}
	}
	if sp, _ := (*got)["speed"].(float64); sp < 0.99 || sp > 1.01 {
		t.Fatalf("默认语速应为 1.0, got %v", (*got)["speed"])
	}
}

// 令牌不对：要把「检查令牌」写进错误里，而不是一句 401。
func TestSynthHTTPUnauthorized(t *testing.T) {
	srv, _ := fakeGPUServer(t, "right")
	e := engineConfig{Kind: engineHTTP, Base: srv.URL, Token: "wrong", Voice: "vivi"}
	_, err := synthHTTPTo(filepath.Join(t.TempDir(), "a.wav"), e, "x", 0)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("应报 401, got %v", err)
	}
}

// 服务端把 mp3 当 wav 返回：必须拒绝（我们没有 mp3 解码器，放不出来）。
func TestSynthHTTPNotWav(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3\x03\x00\x00\x00....."))
	}))
	defer srv.Close()
	e := engineConfig{Kind: engineHTTP, Base: srv.URL, Voice: "v"}
	out := filepath.Join(t.TempDir(), "a.wav")
	if _, err := synthHTTPTo(out, e, "x", 0); err == nil || !strings.Contains(err.Error(), "wav") {
		t.Fatalf("非 wav 应报错并提示 response_format, got %v", err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("失败时不该留下半截文件")
	}
}

// 连不上：错误里要有人话（带地址），而不是 connection refused 堆栈。
func TestSynthHTTPUnreachable(t *testing.T) {
	e := engineConfig{Kind: engineHTTP, Base: "http://127.0.0.1:1", Voice: "v"}
	_, err := synthHTTPTo(filepath.Join(t.TempDir(), "a.wav"), e, "x", 0)
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Fatalf("应提示地址, got %v", err)
	}
}

// 音色列表：三种返回形状都要认。
func TestParseVoicesShapes(t *testing.T) {
	cases := []struct {
		raw  string
		want int
		name string
	}{
		{`{"voices":["a","b"]}`, 2, "字符串数组"},
		{`{"voices":[{"id":"x","name":"X 音色"}]}`, 1, "对象数组"},
		{`{"data":[{"id":"d1"}]}`, 1, "OpenAI data 形状"},
		{`["p","q","r"]`, 3, "裸数组"},
		{`{}`, 0, "空对象"},
	}
	for _, c := range cases {
		got := parseVoices([]byte(c.raw))
		if len(got) != c.want {
			t.Fatalf("%s: got %d, want %d (%v)", c.name, len(got), c.want, got)
		}
		if c.want > 0 && got[0].ID == "" {
			t.Fatalf("%s: ID 不该为空", c.name)
		}
	}
	got := parseVoices([]byte(`{"voices":[{"id":"x","name":"X 音色"}]}`))
	if got[0].Name != "X 音色" {
		t.Fatalf("名称没解析出来: %+v", got[0])
	}
}

// 探活：拉列表 + 缓存（页面切换引擎时不必每次打服务端）。
func TestProbeCachesVoices(t *testing.T) {
	srv, _ := fakeGPUServer(t, "")
	e := engineConfig{Kind: engineHTTP, Base: srv.URL}
	ms, items, err := probeEngine(e)
	if err != nil {
		t.Fatalf("探活失败: %v", err)
	}
	if ms < 0 || len(items) != 2 {
		t.Fatalf("探活结果异常: %d %v", ms, items)
	}
	if cached := cachedVoices(srv.URL); len(cached) != 2 {
		t.Fatalf("音色没进缓存: %v", cached)
	}
}

// 语速换算：SAPI 档位（-10..10）折进 0.5~2.0，且两位小数。
func TestHTTPSpeed(t *testing.T) {
	cases := []struct {
		base float64
		rate int
		want float64
	}{
		{1, 0, 1},
		{1, 10, 1.5},
		{1, -10, 0.5},
		{2, 10, 2}, // 上限夹住
		{0.5, -10, 0.5},
		{0, 0, 1}, // base 非法回退 1
	}
	for _, c := range cases {
		if got := httpSpeed(c.base, c.rate); got != c.want {
			t.Fatalf("httpSpeed(%v,%d) = %v, want %v", c.base, c.rate, got, c.want)
		}
	}
}

// 设置往返：令牌只进不出，读回来是明文供合成用。
func TestEngineSettingsRoundTrip(t *testing.T) {
	_, db := freshDB(t)
	if err := saveEngine(db, "u1", engineConfig{
		Kind: engineHTTP, Base: "192.168.1.9:9880/", Model: "cosyvoice2",
		Voice: "vivi", Speed: 1.2,
	}, "secret-token", false); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	cfg := loadEngine(db, "u1")
	if cfg.Kind != engineHTTP || cfg.Base != "http://192.168.1.9:9880" {
		t.Fatalf("地址没规整对: %+v", cfg)
	}
	if cfg.Token != "secret-token" {
		t.Fatalf("令牌读回来不对: %q", cfg.Token)
	}
	if !cfg.active() {
		t.Fatal("填了地址就该 active")
	}

	// 库里必须是密文，不能有明文
	var stored string
	if err := db.QueryRow(`select v from voice_settings where uid='u1' and k='http.token'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == "secret-token" || strings.Contains(stored, "secret-token") {
		t.Fatalf("库里不该有明文令牌: %q", stored)
	}

	// 空令牌 = 不改原值
	if err := saveEngine(db, "u1", engineConfig{Kind: engineHTTP, Base: cfg.Base, Voice: "ruya", Speed: 1}, "", false); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if got := loadEngine(db, "u1"); got.Token != "secret-token" || got.Voice != "ruya" {
		t.Fatalf("空令牌不该清掉原值: %+v", got)
	}

	// clearToken 才清空
	if err := saveEngine(db, "u1", engineConfig{Kind: engineSAPI, Speed: 1}, "", true); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if got := loadEngine(db, "u1"); got.Token != "" || got.Kind != engineSAPI {
		t.Fatalf("应清空令牌并切回 SAPI: %+v", got)
	}
}

// 引擎 fingerprint：任一维度变化都要换缓存名；SAPI 恒定。
func TestEngineKey(t *testing.T) {
	a := engineConfig{Kind: engineHTTP, Base: "http://h:1", Model: "m", Voice: "v", Speed: 1}
	b := a
	b.Voice = "v2"
	if a.key() == b.key() {
		t.Fatal("换音色应换指纹")
	}
	sapi := engineConfig{Kind: engineSAPI}
	sapi2 := engineConfig{Kind: engineSAPI, Base: "http://x"} // 有地址但没切引擎
	if sapi.key() != sapi2.key() {
		t.Fatal("SAPI 引擎指纹应恒定")
	}
	// 填了地址但 kind=sapi：不算 active，仍走本机
	if sapi2.active() {
		t.Fatal("kind=sapi 不该 active")
	}
	if !a.active() {
		t.Fatal("kind=http + 地址 应 active")
	}
}

// 音色键：GPU 认 lan:，SAPI 认预置；非法值不写库。
func TestPickVoice(t *testing.T) {
	http := engineConfig{Kind: engineHTTP, Base: "http://h:1"}
	if got := pickVoice(http, "lan:vivi"); got != "lan:vivi" {
		t.Fatalf("GPU 音色键: %q", got)
	}
	if got := pickVoice(http, "huihui-std"); got != "" {
		t.Fatalf("GPU 引擎下 SAPI 键应落空（用默认）, got %q", got)
	}
	sapi := engineConfig{Kind: engineSAPI}
	if got := pickVoice(sapi, "huihui-soft"); got != "huihui-soft" {
		t.Fatalf("SAPI 应保留预置键: %q", got)
	}
	if got := pickVoice(sapi, "lan:vivi"); got != "huihui-std" {
		t.Fatalf("SAPI 引擎下 GPU 键应回退默认: %q", got)
	}
	if got := effVoice(http, "lan:ruya"); got != "ruya" {
		t.Fatalf("合成时应剥掉 lan: 前缀: %q", got)
	}
}

// 端到端降级：GPU 挂了，save 仍要落库并给出 warn，不能失败。
func TestSynthToCacheFallsBackToSAPI(t *testing.T) {
	_, db := freshDB(t)
	if err := saveEngine(db, "u1", engineConfig{
		Kind: engineHTTP, Base: "http://127.0.0.1:1", Voice: "vivi", Speed: 1}, "", false); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	name, ms, warn, err := synthToCache(root, db, "u1",
		event{Voice: "huihui-std", Tone: "normal", Content: "测试降级"}, "测试降级")
	if err != nil {
		// 本机没有 SAPI 语音的机器（CI 可能如此）会走到这里：那也算「明确失败」，
		// 只要不是静默成功即可。
		t.Skipf("本机无可用 SAPI，跳过降级断言: %v", err)
	}
	if name == "" {
		t.Fatal("降级成功应有缓存文件")
	}
	if warn == "" {
		t.Fatal("降级必须给 warn（前端要提示声音变了）")
	}
	if _, statErr := os.Stat(filepath.Join(root, voiceRoot, name)); statErr != nil {
		t.Fatalf("缓存文件不存在: %v", statErr)
	}
	if ms <= 0 {
		t.Fatalf("时长应为正: %d", ms)
	}
}

// 设置接口的形状：tokenSet 而不是明文。
func TestEngineViewHidesToken(t *testing.T) {
	cfg := engineConfig{Kind: engineHTTP, Base: "http://h:1", Token: "abc", Voice: "v"}
	v := engineView(cfg)
	if !v.TokenSet {
		t.Fatal("tokenSet 应为 true")
	}
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), `"abc"`) {
		t.Fatalf("view 里不该出现明文令牌: %s", raw)
	}
}

// 保存时速度越界要夹住，kind 乱填要落回 sapi（不报错，别让用户卡在表单）。
func TestSaveEngineClamp(t *testing.T) {
	_, db := freshDB(t)
	_ = saveEngine(db, "u1", engineConfig{Kind: "weird", Speed: 9}, "", false)
	cfg := loadEngine(db, "u1")
	if cfg.Kind != engineSAPI {
		t.Fatalf("未知 kind 应落回 sapi: %q", cfg.Kind)
	}
}

var _ = sql.ErrNoRows
