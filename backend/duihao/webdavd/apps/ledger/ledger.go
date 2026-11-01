// Package ledger 账本：两级收支分类（一级组 + 二级分类）、记账、统计与导出。
//
// 接口约定（/-api/ledger/<action>，POST JSON）：
//
//	list         {q?, kind?, groupId?, catId?, range?, from?, to?}
//	             → {entries, groups, accounts, peers, stats}
//	catstats     {q?, kind?, groupId?, catId?, range?, from?, to?}
//	             → {rows:[{kind,groupId,groupName,catId,catName,sum,count}]} 按分类聚合
//	get          {id}                        → {entry}（含凭证全文）
//	save         {id?, kind, amount, groupId, catId, accountId, peer, note, day?, receipt?}
//	delete       {id}
//	bulk.delete  {ids:[...]}
//	group.save   {id?, kind, name}           group.delete  {id}
//	cat.save     {id?, groupId, name}        cat.delete    {id}
//	account.save {id?, name}                 account.delete{id}
//	peer.save    {old?, name}                peer.delete   {name}
//	export       {}                          → {csv, count}
//
// 口径：
//   - 金额一律存「分」（amount_cents，正整数），收/支由 kind 决定符号；
//   - 账目里冗余保存一级/二级分类名与账户名快照 —— 删分类或账户不影响历史账目；
//   - 默认分类每账号只播种一次（ledger_meta.seeded），用户删光后不会自动长回来。
package ledger

import (
	"crypto/rand"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/webui"
)

type App struct{}

func New() *App { return &App{} }

func (a *App) ID() string    { return "ledger" }
func (a *App) Title() string { return "账本" }

func (a *App) Page(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	return webui.Render(w, webui.Page{App: a.ID(), Title: a.Title(), User: c.User, Writable: c.Writable, Role: c.Role, Perms: c.Perms})
}

func (a *App) API(c appkit.Context, w http.ResponseWriter, r *http.Request, action string) error {
	switch action {
	case "ping":
		appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "app": a.ID(), "user": c.User})
		return nil
	case "list":
		return list(c, w, r)
	case "catstats":
		return catStats(c, w, r)
	case "get":
		return getEntry(c, w, r)
	case "save":
		return save(c, w, r)
	case "delete":
		return remove(c, w, r)
	case "bulk.delete":
		return removeMany(c, w, r)
	case "group.save":
		return groupSave(c, w, r)
	case "group.delete":
		return groupDelete(c, w, r)
	case "cat.save":
		return catSave(c, w, r)
	case "cat.delete":
		return catDelete(c, w, r)
	case "account.save":
		return accountSave(c, w, r)
	case "account.delete":
		return accountDelete(c, w, r)
	case "peer.save":
		return peerSave(c, w, r)
	case "peer.delete":
		return peerDelete(c, w, r)
	case "export":
		return export(c, w)
	}
	return appkit.Errorf(http.StatusNotFound, "未知接口: %s", action)
}

// ── 数据结构 ──

type cat struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Sys  bool   `json:"sys"`
}

type group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Sys  bool   `json:"sys"`
	Cats []cat  `json:"cats"`
}

type entry struct {
	ID          string `json:"id"`
	Day         string `json:"day"`
	Kind        string `json:"kind"`
	Amount      int64  `json:"amount"` // 分
	GroupID     string `json:"groupId"`
	GroupName   string `json:"groupName"`
	CatID       string `json:"catId"`
	CatName     string `json:"catName"`
	AccountID   string `json:"accountId"`
	AccountName string `json:"accountName"`
	Peer        string `json:"peer"`
	Note        string `json:"note"`
	Receipt     string `json:"receipt,omitempty"` // 仅 get 返回全文
	HasReceipt  bool   `json:"hasReceipt"`
	CreatedAt   string `json:"createdAt"`
}

