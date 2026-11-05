// 播报引擎设置：这台机器「用什么出声」。
//
// 两种引擎：
//
//	sapi  本机 Windows SAPI5（默认）。断网可用、40ms 出音、零费用，
//	      但音色就是系统装的那几个，做不出「AI 播音腔」。
//	http  局域网里那台 GPU 主机（CosyVoice2 / GPT-SoVITS / Fish-Speech，
//	      它们都自带 OpenAI 兼容的 /v1/audio/speech）。音色好得多，
//	      代价是必须联网到那台机器 —— 所以它永远是「可选增强」，
//	      合成失败一律降级回 SAPI，绝不出现「到点了但没声音」。
//
// 为什么设置是账号级的而不是逐条提醒：引擎是「这台机器怎么出声」的全局决定，
// 逐条配会让「换台 GPU 主机」变成要改十几条提醒。
package voice

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/duihao/duihao/backend/duihao/webdavd/credentials"
)

const (
	engineSAPI = "sapi"
	engineHTTP = "http"
)

// engineConfig 解析后的引擎配置（Token 已是明文，只在内存里停留）。
type engineConfig struct {
	Kind  string  `json:"kind"`  // sapi | http
	Base  string  `json:"base"`  // http://192.168.1.9:9880
	Token string  `json:"-"`     // 不出现给前端
	Model string  `json:"model"` // cosyvoice2 / tts-1 …
	Voice string  `json:"voice"` // 服务端音色名
	Speed float64 `json:"speed"` // 语速倍率
}

func defaultEngine() engineConfig {
	return engineConfig{Kind: engineSAPI, Speed: 1.0, Model: "cosyvoice2"}
}

// active 是否真的走 GPU（填了地址才算，否则等同 SAPI，别让半套配置把人卡住）。
func (e engineConfig) active() bool {
	return e.Kind == engineHTTP && strings.TrimSpace(e.Base) != ""
}

// key 音频缓存指纹：换引擎/地址/模型/音色/语速，同一句话必须重新合成，
// 否则切回 SAPI 会放出 GPU 留下的旧音频（反过来一样）。
func (e engineConfig) key() string {
	if !e.active() {
		return engineSAPI
	}
	return "http|" + e.Base + "|" + e.Model + "|" + e.Voice + "|" +
		strconv.FormatFloat(e.Speed, 'f', 2, 64)
}

// ---------- 读写 ----------

// 30 秒缓存：调度器每 15 秒扫一次库，不该每次都读设置表；改设置时主动清。
var engCache struct {
	mu sync.Mutex
	by map[string]cachedEngine
}

type cachedEngine struct {
	cfg engineConfig
	at  time.Time
}

func loadEngine(db *sql.DB, uid string) engineConfig {
	if db == nil {
		return defaultEngine()
	}
	engCache.mu.Lock()
	if c, ok := engCache.by[uid]; ok && time.Since(c.at) < 30*time.Second {
		engCache.mu.Unlock()
		return c.cfg
	}
	engCache.mu.Unlock()

	cfg := readEngine(db, uid)
	engCache.mu.Lock()
	if engCache.by == nil {
		engCache.by = map[string]cachedEngine{}
	}
	engCache.by[uid] = cachedEngine{cfg: cfg, at: time.Now()}
	engCache.mu.Unlock()
	return cfg
}

func invalidateEngine(uid string) {
	engCache.mu.Lock()
	delete(engCache.by, uid)
	engCache.mu.Unlock()
}

