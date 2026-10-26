package runtimepkg

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// nilAudit 测试用空审计器。
type nilAudit struct{}

func (nilAudit) Ok(actor, action, target string, detail map[string]any)   {}
func (nilAudit) Fail(actor, action, target string, detail map[string]any) {}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// withDataRoot 在独立临时数据根中跑测试，避免污染真实 %LOCALAPPDATA%\Duihao。
func withDataRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DUIHAO_DATA_ROOT", dir)
	return dir
}

// makeZip 生成一个最小 zip（一个文件），返回字节与 SHA256。
func makeZip(t *testing.T, name, content string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256Hex(buf.Bytes())
	return buf.Bytes(), sum
}

func TestResolveDownload(t *testing.T) {
	withDataRoot(t)
	body, sum := makeZip(t, "bin/tool.exe", "hello")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	spec := VersionSpec{PluginID: "tool", Version: "1.0", Platform: "windows-amd64",
		Source: "url", URL: srv.URL, SHA256: sum}
	res, err := Resolve(context.Background(), spec, nilAudit{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Source != "download" {
		t.Fatalf("source = %s, want download", res.Source)
	}
	data, err := os.ReadFile(filepath.Join(res.Dir, "bin", "tool.exe"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("解压内容错误: %v %q", err, data)
	}
	// .source.json 已登记
	info, ok := readSourceInfo(res.Dir)
	if !ok || info.Source != "download" || info.SHA256 != sum {
		t.Fatalf(".source.json 错误: %+v ok=%v", info, ok)
	}

	// 第二次解析 → installed（同版本只存一份；Source 保留首次落盘来源）
	if _, ok := Installed("tool", "1.0"); !ok {
		t.Fatal("第二次解析应命中 installed")
	}
}

func TestResolveSHAMismatch(t *testing.T) {
	withDataRoot(t)
	body, _ := makeZip(t, "a.txt", "x")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	spec := VersionSpec{PluginID: "bad", Version: "1.0", Platform: "windows-amd64",
		Source: "url", URL: srv.URL, SHA256: "0000000000000000000000000000000000000000000000000000000000000000"}
	_, err := Resolve(context.Background(), spec, nilAudit{})
	if err == nil {
		t.Fatal("SHA 不匹配应报错")
	}
}

func TestResolveMirrorOnlyWithoutMirror(t *testing.T) {
	withDataRoot(t)
	spec := VersionSpec{PluginID: "offline", Version: "2.0", Platform: "windows-amd64",
		Source: "mirror-only"}
	_, err := Resolve(context.Background(), spec, nilAudit{})
	if err == nil {
		t.Fatal("mirror-only 无镜像应报错")
	}
	var noSrc *ErrNoSource
	if !errors.As(err, &noSrc) {
		t.Fatalf("应返回 ErrNoSource: %v", err)
	}
}

func TestResolveMirrorLevel(t *testing.T) {
	root := withDataRoot(t)
	body, sum := makeZip(t, "run.exe", "mirror-content")
	filesDir := filepath.Join(root, "incoming")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(filesDir, "mycli-1.1-win64.zip")
	if err := os.WriteFile(zipPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{"schemaVersion":1,"runtimes":[{"id":"mycli","version":"1.1","file":"mycli-1.1-win64.zip","sha256":"` + sum + `"}]}`
	if err := ImportMirror([]byte(manifest), filesDir); err != nil {
		t.Fatalf("ImportMirror: %v", err)
	}

	res, err := Resolve(context.Background(), VersionSpec{PluginID: "mycli", Version: "1.1",
		Platform: "windows-amd64", Source: "mirror-only"}, nilAudit{})
	if err != nil {
		t.Fatalf("Resolve mirror: %v", err)
	}
	if res.Source != "mirror" {
		t.Fatalf("source = %s, want mirror", res.Source)
	}
	data, _ := os.ReadFile(filepath.Join(res.Dir, "run.exe"))
	if string(data) != "mirror-content" {
		t.Fatalf("内容错误: %q", data)
	}
}

func TestResolveBundledLevel(t *testing.T) {
	// bundled 级读取 exe 同级 payload\；测试进程 exe 在 temp 下，
	// 通过在 exeDir 部署 payload 会污染 temp —— 这里直接测 findBundled 的
	// lock 解析与缺失检测，落盘路径覆盖由 m2/harness（testhost 便携目录）完成。
	root := withDataRoot(t)
	body, sum := makeZip(t, "x.txt", "y")
	payload := filepath.Join(root, "payload", "runtimes")
	if err := os.MkdirAll(payload, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "embedded-1.0-win64.zip"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	lock := `{"schemaVersion":1,"edition":"site-builder","runtimes":[{"id":"embedded","version":"1.0","platform":"windows-amd64","file":"runtimes/embedded-1.0-win64.zip","sha256":"` + sum + `"}]}`
	if err := os.MkdirAll(filepath.Join(root, "payload"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "payload", "bundle.lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DUIHAO_PAYLOAD_DIR", filepath.Join(root, "payload"))
	zp, lockPath := findBundled(VersionSpec{PluginID: "embedded", Version: "1.0", Platform: "windows-amd64"})
	if zp == "" || lockPath == "" {
		t.Fatalf("findBundled 未命中: %q %q", zp, lockPath)
	}
}

func TestUnzipRejectsZipSlip(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("../evil.txt")
	_, _ = f.Write([]byte("evil"))
	_ = w.Close()
	src := filepath.Join(t.TempDir(), "evil.zip")
	if err := os.WriteFile(src, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := unzip(src, dest); err == nil {
		t.Fatal("穿越条目应被拒绝")
	}
}
