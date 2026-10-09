package daemon

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/engine"
	"github.com/noetive/riffle/internal/session"
	"github.com/noetive/riffle/internal/wire"
)

// fakeBrowsing is a session whose browser is a counter of live instances.
type fakeBrowsing struct {
	live *liveCount
	// state is what the browser would keep: the last program it ran.
	state  engine.State
	closed bool
	mu     sync.Mutex
}

type liveCount struct {
	all []*fakeBrowsing
	// restored is the state each browser started from, in order.
	restored []engine.State
	// unreadable makes every browser fail to report its state.
	unreadable bool
	n          int
	mu         sync.Mutex
}

// stopAll makes every browser stop on its own, as a crash would.
func stopAll(l *liveCount) {
	l.mu.Lock()
	all := l.all
	l.mu.Unlock()
	for _, f := range all {
		f.Close()
	}
}

func (l *liveCount) get() int { l.mu.Lock(); defer l.mu.Unlock(); return l.n }

func (f *fakeBrowsing) Do(_ context.Context, src string) session.Outcome {
	if src == "slow" {
		time.Sleep(time.Second)
	}
	if !strings.HasPrefix(src, "view") {
		f.mu.Lock()
		f.state = ranState(src)
		f.mu.Unlock()
	}
	return session.Outcome{Text: "did " + src}
}
func (f *fakeBrowsing) Archive(context.Context) (string, error) { return "MHTML", nil }
func (f *fakeBrowsing) State(context.Context) (engine.State, error) {
	f.live.mu.Lock()
	unreadable := f.live.unreadable
	f.live.mu.Unlock()
	if unreadable {
		return engine.State{}, errors.New("the browser did not answer")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}
func (f *fakeBrowsing) Alive() bool { f.mu.Lock(); defer f.mu.Unlock(); return !f.closed }
func (f *fakeBrowsing) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		f.live.mu.Lock()
		f.live.n--
		f.live.mu.Unlock()
	}
}

