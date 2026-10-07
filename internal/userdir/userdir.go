// Package userdir keeps the directories Riffle trusts private to the user
// running it. On a machine with a shared temporary directory another user can
// create a directory with any name first; trusting it would let them plant a
// socket, a log symlink or a process id for Riffle to act on.
package userdir

import (
	"fmt"
	"os"
)

// Ensure creates dir if needed and verifies that it is a real directory,
// owned by the current user, that no one else can write to. An existing
// directory that fails the check is refused, never repaired: repairing would
// adopt whatever another user left inside it.
func Ensure(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if !Owned(fi) {
		return fmt.Errorf("%s belongs to another user", dir)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is writable by other users (mode %o); make it 0700", dir, fi.Mode().Perm())
	}
	return nil
}
