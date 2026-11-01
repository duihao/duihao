package licence

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
)

// ─────────────────────────────────────────────────────────────
//  令牌解析与验签（防 alg 混淆：alg/kid 只用于**选**实现与**选**公钥，
//  校验强度绝不因 alg 放宽 —— 验收④）
// ─────────────────────────────────────────────────────────────

// b64uDecode：与服务端 token_b64u_dec 同口径（URL 字母表、无 padding）。
func b64uDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// verifyToken：取公钥（缓存 → 在线）→ 验签 → 结构校验（aud/dvh/exp）。
// claims 输入的是**未验签**的解析结果，验签通过才算数。
func (m *Manager) verifyToken(token string, claims *Claims) error {
	body, sig, err := splitToken(token)
	if err != nil {
		return err
	}
	pub, alg, err := m.publicKey(claims.Kid)
	if err != nil {
		return err
	}
	if alg != claims.Alg {
		// 公钥缓存里该 kid 的算法与令牌声明不一致 —— 可能是换密钥，
		// 尝试强制刷新一次公钥清单；仍不一致即拒绝。
		if err2 := m.keys.Refresh(m.client); err2 == nil {
			pub, alg, err = m.publicKey(claims.Kid)
			if err != nil {
				return err
			}
		}
		if alg != claims.Alg {
			return fmt.Errorf("密钥版本 %s 的算法与令牌声明不符", claims.Kid)
		}
	}
	// 签名段解码（b64url，无 padding）
	sigRaw, err := b64uDecode(sig)
	if err != nil {
		return errors.New("令牌签名无法解码")
	}
	switch claims.Alg {
	case "ed25519":
		if len(pub) != ed25519.PublicKeySize {
			return errors.New("ed25519 公钥长度异常")
		}
		if !ed25519.Verify(ed25519.PublicKey(pub), []byte(body), sigRaw) {
			return errors.New("签名校验失败")
		}
	case "es256":
		if !verifyES256(pub, []byte(body), sigRaw) {
			return errors.New("签名校验失败")
		}
	default:
		// ★ 未知算法一律拒绝 —— 不存在「未知算法当免费通过」的口子
		return errors.New("不支持的签名算法：" + claims.Alg)
	}

	if claims.Aud != AudDuihaoDesktop {
		return errors.New("令牌 aud 不符")
	}
	if claims.Exp <= 0 {
		return errors.New("令牌缺少过期时间")
	}
	if claims.Sub == "" || claims.Prod == "" || claims.Dvh == "" {
		return errors.New("令牌内容不完整")
	}
	// 令牌 exp 过期**不在这里拒绝**：离线宽限期内仍按已登录权益放行
	//（State.reason = token_expired_offline_grace），由 MaybeRefresh 联网后续签。
	return nil
}

// parseAndFetchKey：解析 token → 需要时在线拉公钥 → 完整验签 → 返回已验证 claims。
func (m *Manager) parseAndFetchKey(token string) (*Claims, error) {
	claims, err := parseToken(token)
	if err != nil {
		return nil, err
	}
	// 本地缓存没有该 kid → 在线拉一次
	if _, _, err := m.publicKey(claims.Kid); err != nil {
		if err2 := m.keys.Refresh(m.client); err2 != nil {
			return nil, errors.New("获取签名公钥失败：" + err.Error())
		}
	}
	if err := m.verifyToken(token, claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func parseToken(token string) (*Claims, error) {
	body, sigB64, err := splitToken(token)
	if err != nil {
		return nil, err
	}
	if sigB64 == "" {
		return nil, errors.New("令牌缺少签名")
	}
	raw, err := b64uDecode(body)
	if err != nil {
		return nil, errors.New("令牌载荷无法解码")
	}
	var c Claims
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, errors.New("令牌载荷不是合法 JSON")
	}
	return &c, nil
}

func splitToken(token string) (body, sig string, err error) {
	// 令牌恰好两个段：body.sig。payload 是 JSON 不含点号，不会出现第三段。
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			if body != "" || sig != "" {
				return "", "", errors.New("令牌格式不正确")
			}
			body, sig = token[:i], ""
			rest := token[i+1:]
			for j := 0; j < len(rest); j++ {
				if rest[j] == '.' {
					return "", "", errors.New("令牌格式不正确")
				}
			}
			return body, rest, nil
		}
	}
	return "", "", errors.New("令牌格式不正确")
}

