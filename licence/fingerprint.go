package licence

import (
	"crypto/sha256"
	"encoding/hex"
)

// 设备指纹：CPU + 主板 + 系统盘三个部件哈希。
//
// ★ 部件值先做一次 sha256（部件哈希），再排序拼接做 dvh —— 部件原值
//   （序列号）绝不落盘、绝不上网，快照里只有不可逆哈希。
// ★ 2/3 表决语义见 licence.go 的 checkDevice：换一个部件仍算同机，
//   整份快照拷到别的机器 0/3 命中拒绝。

// partHash：部件原值 → 64hex 小写；空值/采集失败返回 ""（表决时跳过）。
func partHash(v string) string {
	v = trimSpace(v)
	if v == "" {
		return ""
	}
	h := sha256.Sum256([]byte("dvh-part-v1:" + v))
	return hex.EncodeToString(h[:])
}

// defaultFingerprinter：按 GOOS 分派（Windows 走 WMI，其余走 machine-id）。
type defaultFingerprinter struct{}

func defaultFingerprint() Fingerprinter { return defaultFingerprinter{} }

func (defaultFingerprinter) Parts() ([3]string, error) { return platformParts() }

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r' || s[start] == '\n') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r' || s[end-1] == '\n') {
		end--
	}
	return s[start:end]
}
