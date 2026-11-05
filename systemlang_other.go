//go:build !windows

package main

import (
	"os"
	"strings"
)

// systemUILanguage is the non-Windows fallback: parse LANG/LC_ALL the same
// way the frontend's system-language resolution would land (e.g.
// "zh_CN.UTF-8" -> "zh-CN"). Best effort only; the tray defaults to English
// when nothing usable is found.
func systemUILanguage() string {
	lang := os.Getenv("LC_ALL")
	if lang == "" {
		lang = os.Getenv("LC_MESSAGES")
	}
	if lang == "" {
		lang = os.Getenv("LANG")
	}
	if lang == "" || lang == "C" || lang == "POSIX" {
		return ""
	}
	lang = strings.SplitN(lang, ".", 2)[0]
	lang = strings.SplitN(lang, "@", 2)[0]
	return strings.ReplaceAll(lang, "_", "-")
}
