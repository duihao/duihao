package webdavd

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// 浏览器会话：免掉系统 Basic Auth 弹窗，登录页 POST 凭据 → 签名 Cookie。
// WebDAV 客户端（RaiDrive 等）不受影响，仍走 Basic Auth。
//
// Token 形如 base64url(user|过期Unix秒|HMAC-SHA256(secret, user|过期))。
// 校验只做三件事：格式对、没过期、HMAC 对——无状态，重启不丢登录。

const (
	sessCookie   = "dav_sess"
	sessTTL      = 12 * time.Hour
	sessKeyFile  = "session.key"
	secretLength = 32
)

var errBadToken = errors.New("bad session token")

// loadSessionKey 读（或创建）实例目录下的会话密钥；InstanceDir 为空（测试）
// 时退化为进程内随机密钥（重启失效，可接受）。
func loadSessionKey(dir string) ([]byte, error) {
	if dir == "" {
		k := make([]byte, secretLength)
		if _, err := rand.Read(k); err != nil {
			return nil, err
		}
		return k, nil
	}
	p := filepath.Join(dir, sessKeyFile)
	if data, err := os.ReadFile(p); err == nil && len(data) >= secretLength {
		return data, nil
	}
	k := make([]byte, secretLength)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	// 0600 在 Windows 上落不住（工程已知），靠目录 ACL；密钥泄露面 = 本机用户。
	if err := os.WriteFile(p, k, 0o600); err != nil {
		return nil, fmt.Errorf("写会话密钥失败: %w", err)
	}
	return k, nil
}

func sessMAC(key []byte, user string, exp int64) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(user))
	m.Write([]byte{'|'})
	m.Write([]byte(strconv.FormatInt(exp, 10)))
	return m.Sum(nil)
}

// issueToken 为用户签发会话 token（完整有效期）。
func issueToken(key []byte, user string) string {
	return issueTokenTTL(key, user, sessTTL)
}

// issueTokenTTL 按指定有效期签发 token（扫码快传走短 TTL，见 quick.go）。
func issueTokenTTL(key []byte, user string, ttl time.Duration) string {
	exp := time.Now().Add(ttl).Unix()
	payload := user + "|" + strconv.FormatInt(exp, 10)
	sig := base64.RawURLEncoding.EncodeToString(sessMAC(key, user, exp))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + sig
}

// verifyToken 校验 token，返回用户名。
func verifyToken(key []byte, token string) (string, error) {
	dot := strings.LastIndexByte(token, '.')
	if dot <= 0 || dot == len(token)-1 {
		return "", errBadToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(token[:dot])
	if err != nil {
		return "", errBadToken
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return "", errBadToken
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", errBadToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(token[dot+1:])
	if err != nil {
		return "", errBadToken
	}
	if subtle.ConstantTimeCompare(sig, sessMAC(key, parts[0], exp)) != 1 {
		return "", errBadToken
	}
	return parts[0], nil
}
