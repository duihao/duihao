// 卡拉OK（M2）：LRC 歌词解析入库 + lyrics 接口。
//
// 存储：music_tracks.lyrics 为 JSON `[{t:秒,text}]`（按时间升序）。
// 解析口径：
//   - 行时间戳 [mm:ss.xx] / [mm:ss] / [hh:mm:ss.xx]，一行可带多个时间戳（展开成多行）；
//   - 元数据标签 [ti:][ar:][al:][by:][offset:] —— offset（毫秒）整体平移时间轴；
//   - 逐字增强标签 <mm:ss.xx> 直接剥掉（只做行级高亮）；
//   - 空行保留为空文本（当停顿显示）。
package music

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/duihao/duihao/backend/duihao/webdavd/appkit"
)

// lyricLine 一行歌词（t 秒，浮点保留一位小数）。
type lyricLine struct {
	T    float64 `json:"t"`
	Text string  `json:"text"`
}

// parseLRC 把 .lrc 文本解析为按时间排序的行。
func parseLRC(src string) []lyricLine {
	var offsetMS float64
	var out []lyricLine
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimRight(strings.TrimRight(raw, "\r"), " ")
		if line == "" {
			continue
		}
		// 抓行首所有 [xx:xx...] 标签，分清时间戳和元数据
		var times []float64
		var text = line
		for {
			if !strings.HasPrefix(text, "[") {
				break
			}
			end := strings.IndexByte(text, ']')
			if end < 0 {
				break
			}
			tag := text[1:end]
			text = text[end+1:]
			if sec, ok := parseLrcTime(tag); ok {
				times = append(times, sec)
				continue
			}
			if k, v, ok := strings.Cut(tag, ":"); ok && strings.EqualFold(k, "offset") {
				if ms, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					offsetMS = ms
				}
			}
			// 其他元数据（ti/ar/al/by…）丢弃
		}
		text = stripEnhancedLRC(text)
		if len(times) == 0 {
			if strings.TrimSpace(text) != "" {
				continue // 纯文本没有时间戳：无法对轴，丢弃
			}
			continue
		}
		for _, sec := range times {
			out = append(out, lyricLine{T: round1(sec), Text: text})
		}
	}
	if offsetMS != 0 {
		for i := range out {
			out[i].T = round1(out[i].T + offsetMS/1000)
			if out[i].T < 0 {
				out[i].T = 0
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].T < out[j].T })
	if out == nil {
		out = []lyricLine{}
	}
	return out
}

// parseLrcTime "mm:ss.xx" / "mm:ss" / "hh:mm:ss.xx" → 秒。
func parseLrcTime(tag string) (float64, bool) {
	parts := strings.Split(strings.TrimSpace(tag), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var sec float64
	for _, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return 0, false
		}
		sec = sec*60 + v
	}
	return sec, true
}

// stripEnhancedLRC 剥掉逐字增强标签 <mm:ss.xx>。
func stripEnhancedLRC(s string) string {
	if !strings.Contains(s, "<") {
		return strings.TrimSpace(s)
	}
	var b strings.Builder
	depth := false
	for _, r := range s {
		if r == '<' {
			depth = true
			continue
		}
		if r == '>' {
			depth = false
			continue
		}
		if !depth {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func round1(v float64) float64 { return mathRound(v*10) / 10 }

func mathRound(v float64) float64 {
	if v < 0 {
		return -float64(int(-v + 0.5))
	}
	return float64(int(v + 0.5))
}

// lrcForAudio 同目录同名 .lrc 的路径（不存在返回空）。
func lrcForAudio(full string) string {
	base := strings.TrimSuffix(full, filepath.Ext(full))
	for _, ext := range []string{".lrc", ".LRC"} {
		p := base + ext
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// readLRC 读并解析 .lrc 文件，失败返回空。
func readLRC(full string) []lyricLine {
	p := lrcForAudio(full)
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	if len(b) > 512<<10 {
		return nil // 512KB 封顶，异常大文件不当歌词
	}
	lines := parseLRC(string(b))
	if len(lines) == 0 {
		return nil
	}
	return lines
}

// lyricsJSON 序列化；空歌词返回空串。
func lyricsJSON(lines []lyricLine) string {
	if len(lines) == 0 {
		return ""
	}
	b, err := json.Marshal(lines)
	if err != nil {
		return ""
	}
	return string(b)
}

// lyricsGet 接口：{id} → {items:[{t,text}],has:bool}。
func lyricsGet(c appkit.Context, w http.ResponseWriter, r *http.Request) error {
	var in struct {
		ID string `json:"id"`
	}
	if err := appkit.Decode(r, &in); err != nil || strings.TrimSpace(in.ID) == "" {
		return appkit.Errorf(http.StatusBadRequest, "缺少 id")
	}
	var raw string
	if err := c.DB.QueryRow(`select lyrics from music_tracks where uid=? and id=?`,
		c.User, in.ID).Scan(&raw); err != nil {
		return appkit.Errorf(http.StatusNotFound, "歌曲不存在")
	}
	items := []lyricLine{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &items)
	}
	appkit.JSON(w, http.StatusOK, map[string]any{"items": items, "has": len(items) > 0})
	return nil
}
