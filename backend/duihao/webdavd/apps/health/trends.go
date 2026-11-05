// 指标趋势（H2）：把「报告里的指标」与「日常自测」按同义项归并成一条条曲线。
//
// 接口：
//
//	trend.list {memberId?} → {series:[{key,name,unit,count,first,last,min,max,refLow,refHigh,refCommon,points}]}
//
// 口径：
//   - 一条线 = 同成员 + 同 alias（ALT = 谷丙转氨酶）+ 同单位，单位不同不硬凑；
//   - 点按日期升序，只取「有数值」的记录（定性结果画不出线）；
//   - 参考带用最近一条带范围的记录；日常自测没有报告范围，用该项目的常见区间，
//     refCommon=true 时前端注明「常见范围」，不当成化验单结论；
//   - 每条线最多回 60 个点（跨十年的化验单也没那么多），避免一次传一大堆。
package health

import (
	"database/sql"
	"net/http"
	"sort"
	"strings"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
)

type trendPoint struct {
	At       string   `json:"at"` // YYYY-MM-DD 或 YYYY-MM-DD HH:MM
	Value    float64  `json:"value"`
	Flag     string   `json:"flag"`
	RefLow   *float64 `json:"refLow,omitempty"`
	RefHigh  *float64 `json:"refHigh,omitempty"`
	Src      string   `json:"src"` // report / vital
	ReportID string   `json:"reportId,omitempty"`
}

type trendSeries struct {
	Key       string       `json:"key"`
	Name      string       `json:"name"`
	Unit      string       `json:"unit"`
	Count     int          `json:"count"`
	Min       float64      `json:"min"`
	Max       float64      `json:"max"`
	First     trendPoint   `json:"first"`
	Last      trendPoint   `json:"last"`
	RefLow    *float64     `json:"refLow,omitempty"`
	RefHigh   *float64     `json:"refHigh,omitempty"`
	RefCommon bool         `json:"refCommon"`
	Points    []trendPoint `json:"points"`
}

const trendMaxPoints = 60

func trendList(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		MemberID string `json:"memberId"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "请求格式错误")
	}
	mid, err := ensureMember(c, in.MemberID)
	if err != nil {
		return err
	}
	acc := map[string]*trendSeries{}
	// 顺序去重：同一个 alias+单位 里，名字取最近一次写的那个写法
	add := func(alias, name, unit string, p trendPoint, refCommon bool) {
		alias = strings.TrimSpace(alias)
		if alias == "" || p.At == "" {
			return
		}
		key := alias + "|" + unit
		s := acc[key]
		if s == nil {
			s = &trendSeries{Key: key, Name: name, Unit: unit, Min: p.Value, Max: p.Value, RefCommon: refCommon}
			acc[key] = s
		}
		s.Points = append(s.Points, p)
		if name != "" {
			s.Name = name // 后面覆盖前面的：循环按时间升序，最终留下最近的写法
		}
		if p.Value < s.Min {
			s.Min = p.Value
		}
		if p.Value > s.Max {
			s.Max = p.Value
		}
	}

	// 1) 报告指标
	rows, err := c.DB.Query(`select name,alias,unit,report_at,value_num,flag,ref_low,ref_high,report_id
		from health_metrics where uid=? and member_id=? and value_num is not null
		order by report_at, rowid`, c.User, mid)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name, alias, unit, at, flag, reportID string
		var v float64
		var low, high sql.NullFloat64
		if err := rows.Scan(&name, &alias, &unit, &at, &v, &flag, &low, &high, &reportID); err != nil {
			rows.Close()
			return err
		}
		p := trendPoint{At: at, Value: v, Flag: flag, Src: "report", ReportID: reportID}
		if low.Valid {
			f := low.Float64
			p.RefLow = &f
		}
		if high.Valid {
			f := high.Float64
			p.RefHigh = &f
		}
		add(alias, name, unit, p, false)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// 2) 日常自测：血压拆两条线，其余一条
	vrows, err := c.DB.Query(`select kind,measured_at,v1,v2,unit from health_vitals
		where uid=? and member_id=? order by measured_at`, c.User, mid)
	if err != nil {
		return err
	}
	for vrows.Next() {
		var kind, at, unit string
		var v1 float64
		var v2 sql.NullFloat64
		if err := vrows.Scan(&kind, &at, &v1, &v2, &unit); err != nil {
			vrows.Close()
			return err
		}
		k, ok := vitalKindOf(kind)
		if !ok {
			continue
		}
		// 同义项统一走 aliasTable：体重 → weight、收缩压 → sbp …
		if k.Dual {
			if v2.Valid {
				add("sbp", "收缩压", unit,
					trendPoint{At: at, Value: v1, Flag: vitalFlagOf(v1, vitalKind{Low: k.Low, High: k.High}), Src: "vital"},
					true)
				add("dbp", "舒张压", unit,
					trendPoint{At: at, Value: v2.Float64, Flag: vitalFlagOf(v2.Float64, vitalKind{Low: 60, High: 90}), Src: "vital"},
					true)
			}
			continue
		}
		add(normalizeAlias(k.Name), k.Name, unit,
			trendPoint{At: at, Value: v1, Flag: vitalFlagOf(v1, k), Src: "vital"}, true)
	}
	vrows.Close()
	if err := vrows.Err(); err != nil {
		return err
	}

	out := make([]*trendSeries, 0, len(acc))
	for _, s := range acc {
		s.Count = len(s.Points)
		s.First = s.Points[0]
		s.Last = s.Points[len(s.Points)-1]
		// 参考带：从最近往前找第一条带范围的
		for i := len(s.Points) - 1; i >= 0; i-- {
			p := s.Points[i]
			if p.RefLow != nil || p.RefHigh != nil {
				s.RefLow, s.RefHigh = p.RefLow, p.RefHigh
				break
			}
		}
		if s.RefLow == nil && s.RefHigh == nil && s.RefCommon {
			// 纯自测线：补常见区间（收缩压/舒张压单独填，因为它们是两条线）
			switch strings.SplitN(s.Key, "|", 2)[0] {
			case "sbp":
				lo, hi := 90.0, 140.0
				s.RefLow, s.RefHigh = &lo, &hi
			case "dbp":
				lo, hi := 60.0, 90.0
				s.RefLow, s.RefHigh = &lo, &hi
			default:
				if k, ok := vitalKindOf(vitalKindByName(s.Name)); ok && (k.Low > 0 || k.High > 0) {
					if k.Low > 0 {
						lo := k.Low
						s.RefLow = &lo
					}
					if k.High > 0 {
						hi := k.High
						s.RefHigh = &hi
					}
				}
			}
		}
		if len(s.Points) > trendMaxPoints {
			s.Points = s.Points[len(s.Points)-trendMaxPoints:]
			s.Count = len(s.Points)
			s.First = s.Points[0]
		}
		out = append(out, s)
	}
	// 点多的（体检项目）在前，同点数按最近一次时间倒序
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Last.At != out[j].Last.At {
			return out[i].Last.At > out[j].Last.At
		}
		return out[i].Name < out[j].Name
	})
	appkit.JSON(w, http.StatusOK, map[string]any{"memberId": mid, "series": out})
	return nil
}

// vitalKindByName 中文名 → 类型（补常见区间用）。
func vitalKindByName(name string) string {
	for _, k := range vitalKinds {
		if k.Name == name {
			return k.Kind
		}
	}
	return ""
}
