//go:build !windows

package voice

import "errors"

// 非 Windows 没有 winmm，GPU 合成的 wav 放不出来。返回错误让上层降级，
// 界面上会看到「当前平台不支持直接播放」而不是静默无声。
func playWavFile(path string) error {
	return errors.New("当前平台不支持直接播放合成音频")
}
