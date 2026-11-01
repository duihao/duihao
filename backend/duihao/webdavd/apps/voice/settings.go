// 播报引擎的设置接口与连通性测试。
//
// 两条原则：
//   - 令牌只进不出：settings 只回 tokenSet 布尔，永不回明文；保存时
//     「传空 = 不改」，要清空必须显式 clearToken；
//   - 测试连接用的是「表单里填的」地址与令牌，而不是已存的 —— 用户要能
//     在按保存之前就试出「到底连不连得上」。
package voice

import (
	"net/http"
	"strings"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
)

// saveSettings 保存引擎设置。
func saveSettings(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Kind       string  `json:"kind"`
		Base       string  `json:"base"`
		Model      string  `json:"model"`
		Voice      string  `json:"voice"`
		Speed      float64 `json:"speed"`
		Token      string  `json:"token"`
		ClearToken bool    `json:"clearToken"`
	}
	if err := appkit.Decode(r, &in); err != nil {
		return appkit.Errorf(http.StatusBadRequest, "参数解析失败：%v", err)
	}
	kind := strings.TrimSpace(in.Kind)
	if kind != engineHTTP {
		kind = engineSAPI
	}
	base := normBase(in.Base)
	if kind == engineHTTP && base == "" {
		return appkit.Errorf(http.StatusBadRequest, "选了 GPU 主机就得填地址，例如 http://192.168.1.9:9880")
	}
	if base != "" && !strings.HasPrefix(strings.ToLower(base), "http://") &&
		!strings.HasPrefix(strings.ToLower(base), "https://") {
		return appkit.Errorf(http.StatusBadRequest, "地址要以 http:// 或 https:// 开头")
	}
	speed := in.Speed
	if speed <= 0 {
		speed = 1
	}
	if speed < 0.5 {
		speed = 0.5
	}
	if speed > 2 {
		speed = 2
	}

	cfg := engineConfig{
		Kind:  kind,
		Base:  base,
		Model: strings.TrimSpace(in.Model),
		Voice: strings.TrimSpace(in.Voice),
		Speed: speed,
	}
	if err := saveEngine(c.DB, c.User, cfg, strings.TrimSpace(in.Token), in.ClearToken); err != nil {
		return err
	}
	// 换了地址就顺手拉一次音色列表（失败不管，页面会显示空列表 + 测试按钮）
	saved := loadEngine(c.DB, c.User)
	if saved.active() {
		_, _ = listHTTPVoices(saved)
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"ok": true, "engine": engineView(loadEngine(c.DB, c.User))})
	return nil
}

// testEngine 测试连接并返回服务端音色列表。
func testEngine(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Base  string `json:"base"`
		Model string `json:"model"`
		Token string `json:"token"`
	}
	_ = appkit.Decode(r, &in) // 允许空 body：直接测已存的配置

	saved := loadEngine(c.DB, c.User)
	cfg := engineConfig{
		Kind:  engineHTTP,
		Base:  normBase(in.Base),
		Model: strings.TrimSpace(in.Model),
		Token: strings.TrimSpace(in.Token),
		Speed: 1,
	}
	if cfg.Base == "" {
		cfg.Base = saved.Base
	}
	if cfg.Token == "" {
		cfg.Token = saved.Token // 表单没重填就用已存的，省得每次重新粘令牌
	}
	if cfg.Base == "" {
		return appkit.Errorf(http.StatusBadRequest, "先填 GPU 主机地址")
	}

	ms, items, err := probeEngine(cfg)
	if err != nil {
		return appkit.Errorf(http.StatusBadGateway, "%v", err)
	}
	appkit.JSON(w, http.StatusOK, map[string]any{
		"ok": true, "ms": ms, "voices": items, "count": len(items),
		"hint": "能连上。若音色列表为空，服务端可能没实现 /v1/voices —— 直接在音色里手填名称即可。",
	})
	return nil
}
