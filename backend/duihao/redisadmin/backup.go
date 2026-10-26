// Redis 快照备份：BGSAVE 落 dump.rdb 后复制出来。
//
// 为什么是复制而不是直接让 Redis 写到备份目录：rdb 路径由 redis.conf 的 dir
// 决定，运行时改 dir 会影响 AOF/临时文件；复制一份即时快照更安全，也不动实例配置。
package redisadmin

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Backup 一份 rdb 快照。
type Backup struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"createdAt"`
}

// Backup 触发一次 BGSAVE 并复制生成的 rdb 文件。
func (c *Client) Backup(ctx context.Context) (*Backup, error) {
	cl := c.db(0)
	// BGSAVE 在已有子进程在跑时会返回错误 —— 这不算失败，下面等它完成即可。
	_ = cl.BgSave(ctx).Err()

	if err := c.waitSave(ctx, 90*time.Second); err != nil {
		return nil, err
	}

	src, err := c.rdbPath(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(src); err != nil {
		return nil, fmt.Errorf("快照文件不存在（%s）: %w", src, err)
	}

	dir := filepath.Join(c.p.BackupRoot, "redis", c.p.InstanceID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建备份目录失败: %w", err)
	}
	name := fmt.Sprintf("dump-%s.rdb", time.Now().Format("20060102-150405"))
	dst := filepath.Join(dir, name)

	if err := copyFile(src, dst); err != nil {
		return nil, fmt.Errorf("复制快照失败: %w", err)
	}
	st, err := os.Stat(dst)
	if err != nil {
		return nil, err
	}
	return &Backup{Name: name, Path: dst, Size: st.Size(), CreatedAt: st.ModTime().Format(time.RFC3339)}, nil
}

// waitSave 轮询 INFO persistence，直到 BGSAVE 结束且状态 ok。
func (c *Client) waitSave(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		raw, err := c.db(0).Info(ctx, "persistence").Result()
		if err != nil {
			return fmt.Errorf("读取持久化状态失败: %w", err)
		}
		kv := parseInfo(raw)
		status := kv["rdb_last_bgsave_status"]
		if status == "err" {
			return fmt.Errorf("Redis 后台保存失败（rdb_last_bgsave_status=err），检查磁盘空间与日志")
		}
		if kv["rdb_bgsave_in_progress"] == "0" && status == "ok" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待 Redis 完成快照超时（%s），数据量较大时可稍后重试", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// rdbPath 由 CONFIG GET dir / dbfilename 得到磁盘上的 rdb 路径。
func (c *Client) rdbPath(ctx context.Context) (string, error) {
	cl := c.db(0)
	// ConfigGet 在 go-redis v9 返回 map[string]string，且一次只接受一个参数。
	dirCfg, err := cl.ConfigGet(ctx, "dir").Result()
	if err != nil {
		return "", fmt.Errorf("读取 Redis dir 配置失败: %w", err)
	}
	nameCfg, err := cl.ConfigGet(ctx, "dbfilename").Result()
	if err != nil {
		return "", fmt.Errorf("读取 Redis dbfilename 配置失败: %w", err)
	}
	d := strings.TrimSpace(dirCfg["dir"])
	f := strings.TrimSpace(nameCfg["dbfilename"])
	if d == "" || f == "" {
		return "", fmt.Errorf("Redis 未返回 dir/dbfilename 配置")
	}
	return filepath.Join(d, f), nil
}

// ListBackups 全部快照（时间倒序）。
func (c *Client) ListBackups() ([]Backup, error) {
	dir := filepath.Join(c.p.BackupRoot, "redis", c.p.InstanceID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Backup{}, nil
		}
		return nil, err
	}
	out := make([]Backup, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".rdb") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Backup{
			Name: e.Name(), Path: filepath.Join(dir, e.Name()),
			Size: info.Size(), CreatedAt: info.ModTime().Format(time.RFC3339),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// DeleteBackup 删除快照（只允许本实例备份目录内的路径）。
func (c *Client) DeleteBackup(path string) error {
	base, err := filepath.Abs(filepath.Join(c.p.BackupRoot, "redis", c.p.InstanceID))
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(base, abs)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("路径不在本实例的备份目录内: %s", path)
	}
	if err := os.Remove(abs); err != nil {
		return fmt.Errorf("删除备份失败: %w", err)
	}
	return nil
}

// BackupDir 备份目录（UI「打开目录」用）。
func (c *Client) BackupDir() string {
	return filepath.Join(c.p.BackupRoot, "redis", c.p.InstanceID)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
