//go:build !windows

package licence

import (
	"os"
	"path/filepath"
)

// 非 Windows：用户配置目录下 0600 文件（桌面版当前只发 Windows；
// 这份实现保证 darwin/linux 开发机可跑测试）。
type fileStorage struct{ path string }

func platformStorage() Storage {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	p := filepath.Join(dir, "duihao-desktop")
	_ = os.MkdirAll(p, 0o700)
	return &fileStorage{path: filepath.Join(p, "licence.json")}
}

func (s *fileStorage) Load() (string, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}

func (s *fileStorage) Save(raw string) error {
	if raw == "" {
		err := os.Remove(s.path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.WriteFile(s.path, []byte(raw), 0o600)
}
