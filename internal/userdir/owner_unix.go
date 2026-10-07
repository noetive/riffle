//go:build !windows

package userdir

import (
	"os"
	"syscall"
)

// Owned reports whether the current user owns the file.
func Owned(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}
