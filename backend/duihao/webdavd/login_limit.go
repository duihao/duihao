package webdavd

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 登录失败限速：公网暴露后（远程访问），登录接口就是爆破的第一目标。
//
// 口径：同一个来源 IP，15 分钟内连续失败 5 次 → 锁 15 分钟（期间一律 429，
// 连「试试看」的机会都不给）；成功登录立刻清零。内存态即可——重启清空
// 无所谓，爆破的人也得重新数。
//
// 为什么不在 guard 层做：guard 拦的是「已登录前的所有 401」，把 WebDAV 客户端
// 配错密码的情况也算进去会误伤自己人；登录接口是爆破的唯一入口，就守这。

const (
	loginMaxFails   = 5
	loginFailWindow = 15 * time.Minute
	loginLockFor    = 15 * time.Minute
)

type loginFails struct {
	fails   int
	firstAt time.Time
	until   time.Time // 非零 = 锁定截止
}

var (
	loginMu    sync.Mutex
	loginState = map[string]*loginFails{}
)

func loginKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return host
}

// loginLocked 该来源是否被锁。顺带做惰性清扫（过期条目顺手删，不单独起协程）。
func loginLocked(ip string) (bool, time.Duration) {
	loginMu.Lock()
	defer loginMu.Unlock()
	st, ok := loginState[ip]
	if !ok {
		return false, 0
	}
	now := time.Now()
	if st.until.After(now) {
		return true, time.Until(st.until)
	}
	// 窗口外的失败计数作废
	if now.Sub(st.firstAt) > loginFailWindow {
		delete(loginState, ip)
	}
	return false, 0
}

func loginFail(ip string) {
	loginMu.Lock()
	defer loginMu.Unlock()
	now := time.Now()
	st, ok := loginState[ip]
	if !ok || now.Sub(st.firstAt) > loginFailWindow {
		st = &loginFails{firstAt: now}
		loginState[ip] = st
	}
	st.fails++
	if st.fails >= loginMaxFails {
		st.until = now.Add(loginLockFor)
	}
	// 顺手清扫（数量小，整表扫没负担）
	for k, v := range loginState {
		if v.until.Before(now) && now.Sub(v.firstAt) > loginFailWindow {
			delete(loginState, k)
		}
	}
}

func loginOK(ip string) {
	loginMu.Lock()
	delete(loginState, ip)
	loginMu.Unlock()
}

// isPrivateClient 来源是不是局域网/本机（公网开关用：关着时只放行内网）。
func isPrivateClient(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}
