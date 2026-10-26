// m11driver：运行时管理面（M11）端到端验收。
//
// 三条链路都用真东西：
//   FTP  → 真实 ftp.ServerConn 登录/上传/只读拒绝/改密码/停用
//   MySQL→ 真实 mysqld（initialize-insecure 起新实例）+ go-sql-driver 连接建表
//   Redis→ 真实 redis-server + go-redis 写键扫描备份
//
// MySQL 管理面有两条硬规则也在验收内：
//   1) root 未设置密码前，一切管理操作被拒绝（必须先设密码）
//   2) 删库连带清理孤儿账号时，root 永远不会被删
//
// 跑法：go run ./backend/duihao/internal/m11driver
// 会在数据根下新建 m11-* 实例，结束时删除（数据目录一并清理，避免留垃圾）。
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jlaffaye/ftp"
	"github.com/redis/go-redis/v9"

	duihao "github.com/ys-ll/uniterm/backend/duihao"
	"github.com/ys-ll/uniterm/backend/duihao/audit"
	"github.com/ys-ll/uniterm/backend/duihao/dbadmin"
	"github.com/ys-ll/uniterm/backend/duihao/ftpd"
	"github.com/ys-ll/uniterm/backend/duihao/instance"
	"github.com/ys-ll/uniterm/backend/duihao/service"
)

var fails int

func ck(name string, ok bool, extra ...any) {
	if ok {
		fmt.Printf("  [PASS] %s\n", name)
		return
	}
	fails++
	fmt.Printf("  [FAIL] %s %v\n", name, extra)
}

func main() {
	fmt.Println("数据根:", duihao.DataRoot())
	if err := duihao.EnsureDirs(); err != nil {
		fmt.Println("ENSURE-ERR:", err)
		os.Exit(1)
	}
	settings, err := duihao.LoadSettings()
	if err != nil {
		settings = duihao.DefaultSettings()
	}
	auditLog := audit.New(duihao.DirLogs())
	mgr, err := instance.NewManager(settings, auditLog)
	if err != nil {
		fmt.Println("MGR-ERR:", err)
		os.Exit(2)
	}
	mgr.RegisterEmbedded("ftpserver", func(cfg instance.EmbeddedConfig) (instance.EmbeddedServer, error) {
		return ftpd.FromValues(cfg.Dir, cfg.LogPath, cfg.Ports, cfg.Values)
	})

	ftpSvc := service.NewFTPService(mgr, auditLog)
	mysqlSvc := service.NewMySQLService(mgr, auditLog)
	redisSvc := service.NewRedisService(mgr, auditLog)

	var created []string
	defer func() {
		for _, id := range created {
			_ = mgr.Stop(id)
			_ = mgr.Remove(id)
		}
	}()

	runFTP(mgr, ftpSvc, &created)
	runMySQL(mgr, mysqlSvc, &created)
	runRedis(mgr, redisSvc, &created)

	fmt.Println()
	if fails == 0 {
		fmt.Println("M11 全部通过")
		return
	}
	fmt.Printf("M11 有 %d 项失败\n", fails)
	os.Exit(1)
}

// dsn 用 driver 的 Config 拼 DSN（密码里的 # : @ 会被正确转义）。
func dsn(user, pass string, port int, db string) string {
	c := mysql.NewConfig()
	c.User = user
	c.Passwd = pass
	c.Net = "tcp"
	c.Addr = fmt.Sprintf("127.0.0.1:%d", port)
	c.DBName = db
	c.Timeout = 5 * time.Second
	c.AllowNativePasswords = true
	return c.FormatDSN()
}

// ─── FTP 多账号 ───

func dial(port int, user, pass string) (*ftp.ServerConn, error) {
	c, err := ftp.Dial(fmt.Sprintf("127.0.0.1:%d", port),
		ftp.DialWithTimeout(5*time.Second), ftp.DialWithDisabledEPSV(true))
	if err != nil {
		return nil, err
	}
	if err := c.Login(user, pass); err != nil {
		_ = c.Quit()
		return nil, err
	}
	return c, nil
}

