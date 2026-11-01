// 音频标签解析（纯 Go，不引第三方 tag 库）：
//
//	MP3  → ID3v2.2/2.3/2.4（含 APIC 内嵌封面），缺标签时退 ID3v1；
//	       时长优先 Xing/Info 头（精确），退码率估算；
//	FLAC → Vorbis 注释 + PICTURE 封面 + STREAMINFO 精确时长；
//	WAV  → fmt/data 块算时长（基本无标签）；
//	其他（m4a/ape/wma…）→ 标签留空，曲名用文件名，时长 0（播放时浏览器拿到精确值）。
//
// 中文兜底：大量国产 MP3 把 GBK 文本塞进 ISO-8859-1 编码位，检测到高位字节
// 就按 GBK 解（x/text 是既有直接依赖，不新增包）。
package music

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// tags 一次解析得到的标签集。
type tags struct {
	Title       string
	Artist      string
	Album       string
	AlbumArtist string
	Genre       string
	Year        int
	TrackNo     int
	DiscNo      int
	Duration    int // 秒
	Bitrate     int // kbps
	Cover       []byte
	CoverExt    string // ".jpg" / ".png"
}

// readTags 打开音频文件解析标签；不认识的格式返回零值 tags（err 仍为 nil）。
func readTags(path string, ext string) (tags, error) {
	var t tags
	f, err := os.Open(path)
	if err != nil {
		return t, err
	}
	defer f.Close()
	switch ext {
	case ".mp3":
		return t, parseMP3(f, &t)
	case ".flac":
		return t, parseFLAC(f, &t)
	case ".wav":
		return t, parseWAV(f, &t)
	}
	return t, nil
}

// ─── MP3 / ID3 ───

