//go:build windows

package licence

import (
	"errors"
	"strings"

	"github.com/danieljoos/wincred"
)

// Windows：凭据管理器（DPAPI 按当前用户加密，拷到别的机器/别的用户读不出明文）。
// 与 backend/sync 的 keychain 同一套约定：target = "<service>:<key>"。
const credService = "duihao-desktop"
const credKey = "licence"

type wincredStorage struct{}

func platformStorage() Storage { return wincredStorage{} }

func (wincredStorage) Load() (string, error) {
	c, err := wincred.GetGenericCredential(credService + ":" + credKey)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || errors.Is(err, wincred.ErrElementNotFound) {
			return "", nil
		}
		return "", err
	}
	return string(c.CredentialBlob), nil
}

func (wincredStorage) Save(raw string) error {
	// 空串 = 清除
	if raw == "" {
		c := wincred.NewGenericCredential(credService + ":" + credKey)
		return c.Delete()
	}
	c := wincred.NewGenericCredential(credService + ":" + credKey)
	c.CredentialBlob = []byte(raw)
	return c.Write()
}
