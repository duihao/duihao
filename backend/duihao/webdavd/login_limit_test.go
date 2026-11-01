package webdavd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func reqFrom(ip string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/-login", nil)
	// IPv6 的 RemoteAddr 要带方括号，SplitHostPort 才拆得开
	if strings.Contains(ip, ":") {
		ip = "[" + ip + "]"
	}
	r.RemoteAddr = ip + ":12345"
	return r
}

func TestLoginRateLimit(t *testing.T) {
	ip := "203.0.113.7"
	// 4 次失败：还放行
	for i := 0; i < 4; i++ {
		loginFail(ip)
	}
	if locked, _ := loginLocked(ip); locked {
		t.Fatal("4 次失败不该锁")
	}
	// 第 5 次：锁
	loginFail(ip)
	locked, wait := loginLocked(ip)
	if !locked {
		t.Fatal("5 次失败必须锁")
	}
	if wait <= 0 || wait > loginLockFor+time.Second {
		t.Errorf("锁定时长不对: %v", wait)
	}
	// 成功登录清零
	loginOK(ip)
	if locked, _ := loginLocked(ip); locked {
		t.Fatal("loginOK 后必须解锁")
	}
}

func TestLoginRateLimitWindowExpires(t *testing.T) {
	ip := "203.0.113.8"
	for i := 0; i < loginMaxFails; i++ {
		loginFail(ip)
	}
	// 手工把锁定截止与首败时间都拨到过去 → 条目应被清掉，且再失败要从零数
	loginMu.Lock()
	loginState[ip].until = time.Now().Add(-time.Minute)
	loginState[ip].firstAt = time.Now().Add(-loginFailWindow - time.Minute)
	failsBefore := len(loginState)
	loginMu.Unlock()
	if locked, _ := loginLocked(ip); locked {
		t.Fatal("锁定期过了不该再锁")
	}
	loginMu.Lock()
	if _, still := loginState[ip]; still {
		t.Fatal("过期条目该被顺手清掉")
	}
	loginMu.Unlock()
	_ = failsBefore
	// 清完之后重新失败：从零开始数
	for i := 0; i < loginMaxFails-1; i++ {
		loginFail(ip)
	}
	if locked, _ := loginLocked(ip); locked {
		t.Fatalf("重新计数后 %d 次失败不该锁", loginMaxFails-1)
	}
}

func TestIsPrivateClient(t *testing.T) {
	priv := []string{"127.0.0.1", "192.168.1.9", "10.0.0.2", "172.16.3.4", "::1", "fe80::1"}
	for _, ip := range priv {
		if !isPrivateClient(reqFrom(ip)) {
			t.Errorf("%s 应算内网", ip)
		}
	}
	pub := []string{"2408:8207::5", "114.114.114.114", "2001:db8::1"}
	for _, ip := range pub {
		if isPrivateClient(reqFrom(ip)) {
			t.Errorf("%s 应算公网", ip)
		}
	}
	if isPrivateClient(reqFrom("not-an-ip")) {
		t.Error("解析不了的地址要按公网处理（默认拒绝）")
	}
}
