package licence

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// ── 测试基建 ──

type fakeFpr struct{ parts [3]string }

func (f fakeFpr) Parts() ([3]string, error) { return f.parts, nil }

// testParts：测试机器的三个部件（与空值语义区分：真实采集不会全空）。
var testParts = [3]string{"cpu-1", "board-1", "disk-1"}

func b64u(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// makeToken：按服务端 token_issue 同格式签令牌（ed25519）。
func makeToken(t *testing.T, priv ed25519.PrivateKey, mutate func(*Claims)) (string, *Claims) {
	t.Helper()
	now := time.Now()
	c := &Claims{
		Iss: "duihao.cn", Aud: AudDuihaoDesktop, Sub: "12", Prod: "duihao-desktop",
		Plan: PlanAnnual, Ent: now.Add(365 * 24 * time.Hour).Unix(), Seats: 1,
		Dvh: dvhFromParts(testParts), Iat: now.Unix(), Exp: now.Add(30 * 24 * time.Hour).Unix(),
		Alg: "ed25519", Kid: "k1", Jti: "testjti",
	}
	if mutate != nil {
		mutate(c)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	body := b64u(raw)
	sig := ed25519.Sign(priv, []byte(body))
	return body + "." + b64u(sig), c
}

func newTestManager(t *testing.T, priv ed25519.PrivateKey) *Manager {
	t.Helper()
	m := New("", WithStore(NewMemoryStorage()))
	m.nowFunc = time.Now
	m.fpr = fakeFpr{parts: testParts}
	// 直接注入公钥缓存（模拟 /api/auth/keys 已拉取）
	m.keys.entries["k1"] = pubKeyEntry{Kid: "k1", Alg: "ed25519", Pub: base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)), Format: "raw-b64"}
	return m
}

// seedToken：模拟「登录成功后快照落盘」。
func seedToken(t *testing.T, m *Manager, token string, claims *Claims, parts [3]string) {
	t.Helper()
	raw, _ := json.Marshal(snapshot{Token: token, Claims: claims, Parts: parts, SavedAt: time.Now().Unix()})
	if err := m.store.Save(string(raw)); err != nil {
		t.Fatal(err)
	}
}

// ── 验收①：令牌在有效期内，全程无网可验（Verify 不走网络） ──

func TestVerifyOfflineHappy(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_ = pub
	m := newTestManager(t, priv)
	token, claims := makeToken(t, priv, nil)
	seedToken(t, m, token, claims, testParts)

	st := m.Verify()
	if !st.LoggedIn || st.Plan != PlanAnnual {
		t.Fatalf("期望已登录年费，得到 %+v", st)
	}
}

// ── 验收②：令牌+快照整体拷到另一台机器 → 0/3 部件命中 → 拒绝 ──

func TestCopiedTokenRejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_ = pub
	m := newTestManager(t, priv)
	token, claims := makeToken(t, priv, nil)
	seedToken(t, m, token, claims, [3]string{"aaa", "bbb", "ccc"})

	// 另一台机器：三个部件完全不同
	m.fpr = fakeFpr{parts: [3]string{"xxx", "yyy", "zzz"}}
	st := m.Verify()
	if st.LoggedIn {
		t.Fatalf("拷贝的令牌应被拒绝，得到 %+v", st)
	}
}

// ── 设备指纹 2/3 表决：换掉一个部件（如重装系统盘）仍算同机 ──

func TestTwoOfThreePartsPass(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_ = pub
	m := newTestManager(t, priv)
	token, claims := makeToken(t, priv, nil)
	seedToken(t, m, token, claims, testParts)

	m.fpr = fakeFpr{parts: [3]string{"cpu-1", "board-1", "zzz"}} // 只换系统盘
	st := m.Verify()
	if !st.LoggedIn {
		t.Fatalf("2/3 部件命中应通过，得到 %+v", st)
	}
}

// ── 验收④：alg 混淆 —— 声明 ed25519 但签名/公钥对不上 → 拒 ──

func TestAlgConfusionRejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_ = pub
	m := newTestManager(t, priv)

	// 用 ed25519 私钥签出的令牌，把 payload 里的 alg 改成 es256：
	// 验签实现被切到 es256，对同一签名必然失败 —— 不允许绕过。
	token, claims := makeToken(t, priv, func(c *Claims) { c.Alg = "es256" })
	_ = claims
	seedToken(t, m, token, nil, testParts)
	if st := m.Verify(); st.LoggedIn {
		t.Fatal("alg 混淆应验签失败")
	}

	// 公钥清单里没有 es256 的 k1 → publicKey 直接报未知算法/未知公钥
	token2, claims2 := makeToken(t, priv, func(c *Claims) { c.Alg = "es256"; c.Kid = "k2" })
	seedToken(t, m, token2, claims2, testParts)
	if st := m.Verify(); st.LoggedIn {
		t.Fatal("未知 kid 应拒绝")
	}
}

// ── 验收③：令牌过期 + 无网 → 降级 free、不锁死、给原因 ──

func TestExpiredTokenOfflineGrace(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_ = pub
	m := newTestManager(t, priv)
	token, claims := makeToken(t, priv, func(c *Claims) { c.Exp = time.Now().Add(-1 * time.Hour).Unix() })
	seedToken(t, m, token, claims, testParts)

	st := m.Verify()
	// 权益未到期：保持登录与年费（离线宽限），但标记令牌已过期
	if !st.LoggedIn || st.Plan != PlanAnnual || st.Reason != "token_expired_offline_grace" {
		t.Fatalf("过期令牌应进入离线宽限，得到 %+v", st)
	}
	if !m.Allows("paid") {
		t.Fatal("宽限期内付费能力应保留")
	}
}

// ── 权益到期（ent 过去）→ 一律降 free，永久(0)不受影响 ──

func TestEntitlementExpiry(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_ = pub
	m := newTestManager(t, priv)
	token, claims := makeToken(t, priv, func(c *Claims) { c.Ent = time.Now().Add(-24 * time.Hour).Unix() })
	seedToken(t, m, token, claims, testParts)

	st := m.Verify()
	if st.Plan != PlanFree || st.Reason != "entitlement_expired" {
		t.Fatalf("权益到期应降 free，得到 %+v", st)
	}
	if m.Allows("paid") {
		t.Fatal("权益到期后 paid 能力应关闭")
	}

	// 永久（ent=0）永不降
	m2 := newTestManager(t, priv)
	token2, claims2 := makeToken(t, priv, func(c *Claims) { c.Plan = PlanLifetime; c.Ent = 0 })
	seedToken(t, m2, token2, claims2, testParts)
	if st := m2.Verify(); st.Plan != PlanLifetime || !m2.Allows("lifetime") {
		t.Fatalf("永久权益应保留，得到 %+v", st)
	}
}

// ── Allows：门禁唯一入口 ──

func TestAllows(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_ = pub
	m := newTestManager(t, priv)
	token, claims := makeToken(t, priv, func(c *Claims) { c.Plan = PlanTrial })
	seedToken(t, m, token, claims, testParts)
	m.Verify()

	if m.Allows("paid") {
		t.Fatal("试用不应通过 paid 门禁")
	}
	if !m.Allows("no-such-cap") == false {
		t.Fatal("未知能力必须拒绝")
	}

	token2, claims2 := makeToken(t, priv, nil) // annual
	seedToken(t, m, token2, claims2, testParts)
	m.Verify()
	if !m.Allows("paid") || !m.Allows("annual") || m.Allows("lifetime") {
		t.Fatal("年费档位判定不符")
	}
}

// ── 篡改 payload 一字节 → 验签失败 ──

func TestTamperedTokenRejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_ = pub
	m := newTestManager(t, priv)
	token, claims := makeToken(t, priv, nil)
	// 签名后偷换 payload（sub=999 换人），签名段原样保留 → 验签必须失败
	forged, _ := json.Marshal(func() *Claims { c := *claims; c.Sub = "999"; return &c }())
	dot := -1
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			dot = i
			break
		}
	}
	tampered := b64u(forged) + token[dot:]
	seedToken(t, m, tampered, nil, testParts)
	if st := m.Verify(); st.LoggedIn {
		t.Fatal("篡改后的令牌应验签失败")
	}
}

// ── 空存储 → free，不报错 ──

func TestEmptyStorage(t *testing.T) {
	m := New("", WithStore(NewMemoryStorage()))
	if st := m.Verify(); st.LoggedIn || st.Plan != PlanFree {
		t.Fatalf("空存储应 free，得到 %+v", st)
	}
	if m.Allows("paid") {
		t.Fatal("未登录不应通过任何付费门禁")
	}
}
