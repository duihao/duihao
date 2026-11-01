package webdavd

import (
	"encoding/json"
	"io"
	"testing"
)

// 收藏夹接口闭环：默认数据播种 → 新增（含描述/账号/密码提示） → 列表 → 重复导入跳过 → 删除。
func TestHomeBookmarks(t *testing.T) {
	instanceDir := t.TempDir()
	_, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = instanceDir })
	cli := newTestClient(t, false)

	post := func(action, body string) (int, string) {
		r := doReq(t, cli, base, "POST", "/-api/home/"+action, user, pass, []byte(body))
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}

	// 0) 首次 list 播种默认收藏（去掉「易等」后的 12 组）
	r := doReq(t, cli, base, "GET", "/-api/home/list", user, pass, nil)
	raw, _ := io.ReadAll(r.Body)
	var list struct {
		Groups    []map[string]any `json:"groups"`
		Bookmarks []struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			URL     string `json:"url"`
			GroupID string `json:"groupId"`
		} `json:"bookmarks"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("列表不是合法 JSON: %v %s", err, string(raw))
	}
	if len(list.Groups) != 12 || len(list.Bookmarks) < 70 {
		t.Fatalf("默认播种应为 12 组、书签 ≥70, got %d 组 %d 条", len(list.Groups), len(list.Bookmarks))
	}
	for _, b := range list.Bookmarks { // 种子数据必须都已归组
		if b.GroupID == "" {
			t.Fatalf("种子书签未归组: %+v", b)
		}
	}

	// 1) 新增（不带协议头 → 补 https://；带描述/账号/密码提示）
	code, body := post("bookmark.save", `{"title":"对号","url":"duihao.cn","descr":"官网","account":"tom","pwdHint":"ab*123"}`)
	if code != 200 {
		t.Fatalf("新增书签失败: %d %s", code, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(body), &created)
	if created.ID == "" {
		t.Fatal("新增未返回 id")
	}

	// 2) 列表：新字段往返
	r = doReq(t, cli, base, "GET", "/-api/home/list", user, pass, nil)
	raw, _ = io.ReadAll(r.Body)
	var list2 struct {
		Bookmarks []struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			URL     string `json:"url"`
			Descr   string `json:"descr"`
			Account string `json:"account"`
			PwdHint string `json:"pwdHint"`
		} `json:"bookmarks"`
	}
	if err := json.Unmarshal(raw, &list2); err != nil {
		t.Fatalf("列表不是合法 JSON: %v", err)
	}
	var got *struct {
		ID      string
		Title   string
		URL     string
		Descr   string
		Account string
		PwdHint string
	}
	for i := range list2.Bookmarks {
		b := &list2.Bookmarks[i]
		if b.ID == created.ID {
			got = (*struct {
				ID      string
				Title   string
				URL     string
				Descr   string
				Account string
				PwdHint string
			})(b)
		}
	}
	if got == nil || got.URL != "https://duihao.cn" || got.Descr != "官网" || got.Account != "tom" || got.PwdHint != "ab*123" {
		t.Fatalf("新增书签字段往返异常: %+v", got)
	}

	// 3) 导入：一条重复（步骤 1 里刚加的对号）+ 一条新增
	code, body = post("import", `{"text":"对号,duihao.cn\n示例,www.example.com"}`)
	if code != 200 {
		t.Fatalf("导入失败: %d %s", code, body)
	}
	var imp struct {
		Added   int `json:"added"`
		Skipped int `json:"skipped"`
	}
	_ = json.Unmarshal([]byte(body), &imp)
	if imp.Added != 1 || imp.Skipped != 1 {
		t.Fatalf("导入去重异常: %+v", imp)
	}

	// 4) 删除后：种子 72 + 导入新增的「示例」= 73
	if code, body = post("bookmark.delete", `{"id":"`+created.ID+`"}`); code != 200 {
		t.Fatalf("删除失败: %d %s", code, body)
	}
	r = doReq(t, cli, base, "GET", "/-api/home/list", user, pass, nil)
	raw, _ = io.ReadAll(r.Body)
	list2.Bookmarks = nil
	_ = json.Unmarshal(raw, &list2)
	if len(list2.Bookmarks) != len(list.Bookmarks)+1 {
		t.Fatalf("删除后应为种子数+导入 1 条 = %d, got %d", len(list.Bookmarks)+1, len(list2.Bookmarks))
	}

	// 5) 非法网址被拒
	if code, _ = post("bookmark.save", `{"title":"坏","url":"file:///c:/windows"}`); code != 400 {
		t.Fatalf("非 http/https 应 400, got %d", code)
	}

	// 6) 拖拽落位（bookmark.move）：跨分类后归到目标分类，再回原分类插到第 2 位
	moved := list.Bookmarks[0]
	code, body = post("group.save", `{"title":"拖拽测试"}`)
	if code != 200 {
		t.Fatalf("建分类失败: %d %s", code, body)
	}
	var ng struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(body), &ng)

	type brief struct {
		ID      string `json:"id"`
		GroupID string `json:"groupId"`
	}
	loadAll := func() []brief {
		r := doReq(t, cli, base, "GET", "/-api/home/list", user, pass, nil)
		raw, _ := io.ReadAll(r.Body)
		var j struct {
			Bookmarks []brief `json:"bookmarks"`
		}
		if err := json.Unmarshal(raw, &j); err != nil {
			t.Fatalf("列表不是合法 JSON: %v", err)
		}
		return j.Bookmarks
	}
	if code, body = post("bookmark.move", `{"id":"`+moved.ID+`","groupId":"`+ng.ID+`","index":0}`); code != 200 {
		t.Fatalf("拖拽移动失败: %d %s", code, body)
	}
	for _, b := range loadAll() {
		if b.ID == moved.ID && b.GroupID != ng.ID {
			t.Fatalf("移动后分类应为 %s, got %s", ng.ID, b.GroupID)
		}
	}

	// 回原分类插到第 2 位（index=1）
	if code, body = post("bookmark.move", `{"id":"`+moved.ID+`","groupId":"`+moved.GroupID+`","index":1}`); code != 200 {
		t.Fatalf("回移失败: %d %s", code, body)
	}
	var same []string
	for _, b := range loadAll() {
		if b.GroupID == moved.GroupID {
			same = append(same, b.ID)
		}
	}
	if len(same) < 2 || same[1] != moved.ID {
		t.Fatalf("回移后应排原分类第 2 位, got %v (moved=%s)", same, moved.ID)
	}

	// 7) 拖拽重排分类（group.move）：把第 1 组拖到第 3 位
	groupIDs := func() []string {
		r := doReq(t, cli, base, "GET", "/-api/home/list", user, pass, nil)
		raw, _ := io.ReadAll(r.Body)
		var j struct {
			Groups []struct {
				ID string `json:"id"`
			} `json:"groups"`
		}
		if err := json.Unmarshal(raw, &j); err != nil {
			t.Fatalf("列表不是合法 JSON: %v", err)
		}
		ids := make([]string, 0, len(j.Groups))
		for _, g := range j.Groups {
			ids = append(ids, g.ID)
		}
		return ids
	}
	before := groupIDs()
	if len(before) < 4 {
		t.Fatalf("分类数不足以测排序: %d", len(before))
	}
	first := before[0]
	if code, body = post("group.move", `{"id":"`+first+`","index":2}`); code != 200 {
		t.Fatalf("分类重排失败: %d %s", code, body)
	}
	after := groupIDs()
	if len(after) != len(before) || after[2] != first {
		t.Fatalf("分类应移到第 3 位: before=%v after=%v first=%s", before, after, first)
	}

	// 8) 未分组收敛：不带 groupId 新增 → 自动落到「其他」
	var otherID string
	for _, g := range list.Groups {
		if t, _ := g["title"].(string); t == "其他" || t == "其它" {
			otherID, _ = g["id"].(string)
		}
	}
	if otherID == "" {
		t.Fatal("默认数据里应有「其他」分类")
	}
	code, body = post("bookmark.save", `{"title":"未指定分类","url":"https://www.example.org/x"}`)
	if code != 200 {
		t.Fatalf("新增失败: %d %s", code, body)
	}
	var nb struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(body), &nb)
	hit := false
	for _, b := range loadAll() {
		if b.ID == nb.ID {
			hit = true
			if b.GroupID != otherID {
				t.Fatalf("未指定分类应落到「其他」%s, got %s", otherID, b.GroupID)
			}
		}
	}
	if !hit {
		t.Fatal("新增书签未出现在列表")
	}

	// 9) 删分类不丢数据：其中的网站移到「其他」；「其他」本身禁删
	code, body = post("bookmark.save", `{"title":"待迁移","url":"https://www.example.net/y","groupId":"`+ng.ID+`"}`)
	if code != 200 {
		t.Fatalf("新增到临时分类失败: %d %s", code, body)
	}
	var moveBook struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(body), &moveBook)
	if code, body = post("group.delete", `{"id":"`+ng.ID+`"}`); code != 200 {
		t.Fatalf("删分类失败: %d %s", code, body)
	}
	for _, b := range loadAll() {
		if b.ID == moveBook.ID && b.GroupID != otherID {
			t.Fatalf("删分类后书签应移到「其他」, got %s", b.GroupID)
		}
	}
	if code, body = post("group.delete", `{"id":"`+otherID+`"}`); code != 400 {
		t.Fatalf("「其他」应禁删, got %d %s", code, body)
	}
}
