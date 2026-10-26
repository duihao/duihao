//go:build !windows

package site

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
