// 数据导出（H4）：把整个健康模块原样吐成一份 JSON。
//
// 接口：export {} → {app,user,exportedAt,counts,members,reports,vitals,meds,visits}
//
// 为什么要有：记录类数据最怕「只能在这个软件里看」。给了导出口，
// 用户随时能把体检数据搬去 Excel 或别处；这也和笔记 export 一个口径。
// 附件只导出路径清单，文件本体本来就在网盘里看得见。
package health

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
)

func exportData(c appkit.Context, w http.ResponseWriter) error {
	ms, err := loadMembers(c)
	if err != nil {
		return err
	}
	type repoOut map[string]any

	// 报告 + 指标（一次查完，按 report_id 归组，避免 N+1）
	reports := []repoOut{}
	repIndex := map[string]int{}
	rrows, err := c.DB.Query(`select id,member_id,kind,title,org,dept,report_at,summary,attachments,created_at,updated_at
		from health_reports where uid=? order by report_at`, c.User)
	if err != nil {
		return err
	}
	for rrows.Next() {
		var id, mid, kind, title, org, dept, at, summary, attach, created, updated string
		if err := rrows.Scan(&id, &mid, &kind, &title, &org, &dept, &at, &summary, &attach, &created, &updated); err != nil {
			rrows.Close()
			return err
		}
		var atts []string
		_ = json.Unmarshal([]byte(attach), &atts)
		if atts == nil {
			atts = []string{}
		}
		repIndex[id] = len(reports)
		reports = append(reports, repoOut{
			"id": id, "memberId": mid, "kind": kind, "kindName": kindNames[kind],
			"title": title, "org": org, "dept": dept, "reportAt": at, "summary": summary,
			"attachments": atts, "metrics": []map[string]any{},
			"createdAt": created, "updatedAt": updated,
		})
	}
	rrows.Close()
	if err := rrows.Err(); err != nil {
		return err
	}

	mrows, err := c.DB.Query(`select report_id,name,alias,value_num,value_text,unit,ref_low,ref_high,ref_text,flag
		from health_metrics where uid=? order by report_at, sort, rowid`, c.User)
	if err != nil {
		return err
	}
	for mrows.Next() {
		var rid, name, alias, vtext, unit, refText, flag string
		var v, low, high *float64
		if err := mrows.Scan(&rid, &name, &alias, &v, &vtext, &unit, &low, &high, &refText, &flag); err != nil {
			mrows.Close()
			return err
		}
		idx, ok := repIndex[rid]
		if !ok {
			continue
		}
		list, _ := reports[idx]["metrics"].([]map[string]any)
		reports[idx]["metrics"] = append(list, map[string]any{
			"name": name, "alias": alias, "value": v, "valueText": vtext, "unit": unit,
			"refLow": low, "refHigh": high, "refText": refText, "flag": flag,
		})
	}
	mrows.Close()
	if err := mrows.Err(); err != nil {
		return err
	}

	vitals := []map[string]any{}
	vrows, err := c.DB.Query(`select member_id,kind,measured_at,v1,v2,unit,note from health_vitals
		where uid=? order by measured_at`, c.User)
	if err != nil {
		return err
	}
	for vrows.Next() {
		var mid, kind, at, unit, note string
		var v1 float64
		var v2 *float64
		if err := vrows.Scan(&mid, &kind, &at, &v1, &v2, &unit, &note); err != nil {
			vrows.Close()
			return err
		}
		row := map[string]any{"memberId": mid, "kind": kind, "measuredAt": at, "v1": v1, "unit": unit, "note": note}
		if v2 != nil {
			row["v2"] = *v2
		}
		if k, ok := vitalKindOf(kind); ok {
			row["kindName"] = k.Name
			d, _ := fmtVital(k, v1, v2)
			row["display"] = d
		}
		vitals = append(vitals, row)
	}
	vrows.Close()
	if err := vrows.Err(); err != nil {
		return err
	}

	meds := []map[string]any{}
	drows, err := c.DB.Query(`select member_id,name,dose,freq,route,start_at,end_at,status,note from health_meds
		where uid=? order by start_at`, c.User)
	if err != nil {
		return err
	}
	for drows.Next() {
		var mid, name, dose, freq, route, start, end, status, note string
		if err := drows.Scan(&mid, &name, &dose, &freq, &route, &start, &end, &status, &note); err != nil {
			drows.Close()
			return err
		}
		meds = append(meds, map[string]any{"memberId": mid, "name": name, "dose": dose, "freq": freq,
			"route": route, "startAt": start, "endAt": end, "status": status, "note": note})
	}
	drows.Close()
	if err := drows.Err(); err != nil {
		return err
	}

	visits := []map[string]any{}
	srows, err := c.DB.Query(`select member_id,visit_at,org,dept,doctor,reason,diagnosis,advice,cost,note
		from health_visits where uid=? order by visit_at`, c.User)
	if err != nil {
		return err
	}
	for srows.Next() {
		var mid, at, org, dept, doctor, reason, diagnosis, advice, note string
		var cost *float64
		if err := srows.Scan(&mid, &at, &org, &dept, &doctor, &reason, &diagnosis, &advice, &cost, &note); err != nil {
			srows.Close()
			return err
		}
		row := map[string]any{"memberId": mid, "visitAt": at, "org": org, "dept": dept, "doctor": doctor,
			"reason": reason, "diagnosis": diagnosis, "advice": advice, "note": note}
		if cost != nil {
			row["cost"] = *cost
		}
		visits = append(visits, row)
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return err
	}

	appkit.JSON(w, http.StatusOK, map[string]any{
		"app": "health", "user": c.User, "exportedAt": time.Now().Format(time.RFC3339),
		"counts": map[string]int{
			"members": len(ms), "reports": len(reports), "vitals": len(vitals),
			"meds": len(meds), "visits": len(visits),
		},
		"members": ms, "reports": reports, "vitals": vitals, "meds": meds, "visits": visits,
	})
	return nil
}
