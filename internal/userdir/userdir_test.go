package userdir_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/noetive/riffle/internal/userdir"
)

func TestEnsureCreatesAPrivateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	if err := userdir.Ensure(dir); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
		t.Errorf("a new directory is private, got %o", fi.Mode().Perm())
	}
	if err := userdir.Ensure(dir); err != nil {
		t.Errorf("a private directory of our own is accepted again: %v", err)
	}
}

func TestEnsureRefusesADirectoryOthersCanWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes are not enforced on Windows")
	}
	dir := filepath.Join(t.TempDir(), "open")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o775, 0o757, 0o777} {
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		if err := userdir.Ensure(dir); err == nil {
			t.Errorf("mode %o: a directory other users can write to must be refused, not adopted", mode)
		}
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o777 {
		t.Error("a refused directory is left as found")
	}
}

func TestEnsureRefusesASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	target := filepath.Join(root, "elsewhere")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := userdir.Ensure(link); err == nil {
		t.Error("a symlink planted in place of the directory must be refused")
	}
}

func TestEnsureRefusesAFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := userdir.Ensure(f); err == nil {
		t.Error("a file is not a directory")
	}
}

func TestOwnedRecognisesOurOwnFiles(t *testing.T) {
	fi, err := os.Lstat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !userdir.Owned(fi) {
		t.Error("a directory we just made is ours")
	}
}

func TestEnsureAcceptsADirectoryOthersCanOnlyRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes are not enforced on Windows")
	}
	dir := filepath.Join(t.TempDir(), "readable")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := userdir.Ensure(dir); err != nil {
		t.Errorf("others cannot plant anything in a directory they can only read: %v", err)
	}
}
