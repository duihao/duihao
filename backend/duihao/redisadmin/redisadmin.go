// Package redisadmin Redis 管理面（M11）：概览、按 db 浏览键、删键、备份。
//
// Redis 没有「库/用户」对象：databases 只是 16 个逻辑分区，账号只有 requirepass
// 一个口令。所以这里的「管理」是键空间管理 + 快照备份，不做建库建用户
// （那需要 ACL，超出 v1 的「本机开发环境」定位）。
package redisadmin

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Params 打开连接所需的外部信息。
type Params struct {
	InstanceID string
	Port       int
	Password   string
	BackupRoot string
}

// Client 多 db 连接持有者：go-redis 的 DB 是客户端级参数，切换 db 只能另建
// 一个 client（连接池各自独立）。管理操作量小，按需建、用完缓存。
type Client struct {
	p       Params
	clients map[int]*redis.Client
}

// Open 建连并 ping（失败按认证/未启动分开提示）。
func Open(p Params) (*Client, error) {
	if p.Port < 1 || p.Port > 65535 {
		return nil, fmt.Errorf("端口非法: %d", p.Port)
	}
	c := &Client{p: p, clients: map[int]*redis.Client{}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.db(0).Ping(ctx).Err(); err != nil {
		c.Close()
		msg := err.Error()
		switch {
		case strings.Contains(msg, "NOAUTH"), strings.Contains(msg, "WRONGPASS"):
			return nil, fmt.Errorf("Redis 需要密码（在实例设置的「密码」里填 requirepass）")
		case strings.Contains(msg, "connection refused"), strings.Contains(msg, "dial tcp"):
			return nil, fmt.Errorf("无法连接 Redis（127.0.0.1:%d），请先启动实例", p.Port)
		}
		return nil, fmt.Errorf("连接 Redis 失败: %w", err)
	}
	return c, nil
}

// db 取（或建）指定 db 的客户端。
func (c *Client) db(n int) *redis.Client {
	if cl, ok := c.clients[n]; ok {
		return cl
	}
	cl := redis.NewClient(&redis.Options{
		Addr:         net.JoinHostPort("127.0.0.1", fmt.Sprint(c.p.Port)),
		Password:     c.p.Password,
		DB:           n,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	})
	c.clients[n] = cl
	return cl
}

// Close 关闭全部连接。
func (c *Client) Close() error {
	for _, cl := range c.clients {
		_ = cl.Close()
	}
	c.clients = map[int]*redis.Client{}
	return nil
}

// DBStat 一个逻辑库的键数。
type DBStat struct {
	Index int   `json:"index"`
	Keys  int64 `json:"keys"`
}

// Overview 面板顶部概览。
type Overview struct {
	Version         string   `json:"version"`
	Mode            string   `json:"mode"`
	UptimeSeconds   int64    `json:"uptimeSeconds"`
	UsedMemoryHuman string   `json:"usedMemoryHuman"`
	UsedMemory      int64    `json:"usedMemory"`
	MaxMemory       int64    `json:"maxMemory"`
	ConnectedClient int64    `json:"connectedClients"`
	TotalCommands   int64    `json:"totalCommands"`
	Databases       int      `json:"databases"`
	TotalKeys       int64    `json:"totalKeys"`
	DBS             []DBStat `json:"dbs"`
}

// GetOverview 汇总 INFO 与 keyspace。
func (c *Client) GetOverview(ctx context.Context) (*Overview, error) {
	raw, err := c.db(0).Info(ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("读取 INFO 失败: %w", err)
	}
	kv := parseInfo(raw)
	o := &Overview{
		Version:         kv["redis_version"],
		Mode:            kv["redis_mode"],
		UptimeSeconds:   atoi64(kv["uptime_in_seconds"]),
		UsedMemoryHuman: kv["used_memory_human"],
		UsedMemory:      atoi64(kv["used_memory"]),
		MaxMemory:       atoi64(kv["maxmemory"]),
		ConnectedClient: atoi64(kv["connected_clients"]),
		TotalCommands:   atoi64(kv["total_commands_processed"]),
		Databases:       16,
	}

	// ConfigGet 在 go-redis v9 返回 map[string]string。
	if cfg, err := c.db(0).ConfigGet(ctx, "databases").Result(); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(cfg["databases"])); err == nil && n > 0 {
			o.Databases = n
		}
	}
	// keyspace 段：db0:keys=3,expires=0,avg_ttl=0
	counts := map[int]int64{}
	for k, v := range kv {
		if !strings.HasPrefix(k, "db") {
			continue
		}
		idx, err := strconv.Atoi(strings.TrimPrefix(k, "db"))
		if err != nil {
			continue
		}
		for _, part := range strings.Split(v, ",") {
			if strings.HasPrefix(part, "keys=") {
				n, _ := strconv.ParseInt(strings.TrimPrefix(part, "keys="), 10, 64)
				counts[idx] = n
				o.TotalKeys += n
			}
		}
	}
	o.DBS = make([]DBStat, 0, o.Databases)
	for i := 0; i < o.Databases; i++ {
		o.DBS = append(o.DBS, DBStat{Index: i, Keys: counts[i]})
	}
	return o, nil
}

