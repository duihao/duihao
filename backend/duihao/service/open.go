// 打开外部浏览器（站点「访问」按钮用）。
package service

import (
	"os/exec"
	"runtime"
)

// openExternal 用系统默认程序打开 URL（浏览器）。
func openExternal(url string) error { return openTarget(url) }

// openPath 在文件管理器中打开目录。
func openPath(dir string) error { return openTarget(dir) }

func openTarget(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// 空标题参数占位：cmd /c start "" <target>
		cmd = exec.Command("cmd", "/c", "start", "", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}
