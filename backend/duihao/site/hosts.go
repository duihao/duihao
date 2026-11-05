// hosts 写入：默认给「复制粘贴」路径，另提供一次 UAC 提权自动写入。
// 铁律：提权只做这一件事（追加一行 hosts），不做别的。
package site

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	duihao "github.com/duihao/duihao/backend/duihao"
)

func hostsPath() string {
	sysRoot := os.Getenv("SystemRoot")
	if sysRoot == "" {
		sysRoot = `C:\Windows`
	}
	return filepath.Join(sysRoot, "System32", "drivers", "etc", "hosts")
}

// HostsLine 站点需要的 hosts 行。
func HostsLine(rec *Record) string {
	return "127.0.0.1 " + strings.Join(rec.Domains, " ")
}

// HostsReady 域名是否已全部解析到本机（读 hosts 判断，无需权限）。
func HostsReady(domains []string) bool {
	if len(domains) == 0 {
		return false
	}
	data, err := os.ReadFile(hostsPath())
	if err != nil {
		return false
	}
	found := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		fields := strings.Fields(line)
		for _, f := range fields[1:] {
			found[strings.ToLower(f)] = true
		}
	}
	for _, d := range domains {
		if !found[strings.ToLower(d)] {
			return false
		}
	}
	return true
}

// WriteHosts 一次 UAC 提权追加 hosts 行（已存在则跳过）。
// 用户取消提权会返回明确错误，UI 应回退到「复制粘贴」路径。
func (m *Manager) WriteHosts(id string) error {
	rec := m.reg.Get(id)
	if rec == nil {
		return fmt.Errorf("站点不存在: %s", id)
	}
	if HostsReady(rec.Domains) {
		return nil
	}
	// 脚本内容纯 ASCII，避免 PowerShell 5.1 的 BOM/编码坑。
	scriptPath := filepath.Join(duihao.DirConfig(), "hosts-"+rec.ID+".ps1")
	line := HostsLine(rec) + " # duihao " + rec.ID
	script := "$ErrorActionPreference='Stop'\n" +
		"$h = Join-Path $env:SystemRoot 'System32\\drivers\\etc\\hosts'\n" +
		"Add-Content -Path $h -Value '" + line + "' -Encoding ASCII\n"
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		return err
	}

	psCmd := fmt.Sprintf(
		"Start-Process -FilePath powershell -Verb RunAs -Wait -WindowStyle Hidden -ArgumentList '-NoProfile','-ExecutionPolicy','Bypass','-File','%s'",
		strings.ReplaceAll(scriptPath, `\`, "/"))
	out, err := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", psCmd).CombinedOutput()
	if m.auditLog != nil {
		if err != nil {
			m.auditLog.Fail("site", "write-hosts", id, map[string]any{"error": err.Error(), "out": string(out)})
		} else {
			m.auditLog.Ok("site", "write-hosts", id, map[string]any{"line": line})
		}
	}
	if err != nil {
		return fmt.Errorf("写入 hosts 失败（可能取消了 UAC 授权）: %v — 可直接复制 hosts 行手动添加", err)
	}
	if !HostsReady(rec.Domains) {
		return fmt.Errorf("hosts 写入后仍未生效，请检查是否有安全软件拦截")
	}
	return nil
}