func parseMP3(f *os.File, t *tags) error {
	head := make([]byte, 10)
	if _, err := io.ReadFull(f, head); err != nil {
		return nil // 文件太小，放弃
	}
	audioStart := 0
	if string(head[:3]) == "ID3" {
		size := syncsafe(head[6:10])
		if size > 16<<20 {
			size = 16 << 20 // 封顶 16MB（正常标签几十 KB，防坏文件）
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(f, body); err != nil {
			return nil
		}
		ver := head[3]
		if head[5]&0x80 != 0 {
			body = deUnsync(body)
		}
		parseID3Frames(ver, body, t)
		audioStart = 10 + size
		if head[5]&0x10 != 0 { // footer
			audioStart += 10
		}
		if t.Title == "" || t.Artist == "" {
			// 关键字段缺 → 试 ID3v1（最后 128 字节）
			var v1 tags
			if parseID3v1(f, &v1) {
				if t.Title == "" {
					t.Title = v1.Title
				}
				if t.Artist == "" {
					t.Artist = v1.Artist
				}
				if t.Album == "" {
					t.Album = v1.Album
				}
				if t.Year == 0 {
					t.Year = v1.Year
				}
			}
		}
	}
	mp3Duration(f, int64(audioStart), t)
	return nil
}

// parseID3Frames 遍历标签帧（body 是去掉 10 字节头的标签体）。
func parseID3Frames(ver byte, body []byte, t *tags) {
	idLen, sizeLen := 4, 4
	if ver == 2 { // v2.2：3 字节 ID + 3 字节长度
		idLen, sizeLen = 3, 3
	}
	for len(body) >= idLen+sizeLen {
		id := string(body[:idLen])
		if id[0] == 0 { // padding 到头了
			return
		}
		raw := body[idLen : idLen+sizeLen]
		var size int
		switch {
		case ver == 2:
			size = int(raw[0])<<16 | int(raw[1])<<8 | int(raw[2])
		case ver == 4:
			size = syncsafe(raw)
		default: // v2.3 普通大端
			size = int(binary.BigEndian.Uint32(raw))
		}
		rest := body[idLen+sizeLen:]
		// v2.3/2.4 帧头在长度后还有 2 字节标志；v2.2 没有
		hdrExtra := 0
		if ver >= 3 {
			hdrExtra = 2
		}
		if size < 0 || len(rest) < size+hdrExtra {
			return
		}
		// 压缩/加密的帧解不了，跳过
		if hdrExtra == 2 && rest[1]&0xC0 != 0 {
			body = rest[hdrExtra+size:]
			continue
		}
		data := rest[hdrExtra : hdrExtra+size]
		body = rest[hdrExtra+size:]
		switch id {
		case "TIT2", "TT2":
			t.Title = frameText(data)
		case "TPE1", "TP1":
			t.Artist = frameText(data)
		case "TALB", "TAL":
			t.Album = frameText(data)
		case "TPE2", "TP2":
			t.AlbumArtist = frameText(data)
		case "TCON", "TCO":
			t.Genre = cleanGenre(frameText(data))
		case "TYER", "TYE", "TDRC":
			if y := atoi4s(strings.TrimSpace(frameText(data))); y > 0 {
				t.Year = y
			}
		case "TRCK", "TRK":
			t.TrackNo = pairNums(frameText(data))[0]
		case "TPOS", "TPA":
			t.DiscNo = pairNums(frameText(data))[0]
		case "APIC", "PIC":
			if c, ext := framePic(ver, data); len(c) > 0 && len(t.Cover) == 0 {
				t.Cover, t.CoverExt = c, ext
			}
		}
	}
}

// frameText 解析文本帧（首字节编码 + 文本，多个值只取第一个）。
func frameText(data []byte) string {
	if len(data) < 1 {
		return ""
	}
	return decodeText(data[0], data[1:])
}

// framePic 解析 APIC/PIC 帧。
func framePic(ver byte, data []byte) ([]byte, string) {
	if len(data) < 4 {
		return nil, ""
	}
	enc := data[0]
	mime := ""
	p := 1
	if ver == 2 { // v2.2：3 字节图像格式
		if len(data) < 4 {
			return nil, ""
		}
		mime = strings.ToLower(string(data[1:4]))
		p = 4
	} else { // v2.3/2.4：null 结尾的 mime
		i := bytes.IndexByte(data[1:], 0)
		if i < 0 {
			return nil, ""
		}
		mime = strings.ToLower(string(data[1 : 1+i]))
		p = 2 + i
	}
	if p >= len(data) {
		return nil, ""
	}
	p++ // picture type
	// 描述按编码定终止符宽度（utf16 双 0）
	if enc == 1 || enc == 2 {
		for p+1 < len(data) {
			if data[p] == 0 && data[p+1] == 0 {
				p += 2
				break
			}
			p += 2
		}
	} else {
		for p < len(data) && data[p] != 0 {
			p++
		}
		p++
	}
	if p >= len(data) {
		return nil, ""
	}
	ext := ".jpg"
	if strings.Contains(mime, "png") {
		ext = ".png"
	}
	return data[p:], ext
}

// decodeText 按 ID3 编码字节解文本。
func decodeText(enc byte, b []byte) string {
	// 去掉结尾的 NUL（多值分隔也一并截断到第一个值）
	switch enc {
	case 0: // ISO-8859-1：实际常见 GBK
		if i := bytes.IndexByte(b, 0); i >= 0 {
			b = b[:i]
		}
		if !hasHighByte(b) {
			return strings.TrimSpace(string(b))
		}
		if s, err := simplifiedchinese.GBK.NewDecoder().Bytes(b); err == nil {
			return strings.TrimSpace(string(s))
		}
		return strings.TrimSpace(string(b))
	case 1: // UTF-16 + BOM
		return trimSpace(utf16Decode(b))
	case 2: // UTF-16BE
		return trimSpace(utf16DecodeBEBare(b))
	default: // 3 = UTF-8
		if i := bytes.IndexByte(b, 0); i >= 0 {
			b = b[:i]
		}
		return strings.TrimSpace(string(b))
	}
}

func utf16Decode(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	var be bool
	switch {
	case b[0] == 0xFE && b[1] == 0xFF:
		be, b = true, b[2:]
	case b[0] == 0xFF && b[1] == 0xFE:
		b = b[2:]
	}
	if be {
		return utf16DecodeBE(b)
	}
	return utf16DecodeLE(b)
}

func utf16DecodeLE(b []byte) string {
	if len(b)%2 == 1 {
		b = b[:len(b)-1]
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(u))
}

func utf16DecodeBE(b []byte) string {
	if len(b)%2 == 1 {
		b = b[:len(b)-1]
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.BigEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(u))
}

func utf16DecodeBEBare(b []byte) string { return utf16DecodeBE(b) }

// parseID3v1 兜底：最后 128 字节。
func parseID3v1(f *os.File, t *tags) bool {
	st, err := f.Stat()
	if err != nil || st.Size() < 128 {
		return false
	}
	buf := make([]byte, 128)
	if _, err := f.ReadAt(buf, st.Size()-128); err != nil || string(buf[:3]) != "TAG" {
		return false
	}
	cut := func(b []byte) string {
		if i := bytes.IndexByte(b, 0); i >= 0 {
			b = b[:i]
		}
		if hasHighByte(b) {
			if s, e := simplifiedchinese.GBK.NewDecoder().Bytes(b); e == nil {
				return strings.TrimSpace(string(s))
			}
		}
		return strings.TrimSpace(string(b))
	}
	t.Title, t.Artist, t.Album = cut(buf[3:33]), cut(buf[33:63]), cut(buf[63:93])
	t.Year = atoi4s(cut(buf[93:97]))
	return true
}

// mp3Duration 时长：Xing/Info 头精确，退码率估算。
func mp3Duration(f *os.File, start int64, t *tags) {
	st, err := f.Stat()
	if err != nil || st.Size() <= start {
		return
	}
	size := st.Size() - start
	buf := make([]byte, 4096)
	n, _ := f.ReadAt(buf, start)
	buf = buf[:n]
	// 找第一个帧头
	for i := 0; i+4 <= len(buf); i++ {
		if buf[i] != 0xFF || buf[i+1]&0xE0 != 0xE0 {
			continue
		}
		verBits := (buf[i+1] >> 3) & 3 // 3=MPEG1 2=MPEG2 0=MPEG2.5
		layer := (buf[i+1] >> 1) & 3   // 1=Layer III
		if layer != 1 || verBits == 1 {
			continue
		}
		brIdx := buf[i+2] >> 4
		srIdx := (buf[i+2] >> 2) & 3
		sr := sampleRateOf(verBits, srIdx)
		br := bitRateOf(verBits, brIdx)
		if sr == 0 || br == 0 {
			return
		}
		spf := 1152
		if verBits != 3 {
			spf = 576
		}
		mono := (buf[i+3]>>6)&3 == 3
		side := 32
		if verBits == 3 {
			if mono {
				side = 17
			}
		} else if mono {
			side = 9
		} else {
			side = 17
		}
		t.Bitrate = br
		// Xing/Info 头位置
		if x := i + 4 + side; x+16 <= len(buf) {
			tag := string(buf[x : x+4])
			if tag == "Xing" || tag == "Info" {
				flags := binary.BigEndian.Uint32(buf[x+4 : x+8])
				if flags&1 != 0 && x+12 <= len(buf) {
					frames := binary.BigEndian.Uint32(buf[x+8 : x+12])
					if frames > 0 {
						t.Duration = int(uint64(frames) * uint64(spf) / uint64(sr))
						if t.Duration > 0 {
							t.Bitrate = int(uint64(size) * 8 / uint64(t.Duration) / 1000)
						}
						return
					}
				}
			}
		}
		// CBR 估算
		t.Duration = int(uint64(size) * 8 / (uint64(br) * 1000))
		return
	}
}

// ─── FLAC ───

func parseFLAC(f *os.File, t *tags) error {
	head := make([]byte, 4)
	if _, err := io.ReadFull(f, head); err != nil || string(head) != "fLaC" {
		return nil
	}
	hdr := make([]byte, 4)
	for i := 0; i < 32; i++ { // 元数据块数量有上限，防坏文件死循环
		if _, err := io.ReadFull(f, hdr); err != nil {
			return nil
		}
		last := hdr[0]&0x80 != 0
		typ := hdr[0] & 0x7F
		size := int(hdr[1])<<16 | int(hdr[2])<<8 | int(hdr[3])
		if size > 32<<20 {
			return nil
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(f, body); err != nil {
			return nil
		}
		switch typ {
		case 0: // STREAMINFO
			if len(body) >= 18 {
				// 8 字节 = 采样率20位 + 声道3位 + 位深5位 + 总样本36位
				v := binary.BigEndian.Uint64(body[10:18])
				sr := v >> 44
				total := v & ((1 << 36) - 1)
				if sr > 0 {
					t.Duration = int(total / sr)
				}
			}
		case 4: // VORBIS_COMMENT
			parseVorbis(body, t)
		case 6: // PICTURE
			if c, ext := flacPic(body); len(c) > 0 && len(t.Cover) == 0 {
				t.Cover, t.CoverExt = c, ext
			}
		}
		if last {
			break
		}
	}
	return nil
}

func parseVorbis(b []byte, t *tags) {
	if len(b) < 8 {
		return
	}
	vlen := int(binary.LittleEndian.Uint32(b[:4]))
	if 4+vlen+4 > len(b) {
		return
	}
	n := int(binary.LittleEndian.Uint32(b[4+vlen : 8+vlen]))
	p := 8 + vlen
	for i := 0; i < n && p+4 <= len(b); i++ {
		l := int(binary.LittleEndian.Uint32(b[p : p+4]))
		p += 4
		if l < 0 || p+l > len(b) {
			return
		}
		kv := string(b[p : p+l])
		p += l
		k, v, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "TITLE":
			t.Title = v
		case "ARTIST":
			if t.Artist == "" {
				t.Artist = v
			}
		case "ALBUM":
			t.Album = v
		case "ALBUMARTIST":
			t.AlbumArtist = v
		case "GENRE":
			t.Genre = v
		case "DATE", "YEAR", "ORIGINALDATE":
			if t.Year == 0 {
				t.Year = atoi4s(v)
			}
		case "TRACKNUMBER":
			t.TrackNo = pairNums(v)[0]
		case "DISCNUMBER":
			t.DiscNo = pairNums(v)[0]
		}
	}
}

func flacPic(b []byte) ([]byte, string) {
	if len(b) < 32 {
		return nil, ""
	}
	p := 4 // picture type
	ml := int(binary.BigEndian.Uint32(b[p : p+4]))
	p += 4 + ml
	dl := int(binary.BigEndian.Uint32(b[p : p+4]))
	p += 4 + dl // description（UTF-8，长度已知，不用找 0）
	p += 16     // w/h/depth/colors
	if p+4 > len(b) {
		return nil, ""
	}
	dl2 := int(binary.BigEndian.Uint32(b[p : p+4]))
	p += 4
	if dl2 <= 0 || p+dl2 > len(b) {
		return nil, ""
	}
	ext := ".jpg"
	if ml >= 3 && strings.Contains(strings.ToLower(string(b[8:8+ml])), "png") {
		ext = ".png"
	}
	return b[p : p+dl2], ext
}

// ─── WAV ───

func parseWAV(f *os.File, t *tags) error {
	buf := make([]byte, 8192)
	n, _ := f.ReadAt(buf, 0)
	buf = buf[:n]
	if len(buf) < 44 || string(buf[:4]) != "RIFF" || string(buf[8:12]) != "WAVE" {
		return nil
	}
	byteRate := 0
	var dataSize uint64
	p := 12
	for p+8 <= len(buf) {
		id := string(buf[p : p+4])
		sz := int(binary.LittleEndian.Uint32(buf[p+4 : p+8]))
		switch id {
		case "fmt ":
			if sz >= 16 && p+8+16 <= len(buf) {
				byteRate = int(binary.LittleEndian.Uint32(buf[p+16 : p+20]))
			}
		case "data":
			dataSize = uint64(sz)
			if dataSize == 0xFFFFFFFF { // 流式写出的常见占位
				if st, e := f.Stat(); e == nil {
					dataSize = uint64(st.Size()) - uint64(p+8)
				}
			}
		}
		if byteRate > 0 && dataSize > 0 {
			t.Duration = int(dataSize / uint64(byteRate))
			return nil
		}
		p += 8 + sz + (sz & 1) // chunk 对齐到偶数
	}
	return nil
}

// ─── 小工具 ───

func syncsafe(b []byte) int {
	return int(b[0]&0x7F)<<21 | int(b[1]&0x7F)<<14 | int(b[2]&0x7F)<<7 | int(b[3]&0x7F)
}

func deUnsync(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		out = append(out, b[i])
		if b[i] == 0xFF && i+1 < len(b) && b[i+1] == 0x00 {
			i++
		}
	}
	return out
}

