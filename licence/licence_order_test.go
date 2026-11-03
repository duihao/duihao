package licence

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ── 会员中心（应用内购买）回归：支付到账 → 强制续签拉新权益 ──

// TestOrderStatusPaidTriggersRefreshOnce：轮询查单遇到 paid，
// ① 必须触发一次 /api/auth/refresh（MaybeRefresh 只在 exp 临近才出网，不补这步
//    用户付完款界面会停在旧档位）；② 同一订单重复轮询只刷一次。
func TestOrderStatusPaidTriggersRefreshOnce(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	refreshCalls := atomic.Int32{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/order/status":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"order_no": "DHB20261102001", "status": "paid", "status_label": "已支付",
				"paid": true, "has_license": true, "amount": 39100,
				"entitlement": map[string]any{
					"product": "duihao-desktop", "plan": "lifetime",
					"plan_text": "永久会员", "ent": 0, "seats": 1,
				},
			})
		case r.URL.Path == "/api/auth/refresh":
			refreshCalls.Add(1)
			// 新令牌：plan 升为 lifetime（服务端归并结果）
			now := time.Now()
			c := &Claims{
				Iss: "duihao.cn", Aud: AudDuihaoDesktop, Sub: "12", Prod: "duihao-desktop",
				Plan: PlanLifetime, Ent: 0, Seats: 1,
				Dvh: dvhFromParts(testParts), Iat: now.Unix(), Exp: now.Add(30 * 24 * time.Hour).Unix(),
				Alg: "ed25519", Kid: "k1", Jti: "refreshed",
			}
			raw, _ := json.Marshal(c)
			body := b64u(raw)
			tok := body + "." + b64u(ed25519.Sign(priv, []byte(body)))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "token": tok,
				"entitlement": map[string]any{"product": "duihao-desktop", "plan": "lifetime", "ent": 0, "seats": 1},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m := New("", WithStore(NewMemoryStorage()), WithBaseURL(srv.URL))
	m.nowFunc = time.Now
	m.fpr = fakeFpr{parts: testParts}
	m.keys.entries["k1"] = pubKeyEntry{
		Kid: "k1", Alg: "ed25519",
		Pub: base64.StdEncoding.EncodeToString(pub), Format: "raw-b64",
	}

	// 登录态：annual（付款前档位）
	token, claims := makeToken(t, priv, nil)
	seedToken(t, m, token, claims, testParts)
	if st := m.Verify(); !st.LoggedIn || st.Plan != PlanAnnual {
		t.Fatalf("前置登录态异常: %+v", st)
	}

	// 第一次轮询 paid → 触发续签
	resp, err := m.OrderStatus("DHB20261102001")
	if err != nil {
		t.Fatalf("OrderStatus: %v", err)
	}
	if !resp.Paid || !resp.HasLicense {
		t.Fatalf("期望 paid+has_license，got %+v", resp)
	}
	if n := refreshCalls.Load(); n != 1 {
		t.Fatalf("第 1 次轮询后续签次数 = %d，期望 1", n)
	}
	if st := m.State(); st.Plan != PlanLifetime {
		t.Fatalf("到账续签后 plan = %q，期望 lifetime", st.Plan)
	}

	// 同一订单再轮询（前端 3s 轮询语义）→ 不再续签
	if _, err := m.OrderStatus("DHB20261102001"); err != nil {
		t.Fatalf("OrderStatus#2: %v", err)
	}
	if n := refreshCalls.Load(); n != 1 {
		t.Fatalf("重复轮询后又续签 %d 次，期望仍为 1", n)
	}
}

// TestFetchQRDataValidation：非 http(s) / 空 URL 必须拒绝（不给 SSRF 钻空子）。
func TestFetchQRDataValidation(t *testing.T) {
	m := New("", WithStore(NewMemoryStorage()))
	for _, bad := range []string{"", "file:///etc/passwd", "ftp://x/y.png", "javascript:alert(1)"} {
		if _, err := m.FetchQRData(bad); err == nil {
			t.Fatalf("FetchQRData(%q) 应拒绝", bad)
		}
	}
}
