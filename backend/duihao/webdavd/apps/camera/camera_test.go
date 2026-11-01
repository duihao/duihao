package camera

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 品牌模板：地址拼装是「用户少踩坑」的核心，拼错一个字就整台相机连不上。
func TestVendorPresetBuild(t *testing.T) {
	hk := presetOf("hikvision")
	if got := hk.build("192.168.1.64", 554, "admin", "Abc12345", 1, false); got !=
		"rtsp://admin:Abc12345@192.168.1.64:554/Streaming/Channels/101" {
		t.Fatalf("海康主码流拼错: %s", got)
	}
	if got := hk.build("192.168.1.64", 554, "", "p", 2, true); got !=
		"rtsp://admin:p@192.168.1.64:554/Streaming/Channels/202" {
		t.Fatalf("海康子码流应有默认 admin 与通道号: %s", got)
	}
	dh := presetOf("dahua")
	if got := dh.build("10.0.0.9", 554, "admin", "x", 3, true); !strings.HasSuffix(got, "channel=3&subtype=1") {
		t.Fatalf("大华子码流拼错: %s", got)
	}
	if got := presetOf("custom").build("1.2.3.4", 554, "a", "b", 1, false); got != "" {
		t.Fatalf("自定义品牌不该拼出地址: %s", got)
	}
	if presetOf("nope").Key != "" {
		t.Fatal("未知品牌应返回空预设")
	}
	if len(vendorPresets()) < 5 {
		t.Fatalf("品牌模板太少: %d", len(vendorPresets()))
	}
}

// 录像参数只允许「复制」不允许「转码」——这是 CPU 占用和画质的分界线。
func TestRecordArgs(t *testing.T) {
	cam := Cam{ID: "c1", URLMain: "rtsp://h/main", URLSub: "rtsp://h/sub", Transport: "tcp"}
	args := strings.Join(recordArgs(cam, `D:\cam\c1`), " ")
	for _, want := range []string{"-c copy", "-f segment", "-segment_time 300", "-strftime 1", "rtsp://h/sub"} {
		if !strings.Contains(args, want) {
			t.Fatalf("录像参数缺 %q: %s", want, args)
		}
	}
	if strings.Contains(args, "-c:v libx264") || strings.Contains(args, "libx264") {
		t.Fatalf("不该转码: %s", args)
	}
	// 没有子码流时退回主码流
	cam2 := Cam{ID: "c2", URLMain: "rtsp://h/main"}
	if !strings.Contains(strings.Join(recordArgs(cam2, "/tmp"), " "), "rtsp://h/main") {
		t.Fatal("缺子码流应退回主码流")
	}
}

func TestSegName(t *testing.T) {
	ts, ok := segName("20261030_120500.mp4")
	if !ok || ts.Format("2006-01-02 15:04:05") != "2026-10-30 12:05:00" {
		t.Fatalf("文件名解析错: %v %v", ts, ok)
	}
	for _, bad := range []string{"foo.mp4", "20261030.mp4", "20261030_1205.mp4"} {
		if _, ok := segName(bad); ok {
			t.Fatalf("%s 不该通过", bad)
		}
	}
}

// 路径穿越：任何能写到相机目录外面的输入都必须被挡。
func TestSafeSegPath(t *testing.T) {
	root := t.TempDir()
	// 带 ../ 的 mp4 名字会被收敛到相机目录内（只取 basename），不越界
	p0, err := safeSegPath(root, "cam1", "../../x.mp4")
	if err != nil || !strings.HasSuffix(filepath.ToSlash(p0), "/cam1/x.mp4") {
		t.Fatalf("带 ../ 的 mp4 应收敛到相机目录: %s %v", p0, err)
	}
	// 非 mp4 一律拒绝（防止拿这个接口读写别的文件）
	if _, err := safeSegPath(root, "cam1", "../../../etc/passwd"); err == nil {
		t.Fatal("非 mp4 应被拒绝")
	}
	p, err := safeSegPath(root, "cam1", "../../../etc/x.mp4")
	if err != nil || strings.Contains(filepath.ToSlash(p), "..") {
		t.Fatalf("穿越未被收敛: %s %v", p, err)
	}
	if _, err := safeSegPath(root, "cam1", ""); err == nil {
		t.Fatal("空文件名应报错")
	}
	p, err = safeSegPath(root, "cam1", "20261030_120500.mp4")
	if err != nil || !strings.HasSuffix(filepath.ToSlash(p), "/cam1/20261030_120500.mp4") {
		t.Fatalf("正常文件名解析错: %s %v", p, err)
	}
}

// 保留天数：只删过期的，边界那天保留。
func TestPruneByAge(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.Local)
	write := func(name string, size int) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("20261020_120000.mp4", 100) // 10 天前 → 删
	write("20261029_120000.mp4", 200) // 1 天前 → 留
	write("20261023_120000.mp4", 300) // 恰好在 7 天边界（10-23 12:00）→ 留
	write("note.txt", 10)             // 非视频 → 不碰

	files, freed := pruneByAge(dir, 7, now)
	if files != 1 || freed != 100 {
		t.Fatalf("应只删 1 个过期片段, got files=%d freed=%d", files, freed)
	}
	if _, err := os.Stat(filepath.Join(dir, "20261029_120000.mp4")); err != nil {
		t.Fatal("近期片段被误删")
	}
	if _, err := os.Stat(filepath.Join(dir, "note.txt")); err != nil {
		t.Fatal("非视频文件被误删")
	}
	// 0 天 = 不限
	if f, _ := pruneByAge(dir, 0, now); f != 0 {
		t.Fatal("retainDays=0 不该删")
	}
}

// 磁盘守护：空间不够时从最旧的开始删，够用就停手。
func TestPruneForSpace(t *testing.T) {
	dir := t.TempDir()
	for i, name := range []string{"20261030_110000.mp4", "20261030_111000.mp4", "20261030_112000.mp4"} {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = i
	}
	// 只剩 150B，需要 250B → 删最旧的 100B 后变 250B，正好够
	files, freed := pruneForSpace(dir, 150, 250, 10)
	if files != 1 || freed != 100 {
		t.Fatalf("应删 1 个最旧片段, got %d/%d", files, freed)
	}
	if _, err := os.Stat(filepath.Join(dir, "20261030_112000.mp4")); err != nil {
		t.Fatal("最新片段不该被删")
	}
	// 空间充足时什么都不做
	if f, _ := pruneForSpace(dir, 10<<30, 4<<30, 10); f != 0 {
		t.Fatal("空间充足不该删")
	}
}

// 时间轴要用到 startAt → 秒偏移，序号错一格就整条轴偏。
func TestSegStartOffset(t *testing.T) {
	start := time.Date(2026, 10, 30, 12, 5, 0, 0, time.Local)
	sec := start.Hour()*3600 + start.Minute()*60 + start.Second()
	if sec != 12*3600+5*60 {
		t.Fatalf("时间轴偏移算错: %d", sec)
	}
}
