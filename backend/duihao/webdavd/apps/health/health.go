// Package health 健康：报告与指标（H1）、指标趋势（H2）、日常测量/用药/就诊（H3）、
// 附件/导出/隐私开关（H4），支持多成员。
//
// 接口约定（/-api/health/<action>，POST JSON）：
//
//	overview      {memberId?}                  → {members, total, last, abnormal:[...]}
//	member.list   {}                           → {members:[{id,name,relation,sex,birth,height,note}]}
//	member.save   {id?,name,relation?,sex?,birth?,height?,note?}  → {id}
//	member.delete {id}                         → {ok}（成员下有报告时拒绝）
//	report.list   {memberId?,q?,kind?,page?,pageSize?}
//	              → {items:[{id,kind,title,org,dept,reportAt,abnormal,total}],total,page,pages}
//	report.get    {id}                         → {report, metrics:[{...,prev:{value,reportAt}}]}
//	report.save   {id?,memberId,kind,title?,org?,dept?,reportAt,summary?,attachments?,metrics?}
//	report.delete {id}                         → {ok}（连带指标）
//	trend.list    {memberId?}                  → {series:[{key,name,unit,count,min,max,first,last,points}]}
//	vital.list    {memberId?,kind?,limit?}     → {items,kinds,latest}
//	vital.save    {id?,memberId,kind,measuredAt,v1,v2?,note?}  → {id}
//	vital.delete  {id}                         → {ok}
//	med.list      {memberId?,status?,q?}       → {items,active}
//	med.save      {id?,memberId,name,dose?,freq?,route?,startAt?,endAt?,status?,note?} → {id}
//	med.delete    {id}                         → {ok}
//	visit.list    {memberId?,q?,page?,pageSize?} → {items,total,page,pages}
//	visit.save    {id?,memberId,visitAt,org?,dept?,doctor?,reason?,diagnosis?,advice?,cost?,note?} → {id}
//	visit.delete  {id}                         → {ok}
//	attach.upload multipart(file)              → {name,path,url,size}（存网盘 _appfiles/health/）
//	attach.delete {path}                       → {ok}
//	attach.save   {reportId,attachments}       → {ok}（只改附件清单，不动指标）
//	settings.get  {}                           → {privacy}
//	settings.save {privacy}                    → {privacy}
//	export        {}                           → 全量 JSON（报告/指标/自测/用药/就诊）
//	ping
//
// 口径：
//   - 报告是容器，指标是明细：一次体检 = 1 报告 + N 指标，一张化验单同理；
//   - 高低只按该行自带参考范围判（不同机构范围不同，绝不套全局标准）；
//   - 日常自测（血压/体重/血糖…）是单点记录，独立一张表，不进报告列表；
//   - alias 是同义项归一键（ALT = 谷丙转氨酶），跨年趋势才能连成一条线；
//   - 附件只存网盘相对路径，文件本体在 _appfiles/health/ 下，点开走 WebDAV；
//   - 不做 OCR、不做医学解读、不给治疗建议 —— 这里只负责记录和呈现。
package health

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/webui"
)

type App struct{}

func New() *App { return &App{} }

func (a *App) ID() string    { return "health" }
func (a *App) Title() string { return "健康" }

func (a *App) Page(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	return webui.Render(w, webui.Page{App: a.ID(), Title: a.Title(), User: c.User, Writable: c.Writable, Role: c.Role, Perms: c.Perms})
}