type stats struct {
	Assets  int64 `json:"assets"`  // 累计收入 − 累计支出（全期，分）
	Income  int64 `json:"income"`  // 本期收入
	Expense int64 `json:"expense"` // 本期支出
	Count   int   `json:"count"`   // 本期笔数
	Total   int   `json:"total"`   // 全部笔数
}

type named struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ── 默认分类（参考易等「我的账本」的分类树，作为首次使用的默认数据）──

type seedGroup struct {
	name string
	cats []string
}

var defaultExpense = []seedGroup{
	{"食品酒水", []string{"伙食费", "早餐", "午餐", "晚餐", "零食水果", "饮料酒水"}},
	{"衣服饰品", []string{"衣服", "鞋包", "饰品"}},
	{"居家生活", []string{"房租", "物业费", "水电燃气", "生活用品", "维修"}},
	{"交通出行", []string{"公交地铁", "打车", "加油", "停车", "汽车保养"}},
	{"医疗教育", []string{"医疗", "药品", "学习", "培训"}},
	{"休闲娱乐", []string{"电影", "游戏", "旅游", "运动"}},
	{"人情往来", []string{"红包礼金", "孝敬长辈"}},
	{"其他支出", []string{"其他支出"}},
}

var defaultIncome = []seedGroup{
	{"职业收入", []string{"加班收入", "利息收入", "工资收入", "兼职收入", "理财收入", "奖金收入"}},
	{"人情收礼", []string{"所收红包", "满月收礼", "白事收礼", "婚嫁收礼", "乔迁收礼", "升学收礼", "寿辰收礼"}},
	{"其他收入", []string{"礼金收入", "中奖收入", "经营所得", "意外来钱"}},
}

