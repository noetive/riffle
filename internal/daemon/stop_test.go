package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/filelock"
	"github.com/noetive/riffle/internal/wire"
)

// The promise of stop: once it returns, no browser of that daemon runs.
func TestStopEndsTheDaemonAndEverySessionBeforeItReturns(t *testing.T) {
	c, live, served := serving(t, Config{Policy: Policy{AllowEval: true}, SessionIdle: time.Hour, DaemonIdle: time.Hour})
	for _, s := range []string{"a", "b"} {
		if _, err := run(t, c, s); err != nil {
			t.Fatal(err)
		}
	}
	holder := NewClient(c.socket).Hold("a")
	t.Cleanup(holder.Close)
	if _, err := holder.Kept(t.Context()); err != nil {
		t.Fatal(err)
	}
	out, found, err := Stop(t.Context(), c.socket)
	if err != nil || !found {
		t.Fatalf("stop: %v %v", found, err)
	}
	if n := live.get(); n != 0 {
		t.Errorf("%d browsers still run after stop returned, one of them held", n)
	}
	if !strings.Contains(out, "pid "+strconv.Itoa(os.Getpid())) || !strings.Contains(out, "-allow-eval") {
		t.Errorf("stop says which daemon it stopped and what it ran with: %q", out)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("a stopped daemon ends without an error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve kept running after stop")
	}
	if conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", c.socket); err == nil {
		_ = conn.Close()
		t.Error("a stopped daemon still takes clients")
	}
}

func TestStopNeverStartsADaemon(t *testing.T) {
	sock := filepath.Join(shortDir(t), "s")
	t.Setenv("RIFFLE_TEST_SPAWN_ARGS", filepath.Join(t.TempDir(), "args"))
	out, found, err := Stop(t.Context(), sock)
	if err != nil || found || out != "" {
		t.Errorf("with no daemon there is nothing to stop: %q %v %v", out, found, err)
	}
	if _, err := os.Stat(os.Getenv("RIFFLE_TEST_SPAWN_ARGS")); err == nil {
		t.Error("stop started a daemon")
	}
}

func TestServeFindsADaemonAlreadyServingAndChangesNothing(t *testing.T) {
	running := Policy{AllowEval: true}
	c, _, _ := serving(t, Config{Policy: running, SessionIdle: time.Hour, DaemonIdle: time.Hour})
	d, err := New(Config{Socket: c.socket, Policy: Policy{AllowPrivate: true}, QuotaDir: filepath.Join(filepath.Dir(c.socket), "browsers")})
	if err != nil {
		t.Fatal(err)
	}
	err = d.Serve(t.Context())
	var s *Serving
	if !errors.As(err, &s) {
		t.Fatalf("err = %v, want the daemon already serving", err)
	}
	if !s.Runs(running) || s.Runs(Policy{AllowPrivate: true}) || s.PID != os.Getpid() {
		t.Errorf("Serving = %+v: it describes the running daemon", s)
	}
	if !strings.Contains(err.Error(), "-allow-eval") || !strings.Contains(err.Error(), c.socket) {
		t.Errorf("the error names the socket and the running policy: %v", err)
	}
	if p, err := LastPolicy(c.socket); err != nil || !same(p, running) {
		t.Errorf("the record still says %v (%v), want %v", p, err, running)
	}
	if _, err := run(t, c, "a"); err != nil {
		t.Errorf("the running daemon still serves: %v", err)
	}
}

// Once the old daemon has agreed to stop, the socket's policy is the new
// one: a client that starts a daemon after it starts it with that.
func TestReplaceRecordsTheNewPolicyAndEndsTheOldDaemon(t *testing.T) {
	c, live, served := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour})
	if _, err := run(t, c, "a"); err != nil {
		t.Fatal(err)
	}
	want := Policy{AllowPrivate: true}
	out, found, err := Replace(t.Context(), c.socket, want)
	if err != nil || !found || !strings.Contains(out, "stopped") {
		t.Fatalf("replace: %q %v %v", out, found, err)
	}
	if n := live.get(); n != 0 {
		t.Errorf("%d browsers of the old daemon still run after replace returned", n)
	}
	if p, err := LastPolicy(c.socket); err != nil || !same(p, want) {
		t.Errorf("the record says %v (%v), want %v", p, err, want)
	}
	if err := <-served; err != nil {
		t.Errorf("the old daemon ends without an error: %v", err)
	}
}

