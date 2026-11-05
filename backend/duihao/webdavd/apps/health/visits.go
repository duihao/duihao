// 就诊（H3）：门诊 / 急诊 / 住院的流水记录。
//
// 接口：
//
//	visit.list   {memberId?,q?,page?,pageSize?} → {items,total,page,pages}
//	visit.save   {id?,memberId,visitAt,org?,dept?,doctor?,reason?,diagnosis?,advice?,cost?,note?} → {id}
//	visit.delete {id}                            → {ok}
//
// 口径：如实抄医生写的，不做二次解释；费用只记总额，不拆分项目。
package health

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
)

type visitItem struct {
	ID        string   `json:"id"`
	MemberID  string   `json:"memberId"`
	VisitAt   string   `json:"visitAt"`
	Org       string   `json:"org"`
	Dept      string   `json:"dept"`
	Doctor    string   `json:"doctor"`
	Reason    string   `json:"reason"`
	Diagnosis string   `json:"diagnosis"`
	Advice    string   `json:"advice"`
	Cost      *float64 `json:"cost,omitempty"`
	Note      string   `json:"note"`
}

func visitList(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		MemberID string `json:"memberId"`
		Q        string `json:"q"`
		Page     int    `json:"page"`
		PageSize int    `json:"pageSize"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	mid, err := ensureMember(c, in.MemberID)
	if err != nil {
		return err
	}
	page, size := clampPage(in.Page, in.PageSize)
	where := ` where uid=? and member_id=?`
	args := []any{c.User, mid}
	if q := strings.TrimSpace(in.Q); q != "" {
		like := "%" + q + "%"
		where += ` and (org like ? or dept like ? or doctor like ? or reason like ? or diagnosis like ? or advice like ?)`
		args = append(args, like, like, like, like, like, like)
	}
	var total int
	if err := c.DB.QueryRow(`select count(*) from health_visits`+where, args...).Scan(&total); err != nil {
		return err
	}
	rows, err := c.DB.Query(`select id,member_id,visit_at,org,dept,doctor,reason,diagnosis,advice,cost,note
		from health_visits`+where+` order by visit_at desc, created_at desc limit ? offset ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []visitItem{}
	for rows.Next() {
		var v visitItem
		var cost sql.NullFloat64
		if err := rows.Scan(&v.ID, &v.MemberID, &v.VisitAt, &v.Org, &v.Dept, &v.Doctor,
			&v.Reason, &v.Diagnosis, &v.Advice, &cost, &v.Note); err != nil {
			return err
		}
		if cost.Valid {
			f := cost.Float64
			v.Cost = &f
		}
		items = append(items, v)
	}
	pages := (total + size - 1) / size
	if pages < 1 {
		pages = 1
	}
	appkit.JSON(w, http.StatusOK, map[string]any{
		"memberId": mid, "items": items, "total": total, "page": page, "pages": pages,
	})
	return rows.Err()
}

func visitSave(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID        string   `json:"id"`
		MemberID  string   `json:"memberId"`
		VisitAt   string   `json:"visitAt"`
		Org       string   `json:"org"`
		Dept      string   `json:"dept"`
		Doctor    string   `json:"doctor"`
		Reason    string   `json:"reason"`
		Diagnosis string   `json:"diagnosis"`
		Advice    string   `json:"advice"`
		Cost      *float64 `json:"cost"`
		Note      string   `json:"note"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	mid, err := ensureMember(c, in.MemberID)
	if err != nil {
		return err
	}
	visitAt := strings.TrimSpace(in.VisitAt)
	if visitAt == "" {
		visitAt = time.Now().Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", visitAt); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "就诊日期格式应为 YYYY-MM-DD")
	}
	if strings.TrimSpace(in.Org) == "" && strings.TrimSpace(in.Reason) == "" && strings.TrimSpace(in.Diagnosis) == "" {
		return appkit.Errorf(http.StatusBadRequest, "至少填机构、就诊原因或诊断中的一项")
	}
	if in.Cost != nil && (*in.Cost < 0 || *in.Cost > 1e7) {
		return appkit.Errorf(http.StatusBadRequest, "费用超出合理范围")
	}
	if in.Cost != nil {
		// 只留两位小数
		c2 := float64(int64(*in.Cost*100+0.5)) / 100
		in.Cost = &c2
	}
	now := time.Now().Format(time.RFC3339)
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = "vs" + newID()
		if _, err := c.DB.Exec(`insert into health_visits(uid,id,member_id,visit_at,org,dept,doctor,reason,diagnosis,advice,cost,note,created_at,updated_at)
			values(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.User, id, mid, visitAt, strings.TrimSpace(in.Org), strings.TrimSpace(in.Dept),
			strings.TrimSpace(in.Doctor), strings.TrimSpace(in.Reason), strings.TrimSpace(in.Diagnosis),
			strings.TrimSpace(in.Advice), in.Cost, strings.TrimSpace(in.Note), now, now); err != nil {
			return err
		}
	} else {
		res, err := c.DB.Exec(`update health_visits set member_id=?,visit_at=?,org=?,dept=?,doctor=?,reason=?,diagnosis=?,advice=?,cost=?,note=?,updated_at=?
			where uid=? and id=?`,
			mid, visitAt, strings.TrimSpace(in.Org), strings.TrimSpace(in.Dept), strings.TrimSpace(in.Doctor),
			strings.TrimSpace(in.Reason), strings.TrimSpace(in.Diagnosis), strings.TrimSpace(in.Advice),
			in.Cost, strings.TrimSpace(in.Note), now, c.User, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return appkit.Errorf(http.StatusNotFound, "就诊记录不存在")
		}
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"id": id})
	return nil
}

func visitDelete(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少记录 id")
	}
	res, err := c.DB.Exec(`delete from health_visits where uid=? and id=?`, c.User, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return appkit.Errorf(http.StatusNotFound, "就诊记录不存在")
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}
