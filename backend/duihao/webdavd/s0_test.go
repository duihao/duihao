package webdavd

import (
	"database/sql"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// S0 骨架验收：单端口多应用（/-home|/-notes|/-ledger|/-video 页面、/-api/* 接口、
// /-ui/* 静态资源）+ 单库 hub.db 自动迁移 + 只读账号写接口拒绝。
func TestPersonalSpaceSkeleton(t *testing.T) {
	instanceDir := t.TempDir()
	srv, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = instanceDir })
	cli := newTestClient(t, false)

	get := func(path string) (int, string) {
		r := doReq(t, cli, base, "GET", path, user, pass, nil)
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}

	// 1) 四个页面都能开，且带统一顶栏与 data-app
	for _, c := range []struct{ path, app, marker string }{
		{"/-home", "home", `id="board"`},         // 收藏夹首页：分组图标墙
		{"/-notes", "notes", `id="body"`},        // 笔记：编辑器正文框
		{"/-ledger", "ledger", `id="st-assets"`}, // 账本：储蓄总览统计卡
		{"/-video", "video", `id="v-wall"`},
		{"/-music", "music", `id="m-list"`},
		{"/-health", "health", `id="h-stats"`},
		{"/-voice", "voice", `id="v-grid"`},
		{"/-camera", "camera", `id="c-cams"`},
		{"/-download", "download", `id="d-list"`},
		{"/-remote", "remote", `id="r-tunnels"`}, // 远程：frp 隧道容器（漏引 js 会白页）
	} {
		code, body := get(c.path)
		if code != 200 {
			t.Fatalf("%s 应 200, got %d", c.path, code)
		}
		// 每个应用页必须自己引应用脚本 —— 少一行 script 页面就是全空白（S4 影视踩过）
		for _, want := range []string{`data-app="` + c.app + `"`, "对号", "NAS", c.marker,
			">账本<", ">网盘<", ">收藏夹<", ">语音提醒<", ">监控<", ">下载<", "/-ui/apps/" + c.app + ".js"} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s 缺少 %q", c.path, want)
			}
		}
	}

	// 1.35) 健康页结构：六个分区（概览/报告/趋势/日常/用药/就诊）+ 附件区 + 隐私条
	if _, body := get("/-health"); true {
		for _, want := range []string{`id="h-trends"`, `id="h-daily"`, `id="h-meds"`, `id="h-visits"`,
			`id="h-svg"`, `id="h-vk"`, `id="h-atts"`, `id="h-privhint"`, `id="h-file"`, `data-sec="trends"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("健康页缺少 %q", want)
			}
		}
	}

	// 1.36) 远程页 IPv6 三项是「一整块、上下排」（rm-facts）：左右两列的 rm-kv 会让
	// 39 字符的地址折行后顶到值列，看着像没对齐（用户提过）
	if _, body := get("/-remote"); true {
		for _, want := range []string{`class="rm-facts"`, `id="r-v6-addr"`, `id="r-v6-out"`, `id="r-v6-url"`} {
			if !strings.Contains(body, want) {
				t.Fatalf("远程页缺少 %q", want)
			}
		}
		if strings.Contains(body, `<div class="rm-kv"><span>本机公网 IPv6`) {
			t.Fatalf("远程页 IPv6 又回到左右两列（rm-kv）")
		}
	}

	// 1.4) 收藏夹首页结构定稿：搜索框带搜索引擎下拉、管理走弹窗、通用输入弹窗、已无快捷块
	if _, body := get("/-home"); true {
		for _, want := range []string{`id="engine"`, `id="manage-mask"`, `id="ask-mask"`, `data-view="vcard"`, `id="btn-add-site"`, "收藏管理", "/-ui/apps/home.js"} {
			if !strings.Contains(body, want) {
				t.Fatalf("收藏夹首页缺少 %q", want)
			}
		}
		for _, bad := range []string{"bm-tiles", "btn-sym"} {
			if strings.Contains(body, bad) {
				t.Fatalf("收藏夹首页不应再有 %q", bad)
			}
		}
	}

	// 1.5) 网盘就在根路径 "/"（WebDAV 根 = 文件管理器首页，不设别名）
	if code, body := get("/"); code != 200 || !strings.Contains(body, `id="rows"`) {
		t.Fatalf("/ 应渲染文件管理器, got %d", code)
	} else {
		// 顶部必须与其他页面同一套：最外层 .hub-top（品牌 + 五项导航 + 账号），在 .shell 之前
		top := strings.Index(body, `<header class="hub-top">`)
		shell := strings.Index(body, `<div class="shell">`)
		if top < 0 {
			t.Fatal("网盘页缺少最外层 .hub-top 顶栏")
		}
		if shell < 0 || top > shell {
			t.Fatal(".hub-top 必须在 .shell 之前（页面最外层顶部）")
		}
		for _, want := range []string{`class="hub-brand"`, `class="hub-nav"`, `href="/"`, `>网盘<`, `>收藏夹<`} {
			if !strings.Contains(body, want) {
				t.Fatalf("网盘顶栏缺少 %q", want)
			}
		}
		// 旧的内嵌顶栏不应再出现（否则就是两套）
		for _, gone := range []string{`class="hubnav"`, `class="mbrand"`} {
			if strings.Contains(body, gone) {
				t.Fatalf("网盘页仍残留旧顶栏 %q", gone)
			}
		}
	}

	// 1.6) 顶栏顺序定稿：网盘 · 影视 · 音乐 · 笔记 · 账本 · 健康 · 收藏夹（顺序错了 UI 就散了）
	if _, body := get("/-home"); true {
		prev := -1
		for _, label := range []string{">网盘<", ">影视<", ">音乐<", ">笔记<", ">账本<", ">健康<", ">收藏夹<"} {
			at := strings.Index(body, label)
			if at < 0 {
				t.Fatalf("顶栏缺少 %s", label)
			}
			if at < prev {
				t.Fatalf("顶栏顺序错：%s 应在上一项之后", label)
			}
			prev = at
		}
	}

	// 1.7) 顶栏右上角账号块：用户名 + 下拉（修改密码 / 退出），网盘页与应用页同一套。
	// 之前这里是一个裸「退出」链接，用户要求收进用户名下拉 —— 两类页面都得有。
	for _, p := range []string{"/-home", "/"} {
		_, body := get(p)
		for _, want := range []string{`class="hub-acct"`, `class="hub-acct-btn"`, `data-acct="passwd"`,
			`>修改密码<`, `>退出<`, `href="/-logout"`, `/-ui/acct.js`} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s 顶栏账号块缺少 %q", p, want)
			}
		}
		if strings.Contains(body, `class="hub-out"`) {
			t.Fatalf("%s 仍残留旧的裸「退出」按钮", p)
		}
		// 账号块必须在导航之后（贴右上角），且只有一个
		if n := strings.Count(body, `class="hub-acct"`); n != 1 {
			t.Fatalf("%s 账号块数量 %d，应为 1", p, n)
		}
	}
	// acct.js 是共享静态资源（两类页面都引），必须能取到并且真的处理 passwd
	if r := doReq(t, cli, base, "GET", "/-ui/acct.js", user, pass, nil); r.StatusCode != 200 {
		t.Fatalf("acct.js 应 200, got %d", r.StatusCode)
	} else if b, _ := io.ReadAll(r.Body); !strings.Contains(string(b), "/-api/users/passwd") {
		t.Fatal("acct.js 未指向 /-api/users/passwd")
	}

	// 2) 静态资源
	if r := doReq(t, cli, base, "GET", "/-ui/shared.css", user, pass, nil); r.StatusCode != 200 ||
		!strings.Contains(r.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("shared.css 应 200 text/css, got %d %s", r.StatusCode, r.Header.Get("Content-Type"))
	}
	// 3) 目录穿越被挡
	if r := doReq(t, cli, base, "GET", "/-ui/../settings.json", user, pass, nil); r.StatusCode == 200 {
		t.Fatal("路径穿越应被拒绝")
	}

	// 4) 接口自检走 JSON（账号取共享用户名）
	r := doReq(t, cli, base, "POST", "/-api/home/ping", user, pass, []byte("{}"))
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 || !strings.Contains(string(b), user) || !strings.Contains(string(b), `"ok":true`) {
		t.Fatalf("ping 响应异常: %d %s", r.StatusCode, string(b))
	}
	// 5) 未知应用/动作
	if r := doReq(t, cli, base, "GET", "/-/nope", user, pass, nil); r.StatusCode == 200 {
		t.Fatal("未注册应用不应 200")
	}
	if r := doReq(t, cli, base, "POST", "/-api/home/nosuch", user, pass, []byte("{}")); r.StatusCode != 404 {
		t.Fatalf("未知动作应 404, got %d", r.StatusCode)
	}
	// 6) 跨站 POST 被拒（Origin 与 Host 不一致 → 403）
	xreq, _ := http.NewRequest("POST", "http://"+base+"/-api/home/ping", strings.NewReader("{}"))
	xreq.Header.Set("Origin", "http://evil.example")
	xreq.SetBasicAuth(user, pass)
	xr, err := newTestClient(t, false).Do(xreq)
	if err != nil {
		t.Fatalf("跨站请求: %v", err)
	}
	_ = xr.Body.Close()
	if xr.StatusCode != 403 {
		t.Fatalf("跨站 POST 应 403, got %d", xr.StatusCode)
	}

	// 7) 单库已建并迁移到最新版本
	dbPath := filepath.Join(instanceDir, "hub.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?mode=ro")
	if err != nil {
		t.Fatalf("打开 hub.db: %v", err)
	}
	defer func() { _ = db.Close() }()
	var ver int
	if err := db.QueryRow("PRAGMA user_version").Scan(&ver); err != nil || ver != 19 {
		t.Fatalf("user_version 应为 19, got %d err=%v", ver, err)
	}
	for _, tb := range []string{"bookmarks", "bookmark_groups", "notes", "note_cats", "ledger_entries", "ledger_groups", "ledger_cats", "ledger_peers", "ledger_meta", "video_items", "video_progress", "favicons",
		"health_members", "health_reports", "health_metrics",
		"health_vitals", "health_meds", "health_visits",
		"music_tracks", "music_playlists", "music_pl_items", "music_stats", "music_exports",
		"voice_events", "voice_logs", "voice_settings",
		"cameras", "camera_segments", "camera_logs",
		"download_tasks", "download_settings",
		"remote_settings", "hub_users", "hub_secrets"} {
		var n int
		if err := db.QueryRow("select count(*) from sqlite_master where type='table' and name=?", tb).Scan(&n); err != nil || n != 1 {
			t.Fatalf("缺表 %s (n=%d err=%v)", tb, n, err)
		}
	}
	_ = srv
}

// 只读共享：写动作一律 405，页面仍可打开。
func TestPersonalSpaceReadOnly(t *testing.T) {
	instanceDir := t.TempDir()
	_, base, user, pass := startTestServer(t, func(c *Config) {
		c.InstanceDir = instanceDir
		c.Shares[0].ReadOnly = true
	})
	cli := newTestClient(t, false)

	if r := doReq(t, cli, base, "GET", "/-home", user, pass, nil); r.StatusCode != 200 {
		t.Fatalf("只读账号仍应能看页面, got %d", r.StatusCode)
	}
	r := doReq(t, cli, base, "POST", "/-api/notes/save", user, pass, []byte("{}"))
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != 405 || !strings.Contains(string(b), "只读") {
		t.Fatalf("只读写动作应 405 且说明原因, got %d %s", r.StatusCode, string(b))
	}
}
