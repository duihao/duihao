package deploy

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeZip 造一个「单层顶层目录 + 两个文件」的包，模拟官方 zip 形态。
func makeZip(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pkg.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	files := map[string]string{
		"duihaocms/index.php":  "<?php echo 'hi';",
		"duihaocms/install.php": "<?php // installer deploy-prefill",
		"duihaocms/data/.keep":  "",
	}
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = dir
	return p
}

func TestDeployStripsSingleRootAndWritesPrefill(t *testing.T) {
	root := filepath.Join(t.TempDir(), "site")
	pkg := makeZip(t, "")

	var phases []string
	res, err := Deploy(context.Background(), Options{
		AppID: "duihaocms",
		Root:  root,
		SiteName: "对号测试站",
		BaseURL:  "http://localhost:8080",
		LocalZip: pkg,
		DB: &Target{Host: "127.0.0.1", Port: 3310, Database: "duihao_cms", User: "duihao_cms", Password: "p'w\\d"},
		OnStep: func(s Step) { phases = append(phases, s.Phase) },
	})
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}

	// 单层顶层目录被剥掉 → 文件直接落在站点根
	if _, err := os.Stat(filepath.Join(root, "index.php")); err != nil {
		t.Fatalf("index.php 未落在站点根: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "duihaocms")); err == nil {
		t.Fatal("顶层目录应被剥离")
	}

	// 预填文件内容
	pf, err := os.ReadFile(filepath.Join(root, "data", "deploy-prefill.php"))
	if err != nil {
		t.Fatalf("预填文件缺失: %v", err)
	}
	for _, want := range []string{"'db_port' => '3310'", "'db_name' => 'duihao_cms'", "'site_name' => '对号测试站'"} {
		if !strings.Contains(string(pf), want) {
			t.Errorf("预填文件缺少 %q\n%s", want, pf)
		}
	}
	// 单引号/反斜杠必须转义，否则 PHP 语法坏掉
	if !strings.Contains(string(pf), `p\'w\\d`) {
		t.Errorf("密码未正确转义:\n%s", pf)
	}

	if res.SetupURL != "http://localhost:8080/install.php" {
		t.Errorf("SetupURL = %q", res.SetupURL)
	}
	if !res.PrefillActive {
		t.Error("install.php 含 deploy-prefill → 应判定为支持预填")
	}
	if res.Files != 3 {
		t.Errorf("Files = %d, want 3", res.Files)
	}
	if len(phases) == 0 || phases[len(phases)-1] != "done" {
		t.Errorf("进度阶段异常: %v", phases)
	}

	m, ok := ReadMarker(root)
	if !ok || m.AppID != "duihaocms" {
		t.Fatalf("部署标记缺失或错误: %+v", m)
	}
}

func TestDeployRefusesNonEmptyRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := makeZip(t, "")
	_, err := Deploy(context.Background(), Options{AppID: "duihaocms", Root: root, LocalZip: pkg})
	if err == nil || !strings.Contains(err.Error(), "已有") {
		t.Fatalf("非空目录应被拒绝，实际: %v", err)
	}
}

func TestDeployForceOverwritesButKeepsOtherFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := makeZip(t, "")
	if _, err := Deploy(context.Background(), Options{AppID: "duihaocms", Root: root, LocalZip: pkg, Force: true}); err != nil {
		t.Fatalf("force 部署应成功: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "keep.txt")); err != nil {
		t.Fatal("用户已有文件被删了")
	}
	if _, err := os.Stat(filepath.Join(root, "index.php")); err != nil {
		t.Fatal("包内文件未写入")
	}
}