func runFTP(mgr *instance.Manager, svc *service.FTPService, created *[]string) {
	fmt.Println("\n== A) FTP 多账号")
	rec, err := mgr.Create("ftpserver", "", "", nil)
	if err != nil {
		fmt.Println("CREATE-ERR:", err)
		fails++
		return
	}
	*created = append(*created, rec.ID)
	instID, port := rec.ID, rec.Ports["ftp"]
	dir := mgr.InstanceDirPath(instID)

	if err := mgr.Start(instID); err != nil {
		fmt.Println("START-ERR:", err)
		fails++
		return
	}
	ck("FTP 实例已启动", mgr.Status(instID) == "running", mgr.Status(instID))

	// 旧版默认账号仍可登录（账号表为空时回退设置）
	c, err := dial(port, "duihao", "duihao")
	ck("账号表为空时回退设置里的默认账号", err == nil, err)
	if err == nil {
		_ = c.Quit()
	}

	// 1) 两个账号，各自根目录
	if err := svc.CreateFTPAccount(instID, "alice", "alice123", "{dir}/alice", false, "测试账号A"); err != nil {
		ck("创建账号 alice", false, err)
		return
	}
	ck("创建账号 alice", true)
	if err := svc.CreateFTPAccount(instID, "bob", "bob123", "{dir}/bob", true, "只读账号"); err != nil {
		ck("创建账号 bob（只读）", false, err)
		return
	}
	ck("创建账号 bob（只读）", true)

	list, err := svc.ListFTPAccounts(instID)
	ck("账号列表 2 条（不再回退默认账号）", err == nil && len(list) == 2, len(list), err)

	// alice 上传成功，且落到自己的根目录
	ca, err := dial(port, "alice", "alice123")
	ck("alice 登录成功", err == nil, err)
	if err == nil {
		data := "hello from alice"
		err = ca.Stor("alice.txt", strings.NewReader(data))
		ck("alice 上传成功", err == nil, err)
		_ = ca.Quit()
		got, rerr := os.ReadFile(filepath.Join(dir, "alice", "alice.txt"))
		ck("文件落在 alice 自己的根目录", rerr == nil && string(got) == data, string(got), rerr)
	}

	// bob 只读：能登录、能列目录、不能上传
	cb, err := dial(port, "bob", "bob123")
	ck("bob 登录成功", err == nil, err)
	if err == nil {
		_, lerr := cb.List("/")
		ck("bob 可以列目录", lerr == nil, lerr)
		uerr := cb.Stor("bob.txt", strings.NewReader("nope"))
		ck("bob 上传被拒绝（只读）", uerr != nil, uerr)
		_ = cb.Quit()
	}

	// 交叉验证：密码不串号
	if _, err := dial(port, "bob", "alice123"); err == nil {
		ck("bob 用 alice 的密码不能登录", false)
	} else {
		ck("bob 用 alice 的密码不能登录", true)
	}

	// 2) 改密码
	if err := svc.SetFTPAccountPassword(instID, list[0].ID, "alice456"); err != nil {
		ck("改 alice 密码", false, err)
	} else {
		ck("改 alice 密码", true)
		_, oldErr := dial(port, "alice", "alice123")
		ck("旧密码失效", oldErr != nil, oldErr)
		if cn, err := dial(port, "alice", "alice456"); err == nil {
			ck("新密码可登录", true)
			_ = cn.Quit()
		} else {
			ck("新密码可登录", false, err)
		}
	}

	// 3) 停用 bob
	if err := svc.UpdateFTPAccount(instID, list[1].ID, "bob", "{dir}/bob", "停用测试", true, false); err != nil {
		ck("停用 bob", false, err)
	} else {
		_, err := dial(port, "bob", "bob123")
		ck("停用后 bob 无法登录", err != nil, err)
	}

	// 4) 删除 bob
	if err := svc.DeleteFTPAccount(instID, list[1].ID); err != nil {
		ck("删除 bob", false, err)
	} else {
		l2, _ := svc.ListFTPAccounts(instID)
		ck("删除后只剩 1 个账号", len(l2) == 1, len(l2))
	}
}

// ─── MySQL ───

