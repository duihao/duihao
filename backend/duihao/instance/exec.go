// exec-once 的进程执行。短命初始化命令（mysqld --initialize 等），
// 跑完即退；M3 用 os/exec（输出进审计），Job Object 化后置。
package instance

import (
	"os/exec"
	"syscall"
)

// execOnce 执行并等待退出，返回退出码。
func execOnce(bin string, args []string, dir string) (int, error) {
	cmd := exec.Command(bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}
