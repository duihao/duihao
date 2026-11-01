// 音色 / 语气 / 频率预设。
//
// 为什么音色只有 5 个且都是「惠惠·xx」：本机 Windows SAPI5 只装了桌面语音
// （简中 Microsoft Huihui + 英文 Zira 两个），截图里 Vivi 2.0 / 儒雅逸辰那类
// AI 音色要云端合成 + key + 联网，与「断网可用、数据不出机」冲突。
// 所以这里把音色定义成「系统声音 × 韵律档」，语气定义成「语速/音量/句前停顿」，
// UI 上如实标注「本机语音」——听起来真的不一样，且 40ms 出音、零费用。
// engine 字段是留给云端插件的缝：将来加 cloud:azure 只多一种 engine，不动表与前端。
package voice

// preset 音色与语气共用一套结构（前端直接渲染卡片 / chip）。
type preset struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Desc   string `json:"desc,omitempty"`
	Engine string `json:"engine,omitempty"` // sapi（预留 cloud:*）
	Lang   string `json:"lang,omitempty"`   // 用来挑本机语音：zh-CN / en-US
	Rate   int    `json:"rate"`             // SAPI 语速 -10..10
	Volume int    `json:"volume"`           // 0..100
	Pause  int    `json:"pause,omitempty"`  // 句前停顿毫秒
}

// voicePresets 音色档：同一个人，四种说法，外加一个英文声音。
var voicePresets = []preset{
	{Key: "huihui-std", Name: "惠惠 · 标准", Desc: "自然播报 · 中文本机语音", Engine: "sapi", Lang: "zh-CN", Rate: 0, Volume: 100},
	{Key: "huihui-soft", Name: "惠惠 · 温柔", Desc: "放慢轻声 · 适合关心与陪伴", Engine: "sapi", Lang: "zh-CN", Rate: -3, Volume: 85},
	{Key: "huihui-fast", Name: "惠惠 · 快报", Desc: "语速偏快 · 效率优先", Engine: "sapi", Lang: "zh-CN", Rate: 3, Volume: 100},
	{Key: "huihui-slow", Name: "惠惠 · 郑重", Desc: "逐字清楚 · 重要的事", Engine: "sapi", Lang: "zh-CN", Rate: -5, Volume: 100},
	{Key: "zira-en", Name: "Zira · English", Desc: "英文播报 · 内容写成英文时选它", Engine: "sapi", Lang: "en-US", Rate: 0, Volume: 100},
}

// tonePresets 语气档：只动语速/音量/句前停顿，不假装是 AI 情绪模型。
var tonePresets = []preset{
	{Key: "normal", Name: "通用", Rate: 0, Volume: 100, Pause: 0},
	{Key: "warm", Name: "温柔", Rate: -1, Volume: 90, Pause: 200},
	{Key: "cheer", Name: "开心", Rate: 2, Volume: 95, Pause: 100},
	{Key: "urge", Name: "着急", Rate: 3, Volume: 100, Pause: 60},
	{Key: "serious", Name: "严肃", Rate: -2, Volume: 100, Pause: 300},
	{Key: "sorry", Name: "抱歉", Rate: -2, Volume: 80, Pause: 300},
	{Key: "encourage", Name: "鼓励", Rate: 1, Volume: 100, Pause: 150},
	{Key: "cute", Name: "俏皮", Rate: 2, Volume: 95, Pause: 100},
	{Key: "calm", Name: "平静", Rate: -2, Volume: 85, Pause: 400},
	{Key: "strong", Name: "坚定", Rate: 1, Volume: 100, Pause: 200},
	{Key: "brief", Name: "播报", Rate: 1, Volume: 100, Pause: 80},
	{Key: "sleepy", Name: "睡前", Rate: -5, Volume: 65, Pause: 600},
}

// periodChoices 频率档（顺序 = 左栏顺序 = 弹窗顺序）。
var periodChoices = []struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Hint string `json:"hint"`
}{
	{"once", "单次", "到点响一次就结束"},
	{"repeat", "重复", "当天窗口内每隔 N 分钟，久坐/喝水/番茄钟"},
	{"daily", "每日", "每天同一时刻"},
	{"weekly", "每周", "选定星期几"},
	{"monthly", "每月", "每月同一天"},
	{"quarterly", "每季", "每 3 个月的同一天"},
	{"yearly", "每年", "每年同月同日（生日/纪念日）"},
}

// voicesFor 当前引擎可选的音色卡。
//
// GPU 引擎下列表来自服务端 /v1/voices；拉不到就给一条「默认音色」，
// 别让弹窗空着——用户可能就是想直接手填个名字先用起来。
func voicesFor(eng engineConfig) []preset {
	if !eng.active() {
		return voicePresets
	}
	base := hostOf(eng.Base)
	items := cachedVoices(eng.Base)
	if len(items) == 0 {
		name := eng.Voice
		if name == "" {
			name = "默认音色"
		}
		return []preset{{Key: "lan:" + name, Name: name,
			Desc: "GPU 合成 · " + base, Engine: "http", Volume: 100}}
	}
	out := make([]preset, 0, len(items))
	for _, it := range items {
		desc := "GPU 合成 · " + base
		if it.Lang != "" {
			desc = it.Lang + " · " + base
		}
		out = append(out, preset{Key: "lan:" + it.ID, Name: it.Name, Desc: desc,
			Engine: "http", Volume: 100})
	}
	return out
}

// resolveSpeech 把「音色 × 语气」合成一组 SAPI 参数。
func resolveSpeech(voiceKey, toneKey string) (lang string, rate, vol, pause int) {
	v := voicePresets[0]
	for _, p := range voicePresets {
		if p.Key == voiceKey {
			v = p
			break
		}
	}
	t := tonePresets[0]
	for _, p := range tonePresets {
		if p.Key == toneKey {
			t = p
			break
		}
	}
	rate = clampInt(v.Rate+t.Rate, -10, 10)
	vol = t.Volume
	if vol <= 0 || vol > 100 {
		vol = v.Volume
	}
	if t.Key == "normal" { // 通用语气不自作主张改音量
		vol = v.Volume
	}
	return v.Lang, rate, vol, t.Pause
}
