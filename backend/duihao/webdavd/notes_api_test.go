package webdavd

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 笔记接口闭环：新建（标题取首行）→ 列表/搜索/标签 → 读取 → 更新 → 导出 → 删除。
func TestNotesAPI(t *testing.T) {
	_, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = t.TempDir() })
	cli := newTestClient(t, false)

	post := func(action, body string) (int, map[string]any) {
		r := doReq(t, cli, base, "POST", "/-api/notes/"+action, user, pass, []byte(body))
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return r.StatusCode, m
	}

	// 1) 新建：不传标题 → 取正文首行；标签规范化（中文逗号 + 去重）
	code, res := post("save", `{"body":"# 周末计划\n- 修水管\n- 买灯","tags":"家务，生活,家务"}`)
	if code != 200 {
		t.Fatalf("新建笔记失败: %d %v", code, res)
	}
	id, _ := res["id"].(string)
	if id == "" {
		t.Fatal("新建未返回 id")
	}

	// 2) 列表：标题来自首行（去掉 #），标签去重后是「家务,生活」
	code, res = post("list", `{}`)
	if code != 200 {
		t.Fatalf("列表失败: %d %v", code, res)
	}
	notes, _ := res["notes"].([]any)
	if len(notes) != 1 {
		t.Fatalf("列表应有 1 条, got %d", len(notes))
	}
	first, _ := notes[0].(map[string]any)
	if first["title"] != "周末计划" {
		t.Fatalf("标题应取正文首行, got %v", first["title"])
	}
	if first["tags"] != "家务,生活" {
		t.Fatalf("标签规范化异常, got %v", first["tags"])
	}
	tags, _ := res["tags"].([]any)
	if len(tags) != 2 {
		t.Fatalf("标签计数应有 2 个, got %v", tags)
	}

	// 3) 搜索（正文命中）与不命中
	if _, res = post("list", `{"q":"修水管"}`); len(res["notes"].([]any)) != 1 {
		t.Fatalf("正文搜索应命中, got %v", res["notes"])
	}
	// 3.5) 快速时间维度（口径与账本一致）：笔记记在今天
	if _, res = post("list", `{"range":"today"}`); len(res["notes"].([]any)) != 1 {
		t.Fatalf("今日维度应命中 1 条, got %v", res["notes"])
	}
	if _, res = post("list", `{"range":"yesterday"}`); len(res["notes"].([]any)) != 0 {
		t.Fatalf("昨日维度不应命中, got %v", res["notes"])
	}
	if _, res = post("list", `{"range":"week"}`); len(res["notes"].([]any)) != 1 {
		t.Fatalf("本周维度应命中 1 条, got %v", res["notes"])
	}
	for _, dim := range []string{"lastweek", "lastmonth", "lastyear"} {
		if _, res = post("list", `{"range":"`+dim+`"}`); res["notes"] == nil {
			t.Fatalf("维度 %s 应返回列表", dim)
		}
	}
	if _, res = post("list", `{"q":"不存在的词"}`); len(res["notes"].([]any)) != 0 {
		t.Fatalf("无关关键词不应命中, got %v", res["notes"])
	}
	// LIKE 通配符要被转义（否则 % 会搜出全部）
	if _, res = post("list", `{"q":"%"}`); len(res["notes"].([]any)) != 0 {
		t.Fatalf("百分号应被转义, got %v", res["notes"])
	}

	// 4) 标签过滤
	if _, res = post("list", `{"tag":"生活"}`); len(res["notes"].([]any)) != 1 {
		t.Fatalf("标签过滤应命中, got %v", res["notes"])
	}
	if _, res = post("list", `{"tag":"工作"}`); len(res["notes"].([]any)) != 0 {
		t.Fatalf("无关标签不应命中, got %v", res["notes"])
	}

	// 4.5) 按分类汇总：与 list 共用筛选，未分类聚合为一行
	if _, res = post("catstats", `{}`); len(res["rows"].([]any)) != 1 {
		t.Fatalf("catstats 应 1 行（未分类）, got %v", res["rows"])
	}
	if _, res = post("catstats", `{"tag":"工作"}`); len(res["rows"].([]any)) != 0 {
		t.Fatalf("catstats 应共用筛选, got %v", res["rows"])
	}

	// 4.6) 分页：pageSize=1 时 total 不变、第二页翻到旧记录
	if _, res = post("list", `{"pageSize":1,"page":1}`); res["total"].(float64) < 1 {
		t.Fatalf("list 应返回 total, got %v", res["total"])
	}
	if _, res = post("list", `{"pageSize":1,"page":99}`); len(res["notes"].([]any)) != 1 {
		t.Fatalf("超页应夹回最后一页, got %v", res["notes"])
	}

	// 5) 读取单篇（正文完整）
	code, res = post("get", `{"id":"`+id+`"}`)
	if code != 200 {
		t.Fatalf("读取失败: %d %v", code, res)
	}
	note, _ := res["note"].(map[string]any)
	if note["body"] != "# 周末计划\n- 修水管\n- 买灯" {
		t.Fatalf("正文不一致: %q", note["body"])
	}
	origDay, _ := note["day"].(string)
	if len(origDay) != 10 {
		t.Fatalf("day 应为 YYYY-MM-DD, got %q", origDay)
	}

	// 6) 更新：只改正文与置顶，不传 day → day 保留
	code, res = post("save", `{"id":"`+id+`","title":"周末计划","body":"改后的正文","tags":"家务","pinned":true}`)
	if code != 200 {
		t.Fatalf("更新失败: %d %v", code, res)
	}
	_, res = post("get", `{"id":"`+id+`"}`)
	note, _ = res["note"].(map[string]any)
	if note["body"] != "改后的正文" || note["pinned"] != true || note["day"] != origDay {
		t.Fatalf("更新结果异常: %v", note)
	}

	// 7) 非法日期被拒
	if code, _ = post("save", `{"title":"x","day":"2026/10/28"}`); code != 400 {
		t.Fatalf("非法日期应 400, got %d", code)
	}

	// 8) 导出（数据自救出口）
	code, res = post("export", `{}`)
	if code != 200 || res["count"].(float64) != 1 {
		t.Fatalf("导出异常: %d %v", code, res["count"])
	}
	if exp, _ := res["notes"].([]any); len(exp) != 1 {
		t.Fatalf("导出的笔记条数异常: %v", res["notes"])
	}

	// 9) 删除后列表清空
	if code, res = post("delete", `{"id":"`+id+`"}`); code != 200 {
		t.Fatalf("删除失败: %d %v", code, res)
	}
	if _, res = post("list", `{}`); len(res["notes"].([]any)) != 0 {
		t.Fatalf("删除后应为空, got %v", res["notes"])
	}
}

