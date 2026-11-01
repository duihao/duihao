// 录像计划：什么时候自动录、什么时候停。
//
// 口径（与 v1 的 24/7 保持兼容）：
//   - plan 为空 = 全天录（老相机的行为不变，不需要迁移任何数据）；
//   - 一段 = 星期集合 + 起止时间；days 为空表示每天；
//   - to < from 表示跨午夜（22:00→次日 02:00），次日凌晨那段算「昨天开始的」；
//   - from == to 视为空段，直接忽略（否则 00:00-00:00 会被当成全天）。
//
// 手动开录不受计划约束：用户点了「开始录像」就是明确意图，计划到点也不去停它。
package camera

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type planSlot struct {
	Days []int  `json:"days"` // 1=周一 … 7=周日；空 = 每天
	From string `json:"from"` // HH:MM
	To   string `json:"to"`   // HH:MM
}

func parsePlan(raw string) []planSlot {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []planSlot
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// normPlan 保存前规范化：只留合法时段，days 去重升序，输出紧凑 JSON。
// 空计划返回 ""（即「全天」）。
func normPlan(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return "", nil
	}
	var in []planSlot
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return "", fmt.Errorf("录像计划格式不对：%v", err)
	}
	out := make([]planSlot, 0, len(in))
	for _, s := range in {
		from, ok1 := slotMinutes(s.From)
		to, ok2 := slotMinutes(s.To)
		if !ok1 || !ok2 {
			return "", fmt.Errorf("时间要写成 HH:MM（收到 %q → %q）", s.From, s.To)
		}
		if from == to {
			continue // 空段
		}
		days := make([]int, 0, 7)
		seen := map[int]bool{}
		for _, d := range s.Days {
			if d < 1 || d > 7 || seen[d] {
				continue
			}
			seen[d] = true
			days = append(days, d)
		}
		sort.Ints(days)
		out = append(out, planSlot{Days: days, From: s.From, To: s.To})
	}
	if len(out) == 0 {
		return "", nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// inPlan 判断某一时刻是否落在计划内（纯函数，便于单测把时间钉死）。
func inPlan(slots []planSlot, t time.Time) bool {
	if len(slots) == 0 {
		return true // 没计划 = 全天
	}
	wd := int(t.Weekday())
	if wd == 0 {
		wd = 7
	}
	prev := wd - 1
	if prev == 0 {
		prev = 7
	}
	cur := t.Hour()*60 + t.Minute()

	for _, s := range slots {
		from, ok1 := slotMinutes(s.From)
		to, ok2 := slotMinutes(s.To)
		if !ok1 || !ok2 || from == to {
			continue
		}
		dayOK := len(s.Days) == 0 || containsInt(s.Days, wd)
		if to > from {
			if dayOK && cur >= from && cur < to {
				return true
			}
			continue
		}
		// 跨夜（22:00→02:00）：今天这一段，或昨天开了头残留到今天凌晨
		if dayOK && cur >= from {
			return true
		}
		if cur < to && (len(s.Days) == 0 || containsInt(s.Days, prev)) {
			return true
		}
	}
	return false
}

// planActive 相机此刻该不该录。
func planActive(raw string, t time.Time) bool {
	return inPlan(parsePlan(raw), t)
}

// planSummary 一句话描述计划（日志与前端列表用）。
func planSummary(raw string) string {
	slots := parsePlan(raw)
	if len(slots) == 0 {
		return "全天"
	}
	parts := make([]string, 0, len(slots))
	for _, s := range slots {
		d := "每天"
		if len(s.Days) > 0 {
			names := []string{"一", "二", "三", "四", "五", "六", "日"}
			ds := make([]string, 0, len(s.Days))
			for _, n := range s.Days {
				if n >= 1 && n <= 7 {
					ds = append(ds, "周"+names[n-1])
				}
			}
			d = strings.Join(ds, "")
		}
		parts = append(parts, d+" "+s.From+"–"+s.To)
	}
	return strings.Join(parts, "、")
}

// slotMinutes "HH:MM" → 当天第几分钟。
func slotMinutes(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	h, ok1 := twoDigits(s[:2])
	m, ok2 := twoDigits(s[3:])
	if !ok1 || !ok2 || h > 23 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func twoDigits(s string) (int, bool) {
	if len(s) != 2 || s[0] < '0' || s[0] > '9' || s[1] < '0' || s[1] > '9' {
		return 0, false
	}
	return int(s[0]-'0')*10 + int(s[1]-'0'), true
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
