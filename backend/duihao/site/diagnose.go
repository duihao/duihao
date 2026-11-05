package site

import (
	"fmt"

	"github.com/duihao/duihao/backend/duihao/processd"
)

// PortDiagnosis 端口诊断结果（界面「换端口」用）。
type PortDiagnosis struct {
	Port     int    `json:"port"`
	Free     bool   `json:"free"`
	Occupier string `json:"occupier,omitempty"` // "phpstudy.exe (PID 4242)"
	Suggest  int    `json:"suggest,omitempty"`  // 建议的空闲端口（0 = 没有）
	Message  string `json:"message,omitempty"`
}

// portBlocked 判断「端口此刻是否被别人占着」，并给出占用者描述。
// 不取锁、不算建议端口——给已经持有 m.mu 的调用方用（典型是 Start 的失败路径）。
//
// 以 netstat 的 LISTENING 记录为准而不是只试 bind：Windows 允许 SO_REUSEADDR
// 同端口多次绑定，bind 能成功不代表端口是空的。
func portBlocked(port int) (bool, string) {
	if pid := processd.ListenerPID(port); pid != 0 {
		return true, processd.Occupier(port)
	}
	return false, ""
}

// DiagnosePort 诊断站点端口：是否可绑、被谁占、建议换到哪个端口。
//
// 换端口建议走「站点自己的分配规则」（80-89 / 8080-8099 或 443-452 / 8443-8459），
// 跳过同一 nginx 实例下已被别的站点用掉的端口——这样建议出来的端口点一下就能用。
func (m *Manager) DiagnosePort(id string) (*PortDiagnosis, error) {
	rec := m.reg.Get(id)
	if rec == nil {
		return nil, fmt.Errorf("站点不存在: %s", id)
	}
	port := rec.EffectivePort()
	out := &PortDiagnosis{Port: port}
	// 先用 netstat 看有没有人 LISTENING（Windows 上 bind 能成功不代表端口空着），
	// 没人听再试一次 bind，兜住「1024 以下没权限」这种不是被占但也起不来的情况。
	if blocked, _ := portBlocked(port); !blocked && processd.ProbeFree(port) {
		out.Free = true
		return out, nil
	}
	out.Free = false
	if out.Occupier = processd.Occupier(port); out.Occupier == "未知进程" && port < 1024 {
		out.Occupier = "需要管理员权限"
	}
	if port < 1024 {
		out.Message = fmt.Sprintf("端口 %d 需要管理员权限，或被 %s 占用", port, out.Occupier)
	} else {
		out.Message = fmt.Sprintf("端口 %d 被 %s 占用", port, out.Occupier)
	}

	// nginx 实例自身端口：在锁外取（GetRecord 自己会加锁，ViewOf 里也会调 List）
	webPort := 0
	if wr := m.inst.GetRecord(rec.Web); wr != nil {
		webPort = wr.Ports["http"]
	}
	m.mu.Lock()
	suggest, perr := m.pickSitePortLocked(rec.Web, webPort, rec.TLS())
	m.mu.Unlock()
	if perr == nil {
		out.Suggest = suggest
	} else {
		out.Message += "；" + perr.Error()
	}
	return out, nil
}

// AutoFixPort 自动换端口：诊断后把站点迁到建议端口（没有建议则返回错误）。
// 返回新端口，前端提示用户「已换到 X，请重新打开」。
func (m *Manager) AutoFixPort(id string) (int, error) {
	d, err := m.DiagnosePort(id)
	if err != nil {
		return 0, err
	}
	if d.Free {
		return d.Port, nil
	}
	if d.Suggest == 0 {
		return 0, fmt.Errorf("%s，且没有可用的空闲端口可替换", d.Message)
	}
	if err := m.SetPort(id, d.Suggest); err != nil {
		return 0, err
	}
	return d.Suggest, nil
}
