//go:build windows

package licence

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// windowsParts：WMI 采集 CPU / 主板 / 系统盘序列号。
//
// 采集链路：优先 wmic（快），不可用（Win11 24H2 起逐步移除）回落
// powershell Get-CimInstance（~1s，仅登录/启动各一次，可接受）。
// 任一部件采集失败返回 ""（表决时该部件按缺失处理，不影响另两个）。
func platformParts() ([3]string, error) {
	cpu := wmicValue("cpu", "ProcessorId")
	board := wmicValue("baseboard", "SerialNumber")
	disk := wmicWhere("diskdrive", `Index=0`, "SerialNumber")

	if cpu == "" {
		cpu = psValue("(Get-CimInstance Win32_Processor | Select-Object -First 1).ProcessorId")
	}
	if board == "" {
		board = psValue("(Get-CimInstance Win32_BaseBoard | Select-Object -First 1).SerialNumber")
	}
	if disk == "" {
		disk = psValue("(Get-CimInstance Win32_DiskDrive | Where-Object Index -eq 0).SerialNumber")
	}

	out := [3]string{partHash(cpu), partHash(board), partHash(disk)}
	if out[0] == "" && out[1] == "" && out[2] == "" {
		return out, errors.New("设备信息采集失败")
	}
	return out, nil
}

// deviceName / deviceOS：登录时上报的设备描述（服务端 device.name/os，各限 60/40 字）。
func deviceName() string {
	h, err := os.Hostname()
	if err != nil || trimSpace(h) == "" {
		return "windows-pc"
	}
	return trimSpace(h)
}

func deviceOS() string { return runtime.GOOS + "/" + strings.TrimSpace(osVersion()) }

func osVersion() string {
	if v := psValue("[System.Environment]::OSVersion.VersionString"); v != "" {
		return v
	}
	return runtime.GOARCH
}

// ── 采集工具 ──

func runCmd(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = hideWindow()
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	if len(out) > 64*1024 {
		out = out[:64*1024]
	}
	return string(out), nil
}

// wmicValue：`wmic <class> get <prop> /format:list` → 取第一个非空 Value。
func wmicValue(class, prop string) string {
	out, err := runCmd(6*time.Second, "wmic", class, "get", prop, "/format:list")
	if err != nil {
		return ""
	}
	return firstKvValue(out, prop)
}

// wmicWhere：`wmic <class> where "<wql>" get <prop> /format:list`。
func wmicWhere(class, wql, prop string) string {
	out, err := runCmd(6*time.Second, "wmic", class, "where", wql, "get", prop, "/format:list")
	if err != nil {
		return ""
	}
	return firstKvValue(out, prop)
}

// psValue：PowerShell 单表达式取值（wmic 不可用时的回落）。
func psValue(expr string) string {
	out, err := runCmd(8*time.Second, "powershell",
		"-NoProfile", "-NonInteractive", "-Command", expr)
	if err != nil {
		return ""
	}
	return trimSpace(out)
}

// firstKvValue：解析 /format:list 输出（Key=Value 行，CRLF、乱序、含空行）。
func firstKvValue(out, key string) string {
	want := strings.ToLower(strings.TrimSpace(key)) + "="
	for _, line := range strings.FieldsFunc(out, func(r rune) bool { return r == '\n' || r == '\r' }) {
		line = trimSpace(line)
		if len(line) <= len(want) {
			continue
		}
		if strings.ToLower(line[:len(want)]) == want {
			v := trimSpace(line[len(want):])
			if v != "" {
				return v
			}
		}
	}
	return ""
}