func TestReplaceWithNoDaemonRunningLeavesTheRecordAlone(t *testing.T) {
	sock := filepath.Join(shortDir(t), "s")
	if err := writeRecord(sock, Policy{AllowEval: true}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Replace(t.Context(), sock, Policy{AllowPrivate: true}); err != nil || found {
		t.Errorf("there was nothing to replace: %v %v", found, err)
	}
	if p, _ := LastPolicy(sock); !same(p, Policy{AllowEval: true}) {
		t.Errorf("the record changed to %v with no daemon replaced", p)
	}
}

// A daemon from before stop existed answers that it does not know the verb:
// it is still running with its own policy, and the way past it is kill.
func TestStopOrReplaceOfAnEarlierRiffleSaysKillAndKeepsItsRecord(t *testing.T) {
	old := newFakeServer(t, func(r wire.Request) wire.Reply {
		return wire.Reply{Failed: true, Body: fmt.Sprintf("unknown verb %q; protocol 2 has run, view, close, archive, hold", r.Verb)}
	}, 0)
	kept := Policy{AllowEval: true}
	if err := writeRecord(old.sock, kept); err != nil {
		t.Fatal(err)
	}
	_, found, err := Replace(t.Context(), old.sock, Policy{AllowPrivate: true})
	if !found || err == nil || !strings.Contains(err.Error(), "earlier Riffle") || !strings.Contains(err.Error(), "kill "+strconv.Itoa(os.Getpid())) {
		t.Errorf("replace names the earlier daemon and its pid to kill: %v %v", found, err)
	}
	if p, _ := LastPolicy(old.sock); !same(p, kept) {
		t.Errorf("the record says %v, want the running daemon's %v", p, kept)
	}
	if _, _, err := Stop(t.Context(), old.sock); err == nil || !strings.Contains(err.Error(), "kill") || !strings.Contains(err.Error(), "leave out -socket") {
		t.Errorf("stop gives the same way past it: %v", err)
	}
	if err := os.Remove(recordPath(old.sock)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Replace(t.Context(), old.sock, Policy{AllowPrivate: true}); err == nil || strings.Contains(err.Error(), "kill ") {
		t.Errorf("with no record there is no pid to name: %v", err)
	}
	if _, found, err := readRecord(old.sock); found || err != nil {
		t.Errorf("a daemon that left no record is left without one: %v %v", found, err)
	}
}

// A client that insists on a policy checks the daemon it reaches after
// starting one: another client may have started its own first.
func TestAClientThatInsistsChecksTheDaemonItFindsAfterStartingOne(t *testing.T) {
	sock := filepath.Join(shortDir(t), "s")
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("RIFFLE_TEST_SPAWN_ARGS", argsFile)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	c := NewClient(sock).Expect(Policy{AllowPrivate: true})
	defer c.Close()
	done := make(chan error, 1)
	go func() {
		_, err := c.Exchange(ctx, wire.Request{Verb: wire.View, Session: "a"})
		done <- err
	}()
	eventually(t, "the client starts a daemon", func() bool { _, err := os.Stat(argsFile); return err == nil })
	if err := writeRecord(sock, Policy{AllowEval: true}); err != nil {
		t.Fatal(err)
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				br := bufio.NewReader(conn)
				for {
					if _, err := wire.ReadRequest(br); err != nil {
						return
					}
					_ = wire.WriteReply(conn, wire.Reply{Body: "served"})
				}
			}()
		}
	}()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "runs with -allow-eval") {
		t.Errorf("err = %v: a daemon with another policy is refused however it was reached", err)
	}
}

