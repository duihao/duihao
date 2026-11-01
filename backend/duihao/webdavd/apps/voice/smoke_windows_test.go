//go:build windows

package voice

import (
	"os"
	"testing"
	"time"
)

// TestRealSpeechThroughScheduler 真机冒烟：走完整链路（扫描 → 入队 → SAPI 出声），
// 会真的从扬声器念一句。默认跳过，需要时 `VOICE_SMOKE=1 go test -run RealSpeech`。
//
// 平时不跑：CI/回归里不该突然冒声音。
func TestRealSpeechThroughScheduler(t *testing.T) {
	if os.Getenv("VOICE_SMOKE") == "" {
		t.Skip("设 VOICE_SMOKE=1 才真出声")
	}
	go speakLoop() // Start 里也是这一行；测试没走 Start，手动起消费者

	_, db := freshDB(t)
	now := time.Now()
	insertEvent(t, db, "u1", event{
		ID: "smoke", Title: "语音提醒已就绪", Content: "到点我就这样念给你听。",
		Voice: "huihui-soft", Tone: "warm", Times: 1, Period: "once",
		NextAt: now.Add(-time.Second).Format(timeLayout),
	})

	sweep(db, "", now)
	time.Sleep(6 * time.Second) // 等念完（同步 Speak 会阻塞播报 goroutine）

	logs := logsOf(t, db, "u1")
	if len(logs) != 1 {
		t.Fatalf("应有 1 条流水, got %v", logs)
	}
	t.Logf("流水: %q", logs[0])
}