// KeyInfo 键列表一行。
type KeyInfo struct {
	Key  string `json:"key"`
	Type string `json:"type"`
	TTL  int64  `json:"ttl"`  // 秒；-1 永久，-2 不存在
	Size int64  `json:"size"` // 元素数 / 字符串字节数
	Preview string `json:"preview,omitempty"`
}

// ScanResult 一页键。
type ScanResult struct {
	Cursor uint64    `json:"cursor"`
	Keys   []KeyInfo `json:"keys"`
}

// ScanKeys 按 SCAN 游标分页（绝不用 KEYS：生产库上会阻塞整个实例）。
func (c *Client) ScanKeys(ctx context.Context, db int, cursor uint64, pattern string, count int64) (*ScanResult, error) {
	if db < 0 || db >= 1024 {
		return nil, fmt.Errorf("db 序号非法: %d", db)
	}
	if pattern == "" {
		pattern = "*"
	}
	if count <= 0 || count > 1000 {
		count = 100
	}
	cl := c.db(db)
	keys, next, err := cl.Scan(ctx, cursor, pattern, count).Result()
	if err != nil {
		return nil, fmt.Errorf("SCAN 失败: %w", err)
	}

	res := &ScanResult{Cursor: next, Keys: make([]KeyInfo, 0, len(keys))}
	if len(keys) == 0 {
		return res, nil
	}

	// 一次 pipeline 拿类型/TTL/大小，避免 N×3 次往返。
	pipe := cl.Pipeline()
	types := make([]*redis.StatusCmd, len(keys))
	ttls := make([]*redis.DurationCmd, len(keys))
	for i, k := range keys {
		types[i] = pipe.Type(ctx, k)
		ttls[i] = pipe.TTL(ctx, k)
	}
	_, _ = pipe.Exec(ctx) // 单键缺失会返回 redis.Nil，忽略整体错误

	for i, k := range keys {
		info := KeyInfo{Key: k, TTL: -1}
		if types[i] != nil {
			info.Type = types[i].Val()
		}
		if ttls[i] != nil {
			switch d := ttls[i].Val(); {
			case d == -1:
				info.TTL = -1
			case d == -2:
				info.TTL = -2
			default:
				info.TTL = int64(d / time.Second)
			}
		}
		info.Size = keySize(ctx, cl, k, info.Type)
		if info.Type == "string" {
			if v, err := cl.Get(ctx, k).Result(); err == nil {
				info.Preview = truncate(oneLine(v), 80)
			}
		}
		res.Keys = append(res.Keys, info)
	}
	return res, nil
}