func (a *App) API(c appkit.Context, w http.ResponseWriter, r *http.Request, action string) error {
	switch action {
	case "ping":
		appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "app": a.ID(), "user": c.User})
		return nil
	case "overview":
		return overview(c, w, r)
	case "member.list":
		return memberList(c, w)
	case "member.save":
		return memberSave(c, w, r)
	case "member.delete":
		return memberDelete(c, w, r)
	case "report.list":
		return reportList(c, w, r)
	case "report.get":
		return reportGet(c, w, r)
	case "report.save":
		return reportSave(c, w, r)
	case "report.delete":
		return reportDelete(c, w, r)
	case "trend.list":
		return trendList(c, w, r)
	case "vital.list":
		return vitalList(c, w, r)
	case "vital.save":
		return vitalSave(c, w, r)
	case "vital.delete":
		return vitalDelete(c, w, r)
	case "med.list":
		return medList(c, w, r)
	case "med.save":
		return medSave(c, w, r)
	case "med.delete":
		return medDelete(c, w, r)
	case "visit.list":
		return visitList(c, w, r)
	case "visit.save":
		return visitSave(c, w, r)
	case "visit.delete":
		return visitDelete(c, w, r)
	case "attach.upload":
		return attachUpload(c, w, r)
	case "attach.delete":
		return attachDelete(c, w, r)
	case "attach.save":
		return attachSave(c, w, r)
	case "settings.get":
		return settingsGet(c, w)
	case "settings.save":
		return settingsSave(c, w, r)
	case "export":
		return exportData(c, w)
	}
	return appkit.Errorf(http.StatusNotFound, "未知接口: %s", action)
}

// ── 成员 ──

type member struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Relation string   `json:"relation"`
	Sex      string   `json:"sex"`
	Birth    string   `json:"birth"`
	Height   *float64 `json:"height,omitempty"`
	Note     string   `json:"note"`
}

// ensureSelf 保证每个账号至少有一个成员（本人），确定性 id 便于重复调用不重复播种。
func ensureSelf(c appkit.Context) error {
	var n int
	if err := c.DB.QueryRow(`select count(*) from health_members where uid=?`, c.User).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := c.DB.Exec(`insert or ignore into health_members(uid,id,name,relation,sex,birth,height,note,sort,created_at)
		values(?,?,?,?,?,?,?,?,?,?)`,
		c.User, "m-self", "本人", "本人", "", "", nil, "", 0, time.Now().Format(time.RFC3339))
	return err
}

