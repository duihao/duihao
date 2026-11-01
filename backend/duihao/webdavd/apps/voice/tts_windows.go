// Windows SAPI5 合成与播报（go-ole 直接调 COM，不依赖任何外部 exe）。
//
// 为什么是 SAPI 而不是 Web Speech API：出声必须由**后端**做，页面关掉、窗口最小化、
// 断网都要能响；浏览器 speechSynthesis 只在页面活着时有效，做不到「秘书」。
//
// 三条实测结论（2026-10-30 本机验证）：
//  1. 22kHz/16bit/mono 合成一句话约 40ms，258KB/5.8s，质量可用；
//  2. <rate absspeed> <volume level> <silence msec> 都生效；
//  3. <pitch absmiddle> 对 Microsoft Huihui 无效（引擎忽略），所以音色不做变调，
//     只靠语速/音量/停顿区分档位。
package voice

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

const (
	ssfmCreateForWrite = 3  // SpeechStreamFileMode
	saft22kHz16BitMono = 22 // SpeechAudioFormatType
)

// comMu COM 是线程亲和 + SAPI 实例不并发：合成来自 HTTP，播报来自调度队列，串行最省心。
var comMu sync.Mutex

// lcid 语言码：zh-CN 0x804、en-US 0x409。
func lcid(lang string) int {
	switch strings.ToLower(lang) {
	case "zh-cn", "zh":
		return 2052
	case "en-us", "en":
		return 1033
	}
	return 0
}

// withSAPI 在锁定的 OS 线程上初始化 COM，建一个 SpVoice 并挑好语音，交给 fn。
func withSAPI(voiceKey string, fn func(v *ole.IDispatch) error) error {
	comMu.Lock()
	defer comMu.Unlock()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// S_FALSE（线程上已按同种模式初始化过）和 RPC_E_CHANGED_MODE（模式不同）
	// 都不是失败：COM 已经能用，直接沿用，但**不能**配对 CoUninitialize —— 那是
	// 别人的计数；真坏了 CreateObject 会给出明确错误。
	// （踩过：Wails/WebView2 在自己的线程上初始化过 COM，goroutine 池会复用这些
	// 线程，CoInitializeEx 返回 S_FALSE 被 go-ole 当成错误 → 播报永远失败。）
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err == nil {
		defer ole.CoUninitialize()
	}

	unk, err := oleutil.CreateObject("SAPI.SpVoice")
	if err != nil {
		return fmt.Errorf("本机没有可用的语音引擎（SAPI）: %w", err)
	}
	defer unk.Release()

	v, err := unk.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return err
	}
	defer v.Release()

	selectVoice(v, voiceKey) // 挑不到就沿用系统默认，不让整次合成失败
	return fn(v)
}

// selectVoice 按音色预设的语言挑第一个匹配的系统语音。
func selectVoice(v *ole.IDispatch, voiceKey string) {
	lang, _, _, _ := resolveSpeech(voiceKey, "normal")
	id := lcid(lang)
	if id == 0 {
		return
	}
	// 注意：CallMethod 返回的 VARIANT 只能靠 Clear() 释放（它内部会 Release），
	// 再手动 Release() 就是双重释放 → 进程直接访问违例（踩过）。
	res, err := oleutil.CallMethod(v, "GetVoices", fmt.Sprintf("Language=%d", id))
	if err != nil || res == nil {
		if res != nil {
			res.Clear()
		}
		return
	}
	defer res.Clear()
	vd := res.ToIDispatch()
	if vd == nil {
		return
	}
	item, err := oleutil.CallMethod(vd, "Item", 0)
	if err != nil || item == nil {
		if item != nil {
			item.Clear()
		}
		return
	}
	defer item.Clear()
	if d := item.ToIDispatch(); d != nil {
		_, _ = oleutil.PutPropertyRef(v, "Voice", d)
	}
}

// synthWAV 合成到 out（绝对路径），返回时长毫秒。
func synthWAV(out, text, voiceKey, toneKey string) (int, error) {
	_, rate, vol, pause := resolveSpeech(voiceKey, toneKey)
	ssml := fmt.Sprintf(`<rate absspeed="%d"/><volume level="%d"/><silence msec="%d"/>%s`,
		rate, vol, pause, xmlEscape(text))

	err := withSAPI(voiceKey, func(v *ole.IDispatch) error {
		f, err := openWavStream(out)
		if err != nil {
			return err
		}
		defer f.Release()
		if _, err := oleutil.PutPropertyRef(v, "AudioOutputStream", f); err != nil {
			oleutil.CallMethod(f, "Close")
			return fmt.Errorf("绑定输出失败: %w", err)
		}
		if _, err := oleutil.CallMethod(v, "Speak", ssml, 0); err != nil { // 0 = 同步
			oleutil.CallMethod(f, "Close")
			return fmt.Errorf("合成失败: %w", err)
		}
		oleutil.CallMethod(f, "Close")
		return nil
	})
	if err != nil {
		return 0, err
	}
	ms := wavDurationMs(out)
	if ms <= 0 {
		return 0, errors.New("合成结果为空（这句念不出来？）")
	}
	return ms, nil
}

