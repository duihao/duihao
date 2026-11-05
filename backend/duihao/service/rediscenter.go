// Redis 管理服务（M11）：概览 / 键浏览 / 删键 / 快照备份。
package service

import (
	"context"
	"fmt"
	"time"

	duihao "github.com/duihao/duihao/backend/duihao"
	"github.com/duihao/duihao/backend/duihao/audit"
	"github.com/duihao/duihao/backend/duihao/instance"
	"github.com/duihao/duihao/backend/duihao/redisadmin"
)

// RedisService Redis 管理。
type RedisService struct {
	mgr   *instance.Manager
	audit *audit.Logger
}

// NewRedisService 构建服务。
func NewRedisService(mgr *instance.Manager, a *audit.Logger) *RedisService {
	return &RedisService{mgr: mgr, audit: a}
}

// RedisDBStat 逻辑库键数。
type RedisDBStat struct {
	Index int   `json:"index"`
	Keys  int64 `json:"keys"`
}

// RedisOverviewView 概览。
type RedisOverviewView struct {
	Version         string        `json:"version"`
	Mode            string        `json:"mode"`
	UptimeSeconds   int64         `json:"uptimeSeconds"`
	UsedMemoryHuman string        `json:"usedMemoryHuman"`
	UsedMemory      int64         `json:"usedMemory"`
	MaxMemory       int64         `json:"maxMemory"`
	ConnectedClient int64         `json:"connectedClients"`
	TotalCommands   int64         `json:"totalCommands"`
	Databases       int           `json:"databases"`
	TotalKeys       int64         `json:"totalKeys"`
	DBS             []RedisDBStat `json:"dbs"`
}

// RedisKeyView 键列表一行。
type RedisKeyView struct {
	Key     string `json:"key"`
	Type    string `json:"type"`
	TTL     int64  `json:"ttl"`
	Size    int64  `json:"size"`
	Preview string `json:"preview,omitempty"`
}

// RedisScanView 一页键。
type RedisScanView struct {
	Cursor uint64         `json:"cursor"`
	Keys   []RedisKeyView `json:"keys"`
}

// RedisKeyDetailView 键详情。
type RedisKeyDetailView struct {
	Key       string   `json:"key"`
	Type      string   `json:"type"`
	TTL       int64    `json:"ttl"`
	Size      int64    `json:"size"`
	Value     string   `json:"value"`
	Members   []string `json:"members"`
	Truncated bool     `json:"truncated"`
}