// Two daemons never listen on one socket: one that finds another owning it,
// even while that one starts or ends, waits rather than taking it over.
func TestServeWaitsForTheDaemonThatOwnsTheSocket(t *testing.T) {
	sock := filepath.Join(shortDir(t), "s")
	f, err := os.OpenFile(sock+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if owned, err := filelock.TryLock(f); !owned || err != nil {
		t.Fatalf("lock: %v %v", owned, err)
	}
	d, err := New(Config{Socket: sock, QuotaDir: filepath.Join(filepath.Dir(sock), "browsers")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- d.Serve(ctx) }()
	time.Sleep(200 * time.Millisecond)
	if conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", sock); err == nil {
		_ = conn.Close()
		t.Fatal("a daemon listened on a socket another owns")
	}
	_ = f.Close()
	eventually(t, "the daemon serves once the socket is free", func() bool {
		conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", sock)
		if err == nil {
			_ = conn.Close()
		}
		return err == nil
	})
	cancel()
	if err := <-served; err != nil {
		t.Errorf("serve: %v", err)
	}
}

// A daemon started with no policy named reads the socket's last one only
// once it owns the socket, so one recorded while it waited is the one used.
func TestADaemonThatKeepsThePolicyReadsItOnceItOwnsTheSocket(t *testing.T) {
	sock := filepath.Join(shortDir(t), "s")
	f, err := os.OpenFile(sock+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if owned, err := filelock.TryLock(f); !owned || err != nil {
		t.Fatalf("lock: %v %v", owned, err)
	}
	d, err := New(Config{Socket: sock, KeepPolicy: true, Policy: Policy{AllowEval: true}, QuotaDir: filepath.Join(filepath.Dir(sock), "browsers")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = d.Serve(ctx) }()
	time.Sleep(100 * time.Millisecond)
	want := Policy{Origins: []string{"https://a.example"}}
	if err := writeRecord(sock, want); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	other, err := New(Config{Socket: sock, QuotaDir: filepath.Join(filepath.Dir(sock), "browsers")})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the daemon serves", func() bool {
		conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", sock)
		if err == nil {
			_ = conn.Close()
		}
		return err == nil
	})
	probe, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	var s *Serving
	if err := other.Serve(probe); !errors.As(err, &s) {
		t.Fatalf("err = %v, want the daemon already serving", err)
	}
	if !s.Runs(want) {
		t.Errorf("the daemon runs with %v, want the policy recorded before it owned the socket, %v", s.Policy, want)
	}
}

// Stop says what a daemon started next would run with when that is not the
// default, since the record outlasts the daemon.
func TestStopSaysWhatTheNextDaemonRunsWith(t *testing.T) {
	c, _, _ := serving(t, Config{Policy: Policy{AllowPrivate: true}, SessionIdle: time.Hour, DaemonIdle: time.Hour})
	out, _, err := Stop(t.Context(), c.socket)
	if err != nil || !strings.Contains(out, "runs with -allow-private again") || !strings.Contains(out, "-allow-private=false") {
		t.Errorf("stop of a widened daemon says it comes back wide, and how not to: %q %v", out, err)
	}
	c, _, _ = serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour})
	if out, _, err := Stop(t.Context(), c.socket); err != nil || strings.Contains(out, "again") {
		t.Errorf("stop of a default daemon has nothing to warn of: %q %v", out, err)
	}
}

// After an upgrade, the daemon an operator wants gone may be an earlier
// Riffle's, beside this one's socket.
func TestStopPointsAtAnEarlierRiffleRunningBesideIt(t *testing.T) {
	privateCache(t)
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socketFor(2))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	if err := writeRecord(socketFor(2), Policy{}); err != nil {
		t.Fatal(err)
	}
	_, found, err := Stop(t.Context(), DefaultSocket())
	if found || err == nil || !strings.Contains(err.Error(), socketFor(2)) || !strings.Contains(err.Error(), "kill "+strconv.Itoa(os.Getpid())) {
		t.Errorf("stop names the earlier daemon and how to stop it: %v %v", found, err)
	}
	_ = ln.Close()
	if _, found, err := Stop(t.Context(), DefaultSocket()); found || err != nil {
		t.Errorf("with nothing running anywhere there is nothing to stop: %v %v", found, err)
	}
}

// A daemon already ending drops a new connection; stop waits until it is
// gone rather than reporting it stopped while it still listens.
func TestStoppingADaemonAlreadyEndingWaitsForIt(t *testing.T) {
	sock := filepath.Join(shortDir(t), "s")
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	closed := make(chan time.Time, 1)
	time.AfterFunc(300*time.Millisecond, func() { closed <- time.Now(); _ = ln.Close() })
	out, found, err := Stop(t.Context(), sock)
	if err != nil || !found || !strings.Contains(out, "already stopping") {
		t.Fatalf("stop: %q %v %v", out, found, err)
	}
	select {
	case <-closed:
	default:
		t.Error("stop returned while the ending daemon still listened")
	}
}

