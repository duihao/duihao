package webdavd

// NAS 内部多用户的认证接线：把「会话/Basic 里的身份」解析成 hub_users 账号，
// 以及桌面端开窗的一次性自动登录票（?_k=）。
//
// 口径（2026-10-31 定稿）：
//   - 会话 token 的 user 字段有三种形态：
//       "hub:<uid>"  登录页按 NAS 账号签发（新）
//       共享账号名    扫码快传 / 老登录页签发（兼容）；若恰好与某 NAS 账号
//                    同名（引导 admin 沿用共享名），同样拿到对应角色
//   - NAS 账号管 /-<app> 与 /-api/*，外加网页文件层（「网盘」模块权限）；
//     WebDAV 挂载客户端仍走共享凭据，不受模块权限约束（挂载=全盘的边界不变）。
//   - NAS 账号的文件根：与启用共享同名 → 跟该共享走；否则落默认共享。
//   - ?_k= 自动登录票只服务「桌面首屏宫格点进 NAS」：主程序现签 60 秒短票，
//     guard 只认本机回环来源；扫码与浏览器直接访问不受影响，照常输账密。

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/hubusers"
)

// hubPrefix 会话 token 里 NAS 账号的标记前缀。
const hubPrefix = "hub:"

// defaultShare 挑「根目录来源」共享：第一个启用的、优先非只读。
// NAS 账号没有自己的共享，文件类资源统一落在这份根上（边界口径：挂载=
// 全盘，网页模块权限不管文件层）。
func (s *Server) defaultShare() *Share {
	var fallback *Share
	for i := range s.cfg.Shares {
		sh := &s.cfg.Shares[i]
		if !sh.Enabled {
			continue
		}
		if fallback == nil {
			fallback = sh
		}
		if !sh.ReadOnly {
			return sh
		}
	}
	return fallback
}

// hubUser 取（并验证启用位）NAS 账号；库不可用/不存在返回 nil。
func (s *Server) hubUser(uid string) *hubusers.User {
	st := s.hub()
	if st == nil {
		return nil
	}
	u, err := hubusers.Get(st.DB(), uid)
	if err != nil || u == nil || !u.Enabled {
		return nil
	}
	return u
}

// authShare 认证并解析身份。返回 (共享, NAS账号uid)。
// 共享提供根目录/只读位；NAS 账号提供角色与权限。共享为 nil = 未认证。
func (s *Server) authShare(r *http.Request) (*Share, string) {
	if user, pass, ok := r.BasicAuth(); ok && user != "" {
		if sh := s.shareByCreds(user, pass); sh != nil {
			return sh, sh.User
		}
		return nil, ""
	}
	c, err := r.Cookie(sessCookie)
	if err != nil || c.Value == "" {
		return nil, ""
	}
	user, err := verifyToken(s.sessKey, c.Value)
	if err != nil || user == "" {
		return nil, ""
	}
	if rest, ok := strings.CutPrefix(user, hubPrefix); ok {
		// NAS 账号会话：账号必须存在且启用。文件根：
		//   有同名启用共享 → 跟该共享走（根目录/只读位一致）；
		//   没有 → 落默认共享（第一个启用、优先非只读）。
		if s.hubUser(rest) == nil {
			return nil, ""
		}
		for i := range s.cfg.Shares {
			if s.cfg.Shares[i].Enabled && s.cfg.Shares[i].User == rest {
				return &s.cfg.Shares[i], rest
			}
		}
		sh := s.defaultShare()
		if sh == nil {
			return nil, ""
		}
		return sh, rest
	}
	// 兼容：共享名直签（扫码快传/老会话）。同名 NAS 账号在 guard 里再解析。
	for i := range s.cfg.Shares {
		if s.cfg.Shares[i].Enabled && s.cfg.Shares[i].User == user {
			return &s.cfg.Shares[i], user
		}
	}
	return nil, ""
}

// tryEntryKey 处理桌面端自动登录票。返回 true 表示已就地响应（种 Cookie +
// 302 到干净地址），调用方直接 return。票无效时剥掉 _k 继续走正常认证。
func (s *Server) tryEntryKey(w http.ResponseWriter, r *http.Request) bool {
	k := r.URL.Query().Get("_k")
	if k == "" {
		return false
	}
	clean := *r.URL
	q := clean.Query()
	q.Del("_k")
	clean.RawQuery = q.Encode()
	// 只认本机回环：票是主程序给本机 webview 现签的，不该出现在网络上。
	if ra, _, err := net.SplitHostPort(r.RemoteAddr); err != nil {
		http.Redirect(w, r, clean.String(), http.StatusFound)
		return true
	} else if ip := net.ParseIP(ra); ip == nil || !ip.IsLoopback() {
		http.Redirect(w, r, clean.String(), http.StatusFound)
		return true
	}
	user, err := verifyToken(s.sessKey, k)
	if err == nil && user != "" {
		valid := false
		if uid, ok := strings.CutPrefix(user, hubPrefix); ok {
			valid = s.hubUser(uid) != nil && s.defaultShare() != nil
		} else {
			for i := range s.cfg.Shares {
				if s.cfg.Shares[i].Enabled && s.cfg.Shares[i].User == user {
					valid = true
					break
				}
			}
		}
		if valid {
			http.SetCookie(w, &http.Cookie{
				Name: sessCookie, Value: issueToken(s.sessKey, user),
				Path: "/", MaxAge: int(sessTTL.Seconds()),
				HttpOnly: true, SameSite: http.SameSiteLaxMode,
			})
			http.Redirect(w, r, clean.String(), http.StatusFound)
			return true
		}
	}
	// 票过期/无效：剥掉参数重走，浏览器用户会看到登录页而不是报错。
	http.Redirect(w, r, clean.String(), http.StatusFound)
	return true
}

// IssueEntryToken 给主程序「开 NAS 窗」用：为 uid 签一张短时会话票，
// 前端拼在 URL 的 ?_k= 上。instanceDir 用实例目录（与会话密钥同源）。
func IssueEntryToken(instanceDir, uid string, ttl time.Duration) (string, error) {
	key, err := loadSessionKey(instanceDir)
	if err != nil {
		return "", err
	}
	return issueTokenTTL(key, hubPrefix+uid, ttl), nil
}