// verifyES256：服务端 openssl_sign(SHA256) 产出 DER 签名，公钥为 PEM。
func verifyES256(pubPEM []byte, msg, sig []byte) bool {
	key, err := pemToECDSAPub(pubPEM)
	if err != nil {
		return false
	}
	var der struct {
		R, S *big.Int
	}
	if _, err := asn1.Unmarshal(sig, &der); err != nil || der.R == nil || der.S == nil {
		return false
	}
	sum := sha256.Sum256(msg)
	return ecdsa.Verify(key, sum[:], der.R, der.S)
}

// pemToECDSAPub：解析 "PUBLIC KEY"（PKIX）PEM。
func pemToECDSAPub(pemBytes []byte) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("公钥 PEM 解析失败")
	}
	anyKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("公钥不是合法 PKIX 结构")
	}
	key, ok := anyKey.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("公钥不是 P-256 ECDSA")
	}
	return key, nil
}

// ─────────────────────────────────────────────────────────────
//  公钥缓存（keys.json，落 dataDir；离线验签的关键）
// ─────────────────────────────────────────────────────────────

type pubKeyEntry struct {
	Kid    string `json:"kid"`
	Alg    string `json:"alg"`
	Pub    string `json:"pub"`    // ed25519: raw-b64；es256: PEM
	Format string `json:"format"`
}

type keyCache struct {
	mu      sync.RWMutex
	path    string            // "" = 不落盘（测试）
	entries map[string]pubKeyEntry
}

func newKeyCache(dataDir string) *keyCache {
	k := &keyCache{entries: map[string]pubKeyEntry{}}
	if dataDir != "" {
		k.path = filepath.Join(dataDir, "licence-keys.json")
		k.load()
	}
	return k
}

func (k *keyCache) load() {
	b, err := os.ReadFile(k.path)
	if err != nil {
		return
	}
	var list []pubKeyEntry
	if json.Unmarshal(b, &list) == nil && len(list) > 0 {
		for _, e := range list {
			k.entries[e.Kid] = e
		}
	}
}

func (k *keyCache) saveLocked() {
	if k.path == "" {
		return
	}
	list := make([]pubKeyEntry, 0, len(k.entries))
	for _, e := range k.entries {
		list = append(list, e)
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err == nil {
		_ = os.MkdirAll(filepath.Dir(k.path), 0o700)
		_ = os.WriteFile(k.path, b, 0o600)
	}
}

// Refresh 在线拉取公钥清单并覆盖缓存。
func (k *keyCache) Refresh(c *Client) error {
	list, err := c.Keys()
	if err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.entries = map[string]pubKeyEntry{}
	for _, e := range list {
		k.entries[e.Kid] = e
	}
	k.saveLocked()
	return nil
}

func (m *Manager) publicKey(kid string) ([]byte, string, error) {
	if kid == "" {
		return nil, "", errors.New("令牌缺少 kid")
	}
	m.keys.mu.RLock()
	e, ok := m.keys.entries[kid]
	m.keys.mu.RUnlock()
	if !ok {
		return nil, "", fmt.Errorf("未知密钥版本：%s", kid)
	}
	switch e.Alg {
	case "ed25519":
		if e.Format != "raw-b64" {
			return nil, "", errors.New("ed25519 公钥格式异常")
		}
		raw, err := base64.StdEncoding.DecodeString(e.Pub)
		if err != nil {
			return nil, "", errors.New("ed25519 公钥解码失败")
		}
		return raw, e.Alg, nil
	case "es256":
		return []byte(e.Pub), e.Alg, nil
	default:
		return nil, "", errors.New("未知公钥算法：" + e.Alg)
	}
}
