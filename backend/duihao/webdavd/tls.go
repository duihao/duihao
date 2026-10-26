// WebDAV 自签证书与磁盘水位（v3.1 §6.2：开局域网必须同时开 TLS）。
//
// 证书：ECDSA P-256 自签，10 年有效；落 instances/<id>/certs/webdav-*.pem，
// 已存在且能加载就复用（不比对 SAN——本机 IP 变化属边缘场景，删证书重启即重签）。
// SAN 覆盖 127.0.0.1 / ::1 / localhost / 本机所有非回环 IPv4（局域网挂载用）。
package webdavd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

type tlsConfigBundle struct {
	cert tls.Certificate
}

func (b *tlsConfigBundle) tlsConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{b.cert},
		MinVersion:   tls.VersionTLS12,
	}
}

// certPaths 证书与私钥落点。
func certPaths(certDir string) (cert, key string) {
	return filepath.Join(certDir, "webdav-cert.pem"), filepath.Join(certDir, "webdav-key.pem")
}

// ensureSelfSigned 取自签证书：有就用，没有就生成。
func ensureSelfSigned(certDir, commonName string) (*tlsConfigBundle, error) {
	certPath, keyPath := certPaths(certDir)
	if cert, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		return &tlsConfigBundle{cert: cert}, nil
	}

	certPEM, keyPEM, err := generateSelfSigned(commonName)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(certDir, 0o755); err != nil {
		return nil, err
	}
	// Windows 上 os.WriteFile 的 perm 不生效（工程已知事实），
	// 私钥保护靠 AppData 目录 ACL，与 site/tls.go 同一事实。
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &tlsConfigBundle{cert: cert}, nil
}

// generateSelfSigned 生成自签证书（PEM 对）。
func generateSelfSigned(commonName string) (certPEM, keyPEM []byte, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}

	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	if addrs, aerr := net.InterfaceAddrs(); aerr == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
				ips = append(ips, ipn.IP)
			}
		}
	}

	tmpl := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"Duihao"}},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           ips,
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func randomSerial() (s *big.Int) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return new(big.Int).SetBytes(b)
}

// diskFreeMB 目录所在盘剩余空间（MB）。失败返回 -1（调用方跳过水位告警）。
func diskFreeMB(dir string) (int64, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return -1, err
	}
	root := filepath.VolumeName(abs) + `\`
	var free, total, avail uint64
	if err := windows.GetDiskFreeSpaceEx(
		windows.StringToUTF16Ptr(root), &avail, &total, &free); err != nil {
		return -1, err
	}
	return int64(free) / 1024 / 1024, nil
}
