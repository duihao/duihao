package music

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ys-ll/uniterm/backend/duihao/webdavd/appkit"
	"github.com/ys-ll/uniterm/backend/duihao/webdavd/store"
)

// ─── 造测试文件 ───

// id3v2 构造一个带 ID3v2.3 标签的假 MP3（尾部补够长度做码率估算）。
func makeMP3(t *testing.T, title, artist, album string, track string, withCover bool, bodyLen int) []byte {
	t.Helper()
	frame := func(id, data string) []byte {
		b := make([]byte, 10+len(data))
		copy(b, id)
		binary.BigEndian.PutUint32(b[4:8], uint32(len(data)))
		copy(b[10:], data)
		return b
	}
	text := func(s string) string { return "\x03" + s } // v2.3 宽松处理：也收 UTF-8
	body := bytes.Join([][]byte{
		frame("TIT2", text(title)),
		frame("TPE1", text(artist)),
		frame("TALB", text(album)),
		frame("TRCK", text(track)),
	}, nil)
	if withCover {
		png := append([]byte{0x89, 'P', 'N', 'G'}, make([]byte, 1200)...)
		ap := make([]byte, 0, 10+4+2+1+len(png)+12)
		ap = append(ap, "APIC"...)
		data := append([]byte{3}, []byte("image/png\x00")...)
		data = append(data, 0x03, 0x00) // picture type + 空 description
		data = append(data, png...)
		binary.BigEndian.PutUint32(make([]byte, 4), 0) // 占位
		hdr := make([]byte, 10)
		copy(hdr, "APIC")
		binary.BigEndian.PutUint32(hdr[4:8], uint32(len(data)))
		ap = append(hdr, data...)
		body = append(body, ap...)
	}
	// 同步安全大小
	size := uint32(len(body))
	ss := []byte{byte(size >> 21 & 0x7F), byte(size >> 14 & 0x7F), byte(size >> 7 & 0x7F), byte(size & 0x7F)}
	head := append([]byte{'I', 'D', '3', 3, 0, 0}, ss...)

	// 帧头：MPEG1 Layer3 64kbps 44100 → CBR 估算时长
	audio := make([]byte, bodyLen)
	audio[0], audio[1], audio[2], audio[3] = 0xFF, 0xFB, 0x50, 0x00
	return append(append(head, body...), audio...)
}

// makeFLAC 构造一个带 STREAMINFO + VORBIS_COMMENT 的假 FLAC。
func makeFLAC(t *testing.T, title, artist string, durSec int) []byte {
	t.Helper()
	out := []byte("fLaC")
	// STREAMINFO：44100Hz / 总样本 = durSec*44100
	total := uint64(durSec) * 44100
	var v uint64 = 44100<<44 | 1<<41 | 15<<36 | total // 单声道 / 16bit
	si := make([]byte, 34)
	binary.BigEndian.PutUint64(si[10:18], v)
	out = append(out, 0x00, byte(len(si)>>16), byte(len(si)>>8), byte(len(si)))
	out = append(out, si...)

	// Vorbis 注释块：vendor(len=1 + "x") + 条数 + "KEY=value" 列表
	vd := make([]byte, 4, 128)
	binary.LittleEndian.PutUint32(vd, 1) // vendor 长度
	vd = append(vd, 'x')
	vd = binary.LittleEndian.AppendUint32(vd, 2) // 条数
	for _, kv := range []string{"TITLE=" + title, "ARTIST=" + artist} {
		vd = binary.LittleEndian.AppendUint32(vd, uint32(len(kv)))
		vd = append(vd, kv...)
	}
	out = append(out, 0x84, byte(len(vd)>>16), byte(len(vd)>>8), byte(len(vd)))
	out = append(out, vd...)
	return out
}

// ─── 标签解析 ───

func TestReadTagsMP3(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.mp3")
	if err := os.WriteFile(p, makeMP3(t, "晴天", "周杰伦", "叶惠美", "3/12", true, 800_000), 0o644); err != nil {
		t.Fatal(err)
	}
	tg, err := readTags(p, ".mp3")
	if err != nil {
		t.Fatal(err)
	}
	if tg.Title != "晴天" || tg.Artist != "周杰伦" || tg.Album != "叶惠美" || tg.TrackNo != 3 {
		t.Fatalf("标签不对: %+v", tg)
	}
	if tg.CoverExt != ".png" || len(tg.Cover) < 1200 {
		t.Fatalf("封面不对: ext=%s len=%d", tg.CoverExt, len(tg.Cover))
	}
	// 800000 字节 @64kbps ≈ 100s（CBR 估算，允许 ±3s）
	if tg.Duration < 97 || tg.Duration > 103 {
		t.Fatalf("时长估算不对: %d", tg.Duration)
	}
	if tg.Bitrate != 64 {
		t.Fatalf("码率不对: %d", tg.Bitrate)
	}
}

