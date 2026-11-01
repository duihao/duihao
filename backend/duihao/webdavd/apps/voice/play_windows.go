//go:build windows

package voice

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// GPU 合成出来的声音是 wav 文件，播放走 winmm 的 PlaySound：
// 不依赖任何播放器、不进系统播放列表，SND_SYNC 返回时已经念完了
// （调度器是串行队列，正好靠它把「连播」排好）。
const (
	sndSync      = 0x0000
	sndNoDefault = 0x0002
	sndFilename  = 0x00020000
)

var (
	winmm       = syscall.NewLazyDLL("winmm.dll")
	procPlaySnd = winmm.NewProc("PlaySoundW")
)

// playWavFile 阻塞播放到结束；文件缺失/格式不对返回错误（调用方降级到 SAPI）。
func playWavFile(path string) error {
	if st, err := os.Stat(path); err != nil || st.Size() < 64 {
		return fmt.Errorf("合成音频不可用：%s", path)
	}
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	r, _, _ := procPlaySnd.Call(uintptr(unsafe.Pointer(p)), 0,
		uintptr(sndFilename|sndNoDefault|sndSync))
	if r == 0 {
		return fmt.Errorf("播放失败：%s", path)
	}
	return nil
}
