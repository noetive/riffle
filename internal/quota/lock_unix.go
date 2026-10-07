//go:build !windows

package quota

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes an exclusive lock on f without waiting. The lock belongs to
// this open file: closing it, or the process ending, releases it.
func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}
