//go:build windows

package credentials

import (
	"encoding/base64"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows 走 DPAPI：由系统按「当前用户 + 当前机器」派生密钥，我们从不接触密钥本身。
// UI_FORBIDDEN 保证任何情况下都不弹系统框（后台进程弹框等于卡死）。

func sealWith(plain string) (string, string, error) {
	in := []byte(plain)
	var inBlob windows.DataBlob
	inBlob.Size = uint32(len(in))
	if len(in) > 0 {
		inBlob.Data = &in[0]
	}
	var out windows.DataBlob
	desc, err := windows.UTF16PtrFromString("duihao credential")
	if err != nil {
		return "", "", err
	}
	if err := windows.CryptProtectData(&inBlob, desc, nil, 0, nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", "", fmt.Errorf("加密失败: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	buf := unsafe.Slice(out.Data, int(out.Size))
	return base64.StdEncoding.EncodeToString(buf), prefixDPAPI, nil
}

func openWith(body, kind string) (string, error) {
	if kind != "dpapi" {
		return aesOpen(body)
	}
	raw, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return "", fmt.Errorf("密文损坏: %w", err)
	}
	var inBlob windows.DataBlob
	inBlob.Size = uint32(len(raw))
	if len(raw) > 0 {
		inBlob.Data = &raw[0]
	}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&inBlob, nil, nil, 0, nil,
		windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", fmt.Errorf("解密失败（凭证不是本机本用户加密的）: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return string(unsafe.Slice(out.Data, int(out.Size))), nil
}
