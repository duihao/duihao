//go:build windows

package voice

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

// TestSynthWAV 真机合成一句，校验时长与体积（22kHz/16bit/mono ≈ 44100 字节/秒）。
func TestSynthWAV(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "t.wav")
	ms, err := synthWAV(out, "该喝水了，宝贝。你已经连续坐了两个小时。", "huihui-std", "warm")
	if err != nil {
		t.Skipf("本机没有可用语音引擎: %v", err)
	}
	if ms < 800 || ms > 20000 {
		t.Errorf("时长异常: %d ms", ms)
	}
	st, err := os.Stat(out)
	if err != nil {
		t.Fatalf("产物不存在: %v", err)
	}
	want := int64(ms) * 44100 / 1000
	if d := st.Size() - want; d > 8000 || d < -8000 {
		t.Errorf("体积 %d 与时长 %d 不匹配（期望≈%d）", st.Size(), ms, want)
	}
}

func TestSystemVoices(t *testing.T) {
	vs := systemVoices()
	if len(vs) == 0 {
		t.Skip("本机没装 SAPI 语音")
	}
	for _, v := range vs {
		if v.ID == "" {
			t.Errorf("语音缺少 ID: %+v", v)
		}
		t.Logf("%s | %s | %s", v.Name, v.Lang, v.ID)
	}
}

// TestWithSAPIOnPreinitializedThread 回归：线程上已有 COM 时播报必须照常工作。
//
// 真机踩过（2026-10-30）：Wails/WebView2 在自己的线程上初始化过 COM，goroutine
// 池复用这些线程，CoInitializeEx 返回 S_FALSE（"已经初始化过"，不是错误）被
// go-ole 当成 error → withSAPI 直接放弃 → 「弹窗弹了、声音没有」。测试进程的
// 线程是干净的，所以单测全绿也发现不了——这里先手动初始化一次再走 withSAPI，
// 模拟的就是那种被复用的线程。
func TestWithSAPIOnPreinitializedThread(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		t.Fatalf("预初始化 COM 失败: %v", err)
	}
	defer ole.CoUninitialize()

	err := withSAPI("", func(v *ole.IDispatch) error {
		res, err := oleutil.CallMethod(v, "GetVoices")
		if err != nil {
			return err
		}
		res.Clear()
		return nil
	})
	if err != nil {
		t.Fatalf("线程已初始化过 COM 时 withSAPI 必须照常工作: %v", err)
	}
}
