package camera

import "strings"

// vendorPreset 品牌 RTSP 地址模板。
//
// 现实里没人记得住海康的 /Streaming/Channels/102 还是大华的 /cam/realmonitor?subtype=1，
// 所以页面选品牌 + 填 IP/账号，地址由这里拼。拼出来的仍然是普通 rtsp:// 地址，
// 用户可以在「高级」里直接改，模板不是黑箱。
type vendorPreset struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Note string `json:"note"`
	Main string `json:"-"` // 主码流模板（占位符 {host} {port} {user} {pass} {ch}）
	Sub  string `json:"-"`
	// Tip 页面提示（比如「默认没开 ONVIF，要去 Web 后台打开」）
	Tip string `json:"tip"`
}

var vendorPresetsList = []vendorPreset{
	{
		Key:  "hikvision",
		Name: "海康威视 Hikvision",
		Note: "子码流 102，主码流 101",
		Main: "rtsp://{user}:{pass}@{host}:{port}/Streaming/Channels/{ch}01",
		Sub:  "rtsp://{user}:{pass}@{host}:{port}/Streaming/Channels/{ch}02",
		Tip:  "若提示鉴权失败，到相机 Web 后台「网络 → 高级设置 → 集成协议」打开 ONVIF 并新建用户",
	},
	{
		Key:  "dahua",
		Name: "大华 Dahua",
		Note: "subtype 0 主码流 / 1 子码流",
		Main: "rtsp://{user}:{pass}@{host}:{port}/cam/realmonitor?channel={ch}&subtype=0",
		Sub:  "rtsp://{user}:{pass}@{host}:{port}/cam/realmonitor?channel={ch}&subtype=1",
		Tip:  "大华默认密码策略较严，首次登录后需在 Web 后台改过密码才能用 RTSP",
	},
	{
		Key:  "uniview",
		Name: "宇视 Uniview",
		Note: "video1 主码流 / video2 子码流",
		Main: "rtsp://{user}:{pass}@{host}:{port}/media/video1",
		Sub:  "rtsp://{user}:{pass}@{host}:{port}/media/video2",
	},
	{
		Key:  "tplink",
		Name: "TP-Link",
		Note: "stream1 / stream2",
		Main: "rtsp://{user}:{pass}@{host}:{port}/stream1",
		Sub:  "rtsp://{user}:{pass}@{host}:{port}/stream2",
	},
	{
		Key:  "ezviz",
		Name: "萤石 Ezviz",
		Note: "需在萤石云开「RTSP 取流」后用验证码当密码",
		Main: "rtsp://admin:{pass}@{host}:{port}/Streaming/Channels/101",
		Sub:  "rtsp://admin:{pass}@{host}:{port}/Streaming/Channels/102",
	},
	{
		Key:  "custom",
		Name: "其它 / 自定义",
		Note: "直接填完整 RTSP 地址",
		Tip:  "不知道地址时，可以在「发现」里扫描局域网相机，或查相机说明书",
	},
}

func vendorPresets() []vendorPreset { return vendorPresetsList }

func presetOf(key string) vendorPreset {
	for _, p := range vendorPresetsList {
		if p.Key == key {
			return p
		}
	}
	return vendorPreset{}
}

// build 按模板拼 RTSP 地址；custom 或模板缺失时返回空（交给用户手填）。
func (p vendorPreset) build(host string, port int, user, pass string, ch int, sub bool) string {
	tpl := p.Main
	if sub {
		tpl = p.Sub
	}
	if tpl == "" {
		return ""
	}
	if user == "" {
		user = "admin" // 海康/大华出厂都是 admin
	}
	r := strings.NewReplacer(
		"{host}", host,
		"{port}", itoa(port),
		"{user}", user,
		"{pass}", pass,
		"{ch}", itoa(ch),
	)
	return r.Replace(tpl)
}

func itoa(v int) string {
	if v <= 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
