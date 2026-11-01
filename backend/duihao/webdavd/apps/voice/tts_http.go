// 局域网 GPU 语音：走 OpenAI 兼容接口（/v1/audio/speech、/v1/voices）。
//
// 为什么是 OpenAI 兼容而不是各家私有协议：CosyVoice2、GPT-SoVITS、Fish-Speech、
// XTTS 的推理服务端（如 fish-speech 的 api_server、CosyVoice 的 fastapi 示例）
// 都提供这一套最小接口。对号只认这两条路径，用户换实现不用改对号。
//
// 三条硬约束：
//   - 一律要 wav（mp3 也能放，但要额外解码器，V1 不为它引入依赖）；
//   - 合成失败必须能降级，绝不出现「到点了但没声音」；
//   - 客户端不走系统代理（本机 127.0.0.1 常被代理软件劫持，踩过）。
package voice

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// httpVoice 服务端一个音色（有的服务端只给 id，name 就退化成 id）。
type httpVoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Lang string `json:"lang,omitempty"`
}

func client() *http.Client {
	return &http.Client{
		Timeout: 90 * time.Second,
		Transport: &http.Transport{
			Proxy: nil, // 见文件头第三条
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}
}

// synthHTTPTo 合成到 out（绝对路径），返回时长毫秒。
func synthHTTPTo(out string, e engineConfig, text string, rate int) (int, error) {
	if !e.active() {
		return 0, errors.New("没配 GPU 主机地址")
	}
	body := map[string]any{
		"input":           text,
		"voice":           effVoice(e, ""),
		"response_format": "wav",
		"speed":           httpSpeed(e.Speed, rate),
	}
	if e.Model != "" {
		body["model"] = e.Model
	}
	raw, _ := json.Marshal(body)

	req, err := http.NewRequest(http.MethodPost, e.Base+"/v1/audio/speech", bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.Token != "" {
		req.Header.Set("Authorization", "Bearer "+e.Token)
	}

	resp, err := client().Do(req)
	if err != nil {
		return 0, fmt.Errorf("连不上 %s（查地址与防火墙）: %w", hostOf(e.Base), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, fmt.Errorf("合成服务返回 %d %s", resp.StatusCode, snippet(string(msg)))
	}
	audio, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return 0, fmt.Errorf("读取合成结果失败: %w", err)
	}
	if !isWAV(audio) {
		return 0, fmt.Errorf("服务端返回的不是 wav（%d 字节，Content-Type %s）：请在服务端指定 response_format=wav",
			len(audio), resp.Header.Get("Content-Type"))
	}
	if err := writeFileAtomic(out, audio); err != nil {
		return 0, err
	}
	ms := wavDurationMs(out)
	if ms <= 0 {
		return 0, errors.New("合成结果为空（这句念不出来？）")
	}
	return ms, nil
}

// listHTTPVoices 拉服务端音色列表。各家返回形状不一，这里都认：
//
//	{"voices":["a","b"]}  {"voices":[{"id":..,"name":..}]}  {"data":[{"id":..}]}
func listHTTPVoices(e engineConfig) ([]httpVoice, error) {
	if strings.TrimSpace(e.Base) == "" {
		return nil, errors.New("还没填 GPU 主机地址")
	}
	req, err := http.NewRequest(http.MethodGet, e.Base+"/v1/voices", nil)
	if err != nil {
		return nil, err
	}
	if e.Token != "" {
		req.Header.Set("Authorization", "Bearer "+e.Token)
	}
	c := client()
	c.Timeout = 12 * time.Second
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连不上 %s: %w", hostOf(e.Base), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("服务端拒绝访问（%d）：检查令牌", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("音色列表返回 %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	items := parseVoices(raw)
	setVoices(e.Base, items)
	return items, nil
}

// parseVoices 解析各种形状（导出便于测试）。
func parseVoices(raw []byte) []httpVoice {
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		out := make([]httpVoice, 0, len(arr))
		for _, s := range arr {
			out = append(out, httpVoice{ID: s, Name: s})
		}
		return out
	}
	var obj struct {
		Voices []json.RawMessage `json:"voices"`
		Data   []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return []httpVoice{}
	}
	list := obj.Voices
	if len(list) == 0 {
		list = obj.Data
	}
	out := make([]httpVoice, 0, len(list))
	for _, it := range list {
		var s string
		if err := json.Unmarshal(it, &s); err == nil {
			out = append(out, httpVoice{ID: s, Name: s})
			continue
		}
		var o struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Lang string `json:"lang"`
		}
		if err := json.Unmarshal(it, &o); err != nil {
			continue
		}
		if o.ID == "" {
			o.ID = o.Name
		}
		if o.Name == "" {
			o.Name = o.ID
		}
		out = append(out, httpVoice{ID: o.ID, Name: o.Name, Lang: o.Lang})
	}
	return out
}

// probeEngine 测试连接：拉一次音色列表，顺带量一下往返耗时。
func probeEngine(e engineConfig) (int, []httpVoice, error) {
	t0 := time.Now()
	items, err := listHTTPVoices(e)
	return int(time.Since(t0).Milliseconds()), items, err
}

// httpSpeed 语速：引擎倍率 × SAPI 语速档（-10..10 折成 ±50%），夹在 0.5~2.0。
func httpSpeed(base float64, rate int) float64 {
	if base <= 0 {
		base = 1
	}
	s := base * (1 + float64(rate)/20)
	if s < 0.5 {
		s = 0.5
	}
	if s > 2.0 {
		s = 2.0
	}
	return float64(int(s*100+0.5)) / 100 // 两位小数，前端好看
}

// ---------- 音色缓存（前端选音色、试听都能用，不必每次打服务端） ----------

var voiceCache struct {
	mu sync.Mutex
	by map[string]voiceCacheEntry
}

type voiceCacheEntry struct {
	items []httpVoice
	at    time.Time
}

func setVoices(base string, items []httpVoice) {
	if base == "" {
		return
	}
	voiceCache.mu.Lock()
	if voiceCache.by == nil {
		voiceCache.by = map[string]voiceCacheEntry{}
	}
	voiceCache.by[base] = voiceCacheEntry{items: items, at: time.Now()}
	voiceCache.mu.Unlock()
}

func cachedVoices(base string) []httpVoice {
	voiceCache.mu.Lock()
	defer voiceCache.mu.Unlock()
	e, ok := voiceCache.by[base]
	if !ok || time.Since(e.at) > 30*time.Minute {
		return nil
	}
	return e.items
}

// ---------- 小工具 ----------

func hostOf(base string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(base, "http://"), "https://")
	if i := strings.IndexAny(s, "/"); i >= 0 {
		s = s[:i]
	}
	return s
}

func snippet(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return s
}

// writeFileAtomic 先写 .part 再改名：合成一半断流不会留下半截 wav 被当成缓存。
func writeFileAtomic(out string, data []byte) error {
	tmp := out + ".part"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
