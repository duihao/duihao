//go:build !windows

// 非 Windows 平台的空实现：语音提醒目前只支持 Windows（SAPI5）。
// 接口照旧，前端会拿到明确的提示而不是 500 堆栈。
package voice

import "errors"

var errUnsupported = errors.New("语音提醒目前只支持 Windows（本机语音引擎是 SAPI5）")

func synthWAV(out, text, voiceKey, toneKey string) (int, error) { return 0, errUnsupported }

func speakText(text, voiceKey, toneKey string) error { return errUnsupported }

func systemVoices() []systemVoice { return []systemVoice{} }

func openTarget(arg string) error { return errUnsupported }
