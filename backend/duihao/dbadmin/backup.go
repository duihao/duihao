// 逻辑备份：mysqldump 导出 / mysql 客户端还原与导入。
//
// 为什么不用 Go 侧逐表 SELECT 拼 SQL：mysqldump 处理字符集、二进制列、视图、
// 存储过程、外键顺序都已经是成熟行为，重写一遍只会引入差异。代价是要有
// mysqldump.exe（MySQL 官方 zip 自带，运行时 bin 目录里就有）。
package dbadmin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Backup 一份备份文件。
type Backup struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Database  string `json:"database"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"createdAt"`
}

// Backup 执行一次逻辑备份，返回生成的备份文件。
func (c *Client) BackupDatabase(ctx context.Context, db string) (*Backup, error) {
	if !identPattern.MatchString(db) {
		return nil, fmt.Errorf("数据库名非法: %s", db)
	}
	if err := c.checkTool("mysqldump.exe"); err != nil {
		return nil, err
	}
	dir := c.backupDir(db)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建备份目录失败: %w", err)
	}
	name := fmt.Sprintf("%s-%s.sql", db, time.Now().Format("20060102-150405"))
	out := filepath.Join(dir, name)

	defaults, cleanup, err := c.writeDefaults()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	args := []string{
		"--defaults-file=" + defaults,
		"--single-transaction", // InnoDB 一致性快照，备份期间不锁表
		"--quick",
		"--routines", // 存储过程/函数
		"--events",
		"--triggers",
		"--default-character-set=utf8mb4",
		"--set-gtid-purged=OFF", // 避免还原时被 GTID 限制
		"--databases", db,
		"--result-file=" + out,
	}
	cmd := exec.CommandContext(ctx, filepath.Join(c.p.BinDir, "mysqldump.exe"), args...)
	hideWindow(cmd)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(out)
		return nil, fmt.Errorf("mysqldump 失败: %w（%s）", err, strings.TrimSpace(stderr.String()))
	}
	st, err := os.Stat(out)
	if err != nil {
		return nil, fmt.Errorf("备份文件未生成: %w", err)
	}
	return &Backup{
		Name: name, Path: out, Database: db,
		Size:      st.Size(),
		CreatedAt: st.ModTime().Format(time.RFC3339),
	}, nil
}