// seed 首次为该账号播种默认分类与「现金」账户。幂等：靠 ledger_meta 标记。
func seed(c appkit.Context) error {
	var v string
	err := c.DB.QueryRow("select v from ledger_meta where uid = ? and k = 'seeded'", c.User).Scan(&v)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := time.Now().Format(time.RFC3339)
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	plant := func(kind string, list []seedGroup, prefix string) error {
		for i, g := range list {
			gid := fmt.Sprintf("g-%s-%d", prefix, i+1)
			if _, err := tx.Exec(`insert or ignore into ledger_groups(uid,id,name,kind,sort,sys,created_at)
				values(?,?,?,?,?,1,?)`, c.User, gid, g.name, kind, i, now); err != nil {
				return err
			}
			for j, name := range g.cats {
				cid := fmt.Sprintf("c-%s-%d-%d", prefix, i+1, j+1)
				if _, err := tx.Exec(`insert or ignore into ledger_cats(uid,id,group_id,name,sort,sys,created_at)
					values(?,?,?,?,?,1,?)`, c.User, cid, gid, name, j, now); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := plant("expense", defaultExpense, "e"); err != nil {
		return err
	}
	if err := plant("income", defaultIncome, "i"); err != nil {
		return err
	}
	if _, err := tx.Exec(`insert or ignore into ledger_accounts(uid,id,name,kind,sort,created_at)
		values(?, 'acc-cash', '现金', 'cash', 0, ?)`, c.User, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`insert or replace into ledger_meta(uid,k,v) values(?, 'seeded', ?)`, c.User, now); err != nil {
		return err
	}
	return tx.Commit()
}

// ── 读 ──

// listIn list / catstats 共用的筛选入参。
type listIn struct {
	Q       string `json:"q"`
	Kind    string `json:"kind"`
	GroupID string `json:"groupId"`
	CatID   string `json:"catId"`
	Range   string `json:"range"`
	From    string `json:"from"`
	To      string `json:"to"`
}

// buildWhere 把共用筛选（类型/一级分类/二级分类/关键词/日期区间）拼成 SQL。
// 返回 where 片段（不含 where 关键字）、参数、以及解析后的起止日期。
func buildWhere(uid string, in listIn) (string, []any, string, string) {
	from, to := strings.TrimSpace(in.From), strings.TrimSpace(in.To)
	if from == "" && to == "" {
		// 时间维度统一走 appkit.Period（today/yesterday/week/lastweek/month/
		// lastmonth/year/lastyear），与笔记统计卡共用同一口径
		from, to = appkit.Period(strings.TrimSpace(in.Range))
	}

	where := "uid = ?"
	args := []any{uid}
	if k := strings.TrimSpace(in.Kind); k == "expense" || k == "income" {
		where += " and kind = ?"
		args = append(args, k)
	}
	if g := strings.TrimSpace(in.GroupID); g != "" {
		where += " and group_id = ?"
		args = append(args, g)
	}
	if cid := strings.TrimSpace(in.CatID); cid != "" {
		where += " and category_id = ?"
		args = append(args, cid)
	}
	if q := strings.TrimSpace(in.Q); q != "" {
		like := "%" + escapeLike(q) + "%"
		where += ` and (coalesce(peer,'') like ? escape '\' or coalesce(note,'') like ? escape '\'` +
			` or coalesce(group_name,'') like ? escape '\' or coalesce(cat_name,'') like ? escape '\')`
		args = append(args, like, like, like, like)
	}
	if from != "" {
		where += " and day >= ?"
		args = append(args, from)
	}
	if to != "" {
		where += " and day <= ?"
		args = append(args, to)
	}
	return where, args, from, to
}

func list(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	if err := seed(c); err != nil {
		return err
	}
	var in listIn
	if err := decodeOpt(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	where, args, from, to := buildWhere(c.User, in)

	rows, err := c.DB.Query(`select id, day, kind, amount_cents, coalesce(group_id,''), coalesce(group_name,''),
		coalesce(category_id,''), coalesce(cat_name,''), coalesce(account_id,''), coalesce(account_name,''),
		coalesce(peer,''), coalesce(note,''), coalesce(receipt,''), created_at
		from ledger_entries where `+where+` order by day desc, created_at desc, id desc`, args...)
	if err != nil {
		return err
	}
	out := make([]entry, 0, 64)
	st := stats{}
	for rows.Next() {
		var e entry
		var receipt string
		if err := rows.Scan(&e.ID, &e.Day, &e.Kind, &e.Amount, &e.GroupID, &e.GroupName,
			&e.CatID, &e.CatName, &e.AccountID, &e.AccountName, &e.Peer, &e.Note, &receipt, &e.CreatedAt); err != nil {
			_ = rows.Close()
			return err
		}
		e.HasReceipt = receipt != ""
		out = append(out, e)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	st.Count = len(out)

	// 进账/支出卡片只跟时间维度联动（收/支各自求和），不受分类、关键词等筛选影响；
	// 影响明细笔数的筛选体现在 st.Count 里。
	pwhere := "uid = ?"
	pargs := []any{c.User}
	if from != "" {
		pwhere += " and day >= ?"
		pargs = append(pargs, from)
	}
	if to != "" {
		pwhere += " and day <= ?"
		pargs = append(pargs, to)
	}
	if err := c.DB.QueryRow(`select coalesce(sum(case when kind = 'income' then amount_cents else 0 end), 0),
		coalesce(sum(case when kind = 'expense' then amount_cents else 0 end), 0)
		from ledger_entries where `+pwhere, pargs...).Scan(&st.Income, &st.Expense); err != nil {
		return err
	}

	if err := c.DB.QueryRow(`select coalesce(sum(case when kind = 'income' then amount_cents else -amount_cents end), 0)
		from ledger_entries where uid = ?`, c.User).Scan(&st.Assets); err != nil {
		return err
	}
	if err := c.DB.QueryRow("select count(*) from ledger_entries where uid = ?", c.User).Scan(&st.Total); err != nil {
		return err
	}

	groups, err := loadGroups(c)
	if err != nil {
		return err
	}
	accounts, err := loadAccounts(c)
	if err != nil {
		return err
	}
	peers, err := loadPeers(c)
	if err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{
		"entries": out, "groups": groups, "accounts": accounts, "peers": peers,
		"stats": st, "from": from, "to": to,
	})
	return nil
}

// catStats 按分类聚合当前筛选范围内的账目（一级分组、二级细分，金额降序）。
func catStats(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	if err := seed(c); err != nil {
		return err
	}
	var in listIn
	if err := decodeOpt(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	where, args, from, to := buildWhere(c.User, in)
	rows, err := c.DB.Query(`select kind, coalesce(group_id,''), coalesce(group_name,''),
		coalesce(category_id,''), coalesce(cat_name,''), sum(amount_cents), count(*)
		from ledger_entries where `+where+`
		group by kind, group_id, group_name, category_id, cat_name
		order by sum(amount_cents) desc`, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	type catRow struct {
		Kind      string `json:"kind"`
		GroupID   string `json:"groupId"`
		GroupName string `json:"groupName"`
		CatID     string `json:"catId"`
		CatName   string `json:"catName"`
		Sum       int64  `json:"sum"` // 分
		Count     int    `json:"count"`
	}
	out := []catRow{}
	for rows.Next() {
		var it catRow
		var n int64
		if err := rows.Scan(&it.Kind, &it.GroupID, &it.GroupName, &it.CatID, &it.CatName, &it.Sum, &n); err != nil {
			return err
		}
		it.Count = int(n)
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"rows": out, "from": from, "to": to})
	return nil
}

func loadGroups(c appkit.Context) ([]group, error) {
	rows, err := c.DB.Query(`select id, name, kind, sys from ledger_groups where uid = ? order by kind desc, sort, name`, c.User)
	if err != nil {
		return nil, err
	}
	groups := make([]group, 0, 16)
	index := map[string]int{}
	for rows.Next() {
		var g group
		var sys int
		if err := rows.Scan(&g.ID, &g.Name, &g.Kind, &sys); err != nil {
			_ = rows.Close()
			return nil, err
		}
		g.Sys = sys == 1
		g.Cats = []cat{}
		index[g.ID] = len(groups)
		groups = append(groups, g)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	crows, err := c.DB.Query(`select id, group_id, name, sys from ledger_cats where uid = ? order by sort, name`, c.User)
	if err != nil {
		return nil, err
	}
	defer func() { _ = crows.Close() }()
	for crows.Next() {
		var it cat
		var gid string
		var sys int
		if err := crows.Scan(&it.ID, &gid, &it.Name, &sys); err != nil {
			return nil, err
		}
		it.Sys = sys == 1
		if i, ok := index[gid]; ok {
			groups[i].Cats = append(groups[i].Cats, it)
		}
	}
	return groups, crows.Err()
}

func loadAccounts(c appkit.Context) ([]named, error) {
	rows, err := c.DB.Query("select id, name from ledger_accounts where uid = ? order by sort, name", c.User)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]named, 0, 4)
	for rows.Next() {
		var n named
		if err := rows.Scan(&n.ID, &n.Name); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func loadPeers(c appkit.Context) ([]string, error) {
	rows, err := c.DB.Query("select name from ledger_peers where uid = ? order by sort, name", c.User)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]string, 0, 8)
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func getEntry(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil || strings.TrimSpace(in.ID) == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id")
	}
	var e entry
	var created string
	err := c.DB.QueryRow(`select id, day, kind, amount_cents, coalesce(group_id,''), coalesce(group_name,''),
		coalesce(category_id,''), coalesce(cat_name,''), coalesce(account_id,''), coalesce(account_name,''),
		coalesce(peer,''), coalesce(note,''), coalesce(receipt,''), created_at
		from ledger_entries where uid = ? and id = ?`, c.User, in.ID).
		Scan(&e.ID, &e.Day, &e.Kind, &e.Amount, &e.GroupID, &e.GroupName,
			&e.CatID, &e.CatName, &e.AccountID, &e.AccountName, &e.Peer, &e.Note, &e.Receipt, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return appkit.Errorf(http.StatusNotFound, "账目不存在")
	}
	if err != nil {
		return err
	}
	e.CreatedAt = created
	e.HasReceipt = e.Receipt != ""
	appkit.JSON(w, http.StatusOK, map[string]any{"entry": e})
	return nil
}

// ── 写：记账 ──

func save(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	if err := seed(c); err != nil {
		return err
	}
	var in struct {
		ID        string `json:"id"`
		Kind      string `json:"kind"`
		Amount    int64  `json:"amount"` // 分
		GroupID   string `json:"groupId"`
		CatID     string `json:"catId"`
		AccountID string `json:"accountId"`
		Peer      string `json:"peer"`
		Note      string `json:"note"`
		Day       string `json:"day"`
		Receipt   string `json:"receipt"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	kind := strings.TrimSpace(in.Kind)
	if kind != "expense" && kind != "income" {
		return appkit.Errorf(http.StatusBadRequest, "收支类型只能是 expense 或 income")
	}
	amount := in.Amount
	if amount < 0 {
		amount = -amount
	}
	if amount == 0 {
		return appkit.Errorf(http.StatusBadRequest, "金额不能为 0")
	}
	if amount > 1_000_000_000_00 { // 10 亿，防误输入
		return appkit.Errorf(http.StatusBadRequest, "金额过大")
	}
	// 一级分类必须属于当前账号、且收/支类型一致
	var gname, gkind string
	if err := c.DB.QueryRow("select name, kind from ledger_groups where uid = ? and id = ?", c.User, in.GroupID).
		Scan(&gname, &gkind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return appkit.Errorf(http.StatusBadRequest, "请选择收支分类")
		}
		return err
	}
	if gkind != kind {
		return appkit.Errorf(http.StatusBadRequest, "分类与收支类型不一致")
	}
	// 二级分类（可选）：必须属于该一级分类
	catID, catName := "", ""
	if strings.TrimSpace(in.CatID) != "" {
		if err := c.DB.QueryRow("select name from ledger_cats where uid = ? and id = ? and group_id = ?",
			c.User, in.CatID, in.GroupID).Scan(&catName); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return appkit.Errorf(http.StatusBadRequest, "二级分类不属于所选分类")
			}
			return err
		}
		catID = in.CatID
	}
	// 结算账户（可选，空则取第一个）
	accID, accName := strings.TrimSpace(in.AccountID), ""
	if accID != "" {
		if err := c.DB.QueryRow("select name from ledger_accounts where uid = ? and id = ?", c.User, accID).
			Scan(&accName); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return appkit.Errorf(http.StatusBadRequest, "结算账户不存在")
			}
			return err
		}
	} else {
		_ = c.DB.QueryRow("select id, name from ledger_accounts where uid = ? order by sort, name limit 1", c.User).
			Scan(&accID, &accName)
	}
	peer := truncate(strings.TrimSpace(in.Peer), 40)
	note := truncate(strings.TrimSpace(in.Note), 200)
	receipt := strings.TrimSpace(in.Receipt)
	if len(receipt) > 3<<20 {
		return appkit.Errorf(http.StatusBadRequest, "凭证图片过大")
	}
	if receipt != "" && !strings.HasPrefix(receipt, "data:image/") {
		return appkit.Errorf(http.StatusBadRequest, "凭证格式不支持")
	}
	day := strings.TrimSpace(in.Day)
	if day != "" {
		if _, err := time.Parse("2006-01-02", day); err != nil {
			return appkit.Errorf(http.StatusBadRequest, "日期格式应为 YYYY-MM-DD")
		}
	}
	now := time.Now()
	stamp := now.Format(time.RFC3339)
	if day == "" {
		day = now.Format("2006-01-02")
	}

	if strings.TrimSpace(in.ID) == "" {
		id := newID()
		if _, err := c.DB.Exec(`insert into ledger_entries
			(uid,id,day,amount_cents,kind,account_id,account_name,category_id,cat_name,group_id,group_name,note,peer,receipt,created_at,updated_at)
			values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.User, id, day, amount, kind, accID, accName, catID, catName, in.GroupID, gname, note, peer, receipt, stamp, stamp); err != nil {
			return err
		}
		appkit.JSON(w, http.StatusOK, map[string]any{"id": id})
		return nil
	}

	// 更新：不传 receipt 时保留原凭证（前端未改动图片就不带这个字段会麻烦，
	// 所以这里约定 receipt 传空串 = 清空，传 null 无法区分 —— 由前端显式带上原值）。
	res, err := c.DB.Exec(`update ledger_entries set day=?, amount_cents=?, kind=?, account_id=?, account_name=?,
		category_id=?, cat_name=?, group_id=?, group_name=?, note=?, peer=?, receipt=?, updated_at=?
		where uid = ? and id = ?`,
		day, amount, kind, accID, accName, catID, catName, in.GroupID, gname, note, peer, receipt, stamp, c.User, in.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return appkit.Errorf(http.StatusNotFound, "账目不存在")
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"id": in.ID})
	return nil
}

func remove(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil || strings.TrimSpace(in.ID) == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id")
	}
	if _, err := c.DB.Exec("delete from ledger_entries where uid = ? and id = ?", c.User, in.ID); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func removeMany(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		IDs []string `json:"ids"`
	}
	if err := appkit.Decode(r, &in); err != nil || len(in.IDs) == 0 {
		return appkit.Errorf(http.StatusBadRequest, "缺少 ids")
	}
	if len(in.IDs) > 500 {
		return appkit.Errorf(http.StatusBadRequest, "一次最多删除 500 条")
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	n := 0
	for _, id := range in.IDs {
		res, err := tx.Exec("delete from ledger_entries where uid = ? and id = ?", c.User, id)
		if err != nil {
			return err
		}
		if k, _ := res.RowsAffected(); k > 0 {
			n++
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": n})
	return nil
}

// ── 写：分类（两级）──

func groupSave(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return appkit.Errorf(http.StatusBadRequest, "分类名称不能为空")
	}
	if len([]rune(name)) > 16 {
		return appkit.Errorf(http.StatusBadRequest, "一级分类最多 16 个字")
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		kind := strings.TrimSpace(in.Kind)
		if kind != "expense" && kind != "income" {
			return appkit.Errorf(http.StatusBadRequest, "收支类型只能是 expense 或 income")
		}
		var maxSort sql.NullInt64
		_ = c.DB.QueryRow("select max(sort) from ledger_groups where uid = ? and kind = ?", c.User, kind).Scan(&maxSort)
		newIDv := newID()
		if _, err := c.DB.Exec(`insert into ledger_groups(uid,id,name,kind,sort,sys,created_at) values(?,?,?,?,?,0,?)`,
			c.User, newIDv, name, kind, maxSort.Int64+1, time.Now().Format(time.RFC3339)); err != nil {
			if isUnique(err) {
				return appkit.Errorf(http.StatusBadRequest, "已有同名分类")
			}
			return err
		}
		appkit.JSON(w, http.StatusOK, map[string]any{"id": newIDv, "name": name})
		return nil
	}
	var sys int
	if err := c.DB.QueryRow("select sys from ledger_groups where uid = ? and id = ?", c.User, id).Scan(&sys); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return appkit.Errorf(http.StatusNotFound, "分类不存在")
		}
		return err
	}
	if sys == 1 {
		return appkit.Errorf(http.StatusForbidden, "系统分类不可修改")
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec("update ledger_groups set name = ? where uid = ? and id = ?", name, c.User, id); err != nil {
		if isUnique(err) {
			return appkit.Errorf(http.StatusBadRequest, "已有同名分类")
		}
		return err
	}
	if _, err := tx.Exec("update ledger_entries set group_name = ? where uid = ? and group_id = ?", name, c.User, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"id": id, "name": name})
	return nil
}

func groupDelete(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil || strings.TrimSpace(in.ID) == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id")
	}
	var sys int
	if err := c.DB.QueryRow("select sys from ledger_groups where uid = ? and id = ?", c.User, in.ID).Scan(&sys); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return appkit.Errorf(http.StatusNotFound, "分类不存在")
		}
		return err
	}
	if sys == 1 {
		return appkit.Errorf(http.StatusForbidden, "系统分类不可删除")
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// 历史账目不动：条目里已存 group_name / cat_name 快照
	if _, err := tx.Exec("delete from ledger_cats where uid = ? and group_id = ?", c.User, in.ID); err != nil {
		return err
	}
	if _, err := tx.Exec("delete from ledger_groups where uid = ? and id = ?", c.User, in.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func catSave(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID      string `json:"id"`
		GroupID string `json:"groupId"`
		Name    string `json:"name"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return appkit.Errorf(http.StatusBadRequest, "分类名称不能为空")
	}
	if len([]rune(name)) > 16 {
		return appkit.Errorf(http.StatusBadRequest, "二级分类最多 16 个字")
	}
	gid := strings.TrimSpace(in.GroupID)
	if gid == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少一级分类")
	}
	var git string
	if err := c.DB.QueryRow("select id from ledger_groups where uid = ? and id = ?", c.User, gid).Scan(&git); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return appkit.Errorf(http.StatusBadRequest, "一级分类不存在")
		}
		return err
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		var maxSort sql.NullInt64
		_ = c.DB.QueryRow("select max(sort) from ledger_cats where uid = ? and group_id = ?", c.User, gid).Scan(&maxSort)
		newC := newID()
		if _, err := c.DB.Exec(`insert into ledger_cats(uid,id,group_id,name,sort,sys,created_at) values(?,?,?,?,?,0,?)`,
			c.User, newC, gid, name, maxSort.Int64+1, time.Now().Format(time.RFC3339)); err != nil {
			if isUnique(err) {
				return appkit.Errorf(http.StatusBadRequest, "该分类下已有同名项")
			}
			return err
		}
		appkit.JSON(w, http.StatusOK, map[string]any{"id": newC, "name": name})
		return nil
	}
	var sys int
	if err := c.DB.QueryRow("select sys from ledger_cats where uid = ? and id = ?", c.User, id).Scan(&sys); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return appkit.Errorf(http.StatusNotFound, "分类不存在")
		}
		return err
	}
	if sys == 1 {
		return appkit.Errorf(http.StatusForbidden, "系统分类不可修改")
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec("update ledger_cats set name = ? where uid = ? and id = ?", name, c.User, id); err != nil {
		if isUnique(err) {
			return appkit.Errorf(http.StatusBadRequest, "该分类下已有同名项")
		}
		return err
	}
	if _, err := tx.Exec("update ledger_entries set cat_name = ? where uid = ? and category_id = ?", name, c.User, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"id": id, "name": name})
	return nil
}

func catDelete(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil || strings.TrimSpace(in.ID) == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id")
	}
	var sys int
	if err := c.DB.QueryRow("select sys from ledger_cats where uid = ? and id = ?", c.User, in.ID).Scan(&sys); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return appkit.Errorf(http.StatusNotFound, "分类不存在")
		}
		return err
	}
	if sys == 1 {
		return appkit.Errorf(http.StatusForbidden, "系统分类不可删除")
	}
	if _, err := c.DB.Exec("delete from ledger_cats where uid = ? and id = ?", c.User, in.ID); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// ── 写：账户 / 对象 ──

func accountSave(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return appkit.Errorf(http.StatusBadRequest, "账户名称不能为空")
	}
	if len([]rune(name)) > 16 {
		return appkit.Errorf(http.StatusBadRequest, "账户名称最多 16 个字")
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		var maxSort sql.NullInt64
		_ = c.DB.QueryRow("select max(sort) from ledger_accounts where uid = ?", c.User).Scan(&maxSort)
		newA := newID()
		if _, err := c.DB.Exec(`insert into ledger_accounts(uid,id,name,kind,sort,created_at) values(?,?,?, 'cash', ?, ?)`,
			c.User, newA, name, maxSort.Int64+1, time.Now().Format(time.RFC3339)); err != nil {
			return err
		}
		appkit.JSON(w, http.StatusOK, map[string]any{"id": newA, "name": name})
		return nil
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec("update ledger_accounts set name = ? where uid = ? and id = ?", name, c.User, id); err != nil {
		return err
	}
	if _, err := tx.Exec("update ledger_entries set account_name = ? where uid = ? and account_id = ?", name, c.User, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"id": id, "name": name})
	return nil
}

func accountDelete(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil || strings.TrimSpace(in.ID) == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id")
	}
	if _, err := c.DB.Exec("delete from ledger_accounts where uid = ? and id = ?", c.User, in.ID); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func peerSave(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Old  string `json:"old"`
		Name string `json:"name"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return appkit.Errorf(http.StatusBadRequest, "对象名称不能为空")
	}
	if len([]rune(name)) > 40 {
		return appkit.Errorf(http.StatusBadRequest, "对象名称最多 40 个字")
	}
	old := strings.TrimSpace(in.Old)
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if old != "" && old != name {
		if _, err := tx.Exec("update ledger_peers set name = ? where uid = ? and name = ?", name, c.User, old); err != nil {
			return err
		}
		if _, err := tx.Exec("update ledger_entries set peer = ? where uid = ? and peer = ?", name, c.User, old); err != nil {
			return err
		}
	}
	var maxSort sql.NullInt64
	_ = tx.QueryRow("select max(sort) from ledger_peers where uid = ?", c.User).Scan(&maxSort)
	if _, err := tx.Exec("insert or ignore into ledger_peers(uid,name,sort) values(?,?,?)", c.User, name, maxSort.Int64+1); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"name": name})
	return nil
}

