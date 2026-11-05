//go:build windows

package main

import "golang.org/x/sys/windows"

// systemUILanguage returns the user's preferred OS UI language as a BCP-47
// style tag (e.g. "zh-CN"), mirroring what navigator.language reports in the
// webview. Used by the tray menu to resolve the "system" language setting —
// the Go side can't read navigator.language, so it asks Windows directly.
// Returns "" when the lookup fails (caller falls back to English).
func systemUILanguage() string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil || len(langs) == 0 {
		return ""
	}
	return langs[0]
}
