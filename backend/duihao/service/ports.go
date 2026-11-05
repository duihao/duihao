// 端口冲突诊断与一键换端（M22）。
//
// 场景：本机装着 phpstudy / 其他 web 服务器时，80、8080、9000 常被占。
// 用户看到的是「启动失败」——这里把「谁占了 + 换到哪个端口」算好，前端一次点击即可。
package service

import (
	"fmt"
	"net"

	"github.com/duihao/duihao/backend/duihao/processd"
)

// PortDiagnosisView 端口诊断结果（站点与实例共用一套结构）。
type PortDiagnosisView struct {
	Port     int    `json:"port"`
	Free     bool   `json:"free"`
	Occupier string `json:"occupier,omitempty"`
	Suggest  int    `json:"suggest,omitempty"`
	Message  string `json:"message,omitempty"`
}

func probePort(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// DiagnoseSitePort 站点端口诊断（含建议端口）。
func (s *SiteService) DiagnoseSitePort(id string) (*PortDiagnosisView, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	d, err := s.mgr.DiagnosePort(id)
	if err != nil {
		return nil, err
	}
	return &PortDiagnosisView{
		Port: d.Port, Free: d.Free, Occupier: d.Occupier,
		Suggest: d.Suggest, Message: d.Message,
	}, nil
}

// AutoFixSitePort 把站点换到建议的空闲端口，返回新端口。
func (s *SiteService) AutoFixSitePort(id string) (int, error) {
	if err := s.ready(); err != nil {
		return 0, err
	}
	return s.mgr.AutoFixPort(id)
}

// DiagnoseInstancePort 实例端口诊断（role：http / fcgi / db / ftp …）。
func (s *PluginCenterService) DiagnoseInstancePort(id, role string) (*PortDiagnosisView, error) {
	if s.mgr == nil {
		return nil, errNotReady()
	}
	v, err := s.view(id)
	if err != nil {
		return nil, err
	}
	port := v.Ports[role]
	if port == 0 {
		return nil, fmt.Errorf("实例 %s 没有运行端口 %q", id, role)
	}
	out := &PortDiagnosisView{Port: port, Free: probePort(port)}
	if out.Free {
		return out, nil
	}
	out.Occupier = processd.Occupier(port)
	out.Message = fmt.Sprintf("端口 %d 被 %s 占用", port, out.Occupier)
	if p, err := s.mgr.SuggestPort(id, role); err == nil {
		out.Suggest = p
	}
	return out, nil
}

// AutoFixInstancePort 把实例端口换到建议值（会重渲染引用站点），返回新端口。
func (s *PluginCenterService) AutoFixInstancePort(id, role string) (int, error) {
	if s.mgr == nil {
		return 0, errNotReady()
	}
	d, err := s.DiagnoseInstancePort(id, role)
	if err != nil {
		return 0, err
	}
	if d.Free {
		return d.Port, nil
	}
	if d.Suggest == 0 {
		return 0, fmt.Errorf("%s，且池里没有空闲端口", d.Message)
	}
	if _, err := s.SetInstancePort(id, role, d.Suggest); err != nil {
		return 0, err
	}
	return d.Suggest, nil
}
