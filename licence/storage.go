package licence

import (
	"sync"
)

// Storage 是令牌快照的持久化出口（Windows = 凭据管理器/DPAPI；
// 其他平台 = 用户配置目录 0600 文件）。
type Storage interface {
	Load() (string, error)
	Save(raw string) error
}

// memoryStorage：测试用。
type memoryStorage struct {
	mu sync.Mutex
	v  string
}

func (s *memoryStorage) Load() (string, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.v, nil }
func (s *memoryStorage) Save(raw string) error { s.mu.Lock(); defer s.mu.Unlock(); s.v = raw; return nil }

var _ Storage = (*memoryStorage)(nil)

// NewMemoryStorage 供测试注入。
func NewMemoryStorage() Storage { return &memoryStorage{} }

func defaultStorage() Storage { return platformStorage() }