// serving starts a daemon on a private socket with fake browsers, and
// returns a client for it, the count of live browsers and the end of Serve.
func serving(t *testing.T, cfg Config) (*Client, *liveCount, <-chan error) {
	t.Helper()
	// A short directory: a socket path is limited to about a hundred bytes.
	dir, err := os.MkdirTemp("", "rf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	cfg.Socket = filepath.Join(dir, "s.sock")
	if cfg.QuotaDir == "" {
		cfg.QuotaDir = filepath.Join(dir, "browsers")
	}
	if cfg.StateDir == "" {
		cfg.StateDir = filepath.Join(dir, "state")
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	live := &liveCount{}
	d.launch = func(_ context.Context, st engine.State) (browsing, error) {
		live.mu.Lock()
		live.n++
		live.restored = append(live.restored, st)
		f := &fakeBrowsing{live: live, state: st}
		live.all = append(live.all, f)
		live.mu.Unlock()
		return f, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	served := make(chan error, 1)
	go func() { served <- d.Serve(ctx) }()
	// Ready once it accepts: a client that finds no daemon starts one of its own.
	eventually(t, "the daemon listens", func() bool {
		select {
		case err := <-served:
			t.Fatalf("the daemon stopped before listening: %v", err)
		default:
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", cfg.Socket)
		if err == nil {
			_ = conn.Close()
		}
		return err == nil
	})
	c := NewClient(cfg.Socket)
	t.Cleanup(c.Close)
	return c, live, served
}

func run(t *testing.T, c *Client, session string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.Do(ctx, wire.Request{Verb: wire.Run, Session: session, Body: "goto https://example.com/"})
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("never: %s", what)
}

func TestNoMoreBrowsersRunThanTheMachineAllowsAndTheRefusalSaysWhoHoldsThem(t *testing.T) {
	c, live, _ := serving(t, Config{MaxBrowsers: 2, SeatWait: 200 * time.Millisecond, SessionIdle: time.Hour, DaemonIdle: time.Hour})
	for _, s := range []string{"one", "two"} {
		if _, err := run(t, c, s); err != nil {
			t.Fatal(err)
		}
	}
	_, err := run(t, c, "three")
	if err == nil {
		t.Fatal("a third browser is refused")
	}
	for _, want := range []string{`"one"`, `"two"`, "riffle close -s one -socket ", "none came free"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal says %s: %v", want, err)
		}
	}
	if n := live.get(); n != 2 {
		t.Errorf("%d browsers run, want 2", n)
	}
	if out, err := run(t, c, "one"); err != nil || !strings.Contains(out, "did goto") {
		t.Errorf("a session already running is unaffected: %q %v", out, err)
	}
}

func TestAnUnusedSessionIsClosedAndItsNextUseSaysSo(t *testing.T) {
	c, live, _ := serving(t, Config{SessionIdle: 50 * time.Millisecond, DaemonIdle: time.Hour})
	if _, err := run(t, c, "work"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the idle browser is closed", func() bool { return live.get() == 0 })
	out, err := run(t, c, "work")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "note: this session's browser was closed after") || !strings.Contains(out, "blank page") {
		t.Errorf("the next use says the browser was closed: %q", out)
	}
	if out, _ := run(t, c, "work"); strings.Contains(out, "note:") {
		t.Errorf("the note is told once: %q", out)
	}
}

// A session held by a connection, as an MCP server holds its own, is never
// closed for being idle; it ends when its last holder disconnects.
func TestAHeldSessionLivesWhileItsHolderIsConnected(t *testing.T) {
	c, live, _ := serving(t, Config{SessionIdle: 50 * time.Millisecond, DaemonIdle: time.Hour, HoldGrace: 50 * time.Millisecond})
	holder := NewClient(c.socket).Hold("agent")
	if _, err := run(t, holder, "agent"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if live.get() != 1 {
		t.Fatal("a held session is not closed for being idle")
	}
	holder.Close()
	eventually(t, "the session ends with its last holder", func() bool { return live.get() == 0 })
}

func TestCloseEndsASessionAndSaysWhenThereIsNone(t *testing.T) {
	c, live, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour})
	if _, err := run(t, c, "work"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	out, err := c.Do(ctx, wire.Request{Verb: wire.Close, Session: "work"})
	if err != nil || !strings.Contains(out, `closed session "work"`) {
		t.Fatalf("close: %q %v", out, err)
	}
	if live.get() != 0 {
		t.Error("the browser ended with its session")
	}
	out, err = c.Do(ctx, wire.Request{Verb: wire.Close, Session: "work"})
	if err != nil || !strings.Contains(out, "nothing to close") {
		t.Errorf("closing what is not open says so: %q %v", out, err)
	}
	if out, _ := run(t, c, "work"); !strings.HasPrefix(out, "note: this session was closed with `riffle close`") {
		t.Errorf("whoever uses a closed session next is told it was closed: %q", out)
	}
}

func TestADaemonWithNothingToServeEnds(t *testing.T) {
	c, _, served := serving(t, Config{SessionIdle: 30 * time.Millisecond, DaemonIdle: 100 * time.Millisecond})
	if _, err := run(t, c, "brief"); err != nil {
		t.Fatal(err)
	}
	c.Close()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("an idle daemon ends cleanly: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the idle daemon kept running")
	}
}

func TestADaemonWithAClientConnectedDoesNotEnd(t *testing.T) {
	c, _, served := serving(t, Config{SessionIdle: 30 * time.Millisecond, DaemonIdle: 50 * time.Millisecond})
	if _, err := run(t, c, "brief"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-served:
		t.Fatal("the daemon ended under a connected client")
	case <-time.After(400 * time.Millisecond):
	}
}

// Two clients holding one session: it lives until the last of them goes.
func TestASessionHeldTwiceEndsOnlyWithItsLastHolder(t *testing.T) {
	c, live, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour, HoldGrace: 50 * time.Millisecond})
	first := NewClient(c.socket).Hold("shared")
	second := NewClient(c.socket).Hold("shared")
	if _, err := run(t, first, "shared"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, second, "shared"); err != nil {
		t.Fatal(err)
	}
	first.Close()
	time.Sleep(200 * time.Millisecond)
	if live.get() != 1 {
		t.Fatal("the session ended while a holder was still connected")
	}
	second.Close()
	eventually(t, "the session ends with its last holder", func() bool { return live.get() == 0 })
}

// Holders of different sessions, as two editor windows are, each drive a
// browser of their own, and one leaving does not end the other's.
func TestHoldersOfDifferentSessionsDoNotShareABrowser(t *testing.T) {
	c, live, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour, HoldGrace: 50 * time.Millisecond})
	first := NewClient(c.socket).Hold("one")
	second := NewClient(c.socket).Hold("two")
	if _, err := run(t, first, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, second, "two"); err != nil {
		t.Fatal(err)
	}
	if n := live.get(); n != 2 {
		t.Fatalf("%d browsers for two sessions, want 2", n)
	}
	first.Close()
	eventually(t, "the session of the holder that left ends", func() bool { return live.get() == 1 })
	if out, err := run(t, second, "two"); err != nil || strings.Contains(out, "note:") {
		t.Errorf("the other session is unaffected: %q %v", out, err)
	}
}

// A note about a closed browser that nobody comes back for is not kept
// forever: an MCP server leaves one under a name no one uses again.
func TestANoteNobodyCameBackForIsDropped(t *testing.T) {
	c, _, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: 100 * time.Millisecond})
	if _, err := run(t, c, "gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(context.Background(), wire.Request{Verb: wire.Close, Session: "gone"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond) // past DaemonIdle; the connected client keeps the daemon up
	if out, err := run(t, c, "gone"); err != nil || strings.Contains(out, "note:") {
		t.Errorf("a note older than DaemonIdle is no longer told: %q %v", out, err)
	}
}

