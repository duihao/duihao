// 日常测量（H3）：体重 / 血压 / 血糖 / 心率 / 体温 / 血氧 / 腰围。
//
// 接口：
//
//	vital.list   {memberId?,kind?,limit?} → {items, kinds:[{kind,name,unit}], latest:{kind:item}}
//	vital.save   {id?,memberId,kind,measuredAt,v1,v2?,note?} → {id}
//	vital.delete {id}                      → {ok}
//
// 口径：
//   - 自测是单点记录，不进 health_metrics（那是报告的指标明细），单独一张表；
//   - 血压一条记录两个数（收缩压/舒张压），趋势里拆成两条线，其余一项一个数；
//   - 这里给的「常见范围」只用于趋势图的淡色参考带和卡片上的浅标记，
//     不做诊断、不给建议；真正的参考范围以每份报告自带的那一行为准。
package health

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
)

type vitalKind struct {
	Kind string  // 存库的 kind
	Name string  // 中文名
	Unit string  // 单位
	Dec  int     // 显示小数位
	Low  float64 // 常见范围下限（0 = 不设）
	High float64 // 常见范围上限（0 = 不设）
	Min  float64 // 录入合法下限
	Max  float64 // 录入合法上限
	Dual bool    // 双值（血压）
}

// vitalKinds 顺序即录入面板里 chips 的顺序。
var vitalKinds = []vitalKind{
	{Kind: "weight", Name: "体重", Unit: "kg", Dec: 1, Min: 2, Max: 300},
	{Kind: "bp", Name: "血压", Unit: "mmHg", Dec: 0, Low: 90, High: 140, Min: 40, Max: 260, Dual: true},
	{Kind: "glucose", Name: "血糖", Unit: "mmol/L", Dec: 1, Low: 3.9, High: 6.1, Min: 0.5, Max: 50},
	{Kind: "pulse", Name: "心率", Unit: "次/分", Dec: 0, Low: 60, High: 100, Min: 20, Max: 260},
	{Kind: "temp", Name: "体温", Unit: "℃", Dec: 1, Low: 36.0, High: 37.2, Min: 30, Max: 45},
	{Kind: "spo2", Name: "血氧", Unit: "%", Dec: 0, Low: 95, High: 100, Min: 50, Max: 100},
	{Kind: "waist", Name: "腰围", Unit: "cm", Dec: 1, Min: 30, Max: 200},
}

func vitalKindOf(k string) (vitalKind, bool) {
	for _, v := range vitalKinds {
		if v.Kind == k {
			return v, true
		}
	}
	return vitalKind{}, false
}

type vitalItem struct {
	ID         string   `json:"id"`
	MemberID   string   `json:"memberId"`
	Kind       string   `json:"kind"`
	KindName   string   `json:"kindName"`
	MeasuredAt string   `json:"measuredAt"`
	V1         float64  `json:"v1"`
	V2         *float64 `json:"v2,omitempty"`
	Unit       string   `json:"unit"`
	Display    string   `json:"display"`
	Flag       string   `json:"flag"` // high / low / ''（按常见范围判，仅作提示）
	Note       string   `json:"note"`
}

// fmtVital 显示值：血压 "128/82"，其余按各自小数位。
func fmtVital(k vitalKind, v1 float64, v2 *float64) (string, string) {
	if k.Dual {
		if v2 == nil {
			return trimNum(v1, k.Dec), ""
		}
		return trimNum(v1, 0) + "/" + trimNum(*v2, 0), vitalFlagDual(v1, *v2, k)
	}
	return trimNum(v1, k.Dec), vitalFlagOf(v1, k)
}

// vitalFlagOf 超出「常见范围」时的浅标记（不是诊断）。
func vitalFlagOf(v float64, k vitalKind) string {
	if k.High > 0 && v > k.High {
		return "high"
	}
	if k.Low > 0 && v < k.Low {
		return "low"
	}
	return ""
}

// vitalFlagDual 血压：取偏离程度更大的一侧。
func vitalFlagDual(sys, dia float64, k vitalKind) string {
	if sys > k.High || dia > 90 {
		return "high"
	}
	if sys < k.Low || dia < 60 {
		return "low"
	}
	return ""
}