// RedisBackupView rdb 快照。
type RedisBackupView struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"createdAt"`
}

// params 拼连接参数（密码来自实例设置的 redis.requirepass）。
func (s *RedisService) params(instanceID string) (redisadmin.Params, error) {
	if s.mgr == nil {
		return redisadmin.Params{}, fmt.Errorf("Redis 管理尚未就绪")
	}
	rec := s.mgr.GetRecord(instanceID)
	if rec == nil {
		return redisadmin.Params{}, fmt.Errorf("实例不存在: %s", instanceID)
	}
	if rec.Plugin != "redis" {
		return redisadmin.Params{}, fmt.Errorf("实例 %s 不是 Redis（%s）", instanceID, rec.Plugin)
	}
	values, err := s.mgr.EffectiveSettings(instanceID)
	if err != nil {
		return redisadmin.Params{}, err
	}
	return redisadmin.Params{
		InstanceID: instanceID,
		Port:       rec.Ports["redis"],
		Password:   values["redis.requirepass"],
		BackupRoot: duihao.DirBackup(),
	}, nil
}

func (s *RedisService) open(instanceID string) (*redisadmin.Client, error) {
	p, err := s.params(instanceID)
	if err != nil {
		return nil, err
	}
	return redisadmin.Open(p)
}

// RedisOverview 概览（面板顶部 + db 键数分布）。
func (s *RedisService) RedisOverview(instanceID string) (*RedisOverviewView, error) {
	c, err := s.open(instanceID)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	o, err := c.GetOverview(ctx)
	if err != nil {
		return nil, err
	}
	view := &RedisOverviewView{
		Version: o.Version, Mode: o.Mode, UptimeSeconds: o.UptimeSeconds,
		UsedMemoryHuman: o.UsedMemoryHuman, UsedMemory: o.UsedMemory, MaxMemory: o.MaxMemory,
		ConnectedClient: o.ConnectedClient, TotalCommands: o.TotalCommands,
		Databases: o.Databases, TotalKeys: o.TotalKeys,
	}
	for _, d := range o.DBS {
		view.DBS = append(view.DBS, RedisDBStat{Index: d.Index, Keys: d.Keys})
	}
	return view, nil
}

// RedisScanKeys 分页浏览键。
func (s *RedisService) RedisScanKeys(instanceID string, db int, cursor uint64, pattern string, count int) (*RedisScanView, error) {
	c, err := s.open(instanceID)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := c.ScanKeys(ctx, db, cursor, pattern, int64(count))
	if err != nil {
		return nil, err
	}
	view := &RedisScanView{Cursor: res.Cursor, Keys: make([]RedisKeyView, 0, len(res.Keys))}
	for _, k := range res.Keys {
		view.Keys = append(view.Keys, RedisKeyView{
			Key: k.Key, Type: k.Type, TTL: k.TTL, Size: k.Size, Preview: k.Preview,
		})
	}
	return view, nil
}

// RedisGetKey 键详情。
func (s *RedisService) RedisGetKey(instanceID string, db int, key string) (*RedisKeyDetailView, error) {
	c, err := s.open(instanceID)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d, err := c.GetKey(ctx, db, key)
	if err != nil {
		return nil, err
	}
	if d.Members == nil {
		d.Members = []string{}
	}
	return &RedisKeyDetailView{
		Key: d.Key, Type: d.Type, TTL: d.TTL, Size: d.Size,
		Value: d.Value, Members: d.Members, Truncated: d.Truncated,
	}, nil
}

// RedisDeleteKey 删键。
func (s *RedisService) RedisDeleteKey(instanceID string, db int, key string) error {
	c, err := s.open(instanceID)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.DeleteKey(context.Background(), db, key); err != nil {
		return err
	}
	s.audit.Ok("ui", "redis.del-key", instanceID, map[string]any{"db": db, "key": key})
	return nil
}

// RedisFlushDB 清空逻辑库（危险：调用方必须已二次确认）。
func (s *RedisService) RedisFlushDB(instanceID string, db int) error {
	c, err := s.open(instanceID)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.FlushDB(context.Background(), db); err != nil {
		s.audit.Fail("ui", "redis.flushdb", instanceID, map[string]any{"db": db, "err": err.Error()})
		return err
	}
	s.audit.Ok("ui", "redis.flushdb", instanceID, map[string]any{"db": db})
	return nil
}

// RedisBackup 触发 BGSAVE 并复制快照。
func (s *RedisService) RedisBackup(instanceID string) (*RedisBackupView, error) {
	c, err := s.open(instanceID)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	b, err := c.Backup(ctx)
	if err != nil {
		s.audit.Fail("ui", "redis.backup", instanceID, map[string]any{"err": err.Error()})
		return nil, err
	}
	s.audit.Ok("ui", "redis.backup", instanceID, map[string]any{"file": b.Name, "size": b.Size})
	return &RedisBackupView{Name: b.Name, Path: b.Path, Size: b.Size, CreatedAt: b.CreatedAt}, nil
}

// ListRedisBackups 快照列表。
func (s *RedisService) ListRedisBackups(instanceID string) ([]RedisBackupView, error) {
	c, err := s.open(instanceID)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	list, err := c.ListBackups()
	if err != nil {
		return nil, err
	}
	out := make([]RedisBackupView, 0, len(list))
	for _, b := range list {
		out = append(out, RedisBackupView{Name: b.Name, Path: b.Path, Size: b.Size, CreatedAt: b.CreatedAt})
	}
	return out, nil
}

// DeleteRedisBackup 删除快照。
func (s *RedisService) DeleteRedisBackup(instanceID, path string) error {
	c, err := s.open(instanceID)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.DeleteBackup(path); err != nil {
		return err
	}
	s.audit.Ok("ui", "redis.delete-backup", instanceID, map[string]any{"path": path})
	return nil
}

// OpenRedisBackupDir 打开备份目录。
func (s *RedisService) OpenRedisBackupDir(instanceID string) error {
	c, err := s.open(instanceID)
	if err != nil {
		return err
	}
	defer c.Close()
	return openPath(c.BackupDir())
}