// ListBackups 全部备份（跨库，按时间倒序）。
func (c *Client) ListBackups() ([]Backup, error) {
	base := c.instanceBackupRoot()
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return []Backup{}, nil
		}
		return nil, err
	}
	var out []Backup
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		db := e.Name()
		files, err := os.ReadDir(filepath.Join(base, db))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(strings.ToLower(f.Name()), ".sql") {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			out = append(out, Backup{
				Name: f.Name(), Path: filepath.Join(base, db, f.Name()), Database: db,
				Size: info.Size(), CreatedAt: info.ModTime().Format(time.RFC3339),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	if out == nil {
		out = []Backup{}
	}
	return out, nil
}

// DeleteBackup 删除一份备份（路径必须落在本实例的备份目录内）。
func (c *Client) DeleteBackup(path string) error {
	safe, err := c.safeBackupPath(path)
	if err != nil {
		return err
	}
	if err := os.Remove(safe); err != nil {
		return fmt.Errorf("删除备份失败: %w", err)
	}
	return nil
}

// RestoreBackup 用备份覆盖还原到同名库（备份里含 CREATE DATABASE 语句）。
func (c *Client) RestoreBackup(ctx context.Context, db, path string) error {
	if !identPattern.MatchString(db) {
		return fmt.Errorf("数据库名非法: %s", db)
	}
	safe, err := c.safeBackupPath(path)
	if err != nil {
		return err
	}
	return c.runSQLFile(ctx, safe, "")
}

// ImportSQL 把外部 .sql 导入指定库（不执行库级 CREATE DATABASE，目标库必须已存在）。
func (c *Client) ImportSQL(ctx context.Context, db, file string) error {
	if !identPattern.MatchString(db) {
		return fmt.Errorf("数据库名非法: %s", db)
	}
	if _, err := os.Stat(file); err != nil {
		return fmt.Errorf("SQL 文件不可读: %w", err)
	}
	return c.runSQLFile(ctx, file, db)
}

// runSQLFile 用 mysql.exe 客户端执行 SQL 文件。db 非空时指定默认库。
func (c *Client) runSQLFile(ctx context.Context, file, db string) error {
	if err := c.checkTool("mysql.exe"); err != nil {
		return err
	}
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("打开 SQL 文件失败: %w", err)
	}
	defer f.Close()

	defaults, cleanup, err := c.writeDefaults()
	if err != nil {
		return err
	}
	defer cleanup()

	args := []string{"--defaults-file=" + defaults, "--default-character-set=utf8mb4"}
	if db != "" {
		args = append(args, "--database="+db)
	}
	cmd := exec.CommandContext(ctx, filepath.Join(c.p.BinDir, "mysql.exe"), args...)
	hideWindow(cmd)
	cmd.Stdin = f
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("导入失败: %w（%s）", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// BackupDir 某库的备份目录（服务层「打开目录」用）。
func (c *Client) BackupDir(db string) string { return c.backupDir(db) }

func (c *Client) backupDir(db string) string {
	return filepath.Join(c.instanceBackupRoot(), db)
}

func (c *Client) instanceBackupRoot() string {
	return filepath.Join(c.p.BackupRoot, "mysql", c.p.InstanceID)
}

// safeBackupPath 限制只能操作本实例备份目录内的文件。
// 前端传来的路径不可信：删/还原都带路径参数，越界就能删用户任意文件。
func (c *Client) safeBackupPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("路径非法: %w", err)
	}
	root, err := filepath.Abs(c.instanceBackupRoot())
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("路径不在本实例的备份目录内: %s", p)
	}
	return abs, nil
}

// checkTool 确认运行时里带了这个客户端工具。
func (c *Client) checkTool(name string) error {
	p := filepath.Join(c.p.BinDir, name)
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("找不到 %s（应位于 %s）", name, c.p.BinDir)
	}
	return nil
}

// writeDefaults 写一份临时 [client] 凭证文件。
//
// 为什么不把 --password=xxx 放命令行：Windows 上任何进程都能读到别的进程的
// 命令行（任务管理器/WMI），root 密码会直接暴露；option file 只有本用户可读。
func (c *Client) writeDefaults() (string, func(), error) {
	if err := os.MkdirAll(c.p.InstDir, 0o755); err != nil {
		return "", func() {}, err
	}
	f, err := os.CreateTemp(c.p.InstDir, ".dbtool-*.cnf")
	if err != nil {
		return "", func() {}, fmt.Errorf("创建临时凭证文件失败: %w", err)
	}
	path := f.Name()
	cleanup := func() { _ = os.Remove(path) }

	user := c.p.Credential.User
	if user == "" {
		user = DefaultUser
	}
	content := fmt.Sprintf("[client]\nhost=127.0.0.1\nport=%d\nuser=%s\npassword=%s\ndefault-character-set=utf8mb4\n",
		c.p.Port, quoteCnf(user), quoteCnf(c.p.Credential.Password))
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	// 0600：CreateTemp 默认已是 0600，这里显式再说一次（跨平台语义清晰）。
	_ = os.Chmod(path, 0o600)
	return path, cleanup, nil
}

// quoteCnf option file 的值：含空格/#/; 时必须加引号，引号内的 \ 与 " 需转义。
func quoteCnf(v string) string {
	if v == "" {
		return `""`
	}
	needQuote := false
	for _, r := range v {
		if r == ' ' || r == '#' || r == ';' || r == '"' || r == '\'' || r == '\t' {
			needQuote = true
			break
		}
	}
	if !needQuote {
		return v
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(v) + `"`
}
