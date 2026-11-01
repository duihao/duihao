package webdavd

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// 账本：默认两级分类、记账、统计、分类/账户/对象管理、导出、批量删除、只读。
func TestLedgerAPI(t *testing.T) {
	_, base, user, pass := startTestServer(t, func(c *Config) { c.InstanceDir = t.TempDir() })
	cli := newTestClient(t, false)

	post := func(action, body string) (int, map[string]any) {
		r := doReq(t, cli, base, "POST", "/-api/ledger/"+action, user, pass, []byte(body))
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return r.StatusCode, m
	}
	num := func(v any) int { f, _ := v.(float64); return int(f) }

	// 1) 首次进列表 → 自动播种默认分类（支出 8 组 30 项 / 收入 3 组 17 项）
	code, res := post("list", `{}`)
	if code != 200 {
		t.Fatalf("list 应 200, got %d %v", code, res)
	}
	groups, _ := res["groups"].([]any)
	if len(groups) != 11 {
		t.Fatalf("默认一级分类应为 11 个, got %d", len(groups))
	}
	expGroups, expCats, incGroups, incCats := 0, 0, 0, 0
	var foodGid, foodCid, salaryGid string
	for _, raw := range groups {
		g, _ := raw.(map[string]any)
		cats, _ := g["cats"].([]any)
		if g["kind"] == "expense" {
			expGroups++
			expCats += len(cats)
			if g["name"] == "食品酒水" {
				foodGid, _ = g["id"].(string)
				if len(cats) > 0 {
					c0, _ := cats[2].(map[string]any) // 午餐
					foodCid, _ = c0["id"].(string)
				}
			}
		} else {
			incGroups++
			incCats += len(cats)
			if g["name"] == "职业收入" {
				salaryGid, _ = g["id"].(string)
			}
		}
	}
	if expGroups != 8 || expCats != 30 || incGroups != 3 || incCats != 17 {
		t.Fatalf("默认分类数量不对: 支出 %d组/%d项, 收入 %d组/%d项", expGroups, expCats, incGroups, incCats)
	}
	if foodGid == "" || foodCid == "" || salaryGid == "" {
		t.Fatal("默认分类 id 缺失")
	}
	accounts, _ := res["accounts"].([]any)
	if len(accounts) != 1 {
		t.Fatalf("默认应有 1 个结算账户（现金）, got %d", len(accounts))
	}
	accID, _ := accounts[0].(map[string]any)["id"].(string)

	// 2) 记一笔支出 + 一笔收入（day 用动态的昨天，供时间维度用例断言）
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	code, res = post("save", `{"kind":"expense","amount":50000,"groupId":"`+foodGid+`","catId":"`+foodCid+
		`","accountId":"`+accID+`","peer":"张飞","note":"物业费","day":"`+yesterday+`"}`)
	if code != 200 {
		t.Fatalf("支出保存失败: %d %v", code, res)
	}
	expID, _ := res["id"].(string)
	code, res = post("save", `{"kind":"income","amount":100000,"groupId":"`+salaryGid+
		`","accountId":"`+accID+`","peer":"小明","day":"`+yesterday+`"}`)
	if code != 200 {
		t.Fatalf("收入保存失败: %d %v", code, res)
	}
	incID, _ := res["id"].(string)

	// 3) 统计：储蓄 500、本期收 1000、支 500
	_, res = post("list", `{"range":"all"}`)
	st, _ := res["stats"].(map[string]any)
	if num(st["assets"]) != 50000 || num(st["income"]) != 100000 || num(st["expense"]) != 50000 {
		t.Fatalf("统计异常: %v", st)
	}
	if num(st["count"]) != 2 || num(st["total"]) != 2 {
		t.Fatalf("笔数异常: %v", st)
	}

	// 4) 按类型、按一级分类、按关键词筛选
	if _, res = post("list", `{"kind":"expense"}`); len(res["entries"].([]any)) != 1 {
		t.Fatalf("按支出筛选应 1 条, got %v", res["entries"])
	}
	// 进账/支出卡片只跟时间维度联动：类型筛选不影响 income/expense（只影响 count）
	if st, _ = res["stats"].(map[string]any); num(st["income"]) != 100000 || num(st["expense"]) != 50000 {
		t.Fatalf("类型筛选不应改变收支卡: %v", st)
	}
	if st, _ = res["stats"].(map[string]any); num(st["count"]) != 1 {
		t.Fatalf("类型筛选后明细笔数应 1, got %v", st)
	}
	if _, res = post("list", `{"groupId":"`+foodGid+`"}`); len(res["entries"].([]any)) != 1 {
		t.Fatalf("按分类筛选应 1 条, got %v", res["entries"])
	}
	if st, _ = res["stats"].(map[string]any); num(st["income"]) != 100000 {
		t.Fatalf("分类筛选不应改变进账卡: %v", st)
	}
	if _, res = post("list", `{"q":"物业"}`); len(res["entries"].([]any)) != 1 {
		t.Fatalf("关键词应命中备注, got %v", res["entries"])
	}
	if _, res = post("list", `{"q":"小明"}`); len(res["entries"].([]any)) != 1 {
		t.Fatalf("关键词应命中对象, got %v", res["entries"])
	}
	// 日期区间之外查不到
	if _, res = post("list", `{"from":"2000-01-01","to":"2000-12-31"}`); len(res["entries"].([]any)) != 0 {
		t.Fatalf("区间外不应命中, got %v", res["entries"])
	}
	// 区间外时收支卡也跟区间走（自定义区间 = 时间维度）
	if st, _ = res["stats"].(map[string]any); num(st["income"]) != 0 || num(st["expense"]) != 0 {
		t.Fatalf("自定义区间外收支卡应为 0: %v", st)
	}
	// 新时间维度：库里两笔账记在昨天，昨日维度应命中 2 条
	if _, res = post("list", `{"range":"yesterday"}`); len(res["entries"].([]any)) != 2 {
		t.Fatalf("昨日维度应 2 条, got %v", res["entries"])
	}
	// 再记一笔今天（动态日期）的账：today/week/month/year 都应命中，收支卡只算这笔
	today := time.Now().Format("2006-01-02")
	code, res = post("save", `{"kind":"expense","amount":700,"groupId":"`+foodGid+`","day":"`+today+`"}`)
	if code != 200 {
		t.Fatalf("今日账保存失败: %d %v", code, res)
	}
	todayID, _ := res["id"].(string)
	if _, res = post("list", `{"range":"today"}`); len(res["entries"].([]any)) != 1 {
		t.Fatalf("今日维度应 1 条, got %v", res["entries"])
	}
	if st, _ = res["stats"].(map[string]any); num(st["expense"]) != 700 || num(st["income"]) != 0 {
		t.Fatalf("今日收支卡异常: %v", st)
	}
	if _, res = post("list", `{"range":"week"}`); len(res["entries"].([]any)) < 1 {
		t.Fatalf("本周维度应至少 1 条, got %v", res["entries"])
	}
	for _, dim := range []string{"lastweek", "lastmonth", "lastyear"} {
		if _, res = post("list", `{"range":"`+dim+`"}`); res["entries"] == nil {
			t.Fatalf("维度 %s 应返回列表", dim)
		}
	}
	// 删掉这笔，保持后续用例的计数不变
	code, res = post("bulk.delete", `{"ids":["`+todayID+`"]}`)
	if code != 200 || num(res["deleted"]) != 1 {
		t.Fatalf("清理今日账失败: %d %v", code, res)
	}

	// 4.5) 二级分类筛选 + 按分类统计（catstats）
	if _, res = post("list", `{"catId":"`+foodCid+`"}`); len(res["entries"].([]any)) != 1 {
		t.Fatalf("按二级分类筛选应 1 条, got %v", res["entries"])
	}
	_, res = post("catstats", `{"range":"all"}`)
	rows, _ := res["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("catstats 应聚合出 2 行, got %v", res["rows"])
	}
	var expRow map[string]any
	for _, raw := range rows {
		it, _ := raw.(map[string]any)
		if it["kind"] == "expense" {
			expRow = it
		}
	}
	if expRow == nil || expRow["groupName"] != "食品酒水" || expRow["catName"] != "午餐" ||
		num(expRow["sum"]) != 50000 || num(expRow["count"]) != 1 {
		t.Fatalf("catstats 支出行异常: %v", expRow)
	}
	_, res = post("catstats", `{"groupId":"`+foodGid+`"}`)
	if rows, _ = res["rows"].([]any); len(rows) != 1 {
		t.Fatalf("catstats 按一级分类筛选应 1 行, got %v", res["rows"])
	}

	// 5) 校验：分类与收支类型不符 / 二级分类不属于该一级分类 / 金额为 0
	if code, _ = post("save", `{"kind":"income","amount":100,"groupId":"`+foodGid+`"}`); code != 400 {
		t.Fatalf("收支类型与分类不符应 400, got %d", code)
	}
	code, _ = post("save", `{"kind":"income","amount":100,"groupId":"`+salaryGid+`","catId":"`+foodCid+`"}`)
	if code != 400 {
		t.Fatalf("二级分类跨组应 400, got %d", code)
	}
	if code, _ = post("save", `{"kind":"expense","amount":0,"groupId":"`+foodGid+`"}`); code != 400 {
		t.Fatalf("金额为 0 应 400, got %d", code)
	}

	// 6) 系统分类只读；自建分类可增改删
	if code, _ = post("group.delete", `{"id":"`+foodGid+`"}`); code != 403 {
		t.Fatalf("系统一级分类不可删, got %d", code)
	}
	if code, _ = post("group.save", `{"id":"`+foodGid+`","name":"吃饭"}`); code != 403 {
		t.Fatalf("系统一级分类不可改名, got %d", code)
	}
	code, res = post("group.save", `{"kind":"expense","name":"宠物"}`)
	if code != 200 {
		t.Fatalf("新增一级分类失败: %d %v", code, res)
	}
	newGid, _ := res["id"].(string)
	code, res = post("cat.save", `{"groupId":"`+newGid+`","name":"猫粮"}`)
	if code != 200 {
		t.Fatalf("新增二级分类失败: %d %v", code, res)
	}
	newCid, _ := res["id"].(string)

	// 7) 一级分类改名：历史账目里的快照名跟着改
	if code, res = post("group.save", `{"id":"`+newGid+`","name":"宠物开销"}`); code != 200 {
		t.Fatalf("改名失败: %d %v", code, res)
	}
	post("save", `{"kind":"expense","amount":1234,"groupId":"`+newGid+`","catId":"`+newCid+`","note":"猫粮"}`)
	_, res = post("list", `{"q":"猫粮"}`)
	row0, _ := res["entries"].([]any)[0].(map[string]any)
	if row0["groupName"] != "宠物开销" || row0["catName"] != "猫粮" {
		t.Fatalf("改名后账目快照未同步: %v", row0)
	}

	// 8) 分类删除后历史账目保留（只是不再出现在分类树里）
	if code, _ = post("group.delete", `{"id":"`+newGid+`"}`); code != 200 {
		t.Fatalf("删除自建一级分类失败, got %d", code)
	}
	_, res = post("list", `{"q":"猫粮"}`)
	if len(res["entries"].([]any)) != 1 {
		t.Fatalf("删分类不应删账目, got %v", res["entries"])
	}
	row0, _ = res["entries"].([]any)[0].(map[string]any)
	if row0["groupName"] != "宠物开销" {
		t.Fatalf("删分类后应保留名称快照: %v", row0)
	}

	// 9) 对象与账户：新增、改名（同步账目快照）、删除只影响候选列表
	if code, res = post("peer.save", `{"name":"公司财务"}`); code != 200 {
		t.Fatalf("新增对象失败: %d %v", code, res)
	}
	if code, res = post("peer.save", `{"old":"张飞","name":"张飞飞"}`); code != 200 {
		t.Fatalf("对象改名失败: %d %v", code, res)
	}
	_, res = post("list", `{}`)
	found := false
	for _, raw := range res["entries"].([]any) {
		e, _ := raw.(map[string]any)
		if e["peer"] == "张飞飞" {
			found = true
		}
	}
	if !found {
		t.Fatal("对象改名应同步到账目")
	}
	if code, _ = post("peer.delete", `{"name":"公司财务"}`); code != 200 {
		t.Fatalf("删除对象失败, got %d", code)
	}
	if code, res = post("account.save", `{"name":"招商银行"}`); code != 200 {
		t.Fatalf("新增账户失败: %d %v", code, res)
	}
	bankID, _ := res["id"].(string)
	if code, res = post("account.save", `{"id":"`+bankID+`","name":"招行储蓄卡"}`); code != 200 {
		t.Fatalf("账户改名失败: %d %v", code, res)
	}
	_, res = post("list", `{}`)
	if len(res["accounts"].([]any)) != 2 {
		t.Fatalf("账户应有 2 个, got %v", res["accounts"])
	}

	// 10) 详情带凭证全文；列表只给 hasReceipt
	receipt := "data:image/png;base64,iVBORw0KGgo="
	code, res = post("save", `{"kind":"expense","amount":800,"groupId":"`+foodGid+`","note":"早餐","receipt":"`+receipt+`"}`)
	if code != 200 {
		t.Fatalf("带凭证保存失败: %d %v", code, res)
	}
	rid, _ := res["id"].(string)
	_, res = post("get", `{"id":"`+rid+`"}`)
	one, _ := res["entry"].(map[string]any)
	if one["receipt"] != receipt {
		t.Fatalf("详情应返回凭证全文, got %v", one["receipt"])
	}
	_, res = post("list", `{"q":"早餐"}`)
	row0, _ = res["entries"].([]any)[0].(map[string]any)
	if _, has := row0["receipt"]; has {
		t.Fatal("列表不应返回凭证全文")
	}
	if row0["hasReceipt"] != true {
		t.Fatalf("列表应标记有凭证: %v", row0)
	}
	if code, _ = post("save", `{"kind":"expense","amount":100,"groupId":"`+foodGid+`","receipt":"data:text/html;base64,xx"}`); code != 400 {
		t.Fatalf("非法凭证格式应 400, got %d", code)
	}

	// 11) 导出 CSV：含表头与两条金额
	_, res = post("export", `{}`)
	csvText, _ := res["csv"].(string)
	if !strings.Contains(csvText, "日期,类型,一级分类") || !strings.Contains(csvText, "-500.00") {
		t.Fatalf("导出内容异常: %q", csvText)
	}
	if num(res["count"]) < 4 {
		t.Fatalf("导出条数异常: %v", res["count"])
	}

	// 12) 批量删除
	code, res = post("bulk.delete", `{"ids":["`+expID+`","`+incID+`"]}`)
	if code != 200 || num(res["deleted"]) != 2 {
		t.Fatalf("批量删除失败: %d %v", code, res)
	}
	_, res = post("list", `{}`)
	if num(res["stats"].(map[string]any)["total"]) != 2 {
		t.Fatalf("批量删除后应剩 2 条, got %v", res["stats"])
	}
}

// 只读账号：读放行，写一律 405（含带点号命名空间的动作名，如 cat.save）。
func TestLedgerReadOnly(t *testing.T) {
	_, base, user, pass := startTestServer(t, func(c *Config) {
		c.InstanceDir = t.TempDir()
		c.ReadOnly = true
	})
	cli := newTestClient(t, false)

	if r := doReq(t, cli, base, "POST", "/-api/ledger/list", user, pass, []byte(`{}`)); r.StatusCode != 200 {
		t.Fatalf("只读账号读接口应放行, got %d", r.StatusCode)
	}
	for _, action := range []string{"save", "delete", "bulk.delete", "group.save", "cat.save", "peer.save", "account.delete"} {
		r := doReq(t, cli, base, "POST", "/-api/ledger/"+action, user, pass, []byte(`{}`))
		if r.StatusCode != 405 {
			t.Fatalf("只读账号 %s 应 405, got %d", action, r.StatusCode)
		}
	}
}
