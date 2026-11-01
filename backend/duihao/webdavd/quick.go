package webdavd

// 扫码快传：PC 端为共享用户签发短时 token，手机扫码访问 /-q 自动登录。
//
// 设计取舍：token 与浏览器会话 Cookie 同密钥同格式（HMAC(user|exp)），只是
// TTL 更短（quickTTL）——不加新机制、不引入一次性表，超时自然失效，重复
// 生成无副作用。可达性由监听设置决定（127.0.0.1 时手机连不上，服务层把关，
// 局域网监听按 v3.1 硬规则必然 TLS，传输加密）。

import (
	"fmt"
	"net/url"
	"time"
)

// quickTTL 快传 token 有效期：够扫码 + 打开浏览器的窗口，短到不担心转发泄露。
const quickTTL = 5 * time.Minute

// QuickToken 为指定共享用户签发快传 token（要求用户名存在且启用）。
// 直接读实例目录里的会话密钥（session.key，持久），无需实例运行中——
// 运行状态由服务层校验。
func QuickToken(instanceDir, user string) (token string, ttl time.Duration, err error) {
	shares, err := LoadShares(instanceDir)
	if err != nil {
		return "", 0, err
	}
	ok := false
	for i := range shares {
		if shares[i].Enabled && shares[i].User == user {
			ok = true
			break
		}
	}
	if !ok {
		return "", 0, fmt.Errorf("共享不存在或未启用: %s", user)
	}
	key, err := loadSessionKey(instanceDir)
	if err != nil {
		return "", 0, err
	}
	return issueTokenTTL(key, user, quickTTL), quickTTL, nil
}

// QuickLoginURL 拼扫码地址：scheme://host:port/-q?t=<token>
func QuickLoginURL(scheme, host string, port int, token string) string {
	return fmt.Sprintf("%s://%s:%d/-q?t=%s", scheme, host, port, url.QueryEscape(token))
}
