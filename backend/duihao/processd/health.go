package processd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"time"
)

// checkHealth 单次健康检查（方案 §3.3：tcp/http/cmd/none）。
func checkHealth(ctx context.Context, h HealthSpec) error {
	switch h.Type {
	case "", "none":
		return nil
	case "tcp":
		if h.Target == "" {
			return fmt.Errorf("tcp 健康检查缺少 target")
		}
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", h.Target)
		if err != nil {
			return err
		}
		_ = conn.Close()
		return nil
	case "http":
		if h.Target == "" {
			return fmt.Errorf("http 健康检查缺少 target")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.Target, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		// 200-499 视为存活（部分服务健康端点带鉴权返回 401）。
		if resp.StatusCode >= 500 {
			return fmt.Errorf("健康端点返回 %s", resp.Status)
		}
		return nil
	case "cmd":
		if h.Cmd == "" {
			return fmt.Errorf("cmd 健康检查缺少 cmd")
		}
		c := exec.CommandContext(ctx, h.Cmd, h.Args...)
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("健康命令失败: %v: %s", err, truncate(out, 200))
		}
		return nil
	default:
		return fmt.Errorf("未知健康检查类型 %q", h.Type)
	}
}

// waitHealthy 轮询直到健康或超时；interval<=0 用默认 500ms。
func waitHealthy(ctx context.Context, h HealthSpec, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	interval := healthTick
	if h.Interval > 0 && time.Duration(h.Interval)*time.Second < healthTick {
		interval = time.Duration(h.Interval) * time.Second
	}
	for {
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := checkHealth(c, h)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("健康检查超时（%s）: %v", timeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// periodicHealth 周期探活：不健康时通过 unhealthy 通道报告（nil 表示关闭）。
// 返回停止函数。
func periodicHealth(h HealthSpec, interval time.Duration, unhealthy chan<- error) (stop func()) {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := checkHealth(ctx, h)
				cancel()
				if err != nil {
					select {
					case unhealthy <- err:
					default: // 已有一条未处理，不堆积
					}
				}
			}
		}
	}()
	return func() { close(done) }
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
