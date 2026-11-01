package download

import (
	"strings"
	"testing"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
)

// 引擎参数里藏着几条安全/行为红线，改参数时必须有断言兜住。
func TestEngineArgs(t *testing.T) {
	st := defaultSetting()
	args := strings.Join(engineArgs(6800, "s3cret", `D:\data\_appfiles\download`, st), " ")

	mustHave := []string{
		"--enable-rpc=true",
		"--rpc-listen-all=false", // 只回环，绝不能让局域网扫到 6800
		"--rpc-listen-port=6800",
		"--rpc-secret=s3cret",
		"--continue=true",   // 断点续传
		"--seed-time=0",     // 默认不做种
		"--enable-dht=true", // 默认设置下 BT 网络开着
		"--listen-port=6881-6899",
		"--stop-with-process=", // 对号死了它得跟着退
	}
	for _, w := range mustHave {
		if !strings.Contains(args, w) {
			t.Errorf("默认参数缺少 %q\n实际：%s", w, args)
		}
	}

	// 关掉 BT 网络：不许再监听，磁力也不再走 DHT
	off := defaultSetting()
	off.BtNet = false
	args2 := strings.Join(engineArgs(6800, "x", "d", off), " ")
	if strings.Contains(args2, "--enable-dht=true") {
		t.Error("btNet=false 时不该开 DHT")
	}
	if strings.Contains(args2, "--listen-port=6881") {
		t.Error("btNet=false 时不该开 6881 监听")
	}

	// 开做种：seed-time 要变成有限长（不是 0，否则等于不做种）
	seed := defaultSetting()
	seed.Seed = true
	args3 := strings.Join(engineArgs(6800, "x", "d", seed), " ")
	if strings.Contains(args3, "--seed-time=0") {
		t.Error("seed=true 时不该是 seed-time=0")
	}
	if !strings.Contains(args3, "--seed-time=1440") {
		t.Error("seed=true 应为 1440 分钟")
	}

	// 并发与限速要落进参数
	lim := defaultSetting()
	lim.MaxConc = 5
	lim.DownLimit = 2048
	args4 := strings.Join(engineArgs(6800, "x", "d", lim), " ")
	if !strings.Contains(args4, "--max-concurrent-downloads=5") {
		t.Error("并发数没进参数")
	}
	if !strings.Contains(args4, "--max-overall-download-limit=2048K") {
		t.Error("限速没进参数")
	}
}

func TestKindOf(t *testing.T) {
	ok := map[string]string{
		"http://a/b.iso":                    "http",
		"https://a/b.iso":                   "http",
		"ftp://a/b.iso":                     "http",
		"magnet:?xt=urn:btih:abc&dn=x":      "magnet",
		"MAGNET:?xt=urn:btih:abc":           "magnet",
		"  https://example.com/a.torrent  ": "http",
	}
	for in, want := range ok {
		got, yes := kindOf(in)
		if !yes || got != want {
			t.Errorf("kindOf(%q) = %q,%v 期望 %q", in, got, yes, want)
		}
	}
	// 本地路径与内网地址一律不收：下载器不该能读本机文件
	for _, bad := range []string{"file:///c:/x", "C:\\x\\y.iso", "//host/share/a", "javascript:alert(1)", ""} {
		if _, yes := kindOf(bad); yes {
			t.Errorf("kindOf(%q) 不该通过", bad)
		}
	}
}

func TestGuessName(t *testing.T) {
	cases := map[string]string{
		"https://mirror.example.com/iso/ubuntu-24.04.iso":       "ubuntu-24.04.iso",
		"https://a/b/file.mkv?token=1&x=2":                      "file.mkv",
		"https://a/b/movie.mp4#t=10":                            "movie.mp4",
		"magnet:?xt=urn:btih:aaaa&dn=Some.Movie.2024.1080p.mkv": "Some.Movie.2024.1080p.mkv",
		"magnet:?xt=urn:btih:aaaa":                              "磁力任务",
		"https://a/":                                            "下载任务",
	}
	for in, want := range cases {
		if got := guessName(in); got != want {
			t.Errorf("guessName(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestNormDir(t *testing.T) {
	if normDir("video") != "video" || normDir("music") != "music" {
		t.Error("合法落点不该被改")
	}
	// 手改请求想写到别处：一律回落到下载目录
	for _, bad := range []string{"", "download", "../etc", "c:/windows", "VIDEO"} {
		if got := normDir(bad); got != "download" && bad != "video" && bad != "music" {
			if got != "download" {
				t.Errorf("normDir(%q) = %q，应回落 download", bad, got)
			}
		}
	}
}

func TestTaskDirUnderAppfiles(t *testing.T) {
	c := testCtx(t, t.TempDir())
	for dir, want := range map[string]string{
		"download": "_appfiles/download",
		"video":    "_appfiles/video",
		"music":    "_appfiles/music",
		"":         "_appfiles/download",
		"weird":    "_appfiles/download",
	} {
		got := taskDir(c, dir)
		if !strings.HasSuffix(strings.ReplaceAll(got, "\\", "/"), want) {
			t.Errorf("taskDir(%q) = %q，应以 %q 结尾", dir, got, want)
		}
	}
}

func TestSettingNorm(t *testing.T) {
	s := normSetting(Setting{MaxConc: 0, DownLimit: -5, UpLimit: -1, AutoDir: "nope"})
	if s.MaxConc != 3 {
		t.Errorf("并发 0 应变 3，got %d", s.MaxConc)
	}
	if s.DownLimit != 0 || s.UpLimit != 0 {
		t.Error("负限速应归零")
	}
	if s.AutoDir != "download" {
		t.Errorf("非法落点应回落 download，got %q", s.AutoDir)
	}
	if normSetting(Setting{MaxConc: 99}).MaxConc != 3 {
		t.Error("并发超上限应回落 3")
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		0:       "0 B",
		512:     "512 B",
		2048:    "2.0 KB",
		5 << 20: "5.0 MB",
		3 << 30: "3.0 GB",
	}
	for in, want := range cases {
		if got := humanSize(in); got != want {
			t.Errorf("humanSize(%d) = %q，期望 %q", in, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("abcdef", 3); got != "abc…" {
		t.Errorf("截断结果 %q", got)
	}
	// 按 rune 截，不能把中文切成半个字节
	if got := truncate("中文测试", 2); got != "中文…" {
		t.Errorf("中文截断 %q", got)
	}
	if got := truncate("abc", 10); got != "abc" {
		t.Errorf("不超长不该动，got %q", got)
	}
}

// testCtx 构造一个最小的请求上下文（这些纯函数只用到 Root / User）。
func testCtx(t *testing.T, root string) appkit.Context {
	t.Helper()
	return appkit.Context{User: "u1", Root: root, Writable: true}
}
