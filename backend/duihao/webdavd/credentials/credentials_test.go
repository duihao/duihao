package credentials

import (
	"strings"
	"testing"
)

// 往返：密文不是明文、能被 Open 还原、带平台前缀。
func TestSealOpenRoundTrip(t *testing.T) {
	secret := "Abc12345@192.168.1.64"
	sealed, err := Seal(secret)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	if sealed == secret || strings.Contains(sealed, secret) {
		t.Fatalf("密文里不该出现明文: %q", sealed)
	}
	if !Sealed(sealed) {
		t.Fatalf("Sealed 应认出自家密文: %q", sealed)
	}
	back, err := Open(sealed)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if back != secret {
		t.Fatalf("往返不一致: %q != %q", back, secret)
	}
}

// 空串 = 没有凭证，不当作错误。
func TestSealEmpty(t *testing.T) {
	s, err := Seal("")
	if err != nil || s != "" {
		t.Fatalf("空串应原样返回空: %q %v", s, err)
	}
	v, err := Open("")
	if err != nil || v != "" {
		t.Fatalf("空密文应解出空: %q %v", v, err)
	}
}

// 历史遗留明文（老库里直接躺着的密码）原样返回，不能因此读不出相机。
func TestOpenPlaintextLegacy(t *testing.T) {
	v, err := Open("plain-pass")
	if err != nil || v != "plain-pass" {
		t.Fatalf("明文应原样返回: %q %v", v, err)
	}
}

// 密文被改动/损坏 → 必须报错，不能悄悄返回垃圾。
func TestOpenTampered(t *testing.T) {
	sealed, err := Seal("token-abc")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	body := sealed[3:]
	// 改掉中间一个字符（末尾字符可能是 base64 padding，改了只是解码失败，测不出完整性）
	mid := len(body) / 2
	b := []byte(body)
	if b[mid] == 'A' {
		b[mid] = 'B'
	} else {
		b[mid] = 'A'
	}
	if _, err := Open(sealed[:3] + string(b)); err == nil {
		t.Fatal("损坏的密文不该解出结果")
	}
}

// 前缀错配（拿 d1 的体当 a1 读）也不许静默成功。
func TestOpenWrongPrefix(t *testing.T) {
	sealed, err := Seal("x")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	body := sealed[3:]
	if _, err := Open("a1:" + body); err == nil {
		// d1 体当 a1 读：base64 能解但 GCM 校验不过；反过来也一样
		t.Log("跨算法读居然成功了，检查前缀分派是否写反")
		t.Fatal("跨算法应解密失败")
	}
}

func TestMask(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"ab":           "****",
		"abcd":         "****",
		"Abc12345":     "****2345",
		"token-abcdef": "****cdef",
	}
	for in, want := range cases {
		if got := Mask(in); got != want {
			t.Fatalf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
}