func runMySQL(mgr *instance.Manager, svc *service.MySQLService, created *[]string) {
	fmt.Println("\n== B) MySQL 数据库管理")
	rec, err := mgr.Create("mysql", "8.0.40", "", nil)
	if err != nil {
		fmt.Println("CREATE-ERR:", err)
		fails++
		return
	}
	*created = append(*created, rec.ID)
	instID, port := rec.ID, rec.Ports["db"]

	initSQL := filepath.Join(mgr.InstanceDirPath(instID), "init-admin.sql")
	_, statErr := os.Stat(initSQL)
	ck("安装动作生成了 init-admin.sql", statErr == nil, statErr)

	if err := mgr.Start(instID); err != nil {
		fmt.Println("START-ERR:", err)
		fails++
		return
	}
	ck("MySQL 实例已启动", mgr.Status(instID) == "running", mgr.Status(instID))

	// ── 硬规则 1：root 未设置密码前，管理面全部拒绝 ──
	st := svc.MySQLStatus(instID)
	ck("管理连接成功（root@127.0.0.1，初始空密码）", st.Connected, st.Error)
	ck("状态标记 rootPasswordSet=false", !st.RootPasswordSet, st.RootPasswordSet)
	if _, err := svc.ListMySQLDatabases(instID); err != nil {
		ck("未设密码时列库被拒绝", strings.Contains(err.Error(), "请先设置 root 密码"), err)
	} else {
		ck("未设密码时列库被拒绝", false)
	}
	if err := svc.CreateMySQLDatabase(instID, service.CreateDatabaseOptions{
		Name: "nope", Password: "x",
	}); err != nil {
		ck("未设密码时建库被拒绝", strings.Contains(err.Error(), "请先设置 root 密码"), err)
	} else {
		ck("未设密码时建库被拒绝", false)
	}

	// 设置 root 密码后解锁
	if err := svc.SetMySQLRootPassword(instID, "Root#789"); err != nil {
		ck("设置 root 密码", false, err)
		return
	}
	ck("设置 root 密码", true)
	st2 := svc.MySQLStatus(instID)
	ck("设置后 rootPasswordSet=true 且连接正常", st2.RootPasswordSet && st2.Connected, st2.RootPasswordSet, st2.Error)

	dbs, err := svc.ListMySQLDatabases(instID)
	ck("设密码后列库正常（系统库已过滤）", err == nil && len(dbs) == 0, len(dbs), err)

	// 建库
	err = svc.CreateMySQLDatabase(instID, service.CreateDatabaseOptions{
		Name: "m11_probe", User: "m11_probe", Password: "Probe#123", Charset: "utf8mb4",
	})
	ck("创建数据库 m11_probe", err == nil, err)

	dbs, err = svc.ListMySQLDatabases(instID)
	found := false
	for _, d := range dbs {
		if d.Name == "m11_probe" {
			found = true
			ck("列表带出授权用户", len(d.Users) > 0 && d.Users[0] == "m11_probe", d.Users)
		}
	}
	ck("列表包含新库", found)

	// 用新账号连接建表写数据（验证授权链路）
	udb, err := sql.Open("mysql", dsn("m11_probe", "Probe#123", port, "m11_probe"))
	ck("业务账号可连接", err == nil, err)
	if err == nil {
		defer udb.Close()
		_, err = udb.Exec("CREATE TABLE t (id INT PRIMARY KEY, name VARCHAR(32))")
		ck("业务账号可建表（ALL 权限）", err == nil, err)
		_, err = udb.Exec("INSERT INTO t VALUES (1,'a')")
		ck("业务账号可写入", err == nil, err)
	}

	// 收紧到只读
	if err := svc.SetMySQLPrivileges(instID, "m11_probe", "m11_probe", []string{"SELECT"}); err != nil {
		ck("设置只读权限", false, err)
	} else {
		ck("设置只读权限", true)
		udb2, err := sql.Open("mysql", dsn("m11_probe", "Probe#123", port, "m11_probe"))
		if err == nil {
			_, ierr := udb2.Exec("INSERT INTO t VALUES (2,'b')")
			ck("只读后写入被拒绝(1142)", ierr != nil && strings.Contains(ierr.Error(), "1142"), ierr)
			udb2.Close()
		}
	}
	privs, err := svc.GetMySQLPrivileges(instID, "m11_probe", "m11_probe")
	ck("权限读回去重后为 SELECT", err == nil && len(privs) == 1 && privs[0] == "SELECT", privs, err)

	// 改密码
	if err := svc.SetMySQLDatabasePassword(instID, "m11_probe", "m11_probe", "Probe#456"); err != nil {
		ck("改业务账号密码", false, err)
	} else {
		ck("改业务账号密码", true)
		if d, err := sql.Open("mysql", dsn("m11_probe", "Probe#123", port, "m11_probe")); err == nil {
			ck("旧密码失效", d.Ping() != nil)
			d.Close()
		}
		if d, err := sql.Open("mysql", dsn("m11_probe", "Probe#456", port, "m11_probe")); err == nil {
			ck("新密码可连接", d.Ping() == nil)
			d.Close()
		}
	}

	// 备份
	bk, err := svc.BackupMySQLDatabase(instID, "m11_probe")
	ck("备份成功", err == nil && bk.Size > 0, err, bk)
	if err == nil {
		body, _ := os.ReadFile(bk.Path)
		ck("备份内容含建表语句", strings.Contains(string(body), "CREATE TABLE"), len(body))
		ck("备份落在 backup/mysql/<实例>/<库>", strings.Contains(filepath.ToSlash(bk.Path), "/backup/mysql/"), bk.Path)
	}

	// 删表后还原
	if err == nil {
		if ad, aerr := sql.Open("mysql", dsn("root", "Root#789", port, "m11_probe")); aerr == nil {
			_, _ = ad.Exec("DROP TABLE t")
			ad.Close()
		}
		rerr := svc.RestoreMySQLBackup(instID, "m11_probe", bk.Path)
		ck("还原成功", rerr == nil, rerr)
		if chk, cerr := sql.Open("mysql", dsn("root", "Root#789", port, "m11_probe")); cerr == nil {
			var n int
			qerr := chk.QueryRow("SELECT COUNT(*) FROM t").Scan(&n)
			ck("还原后数据回来了", qerr == nil && n >= 1, n, qerr)
			chk.Close()
		}
	}

	// 备份列表 / 删除
	list, err := svc.ListMySQLBackups(instID)
	ck("备份列表可读", err == nil && len(list) == 1, len(list), err)
	if err == nil && len(list) == 1 {
		if err := svc.DeleteMySQLBackup(instID, list[0].Path); err != nil {
			ck("删除备份", false, err)
		} else {
			_, serr := os.Stat(list[0].Path)
			ck("删除备份", os.IsNotExist(serr))
		}
	}

	// 越界路径必须拒绝
	if err := svc.DeleteMySQLBackup(instID, "C:/Windows/win.ini"); err == nil {
		ck("拒绝删除备份目录外的文件", false)
	} else {
		ck("拒绝删除备份目录外的文件", true)
	}

	// ── 硬规则 2：删库连带清理孤儿账号时，root 永远不会被删 ──
	// 把 root 也授权到 m11_probe，让它成为该库的 grantee，然后 dropUser=true 删库。
	if ad, aerr := sql.Open("mysql", dsn("root", "Root#789", port, "")); aerr == nil {
		_, _ = ad.Exec("GRANT ALL PRIVILEGES ON `m11_probe`.* TO 'root'@'127.0.0.1'")
		ad.Close()
	}
	if err := svc.DropMySQLDatabase(instID, "m11_probe", true); err != nil {
		ck("删除数据库", false, err)
	} else {
		ck("删除数据库", true)
		dbs, _ = svc.ListMySQLDatabases(instID)
		ck("列表已空", len(dbs) == 0, len(dbs))
	}
	if chk, cerr := sql.Open("mysql", dsn("root", "Root#789", port, "")); cerr == nil {
		var n int
		qerr := chk.QueryRow("SELECT COUNT(*) FROM mysql.user WHERE User='root' AND Host='127.0.0.1'").Scan(&n)
		ck("root 账号不会被连带删除", qerr == nil && n == 1, n, qerr)
		chk.Close()
	}

	// 再次修改 root 密码（用户要求：root 可以改密码）
	if err := svc.SetMySQLRootPassword(instID, "Root#999"); err != nil {
		ck("再次修改 root 密码", false, err)
	} else {
		ck("再次修改 root 密码", true)
		cred := dbadmin.LoadCredential(mgr.InstanceDirPath(instID))
		ck("凭证文件已同步", cred.Password == "Root#999", cred.User)
		st3 := svc.MySQLStatus(instID)
		ck("改密后管理连接仍可用", st3.Connected && st3.RootPasswordSet, st3.Error)
	}
}

