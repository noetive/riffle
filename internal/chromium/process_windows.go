//go:build windows

package chromium

import "os/exec"

func ownGroup(*exec.Cmd) {}

// killTree has nothing to add on Windows: Close kills the process itself.
func killTree(int) {}

// alive is conservative: with no way to ask, leftovers are left alone.
func alive(int) bool { return true }
