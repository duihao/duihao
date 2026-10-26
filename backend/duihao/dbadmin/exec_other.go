//go:build !windows

package dbadmin

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
