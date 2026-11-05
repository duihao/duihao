package processd

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"

	duihao "github.com/duihao/duihao/backend/duihao"
)

// ProbeFree 探测端口是否可 bind（分配前探测，结果不落盘由调用方持久化）。
func ProbeFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// AssignPort 从池中分配一个空闲端口；preferred 非 0 且空闲时优先用它。
// 全池无空闲返回 0（调用方报错，错误信息里附 Occupier 结果）。
func AssignPort(pool string, preferred int, s *duihao.Settings) (int, error) {
	p, ok := s.PortPools[pool]
	if !ok {
		return 0, fmt.Errorf("未知端口池 %q", pool)
	}
	if preferred != 0 {
		if preferred < p.Min || preferred > p.Max {
			return 0, fmt.Errorf("端口 %d 不在 %s 池 [%d-%d] 内", preferred, pool, p.Min, p.Max)
		}
		if ProbeFree(preferred) {
			return preferred, nil
		}
		occ := Occupier(preferred)
		return 0, fmt.Errorf("端口 %d 被占用（%s）", preferred, occ)
	}
	for port := p.Min; port <= p.Max; port++ {
		if ProbeFree(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("%s 池 [%d-%d] 已无空闲端口", pool, p.Min, p.Max)
}

// AssignPortFree 从池中分配一个「未被其他实例占用（skip）且当前可 bind」的端口。
//
// 为什么需要 skip：创建实例时进程都还没起，光看 bind 会把同一个端口分给多个
// 实例（PHP 8.2 / 8.3 各建一个实例都拿到 9000），等到启动第二个才报「端口被
// 占用」——多版本并存就是被这个坑住的。
func AssignPortFree(pool string, skip map[int]bool, s *duihao.Settings) (int, error) {
	p, ok := s.PortPools[pool]
	if !ok {
		return 0, fmt.Errorf("未知端口池 %q", pool)
	}
	for port := p.Min; port <= p.Max; port++ {
		if skip[port] || !ProbeFree(port) {
			continue
		}
		return port, nil
	}
	return 0, fmt.Errorf("%s 池 [%d-%d] 已无空闲端口", pool, p.Min, p.Max)
}

// AssignPortPreferredFree 先试首选端口 want（且未被其他实例占用），否则回落池。
func AssignPortPreferredFree(pool string, want int, skip map[int]bool, s *duihao.Settings) (int, error) {
	if want >= 1 && want <= 65535 && !skip[want] && ProbeFree(want) {
		return want, nil
	}
	return AssignPortFree(pool, skip, s)
}

// AssignPortPreferred 先试首选端口 want（可在池外，如 nginx 的 80），
// 不可用（被占用 / <1024 无管理员权限）时回落到 pool 池内分配。
// 返回实际端口，调用方应把它显示给用户（用户可能以为是 80 实际是 8080）。
func AssignPortPreferred(pool string, want int, s *duihao.Settings) (int, error) {
	if want >= 1 && want <= 65535 && ProbeFree(want) {
		return want, nil
	}
	return AssignPort(pool, 0, s)
}

// ListenerPID 返回正在监听该端口的进程 PID（0 = 没人监听）。
//
// 为什么不能只用 bind 探测：Windows 允许两个进程用 SO_REUSEADDR 抢同一个
// 端口。这时新进程 bind 得通，老进程也还在听——但真正去连的时候，连接归谁
// 是不确定的；而 nginx 这类不设 reuse 的进程反而会直接 10013 失败。所以
// 「端口是否被占」以 netstat 的 LISTENING 记录为准。
func ListenerPID(port int) int {
	out, err := exec.Command("netstat", "-ano", "-p", "tcp").Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		// TCP    127.0.0.1:8080    0.0.0.0:0    LISTENING    1234
		if len(fields) < 5 || !strings.EqualFold(fields[3], "LISTENING") {
			continue
		}
		local := fields[1]
		if i := strings.LastIndex(local, ":"); i >= 0 && local[i+1:] == strconv.Itoa(port) {
			if p, err := strconv.Atoi(fields[4]); err == nil {
				return p
			}
		}
	}
	return 0
}

// Occupier 返回占用该端口的进程描述（"nginx.exe (PID 1234)"）。
// 方案 §3.3：启动失败必须报"端口被哪个进程占用"，不能只报失败。
// 实现：netstat -ano 找 LISTENING 的 PID → tasklist 映射进程名。
func Occupier(port int) string {
	pid := ListenerPID(port)
	if pid == 0 {
		return "未知进程"
	}
	name := procName(pid)
	if name == "" {
		return fmt.Sprintf("PID %d", pid)
	}
	return fmt.Sprintf("%s (PID %d)", name, pid)
}
