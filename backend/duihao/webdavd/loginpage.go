package webdavd

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/duihao/duihao/backend/duihao/webdavd/hubusers"
)

// 登录页与登录/登出端点（/ -login /-logout /-q，下划线开头的保留路径不参与 DAV）。
// 登录成功签发 HttpOnly 会话 Cookie；WebDAV 客户端不受影响。

func reservedPath(p string) bool {
	return p == "/-login" || p == "/-logout" || p == "/-q"
}

// isBrowserNav 浏览器导航请求（会渲染登录页而非 401 challenge）。
func isBrowserNav(r *http.Request) bool {
	if r.Method != "GET" && r.Method != "HEAD" {
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// writeLoginPage 渲染登录页（与文件管理器同风格；深浅色跟随系统）。
func writeLoginPage(w http.ResponseWriter, msg string) {
	note := ""
	if msg != "" {
		note = `<div class="err">` + htmlEscape(msg) + `</div>`
	}
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString(`<title>对号 NAS · 登录</title>`)
	b.WriteString(`<style>` + loginCSS + `</style></head><body><div class="box">`)
	b.WriteString(`<div class="logo"><svg width="34" height="34" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10" fill="none" stroke="#f0247e" stroke-width="1.6"/><path d="M7.5 12.2l3 3 6-6.4" fill="none" stroke="#f0247e" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg></div>`)
	b.WriteString(`<h1>对号 NAS</h1>`)
	b.WriteString(`<p class="hint">输入 NAS 账号密码登录</p>`)
	b.WriteString(`<form id="f"><input id="u" autocomplete="username" placeholder="用户名" autofocus>` +
		`<input id="p" type="password" autocomplete="current-password" placeholder="密码">` +
		note +
		`<button id="go" type="submit">登 录</button></form>`)
	b.WriteString(`<footer>对号 DUIHAO</footer></div>`)
	b.WriteString(`<script>` + loginJS + `</script>`)
	b.WriteString(`</body></html>`)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(b.String()))
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;")
	return r.Replace(s)
}

const loginCSS = `/* 配色与桌面端 style.css 对齐（同 shared.css 那套映射）：登录页只跟随系统明暗，
   不读应用主题 —— 它出现在「还没进 NAS」的时刻，按系统走最不容易出岔子。 */
:root{--bg:#ffffff;--card:#f5f5f5;--tx:#000000;--mut:#5c5c5c;--ln:rgba(0,0,0,.08);--acc:#ff4d8d;--danger:#d13438}
@media (prefers-color-scheme:dark){:root{--bg:#0f0f0f;--card:#1a1a1a;--tx:#ffffff;--mut:#9a9a9a;--ln:rgba(255,255,255,.14);--acc:#ff4d8d;--danger:#f87171}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--tx);font-family:system-ui,"Segoe UI","Microsoft YaHei",sans-serif;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:1rem}
.box{background:var(--card);width:100%;max-width:22rem;border-radius:14px;box-shadow:0 1px 3px rgba(0,0,0,.08),0 12px 32px rgba(0,0,0,.06);padding:2rem 1.75rem 1.25rem;text-align:center}
.logo{margin-bottom:.4rem}
h1{font-size:1.05rem;margin:.2rem 0 .3rem;font-weight:600}
.hint{color:var(--mut);font-size:.8rem;margin:0 0 1.2rem}
input{width:100%;border:1px solid var(--ln);background:var(--card);color:var(--tx);padding:.55rem .75rem;border-radius:8px;font-size:.9rem;margin-bottom:.6rem;outline:none}
input:focus{border-color:var(--acc)}
.err{color:var(--danger);font-size:.8rem;margin:-.1rem 0 .6rem;text-align:left}
button{width:100%;border:none;background:var(--acc);color:#fff;padding:.6rem;border-radius:8px;font-size:.9rem;cursor:pointer;letter-spacing:.3em;text-indent:.3em}
button:hover{opacity:.9}
button:disabled{opacity:.6;cursor:default}
footer{margin-top:1.4rem;color:var(--mut);font-size:.72rem}
`

const loginJS = `(function(){
var f=document.getElementById('f'),u=document.getElementById('u'),p=document.getElementById('p'),go=document.getElementById('go');
function lock(v){go.disabled=v;go.textContent=v?'登录中…':'登 录'}
f.onsubmit=function(ev){
 ev.preventDefault();
 lock(true);
 fetch('/-login',{method:'POST',headers:{'Content-Type':'application/json'},
  body:JSON.stringify({u:u.value.trim(),p:p.value})}).then(function(r){
   if(r.ok){location.reload();return}
   return r.json().then(function(j){
    lock(false);
    var old=document.querySelector('.err');if(old)old.remove();
    var d=document.createElement('div');d.className='err';
    d.textContent=(j&&j.error)||'登录失败（'+r.status+'）';
    f.insertBefore(d,go);
   });
  }).catch(function(){lock(false)});
};
})();`

// handleLogin POST /-login：校验凭据 → 签发会话 Cookie。
// 先查 NAS 账号（hub_users，签 hub:<uid> 票），不中再回落共享账密（兼容扫码
// 快传签发的老票与纯文件用户）。失败限速对两条路一致生效。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := loginKey(r)
	if locked, wait := loginLocked(ip); locked {
		min := int(wait.Minutes()) + 1
		jsonError(w, http.StatusTooManyRequests,
			"失败次数太多，这 "+strconv.Itoa(min)+" 分钟内不接受这个地址的登录")
		return
	}
	var in struct{ U, P string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		jsonError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	in.U = strings.TrimSpace(in.U)

	// 1) NAS 账号（多用户，模块权限在应用层收口）。
	token := ""
	if st := s.hub(); st != nil {
		if u, _ := hubusers.Verify(st.DB(), in.U, in.P); u != nil {
			if !u.Enabled {
				loginFail(ip)
				jsonError(w, http.StatusForbidden, "账号已停用，请联系管理员")
				return
			}
			token = issueToken(s.sessKey, hubPrefix+u.UID)
		}
	}
	// 2) 回落：共享账密（文件用户/老习惯）。
	sh := (*Share)(nil)
	if token == "" {
		sh = s.shareByCreds(in.U, in.P)
		if sh == nil {
			loginFail(ip) // 远程访问开着时，这里是爆破的唯一入口，必须数着
			jsonError(w, http.StatusUnauthorized, "用户名或密码错误")
			return
		}
		token = issueToken(s.sessKey, sh.User)
	}
	loginOK(ip)
	http.SetCookie(w, &http.Cookie{
		Name: sessCookie, Value: token,
		Path: "/", MaxAge: int(sessTTL.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// handleQuick GET /-q?t=<token>：扫码快传自动登录。校验短时 token → 签发
// 完整会话 Cookie → 302 回根路径（手机直接落在文件管理器页面）。
// token 与会话同密钥，签名/过期校验一致；共享被禁用后即使签名有效也拒登。
func (s *Server) handleQuick(w http.ResponseWriter, r *http.Request) {
	user, err := verifyToken(s.sessKey, r.URL.Query().Get("t"))
	if err != nil {
		writeLoginPage(w, "快传链接已失效，请重新扫码")
		return
	}
	found := false
	for i := range s.cfg.Shares {
		if s.cfg.Shares[i].Enabled && s.cfg.Shares[i].User == user {
			found = true
			break
		}
	}
	if !found {
		writeLoginPage(w, "快传链接已失效，请重新扫码")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessCookie, Value: issueToken(s.sessKey, user),
		Path: "/", MaxAge: int(sessTTL.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// handleLogout GET /-logout：清会话 Cookie + 用假凭据请求冲掉浏览器缓存的
// Basic Auth（Chrome/Firefox 对 200 响应会以页面显式提供的 Authorization
// 覆盖缓存），随后回根路径——此时只会看到登录页。
func (s *Server) handleLogout(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(`<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width, initial-scale=1"><title>退出中…</title></head><body>` +
		// bG9nb3V0OmxvZ291dA== = base64("logout:logout")：假凭据冲掉浏览器缓存的真凭据
		`<script>fetch('/-logout',{headers:{Authorization:'Basic bG9nb3V0OmxvZ291dA=='}})` +
		`.then(function(){location.href='/'}).catch(function(){location.href='/'});` +
		`</script></body></html>`))
}

func jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// shareByCreds 按用户名+明文密码找启用的共享；找不到也做 bcrypt 时序对齐。
func (s *Server) shareByCreds(user, pass string) *Share {
	snap := s.cfg.Shares
	for i := range snap {
		if !snap[i].Enabled {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(user), []byte(snap[i].User)) != 1 {
			continue
		}
		if bcrypt.CompareHashAndPassword([]byte(snap[i].PassHash), []byte(pass)) == nil {
			return &snap[i]
		}
		return nil // 用户名存在但密码错：不再继续比对（避免多账号枚举）
	}
	// 时序对齐：真实校验约 50ms，账号不存在也花掉同样的时间。
	_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(pass))
	return nil
}
