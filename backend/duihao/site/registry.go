// Package site 实现站点层（方案 §3.4 第四层 / §6.3）：站点 = 若干实例 + 根目录 + 域名。
//
// 铁律 1：本层不启动进程——启停站点 = 编排其引用实例的启停（经 instance.Manager）；
// nginx 配置以「片段」形式写入 web 实例的 sites/ 目录，由主配置 include。
// 铁律 4：站点操作全部写审计。
package site

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	duihao "github.com/duihao/duihao/backend/duihao"
)

// Record 站点记录（config\sites.json）。
type Record struct {
	ID      string   `json:"id"`             // site-1
	Name    string   `json:"name,omitempty"` // 展示名，默认首域名
	Domains []string `json:"domains"`        // server_name 列表（可选，端口已足够区分）
	// Port 站点监听端口。每个站点独立端口，默认 80（DefaultSitePort）。
	// 0 视为未设置 → 按 80 处理（旧记录兼容）。
	Port int    `json:"port"`
	// PlainPort https 站点额外监听的明文 HTTP 端口（0 = 不开）。
	// 自签证书对很多客户端不友好（挂载盘、老浏览器、嵌入式设备、手机第一次访问），
	// 所以 https 站点同时开一个 http 端口：同一个 server 块、同一份站点根，
	// 爱用哪个协议用哪个（M58 用户定稿：局域网与公网都直接 http）。
	PlainPort int    `json:"plainPort,omitempty"`
	Root      string `json:"root"` // 站点根目录（用户目录，删除站点绝不删它）

	Web string `json:"web"`            // nginx 实例 id
	PHP string `json:"php,omitempty"`  // php 实例 id（静态站点可空）
	DB  string `json:"db,omitempty"`   // 数据库实例 id
	Database string `json:"database,omitempty"` // 库名

	TLSMode string `json:"tlsMode,omitempty"` // none（mkcert 后置 M4+）

	// Disabled 站点是否停用（零值 = 启用，老记录无需迁移）。
	// 停用只把片段改名成 <id>.conf.off（nginx 的 sites/*.conf 不再加载它），
	// 共享的 nginx / php / mysql 进程照常服务其他站点 —— phpStudy 的模型：
	// 服务常驻、站点各自绑定版本、各自开关。
	Disabled bool `json:"disabled,omitempty"`

	// Options 站点级配置（高级/安全/错误页/伪静态/其他）。零值 = 默认行为。
	Options Options `json:"options,omitempty"`

	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// File sites.json 落盘结构。
type File struct {
	SchemaVersion int       `json:"schemaVersion"`
	Sites         []*Record `json:"sites"`
}

// Registry sites.json 的互斥注册表。
type Registry struct {
	path string
	mu   sync.Mutex
	recs []*Record
}

// LoadRegistry 从 config\sites.json 加载（不存在视为空）。
func LoadRegistry() (*Registry, error) {
	r := &Registry{path: filepath.Join(duihao.DirConfig(), "sites.json")}
	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("sites.json 解析失败: %w", err)
	}
	r.recs = f.Sites
	return r, nil
}

func (r *Registry) saveLocked() error {
	data, err := json.MarshalIndent(File{SchemaVersion: 1, Sites: r.recs}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(r.path, append(data, '\n'), 0o644)
}

// Add 注册站点（id 冲突报错）。
func (r *Registry) Add(rec *Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.recs {
		if x.ID == rec.ID {
			return fmt.Errorf("站点 id 已存在: %s", rec.ID)
		}
	}
	now := time.Now().Format(time.RFC3339)
	rec.CreatedAt, rec.UpdatedAt = now, now
	r.recs = append(r.recs, rec)
	return r.saveLocked()
}

// Get 读单条记录。
func (r *Registry) Get(id string) *Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.recs {
		if x.ID == id {
			return x
		}
	}
	return nil
}

// Update 覆盖并落盘。
func (r *Registry) Update(rec *Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range r.recs {
		if x.ID == rec.ID {
			rec.UpdatedAt = time.Now().Format(time.RFC3339)
			r.recs[i] = rec
			return r.saveLocked()
		}
	}
	return fmt.Errorf("站点不存在: %s", rec.ID)
}

// Remove 删除并落盘。
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range r.recs {
		if x.ID == id {
			r.recs = append(r.recs[:i], r.recs[i+1:]...)
			return r.saveLocked()
		}
	}
	return fmt.Errorf("站点不存在: %s", id)
}

// List 全部站点快照。
func (r *Registry) List() []*Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Record, len(r.recs))
	copy(out, r.recs)
	return out
}

// ReferencesInstance 返回引用了该实例的站点 id（引用完整性检查用）。
func (r *Registry) ReferencesInstance(instanceID string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, x := range r.recs {
		if x.Web == instanceID || x.PHP == instanceID || x.DB == instanceID {
			out = append(out, x.ID)
		}
	}
	return out
}
