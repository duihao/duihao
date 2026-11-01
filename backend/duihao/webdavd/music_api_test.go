package webdavd

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// 音乐模块 HTTP 层：扫描 / 列表 / facets / 歌单 CRUD / drive.list / usb 校验 /
// 导出历史 / 只读 405。拷贝逻辑在 apps/music 包内直测（TestDoExport）。
func TestMusicAPI(t *testing.T) {
	srv, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = t.TempDir() })
	root := srv.cfg.Shares[0].Root

	// 造两个假音频（内容无所谓，扫描只认扩展名；标签测试在包内）
	mroot := filepath.Join(root, "_appfiles", "music", "测试歌手")
	if err := os.MkdirAll(mroot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mroot, "歌一.mp3"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mroot, "歌二.flac"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}

	cli := newTestClient(t, false)
	num := func(v any) int { f, _ := v.(float64); return int(f) }
	post := func(action, body string) (int, map[string]any) {
		r := doReq(t, cli, base, "POST", "/-api/music/"+action, user, pass, []byte(body))
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return r.StatusCode, m
	}

	// 1) 首次 list 触发懒同步
	code, res := post("list", `{}`)
	if code != 200 {
		t.Fatalf("list 应 200, got %d %v", code, res)
	}
	if num(res["total"]) != 2 {
		t.Fatalf("懒同步应有 2 首: %v", res)
	}
	items := res["items"].([]any)
	first := items[0].(map[string]any)
	if first["url"] == nil || first["title"] == "" {
		t.Fatalf("列表行缺 url/title: %v", first)
	}

	// 2) facets
	code, res = post("facets", `{}`)
	if code != 200 {
		raw, _ := json.Marshal(res)
		t.Fatalf("facets 应 200, got %d body=%s", code, raw)
	}
	artists := res["artists"].([]any)
	if len(artists) == 0 {
		t.Fatalf("应有歌手聚合: %v", res)
	}

	// 3) 歌单 CRUD
	code, res = post("pl.save", `{"name":"车载热歌"}`)
	if code != 200 || res["id"] == nil {
		t.Fatalf("pl.save 应 200: %d %v", code, res)
	}
	plID := res["id"].(string)
	ids := make([]string, 0, 2)
	for _, it := range items {
		ids = append(ids, it.(map[string]any)["id"].(string))
	}
	addBody, _ := json.Marshal(map[string]any{"id": plID, "trackIds": ids})
	code, res = post("pl.add", string(addBody))
	if code != 200 || num(res["added"]) != 2 {
		t.Fatalf("pl.add 应加 2 首: %d %v", code, res)
	}
	code, res = post("pl.items", `{"id":"`+plID+`"}`)
	if code != 200 || len(res["items"].([]any)) != 2 {
		t.Fatalf("pl.items 应 2 首: %d %v", code, res)
	}
	// 重复添加不生效
	code, res = post("pl.add", string(addBody))
	if code != 200 || num(res["added"]) != 0 {
		t.Fatalf("重复添加应去重: %d %v", code, res)
	}
	// 移出一首
	rmBody, _ := json.Marshal(map[string]any{"id": plID, "trackIds": []string{ids[0]}})
	code, _ = post("pl.remove", string(rmBody))
	if code != 200 {
		t.Fatalf("pl.remove 应 200, got %d", code)
	}
	code, res = post("pl.items", `{"id":"`+plID+`"}`)
	if len(res["items"].([]any)) != 1 {
		t.Fatalf("移出后应剩 1 首: %v", res)
	}

	// 4) 播放统计 + 最近播放
	code, _ = post("stats.play", `{"id":"`+ids[1]+`"}`)
	if code != 200 {
		t.Fatalf("stats.play 应 200, got %d", code)
	}
	code, res = post("recent.list", `{}`)
	if code != 200 || len(res["items"].([]any)) != 1 {
		t.Fatalf("recent.list 应 1 首: %d %v", code, res)
	}

	// 5) drive.list：返回数组即可（测试机有没有 U 盘不作断言）
	code, res = post("drive.list", `{}`)
	if code != 200 {
		t.Fatalf("drive.list 应 200, got %d", code)
	}
	if _, ok := res["drives"]; !ok {
		t.Fatalf("drive.list 缺 drives: %v", res)
	}

	// 6) usb.export 校验：非法盘符 / 固定硬盘
	code, res = post("usb.export", `{"drive":"99","name":"x"}`)
	if code != 400 {
		t.Fatalf("非法盘符应 400, got %d %v", code, res)
	}
	code, res = post("usb.export", `{"drive":"C:","name":"x"}`)
	if code != 400 {
		t.Fatalf("固定硬盘应 400, got %d %v", code, res)
	}
	code, res = post("usb.export", `{"drive":"Z:","name":"x"}`)
	if code != 400 {
		t.Fatalf("不存在的盘应 400, got %d %v", code, res)
	}

	// 7) 导出历史
	code, res = post("usb.history", `{}`)
	if code != 200 {
		t.Fatalf("usb.history 应 200, got %d", code)
	}

	// 8) 只读账号：scan → 405
	srv2, base2, user2, pass2 := startTestServer(t, func(c *Config) {
		c.InstanceDir = t.TempDir()
		c.ReadOnly = true
	})
	cli2 := newTestClient(t, false)
	r := doReq(t, cli2, base2, "POST", "/-api/music/scan", user2, pass2, []byte(`{}`))
	if r.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("只读账号 scan 应 405, got %d", r.StatusCode)
	}
	_ = srv2
}

// 卡拉OK：lrc 同目录识别 → lyrics 接口。
func TestMusicLyricsAPI(t *testing.T) {
	srv, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = t.TempDir() })
	root := srv.cfg.Shares[0].Root
	mroot := filepath.Join(root, "_appfiles", "music", "s")
	if err := os.MkdirAll(mroot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mroot, "晴天.mp3"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	lrc := "[00:01.00]第一句\n[00:05.50]第二句\n"
	if err := os.WriteFile(filepath.Join(mroot, "晴天.lrc"), []byte(lrc), 0o644); err != nil {
		t.Fatal(err)
	}
	cli := newTestClient(t, false)
	post := func(action, body string) (int, map[string]any) {
		r := doReq(t, cli, base, "POST", "/-api/music/"+action, user, pass, []byte(body))
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return r.StatusCode, m
	}

	code, res := post("list", `{}`)
	if code != 200 {
		t.Fatalf("list 应 200: %d %v", code, res)
	}
	items := res["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("应 1 首: %v", res)
	}
	id := items[0].(map[string]any)["id"].(string)

	code, res = post("lyrics", `{"id":"`+id+`"}`)
	if code != 200 || res["has"] != true {
		t.Fatalf("lyrics 应有歌词: %d %v", code, res)
	}
	ls := res["items"].([]any)
	if len(ls) != 2 {
		t.Fatalf("应 2 行歌词: %v", res)
	}
	if g := ls[1].(map[string]any); g["t"].(float64) != 5.5 || g["text"] != "第二句" {
		t.Fatalf("第二行不对: %v", g)
	}

	// 缺 id → 400；不存在的 id → 404
	if code, _ = post("lyrics", `{}`); code != 400 {
		t.Fatalf("缺 id 应 400, got %d", code)
	}
	if code, _ = post("lyrics", `{"id":"nope"}`); code != 404 {
		t.Fatalf("未知 id 应 404, got %d", code)
	}
}