// keySize 按类型取元素数（string 取字节长度）。
func keySize(ctx context.Context, cl *redis.Client, key, typ string) int64 {
	var cmd *redis.IntCmd
	switch typ {
	case "string":
		cmd = cl.StrLen(ctx, key)
	case "list":
		cmd = cl.LLen(ctx, key)
	case "set":
		cmd = cl.SCard(ctx, key)
	case "zset":
		cmd = cl.ZCard(ctx, key)
	case "hash":
		cmd = cl.HLen(ctx, key)
	case "stream":
		cmd = cl.XLen(ctx, key)
	default:
		return 0
	}
	n, err := cmd.Result()
	if err != nil {
		return 0
	}
	return n
}

// KeyDetail 键详情（值预览按类型截断）。
type KeyDetail struct {
	Key     string   `json:"key"`
	Type    string   `json:"type"`
	TTL     int64    `json:"ttl"`
	Size    int64    `json:"size"`
	Value   string   `json:"value"`
	Members []string `json:"members,omitempty"`
	Truncated bool   `json:"truncated"`
}

// GetKey 读取键内容（最多 200 个元素 / 4000 字符，避免把大 value 拖进 UI）。
func (c *Client) GetKey(ctx context.Context, db int, key string) (*KeyDetail, error) {
	cl := c.db(db)
	typ, err := cl.Type(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if typ == "none" {
		return nil, fmt.Errorf("键不存在: %s", key)
	}
	d := &KeyDetail{Key: key, Type: typ, TTL: -1}
	if ttl, err := cl.TTL(ctx, key).Result(); err == nil {
		if ttl >= 0 {
			d.TTL = int64(ttl / time.Second)
		}
	}
	d.Size = keySize(ctx, cl, key, typ)

	const maxElems = 200
	switch typ {
	case "string":
		v, err := cl.Get(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		d.Value = truncate(v, 4000)
		d.Truncated = len(v) > len(d.Value)
	case "list":
		vals, err := cl.LRange(ctx, key, 0, maxElems-1).Result()
		if err == nil {
			d.Members = vals
			d.Truncated = d.Size > int64(len(vals))
		}
	case "set":
		vals, err := cl.SRandMemberN(ctx, key, maxElems).Result()
		if err == nil {
			sort.Strings(vals)
			d.Members = vals
			d.Truncated = d.Size > int64(len(vals))
		}
	case "zset":
		vals, err := cl.ZRange(ctx, key, 0, maxElems-1).Result()
		if err == nil {
			d.Members = vals
			d.Truncated = d.Size > int64(len(vals))
		}
	case "hash":
		vals, err := cl.HGetAll(ctx, key).Result()
		if err == nil {
			keys := make([]string, 0, len(vals))
			for k := range vals {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for i, k := range keys {
				if i >= maxElems {
					d.Truncated = true
					break
				}
				d.Members = append(d.Members, k+" = "+vals[k])
			}
		}
	case "stream":
		vals, err := cl.XRange(ctx, key, "-", "+").Result()
		if err == nil {
			for i, m := range vals {
				if i >= maxElems {
					d.Truncated = true
					break
				}
				d.Members = append(d.Members, fmt.Sprintf("%s %v", m.ID, m.Values))
			}
		}
	}
	return d, nil
}

// DeleteKey 删一个键。
func (c *Client) DeleteKey(ctx context.Context, db int, key string) error {
	n, err := c.db(db).Del(ctx, key).Result()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("键不存在: %s", key)
	}
	return nil
}

// FlushDB 清空一个逻辑库（危险操作，UI 需二次确认）。
func (c *Client) FlushDB(ctx context.Context, db int) error {
	if db < 0 || db >= 1024 {
		return fmt.Errorf("db 序号非法: %d", db)
	}
	return c.db(db).FlushDB(ctx).Err()
}

// ─── 小工具 ───

// parseInfo INFO 响应按 "key:value" 拍平（含 keyspace 段的 db0:keys=... 行）。
func parseInfo(raw string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, ":")
		if i <= 0 {
			continue
		}
		out[line[:i]] = line[i+1:]
	}
	return out
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ⏎ ")
	return s
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n]
}
