// 站点 HTTPS：每个启用 https 的站点生成一份证书，**由对号本地根 CA 签发**
//（见 ca.go）。
//
// 为什么是「本地 CA 签发」而不是裸自签名：自签名证书浏览器的提示没有任何办法
// 消掉（它不在任何受信任根里）。由 Duihao Local CA 签发后，用户只要在最上面
// 装一次根证书（当前用户存储，免 UAC），此后所有站点首次访问就是绿锁。
// 没装根证书时行为与自签名一致——https 能跑，只是黄锁，不影响使用。
//
// 证书落在 web 实例目录（instances/<web>/certs/），不写站点根目录：
// 站点根是用户目录，对号不往里放自己的东西。
package site

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TLSNone 明文 http；TLSSelfSigned https。
const (
	TLSNone       = "none"
	TLSSelfSigned = "selfsigned"
)

// TLS 站点是否启用 https。
func (rec *Record) TLS() bool { return rec.TLSMode == TLSSelfSigned }

// CertDir web 实例下的证书目录。
func CertDir(instDir string) string { return filepath.Join(instDir, "certs") }

// CertPaths 站点证书与私钥路径（不保证存在）。
func CertPaths(instDir, siteID string) (cert, key string) {
	dir := CertDir(instDir)
	return filepath.Join(dir, siteID+".crt"), filepath.Join(dir, siteID+".key")
}

// ensureCert 生成（或按需重建）站点证书，返回证书/私钥的**绝对路径**。
//
// 复用条件：证书存在、未过期、SAN 覆盖当前全部域名、且签发者是当前根 CA。
// 任一不满足就重签——域名改了 SAN 不匹配浏览器会直接拒绝；根 CA 换过（用户
// 删了 config/ca）则旧证书链断了，也必须重签。
func ensureCert(instDir, siteID string, domains []string) (string, string, error) {
	certPath, keyPath := CertPaths(instDir, siteID)
	if ok := certCovers(certPath, domains); ok {
		if _, err := os.Stat(keyPath); err == nil {
			return certPath, keyPath, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o755); err != nil {
		return "", "", fmt.Errorf("创建证书目录失败: %w", err)
	}

	certPEM, keyPEM, err := signSiteCert(domains)
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return "", "", fmt.Errorf("写入证书失败: %w", err)
	}
	// 私钥 0600：同机其他账号不该读得到。
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return "", "", fmt.Errorf("写入私钥失败: %w", err)
	}
	return certPath, keyPath, nil
}

// certCovers 现有证书是否仍然可用（未过期 + 由当前根 CA 签发 + SAN 覆盖全部域名）。
func certCovers(certPath string, domains []string) bool {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	if time.Now().After(cert.NotAfter.Add(-24 * time.Hour)) {
		return false
	}
	// 旧版本签的是裸自签名证书：签发者不是本机 CA → 必须重签，否则装了根证书也
	// 不会变绿（链对不上）。
	if cert.Issuer.CommonName != CACN {
		return false
	}
	have := map[string]bool{}
	for _, d := range cert.DNSNames {
		have[strings.ToLower(d)] = true
	}
	want := make([]string, 0, len(domains))
	for _, d := range domains {
		want = append(want, strings.ToLower(d))
	}
	sort.Strings(want)
	for _, d := range want {
		if !have[d] {
			return false
		}
	}
	return true
}