// readEngine 只读一次库（键值袋，未知键忽略——加键不必改这里）。
func readEngine(db *sql.DB, uid string) engineConfig {
	cfg := defaultEngine()
	rows, err := db.Query(`select k,v from voice_settings where uid=?`, uid)
	if err != nil {
		Logf("voice: 读引擎设置失败: %v", err)
		return cfg
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return cfg
		}
		switch k {
		case "engine":
			if v == engineHTTP {
				cfg.Kind = engineHTTP
			} else {
				cfg.Kind = engineSAPI
			}
		case "http.base":
			cfg.Base = normBase(v)
		case "http.token":
			if p, err := credentials.Open(v); err == nil {
				cfg.Token = p
			} else {
				Logf("voice: 令牌解不开（换过机器/用户？）: %v", err)
			}
		case "http.model":
			cfg.Model = strings.TrimSpace(v)
		case "http.voice":
			cfg.Voice = strings.TrimSpace(v)
		case "http.speed":
			if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0.25 && f <= 4 {
				cfg.Speed = f
			}
		}
	}
	return cfg
}

func putSetting(db *sql.DB, uid, k, v string) error {
	_, err := db.Exec(`insert into voice_settings(uid,k,v,updated_at) values(?,?,?,?)
		on conflict(uid,k) do update set v=excluded.v, updated_at=excluded.updated_at`,
		uid, k, v, time.Now().Format(time.RFC3339))
	return err
}

// saveEngine 落库。token 为空 = 不动原有令牌；clearToken 才清空。
func saveEngine(db *sql.DB, uid string, cfg engineConfig, token string, clearToken bool) error {
	pairs := [][2]string{
		{"engine", cfg.Kind},
		{"http.base", normBase(cfg.Base)},
		{"http.model", cfg.Model},
		{"http.voice", cfg.Voice},
		{"http.speed", strconv.FormatFloat(cfg.Speed, 'f', 2, 64)},
	}
	if clearToken {
		pairs = append(pairs, [2]string{"http.token", ""})
	} else if token != "" {
		sealed, err := credentials.Seal(token)
		if err != nil {
			return fmt.Errorf("令牌加密失败: %w", err)
		}
		pairs = append(pairs, [2]string{"http.token", sealed})
	}
	for _, p := range pairs {
		if err := putSetting(db, uid, p[0], p[1]); err != nil {
			return err
		}
	}
	invalidateEngine(uid)
	return nil
}

// normBase 规整地址：去尾斜杠、没写协议就补 http://。
func normBase(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, "/")
	if s == "" {
		return ""
	}
	low := strings.ToLower(s)
	if !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://") {
		s = "http://" + s
	}
	return s
}

// ---------- 给前端的形状 ----------

// engineJSON 前端用：绝不带明文令牌，只告诉它「有没有配」。
type engineJSON struct {
	Kind     string      `json:"kind"`
	Base     string      `json:"base"`
	Model    string      `json:"model"`
	Voice    string      `json:"voice"`
	Speed    float64     `json:"speed"`
	TokenSet bool        `json:"tokenSet"`
	Voices   []httpVoice `json:"voices"`
	Error    string      `json:"error,omitempty"`
}

func engineView(cfg engineConfig) engineJSON {
	v := engineJSON{Kind: cfg.Kind, Base: cfg.Base, Model: cfg.Model,
		Voice: cfg.Voice, Speed: cfg.Speed, TokenSet: cfg.Token != ""}
	v.Voices = cachedVoices(cfg.Base)
	if v.Voices == nil {
		v.Voices = []httpVoice{}
	}
	return v
}

// ---------- 音色名 ----------

// effVoice 真正用来合成的声音：
//   - http 引擎 → 服务端音色名（事件里存的是 sapi 键也不冲突，切引擎不会串）；
//   - sapi 引擎 → 沿用事件里的键，非法值落回默认。
func effVoice(e engineConfig, eventVoice string) string {
	if e.active() {
		// 逐条提醒指定的音色（lan:vivi）优先，没指定才用引擎里的默认音色。
		// 这样换 GPU 主机只改全局默认，个别提醒想用别的嗓子也不用动全局。
		if v, ok := strings.CutPrefix(strings.TrimSpace(eventVoice), "lan:"); ok && v != "" {
			return v
		}
		if e.Voice != "" {
			return e.Voice
		}
		return "default"
	}
	return pickKey(eventVoice, voicePresets, "huihui-std")
}
