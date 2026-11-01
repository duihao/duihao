// RIFF 解析：算 wav 时长（不看文件大小猜，按 fmt 里的 byteRate 除）。
//
// 单独一个文件是因为它跟平台无关：SAPI 合成要它、局域网 GPU 合成也要它，
// 而 tts_*.go 是按平台分家的。放错地方会让非 Windows 构建直接编译不过。
package voice

import (
	"encoding/binary"
	"os"
)

func wavDurationMs(path string) int {
	b, err := os.ReadFile(path)
	if err != nil || len(b) < 44 || string(b[:4]) != "RIFF" {
		return 0
	}
	pos, byteRate, dataLen := 12, 0, 0
	for pos+8 <= len(b) {
		id := string(b[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		body := pos + 8
		switch id {
		case "fmt ":
			if body+16 <= len(b) {
				ch := int(binary.LittleEndian.Uint16(b[body+2:]))
				rate := int(binary.LittleEndian.Uint32(b[body+4:]))
				bits := int(binary.LittleEndian.Uint16(b[body+14:]))
				byteRate = rate * ch * bits / 8
			}
		case "data":
			dataLen = size
			if body+size > len(b) {
				dataLen = len(b) - body
			}
		}
		if size < 0 {
			break
		}
		pos = body + size + (size & 1)
	}
	if byteRate <= 0 || dataLen <= 0 {
		return 0
	}
	return int(float64(dataLen) / float64(byteRate) * 1000)
}

// isWAV 只看头四字节：GPU 主机偶尔会把 mp3 当 wav 返回来，落盘前要挡一下。
func isWAV(b []byte) bool {
	return len(b) > 44 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WAVE"
}