func TestReadTagsFLAC(t *testing.T) {
	p := filepath.Join(t.TempDir(), "b.flac")
	if err := os.WriteFile(p, makeFLAC(t, "海阔天空", "Beyond", 200), 0o644); err != nil {
		t.Fatal(err)
	}
	tg, err := readTags(p, ".flac")
	if err != nil {
		t.Fatal(err)
	}
	if tg.Title != "海阔天空" || tg.Artist != "Beyond" {
		t.Fatalf("标签不对: %+v", tg)
	}
	if tg.Duration != 200 {
		t.Fatalf("FLAC 时长应精确 200, got %d", tg.Duration)
	}
}

// ─── 扫描 ───

func newCtx(t *testing.T) (appkit.Context, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	root := t.TempDir()
	return appkit.Context{User: "tester", Root: root, DB: st.DB(), Writable: true}, root
}

func TestScanAndRescan(t *testing.T) {
	c, root := newCtx(t)
	mroot := filepath.Join(root, "_appfiles", "music")
	mk := func(rel string, data []byte) {
		p := filepath.Join(mroot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("周杰伦/叶惠美/晴天.mp3", makeMP3(t, "晴天", "周杰伦", "叶惠美", "3", true, 4096))
	mk("Beyond/海阔天空.flac", makeFLAC(t, "海阔天空", "Beyond", 200))
	mk("张三 - 老歌.mp3", make([]byte, 2048))                                 // 无标签 → 文件名兜底
	mk("周杰伦/叶惠美/cover.jpg", append([]byte("JPG"), make([]byte, 2000)...)) // 目录封面

	res, err := scanScan(c)
	if err != nil {
		t.Fatal(err)
	}
	if res["added"].(int) != 3 || res["tracks"].(int) != 3 {
		t.Fatalf("首次扫描不对: %v", res)
	}

	items, err := queryTracks(c, `select `+trackCols+` from music_tracks t where t.uid=? order by t.title`, c.User)
	if err != nil {
		t.Fatal(err)
	}
	byTitle := map[string]rowItem{}
	for _, it := range items {
		byTitle[it.Title] = it
	}
	if q, ok := byTitle["晴天"]; !ok || q.Artist != "周杰伦" || q.Album != "叶惠美" || q.Cover == "" {
		t.Fatalf("晴天元数据不对: %+v", q)
	}
	if q, ok := byTitle["海阔天空"]; !ok || q.Duration != 200 || q.Album != "Beyond" {
		t.Fatalf("海阔天空不对(专辑应兜底为目录名): %+v", q)
	}
	if q, ok := byTitle["张三 - 老歌"]; !ok || q.Artist != "张三" {
		t.Fatalf("无标签文件兜底不对: %+v", q)
	}

	// 重扫：幂等
	res, err = scanScan(c)
	if err != nil {
		t.Fatal(err)
	}
	if res["added"].(int) != 0 || res["removed"].(int) != 0 {
		t.Fatalf("重扫应幂等: %v", res)
	}

	// 文件消失 → 移除
	if err := os.Remove(filepath.Join(mroot, "张三 - 老歌.mp3")); err != nil {
		t.Fatal(err)
	}
	res, err = scanScan(c)
	if err != nil {
		t.Fatal(err)
	}
	if res["removed"].(int) != 1 || res["tracks"].(int) != 2 {
		t.Fatalf("删除后重扫不对: %v", res)
	}
}

// ─── U 盘导出（拷贝段直测，绕开「必须可移动盘」的硬件前提）───

func TestDoExport(t *testing.T) {
	c, root := newCtx(t)
	mroot := filepath.Join(root, "_appfiles", "music")
	mk := func(rel string, data []byte) {
		p := filepath.Join(mroot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("A/第一首.mp3", makeMP3(t, "第一首", "歌手甲", "专辑一", "1", false, 8192))
	mk("A/第二首.flac", makeFLAC(t, "第二首", "歌手乙", 100))
	if _, err := scanScan(c); err != nil {
		t.Fatal(err)
	}
	rows, err := queryTracks(c, `select `+trackCols+` from music_tracks t where t.uid=? order by t.title`, c.User)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("应有 2 首, got %d", len(rows))
	}
	var tracks []trk
	for _, r := range rows {
		tracks = append(tracks, trk{path: r.Path, title: r.Title, artist: r.Artist, dur: r.Duration, size: int(r.Size)})
	}

	dest := t.TempDir()
	items, skipped, copied, warn, err := doExport(c, tracks, dest)
	if err != nil || warn != "" {
		t.Fatalf("导出失败: %v %s", err, warn)
	}
	if items != 2 || skipped != 0 || copied == 0 {
		t.Fatalf("导出统计不对: items=%d skipped=%d bytes=%d", items, skipped, copied)
	}
	ents, _ := os.ReadDir(dest)
	names := map[string]bool{}
	for _, e := range ents {
		names[e.Name()] = true
	}
	if !names["01 - 第一首 - 歌手甲.mp3"] || !names["02 - 第二首 - 歌手乙.flac"] || !names["playlist.m3u8"] {
		for n := range names {
			t.Log("got:", n)
		}
		t.Fatal("导出文件名/歌单不对")
	}
	m3u, _ := os.ReadFile(filepath.Join(dest, "playlist.m3u8"))
	if !bytes.Contains(m3u, []byte("#EXTM3U")) || !bytes.Contains(m3u, []byte("#EXTINF:100,")) {
		t.Fatalf("M3U8 内容不对:\n%s", m3u)
	}
	// 重复导出：全部跳过
	items, skipped, _, _, err = doExport(c, tracks, dest)
	if err != nil || items != 0 || skipped != 2 {
		t.Fatalf("重复导出应全跳过: items=%d skipped=%d err=%v", items, skipped, err)
	}
}

// ─── 安全边界 ───

func TestDoExportRejectsOutsideMusicRoot(t *testing.T) {
	c, root := newCtx(t)
	tracks := []trk{{path: "/_appfiles/other/evil.mp3", title: "evil", artist: "x", size: 4}}
	// 造一个真实文件在 music 外
	p := filepath.Join(root, "_appfiles", "other", "evil.mp3")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("XXXX"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	items, skipped, _, _, _ := doExport(c, tracks, dest)
	if items != 0 || skipped != 1 {
		t.Fatalf("music 目录外的文件应被跳过: items=%d skipped=%d", items, skipped)
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		`a<b>c:d?e.mp3`: "a b c d e.mp3",
		`歌/曲\名:x.mp3`:   "歌 曲 名 x.mp3",
		"  ..怪名字.. ":    "怪名字",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Fatalf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := sanitizeName(strings.Repeat("长", 200)); len([]rune(got)) != 100 {
		t.Fatalf("超长应截断到 100: %d", len([]rune(got)))
	}
}

// ─── 卡拉OK：LRC 解析 + 扫描入库 ───

func TestParseLRC(t *testing.T) {
	src := "[ti:晴天]\n[offset:500]\n" +
		"[00:12.30]第一行\n" +
		"[00:20][00:40]重复两遍\n" +
		"[01:02.50]<00:01.00>带<00:02.00>逐字\n" +
		"[bad tag]无时间戳丢弃\n" +
		"[2:05]两分零五秒\n" +
		"\n"
	ls := parseLRC(src)
	if len(ls) != 5 {
		t.Fatalf("应 5 行(重复行展开), got %d: %+v", len(ls), ls)
	}
	if ls[0].T != 12.8 || ls[0].Text != "第一行" {
		t.Fatalf("offset 应 +0.5s: %+v", ls[0])
	}
	if ls[1].T != 20.5 || ls[2].T != 40.5 || ls[2].Text != "重复两遍" {
		t.Fatalf("多时间戳展开不对: %+v %+v", ls[1], ls[2])
	}
	if ls[3].T != 63 || ls[3].Text != "带逐字" {
		t.Fatalf("逐字标签应剥掉: %+v", ls[3])
	}
	if ls[4].T != 125.5 {
		t.Fatalf("分:秒 应=125.5s(含offset): %+v", ls[4])
	}
	// 排序稳定性
	for i := 1; i < len(ls); i++ {
		if ls[i].T < ls[i-1].T {
			t.Fatalf("应按时间升序: %+v", ls)
		}
	}
}

func TestScanLyrics(t *testing.T) {
	c, root := newCtx(t)
	mroot := filepath.Join(root, "_appfiles", "music")
	if err := os.MkdirAll(filepath.Join(mroot, "s"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mroot, "s", "晴天.mp3"),
		makeMP3(t, "晴天", "周杰伦", "叶惠美", "3", false, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	lrc := "[00:01.00]第一句\n[00:05.50]第二句\n"
	if err := os.WriteFile(filepath.Join(mroot, "s", "晴天.lrc"), []byte(lrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := scanScan(c); err != nil {
		t.Fatal(err)
	}

	var raw string
	if err := c.DB.QueryRow(`select lyrics from music_tracks where uid=?`, c.User).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var ls []lyricLine
	if err := json.Unmarshal([]byte(raw), &ls); err != nil || len(ls) != 2 || ls[1].T != 5.5 {
		t.Fatalf("歌词入库不对: raw=%q err=%v ls=%+v", raw, err, ls)
	}

	// .lrc 消失 → 重扫清空
	if err := os.Remove(filepath.Join(mroot, "s", "晴天.lrc")); err != nil {
		t.Fatal(err)
	}
	if _, err := scanScan(c); err != nil {
		t.Fatal(err)
	}
	if err := c.DB.QueryRow(`select lyrics from music_tracks where uid=?`, c.User).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "" {
		t.Fatalf("lrc 删除后应清空歌词, got %q", raw)
	}
}
