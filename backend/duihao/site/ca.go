// 本地根 CA（M21）：让站点的 https 从「自签名黄锁」变成「绿锁」。
//
// 做法：
//  1. 对号生成一份**自己的根证书**（Duihao Local CA，ECDSA P-256，20 年），
//     落在 config/ca/ 下，本机唯一。
//  2. 站点证书全部由这个根签发（见 tls.go），浏览器拿到的是「根 → 站点」两级链。
//  3. 用户点一次「安装本地证书」→ certutil -user -addstore Root（**当前用户**存储，
//     不需要管理员/UAC），此后所有由对号签发的站点头一次访问就是绿锁。
//
// 为什么不用 mkcert：还要下载第三方二进制、版本与平台矩阵都要跟；对号自己签
// 同样效果，且根证书由对号持有，卸载时能干净撤掉（certutil -delstore）。
//
// 注意：Firefox 用自带证书库，需要在 about:config 里另装（界面已提示）；
// 只影响 Firefox，Edge/Chrome/系统 WebView 都读 Windows 存储。
package site

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	duihao "github.com/duihao/duihao/backend/duihao"
)

// CACN 根证书的 CommonName / Organization（也是 certutil 查存储时的关键字）。
const (
	CACN = "Duihao Local CA"
	CAO  = "Duihao"
	// 站点证书有效期：825 天是 Apple 平台对 TLS 证书的上限，留足余量又不折腾。
	siteCertDays = 825
)

// CAPaths 根证书与私钥路径（不保证存在）。
func CAPaths() (cert, key string) {
	dir := filepath.Join(duihao.DirConfig(), "ca")
	return filepath.Join(dir, "duihao-ca.crt"), filepath.Join(dir, "duihao-ca.key")
}

var (
	caOnce sync.Mutex
)

// EnsureCA 加载或生成根 CA（并发安全，幂等）。
//
// 私钥与证书不配对（用户删了 key）时重新生成：站点证书会因签发者变化在下一次
// ensureCert 时自动重签，不需要人工清理。
func EnsureCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	caOnce.Lock()
	defer caOnce.Unlock()

	certPath, keyPath := CAPaths()
	certPEM, cerr := os.ReadFile(certPath)
	keyPEM, kerr := os.ReadFile(keyPath)
	if cerr == nil && kerr == nil {
		cert, key, err := parseCA(certPEM, keyPEM)
		if err == nil && time.Now().Before(cert.NotAfter.Add(-24*time.Hour)) {
			return cert, key, nil
		}
	}

	cert, key, certPEM, keyPEM, err := generateCA()
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, nil, fmt.Errorf("写入根证书失败: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, nil, fmt.Errorf("写入根私钥失败: %w", err)
	}
	return cert, key, nil
}

func parseCA(certPEM, keyPEM []byte) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cb, _ := pem.Decode(certPEM)
	if cb == nil {
		return nil, nil, fmt.Errorf("根证书不是 PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, nil, fmt.Errorf("根私钥不是 PEM")
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

func generateCA() (*x509.Certificate, *ecdsa.PrivateKey, []byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   CACN,
			Organization: []string{CAO},
			Country:      []string{"CN"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(20, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return cert, key, certPEM, keyPEM, nil
}

// CAThumbprint 根证书 SHA1 指纹（大写十六进制，certutil 用）。
func CAThumbprint() (string, error) {
	certPath, _ := CAPaths()
	data, err := os.ReadFile(certPath)
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", fmt.Errorf("根证书不是 PEM")
	}
	sum := sha1.Sum(block.Bytes)
	return strings.ToUpper(hex.EncodeToString(sum[:])), nil
}

// CAStatus 根 CA 当前状态（给界面展示）。
type CAStatus struct {
	Exists     bool   `json:"exists"`
	Installed  bool   `json:"installed"`
	Thumbprint string `json:"thumbprint,omitempty"`
	CertPath   string `json:"certPath"`
	Error      string `json:"error,omitempty"`
}

// LocalCAStatus 查根 CA 是否已生成、是否已装进当前用户的受信任根存储。
func LocalCAStatus() CAStatus {
	certPath, _ := CAPaths()
	st := CAStatus{CertPath: certPath}
	if _, err := os.Stat(certPath); err == nil {
		st.Exists = true
	}
	if _, _, err := EnsureCA(); err != nil {
		st.Error = err.Error()
		return st
	}
	st.Exists = true
	thumb, err := CAThumbprint()
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Thumbprint = thumb
	st.Installed = caInStore(thumb)
	return st
}

// InstallCA 把根证书装进**当前用户**的受信任根存储（无需 UAC）。
func InstallCA() error {
	if _, _, err := EnsureCA(); err != nil {
		return err
	}
	certPath, _ := CAPaths()
	if runtime.GOOS != "windows" {
		return fmt.Errorf("当前系统暂不支持一键安装根证书（%s）", runtime.GOOS)
	}
	out, err := runCertutil("-user", "-addstore", "-f", "Root", certPath)
	if err != nil {
		return fmt.Errorf("安装根证书失败: %v %s", err, strings.TrimSpace(out))
	}
	return nil
}

// UninstallCA 从当前用户存储里撤掉根证书（卸载/重置用）。
func UninstallCA() error {
	thumb, err := CAThumbprint()
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return fmt.Errorf("当前系统暂不支持")
	}
	out, err := runCertutil("-user", "-delstore", "Root", thumb)
	if err != nil {
		return fmt.Errorf("移除根证书失败: %v %s", err, strings.TrimSpace(out))
	}
	return nil
}

// caInStore 指纹查当前用户受信任根存储（certutil 退出码 0 = 命中）。
func caInStore(thumb string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	if _, err := runCertutil("-user", "-store", "Root", thumb); err != nil {
		return false
	}
	return true
}

func runCertutil(args ...string) (string, error) {
	cmd := exec.Command("certutil", args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// signSiteCert 用根 CA 签发一张站点证书（SAN 覆盖 localhost / 127.0.0.1 / 全部域名）。
func signSiteCert(domains []string) (certPEM, keyPEM []byte, err error) {
	caCert, caKey, err := EnsureCA()
	if err != nil {
		return nil, nil, err
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("生成私钥失败: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("生成序列号失败: %w", err)
	}
	cn := "localhost"
	if len(domains) > 0 {
		cn = domains[0]
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn, Organization: []string{CAO}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(0, 0, siteCertDays),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              append([]string{"localhost"}, domains...),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, caCert, &priv.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("签发证书失败: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("编码私钥失败: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}
