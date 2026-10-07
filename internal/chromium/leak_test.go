package chromium

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/userdir"
)

func TestLaunchArgsKeepChromeAwayFromTheKeychain(t *testing.T) {
	args := launchArgs("/tmp/p")
	for _, want := range []string{"--use-mock-keychain", "--password-store=basic", "--user-data-dir=/tmp/p", "--remote-debugging-pipe", "--headless=new"} {
		if !slices.Contains(args, want) {
			t.Errorf("launch args lack %s: a throwaway profile must never ask the user's keychain for anything", want)
		}
	}
}

func TestSweepRemovesTheLeftoversOfABrowserWhoseDaemonIsGone(t *testing.T) {
	root := t.TempDir()
	mk := func(name, owner, chrome string, age time.Duration) string {
		d := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(d, "profile"), 0o700); err != nil {
			t.Fatal(err)
		}
		if owner != "" {
			_ = os.WriteFile(filepath.Join(d, "owner"), []byte(owner), 0o600)
		}
		if chrome != "" {
			_ = os.WriteFile(filepath.Join(d, "chrome"), []byte(chrome), 0o600)
		}
		old := time.Now().Add(-age)
		_ = os.Chtimes(d, old, old)
		return d
	}
	live := mk("riffle-chrome-live", strconv.Itoa(os.Getpid()), "4242", time.Hour)
	dead := mk("riffle-chrome-dead", "999999", "4243", time.Hour)
	bare := mk("riffle-chrome-bare", "", "", time.Hour)
	fresh := mk("riffle-chrome-fresh", "", "", time.Second)
	other := mk("unrelated-dir", "999999", "4244", time.Hour)

	var killed []int
	sweepStale(root, userdir.Owned, func(pid int) bool { return pid == os.Getpid() }, func(pid int) { killed = append(killed, pid) })

	gone := func(d string) bool { _, err := os.Stat(d); return os.IsNotExist(err) }
	if gone(live) {
		t.Error("a browser whose daemon is alive is not stale")
	}
	if !gone(dead) || !gone(bare) {
		t.Error("leftovers of a dead daemon, and old directories nobody owns, are removed")
	}
	if gone(fresh) {
		t.Error("a directory that was only just made may be mid-launch")
	}
	if gone(other) {
		t.Error("only Riffle's own directories are touched")
	}
	if !slices.Equal(killed, []int{4243}) {
		t.Errorf("killed %v, want only the browser of the dead daemon (4243)", killed)
	}
}

func TestSweepNeverActsOnAnotherUsersDirectory(t *testing.T) {
	root := t.TempDir()
	planted := filepath.Join(root, "riffle-chrome-planted")
	if err := os.Mkdir(planted, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(planted, "owner"), []byte("999999"), 0o600)
	_ = os.WriteFile(filepath.Join(planted, "chrome"), []byte("4245"), 0o600)
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(planted, old, old)

	var killed []int
	notMine := func(os.FileInfo) bool { return false }
	sweepStale(root, notMine, func(int) bool { return false }, func(pid int) { killed = append(killed, pid) })

	if len(killed) != 0 {
		t.Errorf("killed %v: a directory another user made names whatever process they like", killed)
	}
	if _, err := os.Stat(planted); err != nil {
		t.Error("another user's directory is left alone")
	}
}

func TestLaunchArgsKeepChromeFromWakingItsUpdater(t *testing.T) {
	args := launchArgs("/tmp/p")
	for _, want := range []string{"--disable-updater-scheduler", "--disable-component-update", "--disable-background-networking"} {
		if !slices.Contains(args, want) {
			t.Errorf("launch args lack %s: the browser would start work that outlives it and calls home", want)
		}
	}
}
