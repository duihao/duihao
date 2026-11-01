// Package credentials 凭证保险箱：口令 / 令牌在库里只留密文。
//
// 为什么单独一层：对号的核心约束是「凭证与执行权不出对号」——相机的 RTSP 密码、
// 局域网语音主机的令牌，都不该以明文躺在 hub.db 里。库文件会被同步客户端拉走、
// 会被备份工具打包、会被用户自己拷到别的机器上，明文等于泄漏。
//
// 两个平台两套实现，密文自带前缀，读的时候按前缀分派：
//
//	Windows  d1:…  DPAPI（CryptProtectData，当前用户作用域 + UI_FORBIDDEN）
//	         解不开就是解不开：换个用户、换台机器都读不出来，正是要的效果。
//	其它     a1:…  AES-256-GCM，密钥由「机器名 + 用户名 + 固定盐」派生。
//	         防不住同机同用户的本地人（本来也防不住），但库文件被搬走就失效。
//
// 明确不做的事：不引入主密码 / 不弹解锁框（对号是免登录即用的桌面工具，
// 每次打开先输一次密码会毁掉 Alt+Space 那种顺手感）。
package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	prefixDPAPI = "d1:" // Windows DPAPI
	prefixAES   = "a1:" // AES-256-GCM（跨平台兜底）
	saltText    = "duihao/credentials/v1"
)

// ErrEmpty 空串不该走到加解密（调用方先判空）。
var ErrEmpty = errors.New("凭证为空")

// Sealed 判断一个字符串是不是本包产出的密文。
func Sealed(s string) bool {
	return strings.HasPrefix(s, prefixDPAPI) || strings.HasPrefix(s, prefixAES)
}

// Seal 加密。空串原样返回空串（代表「没有凭证」），不报错。
func Seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	body, kind, err := sealWith(plain)
	if err != nil {
		return "", err
	}
	return kind + body, nil
}

// Open 解密。空串返回空串；不是本包密文的（历史遗留明文）原样返回，
// 这样老库里已有的明文密码还能用，下次保存时自然被换成密文。
func Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	switch {
	case strings.HasPrefix(sealed, prefixDPAPI):
		return openWith(strings.TrimPrefix(sealed, prefixDPAPI), "dpapi")
	case strings.HasPrefix(sealed, prefixAES):
		return openWith(strings.TrimPrefix(sealed, prefixAES), "aes")
	}
	return sealed, nil
}

// Mask 给界面/日志看的脱敏形式（只露末 4 位）。
func Mask(secret string) string {
	s := strings.TrimSpace(secret)
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= 4 {
		return "****"
	}
	return "****" + string(r[len(r)-4:])
}

// ---------- AES-256-GCM（非 Windows 与单测共用） ----------

// aesKey 由「机器名 + 用户名 + 固定盐」派生：换台机器就读不出来。
// 用 os.Hostname 而不是 MachineID，是因为后者各平台取法不一，跨平台兜底不必追求极致。
func aesKey() [32]byte {
	host, _ := os.Hostname()
	user := ""
	if u := os.Getenv("USERNAME"); u != "" {
		user = u
	} else {
		user = os.Getenv("USER")
	}
	return sha256.Sum256([]byte(saltText + "|" + host + "|" + user))
}

func aesSeal(plain string) (string, error) {
	key := aesKey()
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("取随机数失败: %w", err)
	}
	out := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

func aesOpen(body string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return "", fmt.Errorf("密文损坏: %w", err)
	}
	key := aesKey()
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("密文太短")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("解密失败（凭证不是本机本用户加密的）")
	}
	return string(plain), nil
}
