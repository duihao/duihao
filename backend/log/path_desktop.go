//go:build !android

package log

import (
	"os"
	"path/filepath"
)

// logPath returns the desktop log location (~/.duihao/duihao.log).
func logPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "duihao.log"
	}
	return filepath.Join(home, ".duihao", "duihao.log")
}