func TestWriteConfigFileFillsDefinesAndSalts(t *testing.T) {
	root := t.TempDir()
	sample := `<?php
define( 'DB_NAME', 'database_name_here' );
define( 'DB_USER', 'username_here' );
define( 'DB_PASSWORD', 'password_here' );
define( 'DB_HOST', 'localhost' );
define( 'AUTH_KEY',         'put your unique phrase here' );
define( 'NONCE_SALT',       'put your unique phrase here' );
define( 'WP_DEBUG', false );
`
	if err := os.WriteFile(filepath.Join(root, "wp-config-sample.php"), []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	cf := ConfigFile{
		From: "wp-config-sample.php", To: "wp-config.php",
		Defines: []string{"DB_NAME", "DB_USER", "DB_PASSWORD", "DB_HOST"}, Salts: true,
	}
	if err := writeConfigFile(root, cf, Options{DB: &Target{Host: "127.0.0.1", Port: 3307, Database: "wp", User: "wpuser", Password: "s3cret"}}); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(root, "wp-config.php"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"'wp'", "'wpuser'", "'s3cret'", "'127.0.0.1:3307'"} {
		if !strings.Contains(s, want) {
			t.Errorf("缺少 %s\n%s", want, s)
		}
	}
	if strings.Contains(s, "put your unique phrase here") {
		t.Error("盐值未替换")
	}
	if !strings.Contains(s, "define( 'WP_DEBUG', false )") {
		t.Error("未声明的 define 被误改")
	}
	// 重复部署不覆盖已有配置
	if err := os.WriteFile(filepath.Join(root, "wp-config.php"), []byte("MINE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeConfigFile(root, cf, Options{}); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(filepath.Join(root, "wp-config.php"))
	if string(again) != "MINE" {
		t.Error("已存在的配置文件被覆盖了")
	}
}

func TestCatalogLoads(t *testing.T) {
	apps := Catalog()
	if len(apps) < 2 {
		t.Fatalf("程序模板太少: %d", len(apps))
	}
	seen := map[string]bool{}
	for _, a := range apps {
		if a.ID == "" || a.Install.Entry == "" {
			t.Errorf("模板字段缺失: %+v", a)
		}
		if seen[a.ID] {
			t.Errorf("重复 id: %s", a.ID)
		}
		seen[a.ID] = true
	}
	if _, err := Find("duihaocms"); err != nil {
		t.Error(err)
	}
	if _, err := Find("nope"); err == nil {
		t.Error("未知 id 应报错")
	}
}

// 内置包不一定在清单里写死摘要（重打包就会失效），改读包旁边的 .sha256。
func TestBundledSourceReadsSidecarDigest(t *testing.T) {
	dataRoot := t.TempDir()
	t.Setenv("DUIHAO_DATA_ROOT", dataRoot)
	t.Setenv("DUIHAO_PAYLOAD_DIR", filepath.Join(dataRoot, "payload-none"))

	pkgDir := filepath.Join(dataRoot, "deploy", "pkgs")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(pkgDir, "duihaocms-v1.0.0.zip")
	if err := os.WriteFile(pkg, []byte("not-a-real-zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	const sum = "0aaddc29818d405e9ce0fad91083a6100abef088675609bebc743bec36fff9c3"
	if err := os.WriteFile(pkg+".sha256", []byte(sum+"  duihaocms-v1.0.0.zip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app, err := Find("duihaocms")
	if err != nil {
		t.Fatal(err)
	}
	got, desc, declared, cleanup, err := resolveSource(context.Background(), app, Options{}, func(string, int, string) {})
	if err != nil {
		t.Fatalf("resolveSource: %v", err)
	}
	if cleanup != nil {
		cleanup()
	}
	if got != pkg {
		t.Errorf("应命中内置包 %s，实际 %s", pkg, got)
	}
	if !strings.Contains(desc, "内置包") {
		t.Errorf("来源描述不对: %s", desc)
	}
	if declared != sum {
		t.Errorf("应读取 sidecar 摘要 %s，实际 %q", sum, declared)
	}

	// 摘要不匹配时必须中止部署，而不是带着坏包去解压。
	if _, err := Deploy(context.Background(), Options{
		AppID: "duihaocms",
		Root:  filepath.Join(t.TempDir(), "site"),
	}); err == nil || !strings.Contains(err.Error(), "校验失败") {
		t.Errorf("摘要不符应报校验失败，实际: %v", err)
	}
}

func TestSidecarSHARejectsGarbage(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "a.zip")
	if err := os.WriteFile(pkg, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := sidecarSHA(pkg); got != "" {
		t.Errorf("没有 sidecar 应返回空串，实际 %q", got)
	}
	_ = os.WriteFile(pkg+".sha256", []byte("deadbeef  a.zip\n"), 0o644)
	if got := sidecarSHA(pkg); got != "" {
		t.Errorf("长度不对的摘要应被拒绝，实际 %q", got)
	}
	_ = os.WriteFile(pkg+".sha256", []byte("ZZZ"+strings.Repeat("0", 61)+"  a.zip\n"), 0o644)
	if got := sidecarSHA(pkg); got != "" {
		t.Errorf("非十六进制摘要应被拒绝，实际 %q", got)
	}
}
