package main

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/daemon"
)

// servingDir is a private directory with a path short enough for a unix
// socket, with the user's cache, where browser seats are kept, inside it.
func servingDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "c"))
	t.Setenv("LocalAppData", filepath.Join(dir, "l"))
	return dir
}

// background runs riffle serve with args until the test ends, and returns
// the end of it.
func background(t *testing.T, args ...string) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	done := make(chan struct{})
	go func() { served <- serve(ctx, io.Discard, args); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})
	return served
}

// listening waits until a daemon on sock with policy p takes clients.
func listening(t *testing.T, sock string, p daemon.Policy) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", sock)
		if err != nil {
			continue
		}
		_ = conn.Close()
		if got, err := daemon.LastPolicy(sock); err == nil && got.Within(p) && p.Within(got) {
			return
		}
	}
	t.Fatalf("no daemon with %v on %s", p, sock)
}

func TestServeWithAnotherPolicyTakesTheRunningDaemonsPlace(t *testing.T) {
	sock := filepath.Join(servingDir(t), "s")
	first := background(t, "-socket", sock)
	listening(t, sock, daemon.Policy{})
	background(t, "-socket", sock, "-allow-private")
	select {
	case err := <-first:
		if err != nil {
			t.Errorf("the daemon taken over ends without an error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the running daemon was left in place")
	}
	listening(t, sock, daemon.Policy{AllowPrivate: true})
}

// Agents run riffle serve before they browse; one that names no policy
// must not end the sessions of the daemon they share.
func TestBareServeLeavesARunningDaemonAlone(t *testing.T) {
	sock := filepath.Join(servingDir(t), "s")
	first := background(t, "-socket", sock, "-allow-eval")
	listening(t, sock, daemon.Policy{AllowEval: true})
	var out strings.Builder
	if err := serve(t.Context(), &out, []string{"-socket", sock}); err != nil || !strings.Contains(out.String(), "already serving") || !strings.Contains(out.String(), "takes its place") {
		t.Errorf("a daemon already serving is what was asked for, and serve says how to change it: %q %v", out.String(), err)
	}
	out.Reset()
	if err := serve(t.Context(), &out, []string{"-socket", sock, "-allow-eval"}); err != nil || !strings.Contains(out.String(), "-allow-eval") || strings.Contains(out.String(), "takes its place") {
		t.Errorf("a daemon already serving with the policy named is what was asked for: %q %v", out.String(), err)
	}
	select {
	case err := <-first:
		t.Fatalf("the running daemon ended: %v", err)
	default:
	}
	listening(t, sock, daemon.Policy{AllowEval: true})
}

func TestServeRefusesLimitsItCannotApplyToARunningDaemon(t *testing.T) {
	sock := filepath.Join(servingDir(t), "s")
	first := background(t, "-socket", sock, "-allow-eval")
	listening(t, sock, daemon.Policy{AllowEval: true})
	err := serve(t.Context(), io.Discard, []string{"-socket", sock, "-allow-eval", "-max-browsers", "2"})
	if err == nil || !strings.Contains(err.Error(), "-max-browsers") || !strings.Contains(err.Error(), "riffle stop -socket "+sock) {
		t.Errorf("a limit the running daemon would not take is an error saying how to apply it: %v", err)
	}
	select {
	case err := <-first:
		t.Fatalf("the running daemon ended: %v", err)
	default:
	}
}

// A restriction the operator gave survives a serve that names none.
func TestBareServeStartsWithThePolicyTheSocketLastRanWith(t *testing.T) {
	sock := filepath.Join(servingDir(t), "s")
	restricted := daemon.Policy{Origins: []string{"https://a.example"}}
	first := background(t, "-socket", sock, "-allow-origin", "https://a.example")
	listening(t, sock, restricted)
	var out strings.Builder
	if err := stopDaemon(t.Context(), &out, []string{"-socket", sock}); err != nil || !strings.Contains(out.String(), "stopped the daemon") {
		t.Fatalf("stop: %q %v", out.String(), err)
	}
	if err := <-first; err != nil {
		t.Fatalf("stopped daemon: %v", err)
	}
	background(t, "-socket", sock)
	listening(t, sock, restricted)
}

func TestStopWithNoDaemonHasNothingToDo(t *testing.T) {
	sock := filepath.Join(servingDir(t), "s")
	var out strings.Builder
	if err := stopDaemon(t.Context(), &out, []string{"-socket", sock}); err != nil || !strings.Contains(out.String(), "nothing to stop") {
		t.Errorf("no daemon is nothing to stop, not a failure: %q %v", out.String(), err)
	}
	if _, err := os.Stat(sock); err == nil {
		t.Error("stop started a daemon")
	}
}

// A daemon a client starts never takes another's place: two editor entries
// asking for different policies at once must not end each other's sessions.
func TestAServeAClientStartsLeavesADaemonWithAnotherPolicyAlone(t *testing.T) {
	sock := filepath.Join(servingDir(t), "s")
	first := background(t, "-socket", sock, "-allow-eval")
	listening(t, sock, daemon.Policy{AllowEval: true})
	if err := serve(t.Context(), io.Discard, []string{"-socket", sock, "-start-only", "-allow-private"}); err != nil {
		t.Errorf("a daemon already serving leaves a starting one nothing to do: %v", err)
	}
	select {
	case err := <-first:
		t.Fatalf("the running daemon ended: %v", err)
	default:
	}
	listening(t, sock, daemon.Policy{AllowEval: true})
}

// A record that cannot be read is never taken for the default policy, which
// would undo the operator's restrictions.
func TestBareServeRefusesAnUnreadableRecord(t *testing.T) {
	sock := filepath.Join(servingDir(t), "s")
	if err := os.WriteFile(sock+".policy", []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := serve(t.Context(), io.Discard, []string{"-socket", sock}); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Errorf("err = %v", err)
	}
	if conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", sock); err == nil {
		_ = conn.Close()
		t.Error("a daemon serves with a guessed policy")
	}
}
