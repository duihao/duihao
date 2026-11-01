// Package store 个人空间的单库数据层：一个 SQLite 文件承载所有应用。
//
// 口径（2026-10-28 定稿）：
//   - 单库：所有应用共用 hub.db，不按账号分库；
//   - 多租户靠 uid 列：每张表第一列是 uid，主键/唯一索引一律 (uid, …)，
//     所有语句由各应用按 c.User 传参，store 不提供裸查询入口；
//   - 库文件放实例数据目录（%APPDATA%\...\instances\<id>\hub.db），
//     绝不放共享根目录 —— 同步客户端会拉走写入中的 SQLite 导致锁/损坏；
//   - 迁移：migrations/NNN_*.sql 用 user_version 递进，只加不改。
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // 纯 Go 驱动，无 CGO（打包不依赖 gcc）
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store 单库句柄（进程内一个实例一份）。
type Store struct {
	db   *sql.DB
	path string
}

// Open 打开（必要时创建）库文件并跑完迁移。
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录失败: %w", err)
	}
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// WAL 下多读单写；连接数太少会卡页面并发，太多会抢写锁，4 对单机足够。
	db.SetMaxOpenConns(4)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("数据库不可用: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, path: path}, nil
}

// DB 暴露连接（各应用自行带 uid 条件查询）。
func (s *Store) DB() *sql.DB { return s.db }

// Path 库文件路径（诊断/备份展示用）。
func (s *Store) Path() string { return s.path }

// Close 关闭连接（实例停止时调用）。
func (s *Store) Close() error { return s.db.Close() }

// Snapshot 在线快照（VACUUM INTO，不影响读写），用于「导出/备份」。
func (s *Store) Snapshot(dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	_ = os.Remove(dst)
	q := "VACUUM INTO '" + strings.ReplaceAll(dst, "'", "''") + "'"
	_, err := s.db.Exec(q)
	return err
}

// migrate 按 user_version 递进执行内嵌迁移脚本。
func migrate(db *sql.DB) error {
	var cur int
	if err := db.QueryRow("PRAGMA user_version").Scan(&cur); err != nil {
		return fmt.Errorf("读取数据库版本失败: %w", err)
	}
	ents, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		v := versionOf(name)
		if v <= cur {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("迁移 %s 失败: %w", name, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", v)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("迁移 %s 写入版本失败: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("迁移 %s 提交失败: %w", name, err)
		}
	}
	return nil
}

// versionOf 从 "001_init.sql" 取 1。
func versionOf(name string) int {
	i := strings.IndexByte(name, '_')
	if i <= 0 {
		return 0
	}
	n, err := strconv.Atoi(name[:i])
	if err != nil {
		return 0
	}
	return n
}