func trimNum(v float64, dec int) string {
	s := fmt.Sprintf("%.*f", dec, v)
	// 去掉多余的 0（68.0 → 68），但保留整数与一位小数这两种自然写法
	if dec > 0 {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// normVitalAt 归一测量时间：接受 "YYYY-MM-DD HH:MM" / "YYYY-MM-DDTHH:MM" / "YYYY-MM-DD"。
// 统一按本地时间存成 "YYYY-MM-DD HH:MM"，字符串排序即时间排序。
func normVitalAt(s string) (string, error) {
	s = strings.TrimSpace(strings.Replace(s, "T", " ", 1))
	if s == "" {
		return time.Now().Format("2006-01-02 15:04"), nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t.Format("2006-01-02 15:04"), nil
		}
	}
	return "", fmt.Errorf("时间格式应为 YYYY-MM-DD HH:MM")
}

func vitalList(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		MemberID string `json:"memberId"`
		Kind     string `json:"kind"`
		Limit    int    `json:"limit"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	mid, err := ensureMember(c, in.MemberID)
	if err != nil {
		return err
	}
	limit := in.Limit
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	where := ` where uid=? and member_id=?`
	args := []any{c.User, mid}
	if k := strings.TrimSpace(in.Kind); k != "" {
		if _, ok := vitalKindOf(k); !ok {
			return appkit.Errorf(http.StatusBadRequest, "未知的测量类型")
		}
		where += ` and kind=?`
		args = append(args, k)
	}
	rows, err := c.DB.Query(`select id,member_id,kind,measured_at,v1,v2,unit,note from health_vitals`+where+
		` order by measured_at desc, created_at desc limit ?`, append(args, limit)...)
	if err != nil {
		return err
	}
	defer rows.Close()

	items := []vitalItem{}
	latest := map[string]vitalItem{}
	for rows.Next() {
		var it vitalItem
		var v2 sql.NullFloat64
		if err := rows.Scan(&it.ID, &it.MemberID, &it.Kind, &it.MeasuredAt, &it.V1, &v2, &it.Unit, &it.Note); err != nil {
			return err
		}
		k, ok := vitalKindOf(it.Kind)
		if !ok {
			continue // 历史脏数据：跳过而不是整页报错
		}
		if v2.Valid {
			f := v2.Float64
			it.V2 = &f
		}
		it.KindName = k.Name
		it.Display, it.Flag = fmtVital(k, it.V1, it.V2)
		items = append(items, it)
		if _, seen := latest[it.Kind]; !seen {
			latest[it.Kind] = it // 已按时间倒序，首次出现即最近一次
		}
	}
	kinds := make([]map[string]any, 0, len(vitalKinds))
	for _, k := range vitalKinds {
		kinds = append(kinds, map[string]any{
			"kind": k.Kind, "name": k.Name, "unit": k.Unit, "dec": k.Dec,
			"low": k.Low, "high": k.High, "dual": k.Dual,
		})
	}
	appkit.JSON(w, http.StatusOK, map[string]any{
		"memberId": mid, "items": items, "kinds": kinds, "latest": latest,
	})
	return rows.Err()
}

func vitalSave(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID         string   `json:"id"`
		MemberID   string   `json:"memberId"`
		Kind       string   `json:"kind"`
		MeasuredAt string   `json:"measuredAt"`
		V1         *float64 `json:"v1"`
		V2         *float64 `json:"v2"`
		Note       string   `json:"note"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	mid, err := ensureMember(c, in.MemberID)
	if err != nil {
		return err
	}
	k, ok := vitalKindOf(strings.TrimSpace(in.Kind))
	if !ok {
		return appkit.Errorf(http.StatusBadRequest, "请选择测量类型")
	}
	if in.V1 == nil {
		return appkit.Errorf(http.StatusBadRequest, "缺少测量数值")
	}
	if *in.V1 < k.Min || *in.V1 > k.Max {
		return appkit.Errorf(http.StatusBadRequest, "「%s」数值超出合理范围（%s–%s %s）",
			k.Name, trimNum(k.Min, 0), trimNum(k.Max, 0), k.Unit)
	}
	if k.Dual {
		if in.V2 == nil {
			return appkit.Errorf(http.StatusBadRequest, "血压请同时填收缩压和舒张压")
		}
		if *in.V2 < 20 || *in.V2 > 200 {
			return appkit.Errorf(http.StatusBadRequest, "舒张压超出合理范围")
		}
		if *in.V2 >= *in.V1 {
			return appkit.Errorf(http.StatusBadRequest, "舒张压应低于收缩压")
		}
	}
	at, err := normVitalAt(in.MeasuredAt)
	if err != nil {
		return appkit.Errorf(http.StatusBadRequest, "%s", err.Error())
	}
	note := strings.TrimSpace(in.Note)
	if len([]rune(note)) > 120 {
		return appkit.Errorf(http.StatusBadRequest, "备注最多 120 个字")
	}
	var v2 *float64
	if k.Dual {
		v2 = in.V2
	}
	now := time.Now().Format(time.RFC3339)
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = "v" + newID()
		if _, err := c.DB.Exec(`insert into health_vitals(uid,id,member_id,kind,measured_at,v1,v2,unit,note,created_at)
			values(?,?,?,?,?,?,?,?,?,?)`,
			c.User, id, mid, k.Kind, at, *in.V1, v2, k.Unit, note, now); err != nil {
			return err
		}
	} else {
		res, err := c.DB.Exec(`update health_vitals set member_id=?,kind=?,measured_at=?,v1=?,v2=?,unit=?,note=?
			where uid=? and id=?`, mid, k.Kind, at, *in.V1, v2, k.Unit, note, c.User, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return appkit.Errorf(http.StatusNotFound, "记录不存在")
		}
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"id": id})
	return nil
}

func vitalDelete(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
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
	res, err := c.DB.Exec(`delete from health_vitals where uid=? and id=?`, c.User, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return appkit.Errorf(http.StatusNotFound, "记录不存在")
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// ensureMember 解析目标成员：空 = 第一个成员（通常是「本人」），非法 = 400。
func ensureMember(c appkit.Context, raw string) (string, error) {
	if err := ensureSelf(c); err != nil { // 新账号第一笔就可能带 m-self 进来，先保证成员已播种
		return "", err
	}
	mid := strings.TrimSpace(raw)
	if mid == "" {
		ms, err := loadMembers(c)
		if err != nil {
			return "", err
		}
		if len(ms) == 0 {
			return "", appkit.Errorf(http.StatusBadRequest, "缺少成员")
		}
		return ms[0].ID, nil
	}
	var one int
	if err := c.DB.QueryRow(`select 1 from health_members where uid=? and id=?`, c.User, mid).Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return "", appkit.Errorf(http.StatusBadRequest, "成员不存在")
		}
		return "", err
	}
	return mid, nil
}
