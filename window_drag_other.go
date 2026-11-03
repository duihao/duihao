//go:build !windows

package main

// 非 Windows 平台暂时没有 Frameless 拖动：模块窗按钮的 /-shell/drag 动作空转。
// 三个按钮（最小化/最大化/关闭）走的是 Wails 跨平台 API，照常可用。

import "github.com/wailsapp/wails/v3/pkg/application"

func dragWindow(*application.WebviewWindow) {}
