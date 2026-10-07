//go:build !windows

package chromium

import (
	"errors"
	"os/exec"
	"syscall"
)

// ownGroup puts the browser in a process group of its own, so that it and
// every helper it starts can be ended together.
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// killTree ends the browser's whole process group. A browser killed alone
// leaves its renderer, GPU and utility processes running.
func killTree(pid int) {
	if pid > 1 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}

// alive reports whether a process with this id exists.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
