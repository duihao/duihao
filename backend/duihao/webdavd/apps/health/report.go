package health

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
)

var kindNames = map[string]string{
	"checkup": "体检", "lab": "化验", "imaging": "影像", "other": "其他",
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func clampPage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	return page, size
}

// ── 列表 ──

func reportList(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		MemberID string `json:"memberId"`
		Q        string `json:"q"`
		Kind     string `json:"kind"`
		Page     int    `json:"page"`
		PageSize int    `json:"pageSize"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	page, size := clampPage(in.Page, in.PageSize)

	where := ` where r.uid=?`
	args := []any{c.User}
	if mid := strings.TrimSpace(in.MemberID); mid != "" {
		where += ` and r.member_id=?`
		args = append(args, mid)
	}
	if k := strings.TrimSpace(in.Kind); k != "" {
		where += ` and r.kind=?`
		args = append(args, k)
	}
	if q := strings.TrimSpace(in.Q); q != "" {
		like := "%" + q + "%"
		where += ` and (r.title like ? or r.org like ? or r.dept like ? or r.summary like ? or exists(
			select 1 from health_metrics m where m.uid=r.uid and m.report_id=r.id and (m.name like ? or m.value_text like ?)))`
		args = append(args, like, like, like, like, like, like)
	}

	var total int
	if err := c.DB.QueryRow(`select count(*) from health_reports r`+where, args...).Scan(&total); err != nil {
		return err
	}
	q := `select r.id,r.member_id,r.kind,r.title,r.org,r.dept,r.report_at,r.summary,r.updated_at,
		(select count(*) from health_metrics m where m.uid=r.uid and m.report_id=r.id),
		(select count(*) from health_metrics m where m.uid=r.uid and m.report_id=r.id and m.flag<>'')
		from health_reports r` + where + ` order by r.report_at desc, r.created_at desc limit ? offset ?`
	qargs := append(append([]any{}, args...), size, (page-1)*size)
	rows, err := c.DB.Query(q, qargs...)
	if err != nil {
		return err
	}
	defer rows.Close()

	items := []map[string]any{}
	for rows.Next() {
		var id, mid, kind, title, org, dept, reportAt, summary, updatedAt string
		var metricCount, abnormal int
		if err := rows.Scan(&id, &mid, &kind, &title, &org, &dept, &reportAt, &summary, &updatedAt,
			&metricCount, &abnormal); err != nil {
			return err
		}
		items = append(items, map[string]any{
			"id": id, "memberId": mid, "kind": kind, "kindName": kindNames[kind],
			"title": title, "org": org, "dept": dept, "reportAt": reportAt,
			"summary": summary, "updatedAt": updatedAt,
			"total": metricCount, "abnormal": abnormal,
		})
	}
	pages := (total + size - 1) / size
	if pages < 1 {
		pages = 1
	}
	appkit.JSON(w, http.StatusOK, map[string]any{
		"items": items, "total": total, "page": page, "pages": pages,
	})
	return rows.Err()
}

// ── 详情 ──

type metricRow struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Alias     string   `json:"alias"`
	Value     *float64 `json:"value,omitempty"`
	ValueText string   `json:"valueText"`
	Unit      string   `json:"unit"`
	RefLow    *float64 `json:"refLow,omitempty"`
	RefHigh   *float64 `json:"refHigh,omitempty"`
	RefText   string   `json:"refText"`
	Flag      string   `json:"flag"`
	Prev      *prevVal `json:"prev,omitempty"`
}

type prevVal struct {
	Value     *float64 `json:"value,omitempty"`
	ValueText string   `json:"valueText,omitempty"`
	ReportAt  string   `json:"reportAt"`
}

func reportGet(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少报告 id")
	}

	var rep map[string]any
	var mid, kind, title, org, dept, reportAt, summary, attach, createdAt, updatedAt string
	err := c.DB.QueryRow(`select member_id,kind,title,org,dept,report_at,summary,attachments,created_at,updated_at
		from health_reports where uid=? and id=?`, c.User, id).
		Scan(&mid, &kind, &title, &org, &dept, &reportAt, &summary, &attach, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return appkit.Errorf(http.StatusNotFound, "报告不存在")
	}
	if err != nil {
		return err
	}
	var atts []string
	_ = json.Unmarshal([]byte(attach), &atts)
	rep = map[string]any{"id": id, "memberId": mid, "kind": kind, "kindName": kindNames[kind],
		"title": title, "org": org, "dept": dept, "reportAt": reportAt, "summary": summary,
		"attachments": atts, "createdAt": createdAt, "updatedAt": updatedAt}

	rows, err := c.DB.Query(`select id,name,alias,value_num,value_text,unit,ref_low,ref_high,ref_text,flag
		from health_metrics where uid=? and report_id=? order by sort, rowid`, c.User, id)
	if err != nil {
		return err
	}
	metrics := []metricRow{}
	for rows.Next() {
		var m metricRow
		var v, low, high sql.NullFloat64
		if err := rows.Scan(&m.ID, &m.Name, &m.Alias, &v, &m.ValueText, &m.Unit, &low, &high, &m.RefText, &m.Flag); err != nil {
			rows.Close()
			return err
		}
		if v.Valid {
			f := v.Float64
			m.Value = &f
		}
		if low.Valid {
			f := low.Float64
			m.RefLow = &f
		}
		if high.Valid {
			f := high.Float64
			m.RefHigh = &f
		}
		metrics = append(metrics, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// 与「上一次」对比：同成员、同 alias、日期更早的最近一条。
	for i := range metrics {
		var pv sql.NullFloat64
		var pt, ptext string
		err := c.DB.QueryRow(`select value_num,value_text,report_at from health_metrics
			where uid=? and member_id=? and alias=? and report_at<?
			order by report_at desc limit 1`, c.User, mid, metrics[i].Alias, reportAt).
			Scan(&pv, &ptext, &pt)
		if err == nil {
			prev := &prevVal{ReportAt: pt, ValueText: ptext}
			if pv.Valid {
				f := pv.Float64
				prev.Value = &f
			}
			metrics[i].Prev = prev
		}
	}

	appkit.JSON(w, http.StatusOK, map[string]any{"report": rep, "metrics": metrics})
	return nil
}

// ── 保存 / 删除 ──

type metricIn struct {
	Name      string   `json:"name"`
	Value     *float64 `json:"value"`
	ValueText string   `json:"valueText"`
	Unit      string   `json:"unit"`
	RefLow    *float64 `json:"refLow"`
	RefHigh   *float64 `json:"refHigh"`
	RefText   string   `json:"refText"`
}

func reportSave(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID          string     `json:"id"`
		MemberID    string     `json:"memberId"`
		Kind        string     `json:"kind"`
		Title       string     `json:"title"`
		Org         string     `json:"org"`
		Dept        string     `json:"dept"`
		ReportAt    string     `json:"reportAt"`
		Summary     string     `json:"summary"`
		Attachments []string   `json:"attachments"`
		Metrics     []metricIn `json:"metrics"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}

	mid := strings.TrimSpace(in.MemberID)
	if err := ensureSelf(c); err != nil { // 新账号第一份报告就可能带 m-self，先播种「本人」
		return err
	}
	if mid == "" {
		ms, err := loadMembers(c)
		if err != nil {
			return err
		}
		if len(ms) == 0 {
			return appkit.Errorf(http.StatusBadRequest, "缺少成员")
		}
		mid = ms[0].ID
	} else {
		var one int
		if err := c.DB.QueryRow(`select 1 from health_members where uid=? and id=?`, c.User, mid).Scan(&one); err != nil {
			if err == sql.ErrNoRows {
				return appkit.Errorf(http.StatusBadRequest, "成员不存在")
			}
			return err
		}
	}

	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = "checkup"
	}
	if _, ok := kindNames[kind]; !ok {
		return appkit.Errorf(http.StatusBadRequest, "未知的报告类型")
	}
	reportAt := strings.TrimSpace(in.ReportAt)
	if reportAt == "" {
		reportAt = time.Now().Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", reportAt); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "报告日期格式应为 YYYY-MM-DD")
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = kindNames[kind] + " · " + reportAt
	}
	if len([]rune(title)) > 60 {
		return appkit.Errorf(http.StatusBadRequest, "标题最多 60 个字")
	}

	// 指标行校验：名称必填；数值与定性结果至少给一个。
	clean := make([]metricIn, 0, len(in.Metrics))
	for _, m := range in.Metrics {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			continue // 空行直接忽略，前端增删行时常见
		}
		if len([]rune(name)) > 40 {
			return appkit.Errorf(http.StatusBadRequest, "指标名称最多 40 个字：「%s」", name)
		}
		m.Name = name
		m.Unit = strings.TrimSpace(m.Unit)
		m.ValueText = strings.TrimSpace(m.ValueText)
		m.RefText = strings.TrimSpace(m.RefText)
		if m.Value == nil && m.ValueText == "" {
			return appkit.Errorf(http.StatusBadRequest, "「%s」缺少结果值", name)
		}
		if m.RefLow != nil && m.RefHigh != nil && *m.RefLow > *m.RefHigh {
			return appkit.Errorf(http.StatusBadRequest, "「%s」参考范围下限大于上限", name)
		}
		clean = append(clean, m)
	}

	atts, _ := json.Marshal(in.Attachments)
	now := time.Now().Format(time.RFC3339)
	id := strings.TrimSpace(in.ID)
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if id == "" {
		id = "r" + newID()
		if _, err := tx.Exec(`insert into health_reports(uid,id,member_id,kind,title,org,dept,report_at,summary,attachments,created_at,updated_at)
			values(?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.User, id, mid, kind, title, strings.TrimSpace(in.Org), strings.TrimSpace(in.Dept),
			reportAt, strings.TrimSpace(in.Summary), string(atts), now, now); err != nil {
			return err
		}
	} else {
		res, err := tx.Exec(`update health_reports set member_id=?,kind=?,title=?,org=?,dept=?,report_at=?,summary=?,attachments=?,updated_at=?
			where uid=? and id=?`,
			mid, kind, title, strings.TrimSpace(in.Org), strings.TrimSpace(in.Dept), reportAt,
			strings.TrimSpace(in.Summary), string(atts), now, c.User, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return appkit.Errorf(http.StatusNotFound, "报告不存在")
		}
		if _, err := tx.Exec(`delete from health_metrics where uid=? and report_id=?`, c.User, id); err != nil {
			return err
		}
	}

	for i, m := range clean {
		alias := normalizeAlias(m.Name)
		flag := judgeFlag(m.Value, m.RefLow, m.RefHigh)
		if _, err := tx.Exec(`insert into health_metrics(uid,id,report_id,member_id,report_at,name,alias,value_num,value_text,unit,ref_low,ref_high,ref_text,flag,sort)
			values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.User, "x"+newID(), id, mid, reportAt, m.Name, alias, m.Value, m.ValueText, m.Unit,
			m.RefLow, m.RefHigh, m.RefText, flag, i); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	var abnormal int
	_ = c.DB.QueryRow(`select count(*) from health_metrics where uid=? and report_id=? and flag<>''`,
		c.User, id).Scan(&abnormal)
	appkit.JSON(w, http.StatusOK, map[string]any{"id": id, "total": len(clean), "abnormal": abnormal})
	return nil
}

func reportDelete(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少报告 id")
	}
	tx, err := c.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`delete from health_metrics where uid=? and report_id=?`, c.User, id); err != nil {
		return err
	}
	res, err := tx.Exec(`delete from health_reports where uid=? and id=?`, c.User, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return appkit.Errorf(http.StatusNotFound, "报告不存在")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}