// speakText 直接念（不落盘）：调度器到点播报走这条，wav 丢了也不影响出声。
func speakText(text, voiceKey, toneKey string) error {
	_, rate, vol, pause := resolveSpeech(voiceKey, toneKey)
	ssml := fmt.Sprintf(`<rate absspeed="%d"/><volume level="%d"/><silence msec="%d"/>%s`,
		rate, vol, pause, xmlEscape(text))
	return withSAPI(voiceKey, func(v *ole.IDispatch) error {
		_, err := oleutil.CallMethod(v, "Speak", ssml, 0)
		if err != nil {
			return fmt.Errorf("播报失败: %w", err)
		}
		return nil
	})
}

// openWavStream 建一个写模式的文件流（22kHz/16bit/单声道，够清楚又不大）。
func openWavStream(out string) (*ole.IDispatch, error) {
	unk, err := oleutil.CreateObject("SAPI.SpFileStream")
	if err != nil {
		return nil, fmt.Errorf("创建文件流失败: %w", err)
	}
	defer unk.Release()
	f, err := unk.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return nil, err
	}

	if afUnk, err := oleutil.CreateObject("SAPI.SpAudioFormat"); err == nil {
		if af, err2 := afUnk.QueryInterface(ole.IID_IDispatch); err2 == nil {
			_, _ = oleutil.PutProperty(af, "Type", saft22kHz16BitMono)
			_, _ = oleutil.PutPropertyRef(f, "Format", af)
			af.Release()
		}
		afUnk.Release()
	}

	_ = os.Remove(out) // SpFileStream 不覆盖已有文件
	if _, err := oleutil.CallMethod(f, "Open", out, ssfmCreateForWrite); err != nil {
		f.Release()
		return nil, fmt.Errorf("打开输出文件失败: %w", err)
	}
	return f, nil
}

// 常见 token 的中文名（GetDescription 在部分机器上取不到，靠 Id 兜底）。
var voiceNames = map[string]string{
	"ZH-CN_HUIHUI":   "惠惠（简中女声）",
	"ZH-CN_KANGKANG": "康康（简中男声）",
	"ZH-CN_YAOYAO":   "瑶瑶（简中女声）",
	"EN-US_ZIRA":     "Zira（英文女声）",
	"EN-US_DAVID":    "David（英文男声）",
	"EN-US_MARK":     "Mark（英文男声）",
}

// systemVoices 列出本机 SAPI 语音（前端「语音设置」用）。
func systemVoices() []systemVoice {
	out := []systemVoice{}
	_ = withSAPI("", func(v *ole.IDispatch) error {
		res, err := oleutil.CallMethod(v, "GetVoices")
		if err != nil || res == nil {
			if res != nil {
				res.Clear()
			}
			return nil
		}
		defer res.Clear()
		vd := res.ToIDispatch()
		if vd == nil {
			return nil
		}
		n, err := oleutil.GetProperty(vd, "Count")
		if err != nil || n == nil {
			if n != nil {
				n.Clear()
			}
			return nil
		}
		count := int(n.Val)
		n.Clear()
		for i := 0; i < count; i++ {
			item, err := oleutil.CallMethod(vd, "Item", i)
			if err != nil || item == nil {
				if item != nil {
					item.Clear()
				}
				continue
			}
			d := item.ToIDispatch()
			tok, _ := oleutil.GetProperty(d, "Id")
			id := ""
			if tok != nil {
				id, _ = tok.Value().(string)
				tok.Clear()
			}
			item.Clear()
			out = append(out, describeToken(id))
		}
		return nil
	})
	return out
}

// describeToken 从 HKEY...\Tokens\TTS_MS_ZH-CN_HUIHUI_11.0 这样的 Id 里取语言与别名。
func describeToken(id string) systemVoice {
	token := id
	if i := strings.LastIndexByte(token, '\\'); i >= 0 {
		token = token[i+1:]
	}
	token = strings.TrimPrefix(token, "TTS_MS_")
	lang := ""
	for _, seg := range strings.Split(token, "_") {
		if len(seg) == 5 && seg[2] == '-' {
			lang = strings.ToLower(seg)
			break
		}
	}
	name := token
	for key, friendly := range voiceNames {
		if strings.Contains(strings.ToUpper(token), key) {
			name = friendly
			break
		}
	}
	return systemVoice{ID: id, Name: name, Lang: lang}
}

// xmlEscape SAPI 把参数当 XML 片段解析，用户文案里的 < & 必须转义。
func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}
