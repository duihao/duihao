// 组件管理：ffmpeg 用得到才下载。
//
// 监控依赖 ffmpeg（预览转 MJPEG + 分段录像 -c copy），它有 80~100MB，
// 不该塞进安装包。取包与状态机在 compkit 里（下载模块也用同一套），
// 这里只放监控自己的那一条清单与几个薄包装，保持调用点不变。
//
// 关键取舍：**只装一个组件**。实时预览用 ffmpeg 转 MJPEG、录像用同一个
// ffmpeg 做 -c copy 分段，一份二进制两件事；等真要 <200ms 延迟的 WebRTC，
// 再往 componentList 里加 go2rtc，不动其它层。
package camera

import (
	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
	"github.com/duihao/duihao/backend/duihao/webdavd/compkit"
)

// componentList 监控用到的可选组件。
// ffmpeg 用 BtbN 的 **LGPL** 构建：我们只做 -c copy 与 MJPEG 转码，
// LGPL 足够，避免 GPL 传染分发。
var componentList = []compkit.Spec{{
	Tag:     "camera",
	ID:      "ffmpeg",
	Name:    "ffmpeg",
	Version: "7.1-lgpl",
	Bin:     "bin/ffmpeg.exe",
	Size:    95 << 20,
	Note:    "拉流、转 MJPEG 预览、分段录像（不解码不转码，CPU 占用低）",
	URLs: []string{
		"https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-lgpl.zip",
		"https://ghproxy.net/https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-lgpl.zip",
		"https://gh-proxy.com/https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-win64-lgpl.zip",
	},
}}

func specOf(id string) (compkit.Spec, bool) {
	for _, c := range componentList {
		if c.ID == id {
			return c, true
		}
	}
	return compkit.Spec{}, false
}

// binPath 已安装组件的可执行文件路径；未安装返回空串。
func binPath(id string) string {
	c, ok := specOf(id)
	if !ok {
		return ""
	}
	return compkit.Bin(c)
}

// ffmpeg 返回可用的 ffmpeg 路径（监控的所有功能都依赖它）。
func ffmpeg() (string, error) {
	if p := binPath("ffmpeg"); p != "" {
		return p, nil
	}
	return "", appkit.Errorf(409, "缺少运行组件 ffmpeg：请在页面点「下载并启用」")
}

// statusOf 组件状态（页面引导卡用）。
func statusOf(id string) compkit.Status {
	c, ok := specOf(id)
	if !ok {
		return compkit.Status{ID: id, State: "missing"}
	}
	return compkit.StatusOf(c)
}

// installAsync 后台下载组件（80MB 级，HTTP 不能挂着等）。
func installAsync(id string) error {
	c, ok := specOf(id)
	if !ok {
		return appkit.Errorf(404, "未知组件 %s", id)
	}
	if err := compkit.Install(c); err != nil {
		return appkit.Errorf(500, "%v", err)
	}
	return nil
}

// importZip 离线导入：把 zip 登记进 mirror/manifest.json，再走第 3 级取包。
func importZip(id, zipPath string) (string, error) {
	c, ok := specOf(id)
	if !ok {
		return "", appkit.Errorf(404, "未知组件 %s", id)
	}
	dir, err := compkit.Import(c, zipPath)
	if err != nil {
		return "", appkit.Errorf(400, "%v", err)
	}
	return dir, nil
}
