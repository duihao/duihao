//go:build !windows

package credentials

// 非 Windows 平台：AES-GCM 兜底（密钥派生见 aesKey）。
// v1 只发 Windows 版，这里存在的意义是让 linux/darwin 构建与单测不缺席。

func sealWith(plain string) (string, string, error) {
	body, err := aesSeal(plain)
	if err != nil {
		return "", "", err
	}
	return body, prefixAES, nil
}

func openWith(body, kind string) (string, error) {
	return aesOpen(body)
}
