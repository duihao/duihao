package processd

import (
	"fmt"
	"os/exec"
	"strings"
)

// procName Windows 实现：tasklist 查 PID 对应进程名。
func procName(pid int) string {
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	if line == "" || strings.HasPrefix(line, "信息:") || strings.HasPrefix(line, "INFO:") {
		return ""
	}
	// "nginx.exe","1234","Console",...
	if i := strings.Index(line, `","`); i > 0 {
		return strings.Trim(line[:i], `"`)
	}
	return ""
}
