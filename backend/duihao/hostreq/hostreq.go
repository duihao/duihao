// Package hostreq 求值插件清单的 requires.host 条件（方案 §4.3）。
//
// 支持（M2 范围）：
//
//	disk:free>=2G     数据根所在盘剩余空间 ≥ 2 GiB
//	port:free:8080    端口 8080 可 bind
//
// 未知条件按"不通过"处理并附说明 —— 加字段不扩权，缺省拒绝比缺省放行安全。
package hostreq

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Failure 一条未通过的条件。
type Failure struct {
	Check string `json:"check"`
	Msg   string `json:"msg"`
}

// Check 求值全部条件，返回未通过列表（空切片 = 全部通过）。
// dataDir 用于 disk:free 判定（传空用默认数据根）。
func Check(checks []string, dataDir string) []Failure {
	var fails []Failure
	for _, c := range checks {
		if err := evalOne(c, dataDir); err != nil {
			fails = append(fails, Failure{Check: c, Msg: err.Error()})
		}
	}
	return fails
}

func evalOne(c, dataDir string) error {
	i := strings.Index(c, ":")
	if i < 0 {
		return fmt.Errorf("不支持的条件 %q", c)
	}
	switch c[:i] {
	case "disk":
		return evalDisk(c, c[i+1:], dataDir)
	case "port":
		return evalPort(c, strings.Split(c[i+1:], ":"))
	default:
		return fmt.Errorf("不支持的条件 %q（当前支持 disk:free>=<N>G|<N>M、port:free:<port>）", c)
	}
}

// evalDisk 处理 free>=<N>G / free>=<N>M（token 形如 "free>=2G"）。
func evalDisk(raw, token, dataDir string) error {
	if !strings.HasPrefix(token, "free") {
		return fmt.Errorf("不支持 disk 条件 %q", token)
	}
	rest := strings.TrimPrefix(token, "free")
	var cmp string
	for _, p := range []string{">=", "<=", ">", "<"} {
		if strings.HasPrefix(rest, p) {
			cmp = p
			break
		}
	}
	if cmp == "" {
		return fmt.Errorf("disk 条件缺少比较符: %s", raw)
	}
	numPart := strings.TrimPrefix(rest, cmp)
	bytes, err := parseSize(numPart)
	if err != nil {
		return fmt.Errorf("disk 条件大小解析失败: %s", raw)
	}
	dir := dataDir
	if dir == "" {
		dir = "."
	}
	free, err := diskFree(dir)
	if err != nil {
		return fmt.Errorf("无法查询磁盘空间: %v", err)
	}
	switch cmp {
	case ">=":
		if free < bytes {
			return fmt.Errorf("磁盘剩余 %s，要求 ≥ %s", human(free), human(bytes))
		}
	case ">":
		if free <= bytes {
			return fmt.Errorf("磁盘剩余 %s，要求 > %s", human(free), human(bytes))
		}
	}
	return nil
}

func evalPort(raw string, rest []string) error {
	// port:free:8080
	if len(rest) < 2 || rest[0] != "free" {
		return fmt.Errorf("不支持 port 条件: %s", raw)
	}
	port, err := strconv.Atoi(rest[1])
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("端口非法: %s", rest[1])
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", rest[1]))
	if err != nil {
		return fmt.Errorf("端口 %d 不可用（已被占用？）", port)
	}
	_ = ln.Close()
	return nil
}

func parseSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "G"), strings.HasSuffix(s, "GB"):
		mult = 1 << 30
		s = strings.TrimRight(s, "GB")
	case strings.HasSuffix(s, "M"), strings.HasSuffix(s, "MB"):
		mult = 1 << 20
		s = strings.TrimRight(s, "MB")
	case strings.HasSuffix(s, "K"), strings.HasSuffix(s, "KB"):
		mult = 1 << 10
		s = strings.TrimRight(s, "KB")
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int64(n * float64(mult)), nil
}

func human(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(b)/(1<<20))
	default:
		return fmt.Sprintf("%dK", b/(1<<10))
	}
}