// 只读账号能读笔记，但不能写入（appkit 按动作名判定）。
func TestNotesReadOnly(t *testing.T) {
	_, base, user, pass := startTestServer(t, func(c *Config) {
		c.InstanceDir = t.TempDir()
		c.ReadOnly = true
	})
	cli := newTestClient(t, false)

	r := doReq(t, cli, base, "POST", "/-api/notes/list", user, pass, []byte(`{}`))
	if r.StatusCode != 200 {
		t.Fatalf("只读账号的读接口应放行, got %d", r.StatusCode)
	}
	r = doReq(t, cli, base, "POST", "/-api/notes/save", user, pass, []byte(`{"body":"x"}`))
	if r.StatusCode != 405 {
		t.Fatalf("只读账号写入应 405, got %d", r.StatusCode)
	}
}

// 媒体闭环：上传进网盘 _appfiles/notes/ → 网盘浏览 → 富文本清洗保留 img/video/audio → 封面往返。
func TestNotesMedia(t *testing.T) {
	srv, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = t.TempDir() })
	root := srv.cfg.Shares[0].Root
	cli := newTestClient(t, false)

	post := func(action, body string) (int, map[string]any) {
		r := doReq(t, cli, base, "POST", "/-api/notes/"+action, user, pass, []byte(body))
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return r.StatusCode, m
	}
	num := func(v any) int { f, _ := v.(float64); return int(f) }

	// 1) multipart 上传 → 落在 _appfiles/notes/<YYYY-MM>/ 下，URL 可直接 GET 回读
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "我的 截图.png")
	_, _ = fw.Write([]byte("fake-png-bytes"))
	_ = mw.Close()
	upReq, _ := http.NewRequest("POST", "http://"+base+"/-api/notes/upload", &buf)
	upReq.SetBasicAuth(user, pass)
	upReq.Header.Set("Content-Type", mw.FormDataContentType())
	upRes, err := cli.Do(upReq)
	if err != nil {
		t.Fatal(err)
	}
	upRaw, _ := io.ReadAll(upRes.Body)
	_ = upRes.Body.Close()
	if upRes.StatusCode != 200 {
		t.Fatalf("上传应 200, got %d %s", upRes.StatusCode, upRaw)
	}
	var up map[string]any
	_ = json.Unmarshal(upRaw, &up)
	imgPath, _ := up["path"].(string)
	imgURL, _ := up["url"].(string)
	if !strings.HasPrefix(imgPath, "/_appfiles/notes/") || !strings.HasSuffix(imgPath, "我的 截图.png") {
		t.Fatalf("上传路径异常: %q", imgPath)
	}
	if imgURL == "" || !strings.HasPrefix(imgURL, "/_appfiles/notes/") {
		t.Fatalf("URL 异常: %q", imgURL)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(imgPath))); err != nil {
		t.Fatalf("文件未落盘: %v", err)
	}
	if r := doReq(t, cli, base, "GET", imgURL, user, pass, nil); r.StatusCode != 200 {
		t.Fatalf("上传后的文件应可直接 GET, got %d", r.StatusCode)
	}

	// 2) pick.list：根目录能看到 _appfiles；进入后能看到月份目录
	_, res := post("pick.list", `{}`)
	items, _ := res["items"].([]any)
	var appDir map[string]any
	for _, raw := range items {
		it, _ := raw.(map[string]any)
		if it["name"] == "_appfiles" {
			appDir = it
		}
	}
	if appDir == nil || appDir["dir"] != true {
		t.Fatalf("根目录应列出 _appfiles: %v", res)
	}
	_, res = post("pick.list", `{"dir":"/_appfiles"}`)
	if items, _ = res["items"].([]any); len(items) == 0 || items[0].(map[string]any)["name"] != "notes" {
		t.Fatalf("_appfiles 下应有 notes 目录: %v", res)
	}
	// 越界目录被拒
	if code, _ := post("pick.list", `{"dir":"/../../etc"}`); code != 403 && code != 404 {
		t.Fatalf("穿越目录应 403/404, got %d", code)
	}

	// 3) 正文带 img/video/audio + 事件属性 → 清洗保留白名单、去危险属性；
	//    文件链接 href 用站内路径也要保留
	body := `<p>看图</p><img src="` + imgURL + `" onerror="alert(1)" alt="截图">` +
		`<video src="/_appfiles/notes/v.mp4" controls></video>` +
		`<a href="/_appfiles/notes/doc.pdf" download="doc.pdf">附件</a>` +
		`<img src="javascript:alert(1)">`
	code, res := post("save", `{"title":"媒体笔记","body":"`+strings.ReplaceAll(body, `"`, `\"`)+`"}`)
	if code != 200 {
		t.Fatalf("保存失败: %d %v", code, res)
	}
	nid, _ := res["id"].(string)
	_, res = post("get", `{"id":"`+nid+`"}`)
	note, _ := res["note"].(map[string]any)
	got, _ := note["body"].(string)
	if strings.Contains(got, "onerror") || strings.Contains(got, "javascript:") {
		t.Fatalf("危险属性未清洗: %q", got)
	}
	for _, want := range []string{`src="` + imgURL + `"`, `<video src="/_appfiles/notes/v.mp4" controls>`, `<a href="/_appfiles/notes/doc.pdf"`, `alt="截图"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("清洗后丢了 %q: %q", want, got)
		}
	}

	// 4) 封面：设置 → 列表/详情可见 → 清除
	if code, res = post("save", `{"id":"`+nid+`","title":"媒体笔记","cover":"`+imgURL+`"}`); code != 200 {
		t.Fatalf("设置封面失败: %d %v", code, res)
	}
	_, res = post("list", `{}`)
	row, _ := res["notes"].([]any)[0].(map[string]any)
	if row["cover"] != imgURL {
		t.Fatalf("列表应带封面: %v", row)
	}
	if code, _ = post("save", `{"id":"`+nid+`","title":"媒体笔记","cover":"http://evil/x.png"}`); code != 400 {
		t.Fatalf("外链封面不应拒绝为 400 之外的码, got %d", code)
	}
	_ = num
}

// 知识卡片形态：富文本清洗、分类增删改、阅读计数、日期筛选、统计卡。
func TestNotesCards(t *testing.T) {
	_, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = t.TempDir() })
	cli := newTestClient(t, false)

	post := func(action, body string) (int, map[string]any) {
		r := doReq(t, cli, base, "POST", "/-api/notes/"+action, user, pass, []byte(body))
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return r.StatusCode, m
	}

	// 1) 新建富文本笔记：script / 事件属性被清洗，排版标签保留
	code, res := post("save", `{"title":"周报","cat":"工作","body":"<h2>本周</h2><p onclick=\"x()\">完成 <b>3</b> 项</p><script>alert(1)</script><p>下周继续</p>"}`)
	if code != 200 {
		t.Fatalf("新建失败: %d %v", code, res)
	}
	id, _ := res["id"].(string)
	if id == "" {
		t.Fatal("新建未返回 id")
	}
	_, res = post("get", `{"id":"`+id+`"}`)
	note, _ := res["note"].(map[string]any)
	body, _ := note["body"].(string)
	if strings.Contains(body, "script") || strings.Contains(body, "onclick") || strings.Contains(body, "alert") {
		t.Fatalf("富文本未清洗: %q", body)
	}
	for _, want := range []string{"<h2>", "<b>3</b>", "下周继续"} {
		if !strings.Contains(body, want) {
			t.Fatalf("清洗后丢了 %q: %q", want, body)
		}
	}

	// 2) 分类出现在 cats 里，且能按分类过滤
	_, res = post("list", `{}`)
	cats, _ := res["cats"].([]any)
	if len(cats) != 1 {
		t.Fatalf("分类清单应有 1 个, got %v", cats)
	}
	if c0, _ := cats[0].(map[string]any); c0["name"] != "工作" || c0["count"].(float64) != 1 {
		t.Fatalf("分类计数异常: %v", c0)
	}
	if _, res = post("list", `{"cat":"工作"}`); len(res["notes"].([]any)) != 1 {
		t.Fatalf("按分类过滤应命中, got %v", res["notes"])
	}
	if _, res = post("list", `{"cat":"生活"}`); len(res["notes"].([]any)) != 0 {
		t.Fatalf("无关分类不应命中, got %v", res["notes"])
	}

	// 3) 阅读次数：每打开一次 +1
	post("get", `{"id":"`+id+`"}`)
	_, res = post("get", `{"id":"`+id+`"}`)
	note, _ = res["note"].(map[string]any)
	if note["views"].(float64) != 3 {
		t.Fatalf("阅读次数应为 3（前面 get 过一次）, got %v", note["views"])
	}

	// 4) 分类重命名：笔记里的 cat 跟着改
	if code, res = post("cat.save", `{"old":"工作","name":"工作记录"}`); code != 200 {
		t.Fatalf("分类重命名失败: %d %v", code, res)
	}
	if _, res = post("list", `{"cat":"工作记录"}`); len(res["notes"].([]any)) != 1 {
		t.Fatalf("重命名后应能按新分类命中, got %v", res["notes"])
	}

	// 5) 统计卡：总数 1、今日 1、覆盖分类 1
	_, res = post("list", `{}`)
	st, _ := res["stats"].(map[string]any)
	if st["total"].(float64) != 1 || st["today"].(float64) != 1 || st["cats"].(float64) != 1 {
		t.Fatalf("统计异常: %v", st)
	}

	// 6) 日期筛选：区间之外查不到
	if _, res = post("list", `{"from":"2000-01-01","to":"2000-12-31"}`); len(res["notes"].([]any)) != 0 {
		t.Fatalf("区间外不应命中, got %v", res["notes"])
	}

	// 7) 删除分类：笔记保留，回到未分类
	if code, res = post("cat.delete", `{"name":"工作记录"}`); code != 200 {
		t.Fatalf("删除分类失败: %d %v", code, res)
	}
	_, res = post("list", `{}`)
	if len(res["notes"].([]any)) != 1 {
		t.Fatalf("删分类不应删笔记, got %v", res["notes"])
	}
	if cs, _ := res["cats"].([]any); len(cs) != 0 {
		t.Fatalf("删分类后分类清单应为空, got %v", cs)
	}
	if _, res = post("list", `{"cat":"__none__"}`); len(res["notes"].([]any)) != 1 {
		t.Fatalf("未分类筛选应命中, got %v", res["notes"])
	}
}
