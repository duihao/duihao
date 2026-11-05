package processd

import (
	"testing"

	duihao "github.com/duihao/duihao/backend/duihao"
)

func TestProbeFreeAndOccupied(t *testing.T) {
	ln, port := listenFree(t)
	defer ln.Close()
	if ProbeFree(port) {
		t.Fatalf("端口 %d 已被占用，探测应失败", port)
	}
	if !ProbeFree(0) {
		t.Fatal("端口 0（系统分配）探测应成功")
	}
}

func TestAssignPortPreferred(t *testing.T) {
	s := duihao.DefaultSettings()
	// 从 web 池找一个真实空闲端口作为 preferred
	p, err := AssignPort("web", 0, s)
	if err != nil {
		t.Fatalf("AssignPort: %v", err)
	}
	got, err := AssignPort("web", p, s)
	if err != nil || got != p {
		t.Fatalf("preferred 分配失败: got=%d err=%v", got, err)
	}
}

func TestAssignPortOccupiedReports(t *testing.T) {
	ln, port := listenFree(t)
	defer ln.Close()
	s := duihao.DefaultSettings()
	// 构造一个池，只含被占用的端口，应报"被占用（进程名）"
	s.PortPools["solo"] = duihao.PortPool{Min: port, Max: port}
	_, err := AssignPort("solo", 0, s)
	if err == nil {
		t.Fatal("全池占用应报错")
	}
}

func TestRenderArgs(t *testing.T) {
	got := RenderArgs([]string{"-p", "{dir}", "-c", "listen {port:http};"}, map[string]string{
		"dir":       "C:/x",
		"port:http": "18080",
	})
	want := []string{"-p", "C:/x", "-c", "listen 18080;"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RenderArgs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