func peerDelete(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name string `json:"name"`
	}
	if err := appkit.Decode(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少名称")
	}
	// 只从常用列表移除，历史账目里的对象名保留
	if _, err := c.DB.Exec("delete from ledger_peers where uid = ? and name = ?", c.User, in.Name); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// ── 导出 ──

func export(c appkit.Context, w http.ResponseWriter) error {
	rows, err := c.DB.Query(`select day, kind, coalesce(group_name,''), coalesce(cat_name,''), amount_cents,
		coalesce(account_name,''), coalesce(peer,''), coalesce(note,''),
		case when coalesce(receipt,'') = '' then 0 else 1 end, created_at
		from ledger_entries where uid = ? order by day, created_at`, c.User)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	var b strings.Builder
	b.WriteString("\ufeff") // Excel 认 BOM，不然中文乱码
	cw := csv.NewWriter(&b)
	_ = cw.Write([]string{"日期", "类型", "一级分类", "二级分类", "金额(元)", "结算账户", "收支对象", "备注", "有凭证", "创建时间"})
	n := 0
	for rows.Next() {
		var day, kind, gname, cname, acc, peer, note, created string
		var cents int64
		var has int
		if err := rows.Scan(&day, &kind, &gname, &cname, &cents, &acc, &peer, &note, &has, &created); err != nil {
			return err
		}
		sign := "-"
		if kind == "income" {
			sign = "+"
		}
		_ = cw.Write([]string{
			day, kindLabel(kind), gname, cname, sign + strconv.FormatFloat(float64(cents)/100, 'f', 2, 64),
			acc, peer, note, map[bool]string{true: "有", false: "无"}[has == 1], created,
		})
		n++
	}
	cw.Flush()
	if err := rows.Err(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"csv": b.String(), "count": n})
	return nil
}

func kindLabel(k string) string {
	if k == "income" {
		return "收入"
	}
	return "支出"
}

// ── 工具 ──

func decodeOpt(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	if err := appkit.Decode(r, v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// isUnique SQLite 唯一约束冲突（现代c 驱动把错误文本带 UNIQUE constraint）。
func isUnique(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToUpper(err.Error()), "UNIQUE")
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
