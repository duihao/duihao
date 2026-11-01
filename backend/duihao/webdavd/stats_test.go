// stats_test.go 存储统计口径守卫。
//
// 定稿口径（普通模式首页「存储环」）：总量/剩余取**网盘根目录所在盘符**的
// 容量，不是整块物理硬盘、也不是所有分区之和；NAS 占用单独用目录 walk 算。
// 这里盯住三件事：
//  1. VolumeStatsOf 返回的盘符与路径所在卷一致（盘符算错，整个环就错）；
//  2. DirUsage 只累计文件、不把目录项本身算进去；
//  3. StorageStatsOf 的 total/free 必须与 VolumeStatsOf 完全一致（不许悄悄换口径）。
package webdavd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVolumeStatsOfUsesPathDrive(t *testing.T) {
	dir := t.TempDir()
	total, free, ok := VolumeStatsOf(dir)
	if !ok {
		t.Fatalf("VolumeStatsOf(%q) 取不到卷容量", dir)
	}
	if total == 0 {
		t.Fatal("卷总量为 0")
	}
	if free > total {
		t.Fatalf("剩余(%d) 大于总量(%d)", free, total)
	}
	if got, want := DriveOf(dir), filepath.VolumeName(dir); got != want {
		t.Fatalf("DriveOf = %q，期望 %q", got, want)
	}

	// 根目录不存在时不许 panic，ok=false 让前端显示占位
	if _, _, ok := VolumeStatsOf(filepath.Join(dir, "nope", "deeper")); !ok {
		// 有的系统卷信息仍可取（父路径存在即可），两种都算正常，只要不崩
		t.Log("不存在的子路径取不到卷信息（可接受）")
	}
}

func TestDirUsageCountsFilesOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("0123456789")
	if err := os.WriteFile(filepath.Join(root, "a", "b", "f.bin"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "top.txt"), payload[:4], 0o644); err != nil {
		t.Fatal(err)
	}
	n, ok := DirUsage(root)
	if !ok {
		t.Fatal("DirUsage 失败")
	}
	if want := int64(len(payload) + 4); n != want {
		t.Fatalf("DirUsage = %d，期望 %d", n, want)
	}
}

func TestStorageStatsOfMatchesVolume(t *testing.T) {
	root := t.TempDir()
	total, free, used := StorageStatsOf(root)
	vt, vf, ok := VolumeStatsOf(root)
	if !ok {
		t.Fatal("VolumeStatsOf 失败")
	}
	if total != vt || free != vf {
		t.Fatalf("盘容量口径不一致：StorageStatsOf(%d,%d) vs VolumeStatsOf(%d,%d)", total, free, vt, vf)
	}
	if used < 0 {
		t.Fatalf("空目录占用应为 0 或正数，got %d", used)
	}
}