func hasHighByte(b []byte) bool {
	for _, c := range b {
		if c > 0x7F {
			return true
		}
	}
	return false
}

func trimSpace(s string) string { return strings.TrimSpace(strings.Trim(s, "\x00")) }

// cleanGenre 「(17)Rock」/「17」这类数字引用换成纯文本。
func cleanGenre(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, ')'); i == len(s)-1 && strings.HasPrefix(s, "(") {
		if n := atoi4s(s[1:i]); n > 0 {
			return id3Genre(n)
		}
	}
	if n := atoi4s(s); n > 0 && len(s) <= 3 {
		return id3Genre(n)
	}
	return s
}

// pairNums 「3/12」→ [3,12]。
func pairNums(s string) [2]int {
	var out [2]int
	a, b, _ := strings.Cut(strings.TrimSpace(s), "/")
	out[0] = atoi4s(a)
	out[1] = atoi4s(b)
	return out
}

func atoi4s(s string) int {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return 0
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
		if n > 9999 {
			return 9999
		}
	}
	return n
}

func sampleRateOf(verBits, idx byte) int {
	mpeg1 := []int{44100, 48000, 32000}
	mpeg2 := []int{22050, 24000, 16000}
	m25 := []int{11025, 12000, 8000}
	switch verBits {
	case 3:
		if idx < 3 {
			return mpeg1[idx]
		}
	case 2:
		if idx < 3 {
			return mpeg2[idx]
		}
	case 0:
		if idx < 3 {
			return m25[idx]
		}
	}
	return 0
}

