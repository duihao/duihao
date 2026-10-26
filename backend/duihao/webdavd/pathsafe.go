// 路径安全（v3.1 §6.2 加固 5 条的落地点）。
//
// 关键实现约束（M0 踩坑）：必须按「原始 URL 逐段」校验 ..，不能先
// filepath.Clean——Clean 会把 /../etc/passwd 折叠成 /etc/passwd，
// 校验永远不命中。这里两层防御：
//
//  1. CheckRequestPath：HTTP 中间件层，对解码前后的 URL 逐段校验
//     （.. / Windows 保留名 / 非法字符 / 盘符 / 控制字符）；
//  2. CheckRelPath：文件系统层，对 webdav.Handler 送进来的逻辑路径
//     再查一遍（防 URL 层之外的入口）。
//
// 两条路径都不通过 Clean 折叠——非法即拒绝，不尝试"修好"。
package webdavd

import (
	"fmt"
	"strings"
)

// windowsReserved Windows 保留设备名（大小写不敏感；带扩展名同样保留，
// 如 CON.txt 在资源管理器里也无法创建）。
var windowsReserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// forbiddenRunes Windows 文件名非法字符 + 控制字符检查由逐个 rune 完成。
// ':' 单列：同时挡 ADS 备用数据流（file.txt:stream）与盘符混淆。
func forbiddenRunes(name string) error {
	for _, r := range name {
		switch {
		case r < 0x20:
			return fmt.Errorf("包含控制字符")
		case strings.ContainsRune(`<>:"|?*`, r):
			return fmt.Errorf("包含非法字符 %q", r)
		}
	}
	return nil
}

// checkSegment 校验单个路径段（已解码）。空段合法（"a//b" 折叠为空段，无害）。
func checkSegment(seg string) error {
	if seg == "" || seg == "." {
		return nil
	}
	if seg == ".." {
		return fmt.Errorf("不允许路径穿越（..）")
	}
	if err := forbiddenRunes(seg); err != nil {
		return fmt.Errorf("段 %q %s", seg, err)
	}
	base := seg
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	if windowsReserved[strings.ToLower(base)] {
		return fmt.Errorf("段 %q 是 Windows 保留设备名", seg)
	}
	if strings.HasSuffix(seg, " ") || strings.HasSuffix(seg, ".") {
		// Windows 会截掉结尾的点与空格，造成 file. 与 file 同名覆盖。
		return fmt.Errorf("段 %q 以点或空格结尾（Windows 会被截断）", seg)
	}
	return nil
}

// CheckRequestPath 对 HTTP 请求路径做安全校验。
// decoded 与 raw 都要查：decoded 防明文 .. 与非法字符，raw 防 %2e%2e
// 之类的编码变形（RawPath 非空时与 Path 不一致）。
func CheckRequestPath(decoded, raw string) error {
	paths := []string{decoded}
	if raw != "" && raw != decoded {
		paths = append(paths, raw)
	}
	for _, p := range paths {
		for _, seg := range strings.Split(p, "/") {
			if err := checkSegment(seg); err != nil {
				return err
			}
		}
	}
	return nil
}

// CheckRelPath 校验 webdav 逻辑路径（/ 分隔、已解码）。返回 nil 表示安全。
func CheckRelPath(p string) error {
	if strings.Contains(p, "\x00") {
		return fmt.Errorf("包含 NUL 字符")
	}
	for _, seg := range strings.Split(p, "/") {
		if err := checkSegment(seg); err != nil {
			return err
		}
	}
	return nil
}

// CheckRoot 校验共享根目录（用户在界面上填的本地绝对路径）。
// 白名单规则：必须是绝对路径；拒绝盘符根（C:\ 整盘暴露）、UNC（\\server\share）。
// {dir} token（实例数据目录）合法——展开后是绝对路径，这里跳过绝对性检查。
func CheckRoot(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return fmt.Errorf("根目录不能为空")
	}
	if strings.HasPrefix(root, `\\`) || strings.HasPrefix(root, `//`) {
		return fmt.Errorf("不支持 UNC 网络路径（%s）", root)
	}
	if strings.HasPrefix(root, "{dir}") {
		return nil
	}
	vol := windowsVolumeName(filepathSlash(root))
	if vol != "" {
		rest := strings.TrimPrefix(filepathSlash(root), vol)
		rest = strings.TrimLeft(rest, `/\`)
		if rest == "" {
			return fmt.Errorf("根目录不能是整个盘（%s），请选一个具体目录", root)
		}
		return nil
	}
	if !strings.Contains(root, `:\`) && !strings.HasPrefix(root, `/`) {
		return fmt.Errorf("根目录必须是绝对路径（如 D:/www/dav）")
	}
	return nil
}

// filepathSlash 把 / 统一成 \（后续盘符/UNC 判断只在反斜杠语义下做）。
func filepathSlash(p string) string { return strings.ReplaceAll(p, "/", `\`) }

// windowsVolumeName 取盘符卷标（"D:\www" → "D:"）。不依赖 path 包，
// 规则极简：第一个 ':' 前如果只有一个字母就是盘符。
func windowsVolumeName(p string) string {
	i := strings.IndexByte(p, ':')
	if i == 1 && len(p) >= 2 && isLetter(p[0]) {
		return p[:2]
	}
	return ""
}

func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
