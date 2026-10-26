package site

import (
	"fmt"
	"os"
)

// ReissueTLSCerts 用当前根 CA 重签全部 https 站点证书，并重载 nginx。
//
// 用在「刚装完根证书」这一下：老站点的证书是自签名的（签发者不是 CA），
// 装完根证书也不会变绿，必须整体重签。返回重签成功的站点数。
func (m *Manager) ReissueTLSCerts() (int, error) {
	if m == nil {
		return 0, fmt.Errorf("站点管理尚未就绪")
	}
	var firstErr error
	n := 0
	for _, rec := range m.reg.List() {
		if !rec.TLS() || rec.Web == "" {
			continue
		}
		instDir := m.inst.InstanceDirPath(rec.Web)
		cert, key := CertPaths(instDir, rec.ID)
		_ = os.Remove(cert)
		_ = os.Remove(key)
		if _, _, err := ensureCert(instDir, rec.ID, rec.Domains); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		// 证书路径不变，但 nginx 在 worker 启动时就把证书读进内存了，必须 reload。
		if err := m.Rebuild(rec.ID); err != nil && firstErr == nil {
			firstErr = err
		}
		n++
	}
	if m.auditLog != nil {
		m.auditLog.Ok("ui", "site.reissue-certs", "all", map[string]any{"sites": n})
	}
	return n, firstErr
}