// A holder that reconnects within the grace finds its session still there.
func TestAHolderThatReconnectsKeepsItsSession(t *testing.T) {
	c, live, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour, HoldGrace: 2 * time.Second})
	holder := NewClient(c.socket).Hold("agent")
	if _, err := run(t, holder, "agent"); err != nil {
		t.Fatal(err)
	}
	holder.Close()                     // a dropped connection
	time.Sleep(800 * time.Millisecond) // past a tend round, within the grace
	again := NewClient(c.socket).Hold("agent")
	out, err := run(t, again, "agent")
	if err != nil || strings.Contains(out, "note:") || live.get() != 1 {
		t.Errorf("the session survived the reconnect: %q %v, %d browsers", out, err, live.get())
	}
}

// A held session whose browser stopped gives back its seat at once, and its
// next use says what happened.
func TestAStoppedBrowserIsEndedEvenWhenHeld(t *testing.T) {
	c, live, _ := serving(t, Config{MaxBrowsers: 1, SeatWait: 200 * time.Millisecond, SessionIdle: 40 * time.Millisecond, DaemonIdle: time.Hour})
	holder := NewClient(c.socket).Hold("agent")
	if _, err := run(t, holder, "agent"); err != nil {
		t.Fatal(err)
	}
	// The browser stops, as when Chrome crashes.
	stopAll(live)
	eventually(t, "the seat comes free", func() bool {
		_, err := run(t, c, "other")
		return err == nil
	})
	if _, err := c.Do(context.Background(), wire.Request{Verb: wire.Close, Session: "other"}); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, holder, "agent")
	if err != nil || !strings.HasPrefix(out, "note: this session's browser had stopped") {
		t.Errorf("the next use says the browser had stopped: %q %v", out, err)
	}
}

func TestCloseWaitsForNoRunningProgram(t *testing.T) {
	c, _, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour})
	if _, err := run(t, c, "busy"); err != nil {
		t.Fatal(err)
	}
	slow := NewClient(c.socket)
	started := make(chan struct{})
	go func() {
		close(started)
		_, _ = slow.Do(context.Background(), wire.Request{Verb: wire.Run, Session: "busy", Body: "slow"})
	}()
	<-started
	eventually(t, "close is refused while the program runs", func() bool {
		_, err := c.Do(context.Background(), wire.Request{Verb: wire.Close, Session: "busy"})
		return err != nil && strings.Contains(err.Error(), "running a program now")
	})
}

func TestTheRefusalTellsAHeldSessionFromAnIdleOne(t *testing.T) {
	c, _, _ := serving(t, Config{MaxBrowsers: 1, SeatWait: 200 * time.Millisecond, SessionIdle: time.Hour, DaemonIdle: time.Hour})
	holder := NewClient(c.socket).Hold("agent")
	if _, err := run(t, holder, "agent"); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, c, "other")
	if err == nil || !strings.Contains(err.Error(), "held open by a program") || strings.Contains(err.Error(), "riffle close -s agent") {
		t.Errorf("a held session is not offered for closing: %v", err)
	}
}

func TestASessionNameIsBounded(t *testing.T) {
	c, _, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour})
	_, err := run(t, c, strings.Repeat("x", 65))
	if err == nil || !strings.Contains(err.Error(), "at most 64") {
		t.Errorf("an overlong session name is refused: %v", err)
	}
}

func TestAnArchiveNeedsAnOpenSessionAndNeverStartsOne(t *testing.T) {
	c, live, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour})
	ctx := context.Background()
	_, err := c.Do(ctx, wire.Request{Verb: wire.Archive, Session: "none"})
	if err == nil || !strings.Contains(err.Error(), "no page to archive") || live.get() != 0 {
		t.Fatalf("an archive of no session is refused without a browser: %v, %d browsers", err, live.get())
	}
	if _, err := run(t, c, "work"); err != nil {
		t.Fatal(err)
	}
	if page, err := c.Do(ctx, wire.Request{Verb: wire.Archive, Session: "work"}); err != nil || page != "MHTML" {
		t.Errorf("archive of an open session = %q, %v", page, err)
	}
}

// The daemon serves every verb its protocol has and refuses any other, so a
// verb is only added by adding it to wire.Verbs, which changes the protocol.
func TestTheDaemonServesExactlyTheVerbsOfItsProtocol(t *testing.T) {
	c, _, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour})
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", c.socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	ask := func(verb string) wire.Reply {
		t.Helper()
		if err := wire.WriteRequest(conn, wire.Request{Verb: verb, Session: "s1"}); err != nil {
			t.Fatal(err)
		}
		rep, err := wire.ReadReply(br)
		if err != nil {
			t.Fatalf("%s: the daemon did not answer: %v", verb, err)
		}
		return rep
	}
	for _, v := range wire.Verbs {
		if rep := ask(v); strings.Contains(rep.Body, "unknown verb") {
			t.Errorf("%s is in protocol %d and is refused: %s", v, wire.Protocol, rep.Body)
		}
	}
	if rep := ask("jump"); !rep.Failed || !strings.Contains(rep.Body, "unknown verb") {
		t.Errorf("a verb outside the protocol is refused: %+v", rep)
	}
}