func loadMembers(c appkit.Context) ([]member, error) {
	if err := ensureSelf(c); err != nil {
		return nil, err
	}
	rows, err := c.DB.Query(`select id,name,relation,sex,birth,height,note from health_members
		where uid=? order by sort, created_at`, c.User)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []member{}
	for rows.Next() {
		var m member
		var h sql.NullFloat64
		if err := rows.Scan(&m.ID, &m.Name, &m.Relation, &m.Sex, &m.Birth, &h, &m.Note); err != nil {
			return nil, err
		}
		if h.Valid {
			v := h.Float64
			m.Height = &v
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func memberList(c appkit.Context, w http.ResponseWriter) error {
	ms, err := loadMembers(c)
	if err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"members": ms})
	return nil
}

func memberSave(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		Relation string   `json:"relation"`
		Sex      string   `json:"sex"`
		Birth    string   `json:"birth"`
		Height   *float64 `json:"height"`
		Note     string   `json:"note"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return appkit.Errorf(http.StatusBadRequest, "姓名不能为空")
	}
	if len([]rune(name)) > 16 {
		return appkit.Errorf(http.StatusBadRequest, "姓名最多 16 个字")
	}
	rel := strings.TrimSpace(in.Relation)
	if rel == "" {
		rel = "本人"
	}
	if in.Birth != "" {
		if _, err := time.Parse("2006-01-02", in.Birth); err != nil {
			return appkit.Errorf(http.StatusBadRequest, "生日格式应为 YYYY-MM-DD")
		}
	}
	if in.Height != nil && (*in.Height < 30 || *in.Height > 260) {
		return appkit.Errorf(http.StatusBadRequest, "身高超出合理范围")
	}
	now := time.Now().Format(time.RFC3339)
	id := strings.TrimSpace(in.ID)
	if id == "" {
		var maxSort sql.NullInt64
		_ = c.DB.QueryRow(`select max(sort) from health_members where uid=?`, c.User).Scan(&maxSort)
		id = "m" + newID()
		if _, err := c.DB.Exec(`insert into health_members(uid,id,name,relation,sex,birth,height,note,sort,created_at)
			values(?,?,?,?,?,?,?,?,?,?)`,
			c.User, id, name, rel, in.Sex, in.Birth, in.Height, strings.TrimSpace(in.Note), maxSort.Int64+1, now); err != nil {
			return err
		}
	} else {
		res, err := c.DB.Exec(`update health_members set name=?,relation=?,sex=?,birth=?,height=?,note=?
			where uid=? and id=?`, name, rel, in.Sex, in.Birth, in.Height, strings.TrimSpace(in.Note), c.User, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return appkit.Errorf(http.StatusNotFound, "成员不存在")
		}
		// 成员改名后，报告里冗余的名称不需要跟着改（只存 id），无需级联。
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"id": id})
	return nil
}

func memberDelete(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少成员 id")
	}
	if id == "m-self" {
		return appkit.Errorf(http.StatusBadRequest, "「本人」不能删除")
	}
	var n int
	if err := c.DB.QueryRow(`select count(*) from health_reports where uid=? and member_id=?`, c.User, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return appkit.Errorf(http.StatusBadRequest, "该成员下还有 %d 份报告，请先删除报告", n)
	}
	if _, err := c.DB.Exec(`delete from health_members where uid=? and id=?`, c.User, id); err != nil {
		return err
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// ── 概览 ──

func overview(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		MemberID string `json:"memberId"`
	}
	_ = appkit.Decode(r, &in) // 允许空 body：不传成员就看全部
	ms, err := loadMembers(c)
	if err != nil {
		return err
	}
	mid := strings.TrimSpace(in.MemberID)
	if mid == "" && len(ms) > 0 {
		mid = ms[0].ID
	}

	where := ` where uid=?`
	args := []any{c.User}
	if mid != "" {
		where += ` and member_id=?`
		args = append(args, mid)
	}
	var total int
	var lastAt sql.NullString
	if err := c.DB.QueryRow(`select count(*), max(report_at) from health_reports`+where, args...).
		Scan(&total, &lastAt); err != nil {
		return err
	}

	var last map[string]any
	if lastAt.Valid {
		var id, kind, title, org, reportAt string
		err := c.DB.QueryRow(`select id,kind,title,org,report_at from health_reports`+where+
			` order by report_at desc, created_at desc limit 1`, args...).Scan(&id, &kind, &title, &org, &reportAt)
		if err == nil {
			last = map[string]any{"id": id, "kind": kind, "title": title, "org": org, "reportAt": reportAt}
		}
	}

	abWhere := strings.Replace(where, " where uid=?", " where uid=? and flag<>''", 1)
	var abnTotal int
	if err := c.DB.QueryRow(`select count(*) from health_metrics`+abWhere, args...).Scan(&abnTotal); err != nil {
		return err
	}
	rows, err := c.DB.Query(`select id,report_id,report_at,name,value_num,value_text,unit,ref_text,flag
		from health_metrics`+abWhere+` order by report_at desc, sort limit 6`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	abnormal := []map[string]any{}
	for rows.Next() {
		var id, reportID, reportAt, name, valueText, unit, refText, flag string
		var v sql.NullFloat64
		if err := rows.Scan(&id, &reportID, &reportAt, &name, &v, &valueText, &unit, &refText, &flag); err != nil {
			return err
		}
		row := map[string]any{"id": id, "reportId": reportID, "reportAt": reportAt,
			"name": name, "unit": unit, "refText": refText, "flag": flag, "valueText": valueText}
		if v.Valid {
			row["value"] = v.Float64
		}
		abnormal = append(abnormal, row)
	}

	appkit.JSON(w, http.StatusOK, map[string]any{
		"members": ms, "memberId": mid, "total": total, "last": last,
		"abnormal": abnormal, "abnormalTotal": abnTotal,
	})
	return rows.Err()
}