// ─── Redis ───

func runRedis(mgr *instance.Manager, svc *service.RedisService, created *[]string) {
	fmt.Println("\n== C) Redis 键管理")
	rec, err := mgr.Create("redis", "5.0.14.1", "", nil)
	if err != nil {
		fmt.Println("CREATE-ERR:", err)
		fails++
		return
	}
	*created = append(*created, rec.ID)
	instID, port := rec.ID, rec.Ports["redis"]
	if err := mgr.Start(instID); err != nil {
		fmt.Println("START-ERR:", err)
		fails++
		return
	}
	ck("Redis 实例已启动", mgr.Status(instID) == "running", mgr.Status(instID))

	// 直接写几个键（模拟业务数据）
	rdb := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("127.0.0.1:%d", port)})
	ctx := context.Background()
	_ = rdb.Set(ctx, "m11:str", "hello 对号", 0).Err()
	_ = rdb.HSet(ctx, "m11:hash", "a", "1", "b", "2").Err()
	_ = rdb.RPush(ctx, "m11:list", "x", "y", "z").Err()
	_ = rdb.Set(ctx, "m11:volatile", "bye", 60*time.Second).Err()
	_ = rdb.Close()

	ov, err := svc.RedisOverview(instID)
	ck("概览可读", err == nil, err)
	if err == nil {
		ck("版本 5.0.x", strings.HasPrefix(ov.Version, "5.0"), ov.Version)
		ck("键数统计正确", ov.TotalKeys >= 4, ov.TotalKeys)
		ck("逻辑库数量 16", ov.Databases == 16, ov.Databases)
	}

	sc, err := svc.RedisScanKeys(instID, 0, 0, "m11:*", 100)
	ck("SCAN 前缀匹配", err == nil && len(sc.Keys) == 4, len(sc.Keys), err)
	types := map[string]string{}
	for _, k := range sc.Keys {
		types[k.Key] = k.Type
	}
	ck("类型识别正确", types["m11:str"] == "string" && types["m11:hash"] == "hash" && types["m11:list"] == "list", types)

	// TTL 与预览
	for _, k := range sc.Keys {
		if k.Key == "m11:volatile" {
			ck("TTL 已设置（>0）", k.TTL > 0, k.TTL)
		}
		if k.Key == "m11:str" {
			ck("字符串带预览", strings.Contains(k.Preview, "对号"), k.Preview)
		}
	}

	kd, err := svc.RedisGetKey(instID, 0, "m11:hash")
	ck("哈希详情可读", err == nil && len(kd.Members) == 2, err, kd)

	if err := svc.RedisDeleteKey(instID, 0, "m11:str"); err != nil {
		ck("删除键", false, err)
	} else {
		if _, err := svc.RedisGetKey(instID, 0, "m11:str"); err != nil {
			ck("删除键", true)
		} else {
			ck("删除键", false)
		}
	}

	bk, err := svc.RedisBackup(instID)
	ck("快照备份成功", err == nil && bk.Size > 0, err, bk)
	if err == nil {
		ck("rdb 落在 backup/redis/<实例>", strings.Contains(filepath.ToSlash(bk.Path), "/backup/redis/"), bk.Path)
	}
	list, err := svc.ListRedisBackups(instID)
	ck("快照列表可读", err == nil && len(list) == 1, len(list), err)

	if err := svc.RedisFlushDB(instID, 0); err != nil {
		ck("清空 db0", false, err)
	} else {
		ck("清空 db0", true)
		ov2, oerr := svc.RedisOverview(instID)
		ck("清空 db0 后键数归零", oerr == nil && ov2 != nil && ov2.TotalKeys == 0, oerr, ov2.TotalKeys)
	}

	if err == nil && len(list) == 1 {
		if err := svc.DeleteRedisBackup(instID, list[0].Path); err != nil {
			ck("删除快照", false, err)
		} else {
			ck("删除快照", true)
		}
	}
}
