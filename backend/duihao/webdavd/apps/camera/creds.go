// 相机凭证：地址与密码分开存。
//
// 为什么不在 URL 里带账号密码：
//   - hub.db 会被同步盘拉走、被备份工具打包、被用户拷到别的机器上；
//   - 一张 `cameras` 表里躺着 `rtsp://admin:hik12345@192.168.1.64/...`，
//     等于把全屋摄像头的钥匙放在明面上。
//
// 现在：url_main / url_sub 只留 host 与路径，用户名单独一列（不算机密），
// 密码走 credentials 包变成 `d1:…`/`a1:…` 密文。ffmpeg 启动前才拼回去，
// 拼接结果只活在内存里，不进库、不进日志。
package camera

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/duihao/duihao/backend/duihao/webdavd/credentials"
)

// splitCred 把 rtsp 地址里的 `user:pass@` 摘下来。
// 返回的 clean 是可以入库的地址。
func splitCred(raw string) (clean, user, pass string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw, "", ""
	}
	if u.User == nil {
		return raw, "", ""
	}
	user = u.User.Username()
	pass, _ = u.User.Password()
	u.User = nil
	return u.String(), user, pass
}

// withCred 把账号密码拼回地址（只在给 ffmpeg 用的时候调）。
func withCred(raw, user, pass string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || (user == "" && pass == "") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = url.UserPassword(user, pass)
	return u.String()
}

// sealPass 加密密码；空密码返回空串（表示这台相机不需要鉴权）。
func sealPass(pass string) (string, error) {
	if strings.TrimSpace(pass) == "" {
		return "", nil
	}
	return credentials.Seal(pass)
}

// camPass 解出密码。解不开是「换过机器 / 换过 Windows 账号」的典型症状，
// 直接给一句人能看懂的话，别抛 base64 乱码。
func camPass(cam Cam) (string, error) {
	if cam.CredSeal == "" {
		return "", nil
	}
	p, err := credentials.Open(cam.CredSeal)
	if err != nil {
		return "", fmt.Errorf("相机密码解不开（可能换过电脑或 Windows 账号），请重新填一次密码")
	}
	return p, nil
}

// streamURL 交给 ffmpeg 的地址：带上凭据。sub=true 优先子码流。
func streamURL(cam Cam, sub bool) string {
	raw := strings.TrimSpace(cam.URLSub)
	if !sub || raw == "" {
		raw = strings.TrimSpace(cam.URLMain)
	}
	if raw == "" {
		return ""
	}
	pass, err := camPass(cam)
	if err != nil {
		// 解不开就先按无密码连：让 ffmpeg 报 401，日志里另有明确提示
		pass = ""
	}
	return withCred(raw, cam.CredUser, pass)
}

// maskURL 日志与错误信息里显示的地址（不出现密码）。
// 不用 url.UserPassword 是为了避免 `***` 被转义成 %2A%2A%2A，日志里看不清。
func maskURL(raw string) string {
	clean, user, pass := splitCred(raw)
	if user == "" && pass == "" {
		return raw
	}
	if user == "" {
		user = "***"
	}
	i := strings.Index(clean, "://")
	if i < 0 {
		return clean
	}
	return clean[:i+3] + user + ":***@" + clean[i+3:]
}
