package chromium

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/noetive/riffle/internal/userdir"
)

// staleAfter is how old a profile directory with no owner must be before it
// is taken for a leftover and not a launch in progress.
const staleAfter = time.Minute

// claim records which daemon and which browser a profile directory belongs
// to, so that a later daemon can tell it is a leftover.
func claim(dir string, chromePID int) {
	_ = os.WriteFile(filepath.Join(dir, "owner"), []byte(strconv.Itoa(os.Getpid())), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "chrome"), []byte(strconv.Itoa(chromePID)), 0o600)
}

// SweepStale removes the profile directories, and ends the browsers, that
// earlier daemons left behind when they were killed without a chance to
// clean up. Only directories the current user owns are considered: the
// temporary directory may be shared, and a directory someone else made names
// whatever process they want ended.
func SweepStale() { sweepStale(os.TempDir(), userdir.Owned, alive, killTree) }

func sweepStale(root string, owned func(os.FileInfo) bool, alive func(pid int) bool, kill func(pgid int)) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "riffle-chrome-") {
			continue
		}
		info, err := e.Info()
		if err != nil || !owned(info) {
			continue
		}
		dir := filepath.Join(root, e.Name())
		owner := readPID(filepath.Join(dir, "owner"))
		if owner > 0 {
			if alive(owner) {
				continue
			}
			if chrome := readPID(filepath.Join(dir, "chrome")); chrome > 0 {
				kill(chrome)
			}
		} else if time.Since(info.ModTime()) < staleAfter {
			continue
		}
		_ = os.RemoveAll(dir)
	}
}

func readPID(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return n
}