func bitRateOf(verBits, idx byte) int {
	mpeg1 := []int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	mpeg2 := []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
	if idx == 0 || idx == 15 {
		return 0
	}
	if verBits == 3 {
		return mpeg1[idx]
	}
	return mpeg2[idx]
}

func id3Genre(n int) string {
	if n < 0 || n >= len(id3Genres) {
		return ""
	}
	return id3Genres[n]
}

var id3Genres = []string{
	"Blues", "Classic Rock", "Country", "Dance", "Disco", "Funk", "Grunge",
	"Hip-Hop", "Jazz", "Metal", "New Age", "Oldies", "Other", "Pop", "R&B",
	"Rap", "Reggae", "Rock", "Techno", "Industrial", "Alternative", "Ska",
	"Death Metal", "Pranks", "Soundtrack", "Euro-Techno", "Ambient",
	"Trip-Hop", "Vocal", "Jazz+Funk", "Fusion", "Trance", "Classical",
	"Instrumental", "Acid", "House", "Game", "Sound Clip", "Gospel", "Noise",
	"Alt. Rock", "Bass", "Soul", "Punk", "Space", "Meditative",
	"Instrumental Pop", "Instrumental Rock", "Ethnic", "Gothic", "Darkwave",
	"Techno-Industrial", "Electronic", "Pop-Folk", "Eurodance", "Dream",
	"Southern Rock", "Comedy", "Cult", "Gangsta Rap", "Top 40", "Christian Rap",
	"Pop/Funk", "Jungle", "Native American", "Cabaret", "New Wave",
	"Psychedelic", "Rave", "Showtunes", "Trailer", "Lo-Fi", "Tribal",
	"Acid Punk", "Acid Jazz", "Polka", "Retro", "Musical", "Rock & Roll",
	"Hard Rock", "Folk", "Folk-Rock", "National Folk", "Swing", "Fast-Fusion",
	"Bebop", "Latin", "Revival", "Celtic", "Bluegrass", "Avantgarde",
	"Gothic Rock", "Progressive Rock", "Psychedelic Rock", "Symphonic Rock",
	"Slow Rock", "Big Band", "Chorus", "Easy Listening", "Acoustic", "Humour",
	"Speech", "Chanson", "Opera", "Chamber Music", "Sonata", "Symphony",
	"Booty Bass", "Primus", "Porn Groove", "Satire", "Slow Jam", "Club",
	"Tango", "Samba", "Folklore", "Ballad", "Power Ballad", "Rhythmic Soul",
	"Freestyle", "Duet", "Punk Rock", "Drum Solo", "A Cappella", "Euro-House",
	"Dance Hall", "Goa", "Drum & Bass", "Club-House", "Hardcore", "Terror",
	"Indie", "BritPop", "Afro-Punk", "Polsk Punk", "Beat",
	"Christian Gangsta Rap", "Heavy Metal", "Black Metal", "Crossover",
	"Contemporary Christian", "Christian Rock", "Merengue", "Salsa",
	"Thrash Metal", "Anime", "JPop", "Synthpop",
}
