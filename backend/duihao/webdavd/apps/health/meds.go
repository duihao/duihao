// 用药（H3）：在服 / 已停的药，按成员归集。
//
// 接口：
//
//	med.list   {memberId?,status?,q?} → {items:[{id,name,dose,freq,route,startAt,endAt,status,note}]}
//	med.save   {id?,memberId,name,dose?,freq?,route?,startAt?,endAt?,status?,note?} → {id}
//	med.delete {id}                    → {ok}
//
// 口径：只记录「医生说吃什么、怎么吃、吃到什么时候」，不判断该不该吃、不提示相互作用。
package health

import (
	"net/http"
	"strings"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
)

type medItem struct {
	ID       string `json:"id"`
	MemberID string `json:"memberId"`
	Name     string `json:"name"`
	Dose     string `json:"dose"`
	Freq     string `json:"freq"`
	Route    string `json:"route"`
	StartAt  string `json:"startAt"`
	EndAt    string `json:"endAt"`
	Status   string `json:"status"`
	Note     string `json:"note"`
}

func medList(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		MemberID string `json:"memberId"`
		Status   string `json:"status"`
		Q        string `json:"q"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	mid, err := ensureMember(c, in.MemberID)
	if err != nil {
		return err
	}
	where := ` where uid=? and member_id=?`
	args := []any{c.User, mid}
	switch s := strings.TrimSpace(in.Status); s {
	case "active", "stopped":
		where += ` and status=?`
		args = append(args, s)
	}
	if q := strings.TrimSpace(in.Q); q != "" {
		like := "%" + q + "%"
		where += ` and (name like ? or dose like ? or freq like ? or note like ?)`
		args = append(args, like, like, like, like)
	}
	// 在服的排前面，再按开始日期倒序（新加的药在最上面）
	rows, err := c.DB.Query(`select id,member_id,name,dose,freq,route,start_at,end_at,status,note
		from health_meds`+where+` order by case status when 'active' then 0 else 1 end, start_at desc, created_at desc`,
		args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []medItem{}
	for rows.Next() {
		var m medItem
		if err := rows.Scan(&m.ID, &m.MemberID, &m.Name, &m.Dose, &m.Freq, &m.Route,
			&m.StartAt, &m.EndAt, &m.Status, &m.Note); err != nil {
			return err
		}
		items = append(items, m)
	}
	var active int
	_ = c.DB.QueryRow(`select count(*) from health_meds where uid=? and member_id=? and status='active'`,
		c.User, mid).Scan(&active)
	appkit.JSON(w, http.StatusOK, map[string]any{"memberId": mid, "items": items, "active": active})
	return rows.Err()
}

func medSave(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID       string `json:"id"`
		MemberID string `json:"memberId"`
		Name     string `json:"name"`
		Dose     string `json:"dose"`
		Freq     string `json:"freq"`
		Route    string `json:"route"`
		StartAt  string `json:"startAt"`
		EndAt    string `json:"endAt"`
		Status   string `json:"status"`
		Note     string `json:"note"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	mid, err := ensureMember(c, in.MemberID)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return appkit.Errorf(http.StatusBadRequest, "药名不能为空")
	}
	if len([]rune(name)) > 40 {
		return appkit.Errorf(http.StatusBadRequest, "药名最多 40 个字")
	}
	for _, d := range []struct{ v, label string }{{in.StartAt, "开始日期"}, {in.EndAt, "结束日期"}} {
		if v := strings.TrimSpace(d.v); v != "" {
			if _, err := time.Parse("2006-01-02", v); err != nil {
				return appkit.Errorf(http.StatusBadRequest, "%s格式应为 YYYY-MM-DD", d.label)
			}
		}
	}
	status := strings.TrimSpace(in.Status)
	if status != "stopped" {
		status = "active"
	}
	note := strings.TrimSpace(in.Note)
	if len([]rune(note)) > 200 {
		return appkit.Errorf(http.StatusBadRequest, "备注最多 200 个字")
	}
	now := time.Now().Format(time.RFC3339)
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = "md" + newID()
		if _, err := c.DB.Exec(`insert into health_meds(uid,id,member_id,name,dose,freq,route,start_at,end_at,status,note,created_at,updated_at)
			values(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.User, id, mid, name, strings.TrimSpace(in.Dose), strings.TrimSpace(in.Freq),
			strings.TrimSpace(in.Route), strings.TrimSpace(in.StartAt), strings.TrimSpace(in.EndAt),
			status, note, now, now); err != nil {
			return err
		}
	} else {
		res, err := c.DB.Exec(`update health_meds set member_id=?,name=?,dose=?,freq=?,route=?,start_at=?,end_at=?,status=?,note=?,updated_at=?
			where uid=? and id=?`,
			mid, name, strings.TrimSpace(in.Dose), strings.TrimSpace(in.Freq), strings.TrimSpace(in.Route),
			strings.TrimSpace(in.StartAt), strings.TrimSpace(in.EndAt), status, note, now, c.User, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return appkit.Errorf(http.StatusNotFound, "用药记录不存在")
		}
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"id": id})
	return nil
}

func medDelete(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
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
	res, err := c.DB.Exec(`delete from health_meds where uid=? and id=?`, c.User, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return appkit.Errorf(http.StatusNotFound, "用药记录不存在")
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}
