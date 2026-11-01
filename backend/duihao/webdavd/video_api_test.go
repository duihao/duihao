package webdavd

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 影视 S4：扫 _appfiles/video 建索引（目录型/文件型/未分类）、列表筛选、
// 详情与元数据编辑（custom 不被重扫覆盖）、进度与继续观看。
func TestVideoAPI(t *testing.T) {
	srv, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = t.TempDir() })
	root := srv.cfg.Shares[0].Root

	// 造目录：电影（目录型）+ 纪录片（文件型带同名海报）+ 根下散文件（未分类）
	vroot := filepath.Join(root, "_appfiles", "video")
	mk := func(rel string, size int) string {
		p := filepath.Join(vroot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	mk("电影/流浪地球2 (2023)/流浪地球2.mp4", 4096)
	mk("电影/流浪地球2 (2023)/流浪地球2-幕后.mkv", 2048)
	mk("电影/盗梦空间/盗梦空间.mp4", 1024)
	mk("电影/盗梦空间/poster.jpg", 4096)
	mk("纪录片/蓝色星球 S01E02.mkv", 2048)
	mk("随手放的.mp4", 1024)

	cli := newTestClient(t, false)
	num := func(v any) int { f, _ := v.(float64); return int(f) }
	post := func(action, body string) (int, map[string]any) {
		r := doReq(t, cli, base, "POST", "/-api/video/"+action, user, pass, []byte(body))
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return r.StatusCode, m
	}

	// 1) 扫描：4 部影片（流浪地球2 / 盗梦空间 / 蓝色星球 / 随手放的）
	code, res := post("scan", `{}`)
	if code != 200 {
		t.Fatalf("scan 应 200, got %d %v", code, res)
	}
	if num(res["items"]) != 4 || num(res["cats"]) != 2 || num(res["added"]) != 4 {
		t.Fatalf("扫描结果不对: %v", res)
	}

	// 2) 列表：分类 chips 数量与排序
	code, res = post("list", `{}`)
	if code != 200 {
		t.Fatalf("list 应 200, got %d", code)
	}
	if num(res["total"]) != 4 {
		t.Fatalf("total 应 4, got %v", res["total"])
	}
	cats, _ := res["cats"].([]any)
	if len(cats) != 3 {
		t.Fatalf("分类应 3 个（电影/纪录片/未分类）, got %v", cats)
	}
	if c0, _ := cats[0].(map[string]any); c0["name"] != "电影" || num(c0["count"]) != 2 {
		t.Fatalf("第一个分类应为 电影(2), got %v", cats[0])
	}
	// 未分类必须用哨兵值（否则与「全部」的 cat='' 撞车，前端两个 chip 同时高亮）
	if c2, _ := cats[2].(map[string]any); c2["name"] != "__none__" || c2["label"] != "未分类" {
		t.Fatalf("未分类分类项应为 __none__/未分类, got %v", cats[2])
	}
	_, res = post("list", `{"cat":"__none__"}`)
	if num(res["total"]) != 1 {
		t.Fatalf("按未分类筛选应 1 部, got %v", res["total"])
	}
	_, res = post("list", `{"cat":""}`)
	if num(res["total"]) != 4 {
		t.Fatalf("不传 cat（全部）应 4 部, got %v", res["total"])
	}

	// 3) 详情：目录型影片 = 2 个文件、year 提取、剧集判定
	_, res = post("list", `{}`)
	items, _ := res["items"].([]any)
	var earthID string
	for _, raw := range items {
		it, _ := raw.(map[string]any)
		if it["title"] == "流浪地球2" {
			earthID, _ = it["id"].(string)
		}
	}
	if earthID == "" {
		t.Fatal("找不到流浪地球2")
	}
	_, res = post("get", `{"id":"`+earthID+`"}`)
	it, _ := res["item"].(map[string]any)
	files, _ := it["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("流浪地球2 应 2 个文件, got %d", len(files))
	}
	if num(it["year"]) != 2023 || it["kind"] != "series" {
		t.Fatalf("year/kind 不对: %v %v", it["year"], it["kind"])
	}

	// 3.5) 海报路径必须带分类目录（少了分类会 404，前端只剩渐变占位）
	var dreamPoster string
	for _, raw := range items {
		row, _ := raw.(map[string]any)
		if row["title"] == "盗梦空间" {
			dreamPoster, _ = row["poster"].(string)
		}
	}
	if !strings.Contains(dreamPoster, "/_appfiles/video/%E7%94%B5%E5%BD%B1/") &&
		!strings.Contains(dreamPoster, "/_appfiles/video/电影/") {
		t.Fatalf("海报路径应含分类目录, got %q", dreamPoster)
	}

	// 4) 元数据编辑：评分/简介落库；custom 标题不被重扫覆盖
	code, res = post("meta.save", `{"id":"`+earthID+`","title":"流浪地球2","rating":8.3,"intro":"太阳即将毁灭","tags":"科幻,灾难","director":"郭帆","actors":"吴京,刘德华","year":2023,"kind":"movie","area":"中国"}`)
	if code != 200 {
		t.Fatalf("meta.save 应 200, got %d %v", code, res)
	}
	it, _ = res["item"].(map[string]any)
	if it["title"] != "流浪地球2" || it["intro"] != "太阳即将毁灭" {
		t.Fatalf("编辑未生效: %v", it)
	}
	code, res = post("scan", `{}`)
	if code != 200 || num(res["items"]) != 4 {
		t.Fatalf("重扫应 200/4 部, got %d %v", code, res)
	}
	_, res = post("get", `{"id":"`+earthID+`"}`)
	it, _ = res["item"].(map[string]any)
	if it["title"] != "流浪地球2" || it["rating"] == nil {
		t.Fatalf("重扫不应覆盖用户字段: %v", it)
	}

	// 5) 播放文件可直达且支持 Range（走 WebDAV 路径）
	url, _ := files[0].(map[string]any)["url"].(string)
	req, _ := http.NewRequest("GET", "http://"+base+url, nil)
	req.SetBasicAuth(user, pass)
	req.Header.Set("Range", "bytes=0-99")
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusPartialContent || len(b) != 100 {
		t.Fatalf("Range 播放应 206/100B, got %d %dB", resp.StatusCode, len(b))
	}

	// 6) 进度与继续观看：写到一半的文件出现，看完的不出现
	code, res = post("progress.save", `{"itemId":"`+earthID+`","path":"`+files[0].(map[string]any)["path"].(string)+`","position":300,"duration":5400}`)
	if code != 200 {
		t.Fatalf("progress.save 应 200, got %d %v", code, res)
	}
	_, res = post("continue.list", `{}`)
	rows, _ := res["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("继续观看应 1 条, got %v", rows)
	}
	_, _ = post("progress.save", `{"itemId":"`+earthID+`","path":"`+files[0].(map[string]any)["path"].(string)+`","position":5350,"duration":5400}`)
	_, res = post("continue.list", `{}`)
	if rows, _ = res["rows"].([]any); len(rows) != 0 {
		t.Fatalf("看完后不应出现在继续观看, got %v", rows)
	}

	// 7) 搜索命中主演
	_, res = post("list", `{"q":"刘德华"}`)
	if num(res["total"]) != 1 {
		t.Fatalf("按主演搜索应命中 1 部, got %v", res["total"])
	}

	// 8) 文件消失 → 重扫后影片删除
	if err := os.RemoveAll(filepath.Join(vroot, "电影")); err != nil {
		t.Fatal(err)
	}
	_, res = post("scan", `{}`)
	if num(res["removed"]) != 2 {
		t.Fatalf("重扫应移除 2 部, got %v", res)
	}
	_, res = post("list", `{}`)
	if num(res["total"]) != 2 {
		t.Fatalf("剩余应 2 部, got %v", res["total"])
	}

	// 9) 只读账号：scan / meta.save / progress.save 被拒（读接口放行）
	roSrv, roBase, roUser, roPass := startTestServer(t, func(c *Config) {
		c.InstanceDir = t.TempDir()
		c.ReadOnly = true
	})
	ro := newTestClient(t, false)
	if r := doReq(t, ro, roBase, "POST", "/-api/video/list", roUser, roPass, []byte(`{}`)); r.StatusCode != 200 {
		t.Fatalf("只读账号 list 应放行, got %d", r.StatusCode)
	}
	for _, action := range []string{"scan", "meta.save", "progress.save"} {
		r := doReq(t, ro, roBase, "POST", "/-api/video/"+action, roUser, roPass, []byte(`{}`))
		raw, _ := io.ReadAll(r.Body)
		if r.StatusCode != http.StatusMethodNotAllowed || !strings.Contains(string(raw), "只读") {
			t.Fatalf("只读账号 %s 应 405, got %d %s", action, r.StatusCode, raw)
		}
	}
	_ = roSrv
}