// The daemon a client starts in the gap, after the old one has gone and
// before the replacing serve listens, runs with the new policy: the old one
// recorded it before it let go of the socket.
func TestADaemonStartedInTheGapOfAReplaceRunsWithTheNewPolicy(t *testing.T) {
	c, _, _ := serving(t, Config{Policy: Policy{AllowPrivate: true}, SessionIdle: time.Hour, DaemonIdle: time.Hour})
	want := Policy{}
	if _, _, err := Replace(t.Context(), c.socket, want); err != nil {
		t.Fatal(err)
	}
	gap, err := New(Config{Socket: c.socket, KeepPolicy: true, Policy: Policy{AllowPrivate: true}, QuotaDir: filepath.Join(filepath.Dir(c.socket), "browsers")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = gap.Serve(ctx) }()
	eventually(t, "the gap daemon serves", func() bool {
		conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", c.socket)
		if err == nil {
			_ = conn.Close()
		}
		return err == nil
	})
	probe, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	other, err := New(Config{Socket: c.socket, QuotaDir: filepath.Join(filepath.Dir(c.socket), "browsers")})
	if err != nil {
		t.Fatal(err)
	}
	var s *Serving
	if err := other.Serve(probe); !errors.As(err, &s) || !s.Runs(want) {
		t.Errorf("the gap daemon runs with %v (%v), want %v", s, err, want)
	}
}

func TestAStopWhoseSuccessorCannotBeRecordedLeavesTheDaemonRunning(t *testing.T) {
	c, _, served := serving(t, Config{Policy: Policy{AllowEval: true}, SessionIdle: time.Hour, DaemonIdle: time.Hour})
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", c.socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := wire.WriteRequest(conn, wire.Request{Verb: wire.Stop, Session: wire.AllSessions, Body: "{"}); err != nil {
		t.Fatal(err)
	}
	rep, err := wire.ReadReply(bufio.NewReader(conn))
	if err != nil || !rep.Failed || !strings.Contains(rep.Body, "keeps running") {
		t.Fatalf("reply = %+v %v", rep, err)
	}
	if _, err := run(t, c, "a"); err != nil {
		t.Errorf("the daemon takes work after a stop it refused: %v", err)
	}
	if p, _ := LastPolicy(c.socket); !same(p, Policy{AllowEval: true}) {
		t.Errorf("the record changed to %v", p)
	}
	select {
	case err := <-served:
		t.Fatalf("the daemon ended: %v", err)
	default:
	}
}

func TestTheCommandsAMessageNamesOmitTheDefaultSocket(t *testing.T) {
	privateCache(t)
	if got := StopCommand(DefaultSocket()); got != "riffle stop" {
		t.Errorf("StopCommand = %q", got)
	}
	if got := serveCommand(DefaultSocket(), Policy{AllowPrivate: true}); got != "riffle serve -allow-private" {
		t.Errorf("serveCommand = %q", got)
	}
	if got := serveCommand("/s", Policy{}); got != "riffle serve -socket /s -allow-private=false" {
		t.Errorf("serveCommand = %q", got)
	}
}

// An earlier Riffle runs beside the default socket only; a socket named by
// hand with nothing on it has nothing to point at.
func TestStopOfASocketNamedByHandLooksNowhereElse(t *testing.T) {
	privateCache(t)
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socketFor(2))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	if _, found, err := Stop(t.Context(), filepath.Join(shortDir(t), "s")); found || err != nil {
		t.Errorf("nothing runs on the socket named: %v %v", found, err)
	}
}

// The record names the daemon that is to run the policy, not the one that
// recorded it.
func TestReplaceRecordsTheReplacingProcess(t *testing.T) {
	c, _, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour})
	if _, _, err := Replace(t.Context(), c.socket, Policy{AllowEval: true}); err != nil {
		t.Fatal(err)
	}
	if rec, _, err := readRecord(c.socket); err != nil || rec.PID != os.Getpid() {
		t.Errorf("record = %+v %v, want pid %d", rec, err, os.Getpid())
	}
}

// A daemon already ending has stopped taking clients before it has ended
// its sessions; stop waits for the end, which is when it lets go of the
// socket.
func TestStoppingADaemonAlreadyEndingWaitsUntilItLetsGoOfTheSocket(t *testing.T) {
	sock := filepath.Join(shortDir(t), "s")
	f, err := os.OpenFile(lockPath(sock), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if owned, err := filelock.TryLock(f); !owned || err != nil {
		t.Fatalf("lock: %v %v", owned, err)
	}
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	time.AfterFunc(100*time.Millisecond, func() { _ = ln.Close() })
	released := make(chan time.Time, 1)
	time.AfterFunc(400*time.Millisecond, func() { released <- time.Now(); _ = f.Close() })
	if _, _, err := Stop(t.Context(), sock); err != nil {
		t.Fatal(err)
	}
	select {
	case <-released:
	default:
		t.Error("stop returned while the ending daemon still owned the socket")
	}
}

// A daemon of a Riffle from before the lock owns nothing, and still serves.
func TestServeFindsADaemonThatHoldsNoLock(t *testing.T) {
	s := newFakeServer(t, echo, 0)
	d, err := New(Config{Socket: s.sock, QuotaDir: filepath.Join(filepath.Dir(s.sock), "browsers")})
	if err != nil {
		t.Fatal(err)
	}
	var serving *Serving
	if err := d.Serve(t.Context()); !errors.As(err, &serving) {
		t.Errorf("err = %v, want the daemon already serving", err)
	}
}
