// Package instance 实现实例层（方案 §3.4 第三层）：把插件清单装配为
// 可运行的实例（runtime + 端口 + 数据目录 + workers），并托管状态机。
//
// 职责边界（铁律 1）：本层不知道"站点"——站点组合属 L3（M4）；
// 进程启动一律委托 processd，本层不直接 CreateProcess。
package instance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	duihao "github.com/duihao/duihao/backend/duihao"
)

// Record 实例注册记录（config\instances.json）。
type Record struct {
	ID      string `json:"id"`      // 实例 id（如 nginx-default）
	Plugin  string `json:"plugin"`  // 插件 id
	Version string `json:"version"` // runtime 版本
	Name    string `json:"name,omitempty"` // 展示名

	// Ports 角色 → 端口（workers=1 或角色首个端口）。
	Ports map[string]int `json:"ports"`
	// WorkerPorts workers 展开后的全部端口（角色 → N 个）。
	WorkerPorts map[string][]int `json:"workerPorts,omitempty"`
	Workers int `json:"workers"`

	State      string `json:"state"` // processd 状态机
	RuntimeDir string `json:"runtimeDir,omitempty"`

	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// File 落盘结构。
type File struct {
	SchemaVersion int       `json:"schemaVersion"`
	Instances     []*Record `json:"instances"`
}

// Registry instances.json 的互斥注册表。
type Registry struct {
	path    string
	mu      sync.Mutex
	records []*Record
}

// LoadRegistry 从 config\instances.json 加载（不存在视为空）。
func LoadRegistry() (*Registry, error) {
	r := &Registry{path: filepath.Join(duihao.DirConfig(), "instances.json")}
	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("instances.json 解析失败: %w", err)
	}
	r.records = f.Instances
	return r, nil
}

func (r *Registry) saveLocked() error {
	f := File{SchemaVersion: 1, Instances: r.records}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(r.path, append(data, '\n'), 0o644)
}

// Add 注册新实例（id 冲突报错）。
func (r *Registry) Add(rec *Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.records {
		if x.ID == rec.ID {
			return fmt.Errorf("实例 id 已存在: %s", rec.ID)
		}
	}
	now := time.Now().Format(time.RFC3339)
	rec.CreatedAt, rec.UpdatedAt = now, now
	r.records = append(r.records, rec)
	return r.saveLocked()
}

// Get 读单条（返回副本指针，调用方修改后须 Update 落盘）。
func (r *Registry) Get(id string) *Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.records {
		if x.ID == id {
			return x
		}
	}
	return nil
}

// Update 覆盖并落盘（不存在报错）。
func (r *Registry) Update(rec *Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range r.records {
		if x.ID == rec.ID {
			rec.UpdatedAt = time.Now().Format(time.RFC3339)
			r.records[i] = rec
			return r.saveLocked()
		}
	}
	return fmt.Errorf("实例不存在: %s", rec.ID)
}

// Remove 删除并落盘。
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, x := range r.records {
		if x.ID == id {
			r.records = append(r.records[:i], r.records[i+1:]...)
			return r.saveLocked()
		}
	}
	return fmt.Errorf("实例不存在: %s", id)
}

// List 全部记录快照。
func (r *Registry) List() []*Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Record, len(r.records))
	copy(out, r.records)
	return out
}
