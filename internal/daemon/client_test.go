package daemon

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/wire"
)

// fakeServer speaks wire on a unix socket. handler maps a request to a reply;
// dropAfter closes each connection after that many replies (0 = never).
type fakeServer struct {
	sock      string
	ln        net.Listener
	conns     atomic.Int32
	dropAfter int
	handler   func(wire.Request) wire.Reply
}

func newFakeServer(t *testing.T, h func(wire.Request) wire.Reply, dropAfter int) *fakeServer {
	t.Helper()
	dir, err := os.MkdirTemp("", "rf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	s := &fakeServer{sock: filepath.Join(dir, "s"), handler: h, dropAfter: dropAfter}
	s.ln, err = (&net.ListenConfig{}).Listen(t.Context(), "unix", s.sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.ln.Close() })
	go func() {
		for {
			c, err := s.ln.Accept()
			if err != nil {
				return
			}
			s.conns.Add(1)
			go s.serve(c)
		}
	}()
	return s
}

func (s *fakeServer) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	br := bufio.NewReader(c)
	for n := 0; s.dropAfter == 0 || n < s.dropAfter; n++ {
		req, err := wire.ReadRequest(br)
		if err != nil {
			return
		}
		if err := wire.WriteReply(c, s.handler(req)); err != nil {
			return
		}
	}
}

func echo(r wire.Request) wire.Reply {
	return wire.Reply{Body: r.Verb + "/" + r.Session + ":" + r.Body}
}

func TestDoReturnsBodyForOKReply(t *testing.T) {
	s := newFakeServer(t, echo, 0)
	c := NewClient(s.sock)
	defer c.Close()
	for _, body := range []string{"", "click b1\nview", "héllo"} {
		got, err := c.Do(context.Background(), wire.Request{Verb: wire.Run, Session: "a", Body: body})
		if err != nil {
			t.Fatal(err)
		}
		if want := "run/a:" + body; got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	}
	if n := s.conns.Load(); n != 1 {
		t.Fatalf("connections = %d, want 1 (connection is reused)", n)
	}
}

func TestDoErrorCarriesBodyForErrReply(t *testing.T) {
	s := newFakeServer(t, func(wire.Request) wire.Reply {
		return wire.Reply{Failed: true, Body: "no such session\nstart one first"}
	}, 0)
	c := NewClient(s.sock)
	defer c.Close()
	got, err := c.Do(context.Background(), wire.Request{Verb: wire.View, Session: "a"})
	if err == nil || err.Error() != "no such session\nstart one first" {
		t.Fatalf("err = %v", err)
	}
	if got != "" {
		t.Fatalf("body = %q", got)
	}
	// An err reply is not a transport failure: the connection stays usable.
	if _, err := c.Do(context.Background(), wire.Request{Verb: wire.View, Session: "a"}); err == nil || strings.Contains(err.Error(), "riffle:") {
		t.Fatalf("second call err = %v", err)
	}
	if n := s.conns.Load(); n != 1 {
		t.Fatalf("connections = %d, want 1", n)
	}
}

func TestDoReconnectsAfterServerClosesConnection(t *testing.T) {
	s := newFakeServer(t, echo, 1) // server hangs up after every reply
	c := NewClient(s.sock)
	defer c.Close()
	req := wire.Request{Verb: wire.Run, Session: "a", Body: "x"}
	if _, err := c.Do(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	// The next call hits the dead connection. It must fail visibly, then recover.
	var recovered bool
	for i := 0; i < 3; i++ {
		got, err := c.Do(context.Background(), req)
		if err == nil {
			if got != "run/a:x" {
				t.Fatalf("got %q", got)
			}
			recovered = true
			break
		}
	}
	if !recovered {
		t.Fatal("client never reconnected")
	}
	if n := s.conns.Load(); n < 2 {
		t.Fatalf("connections = %d, want >= 2", n)
	}
}

func TestDoRejectsBadSessionWithoutHangingUp(t *testing.T) {
	s := newFakeServer(t, echo, 0)
	c := NewClient(s.sock)
	defer c.Close()
	if _, err := c.Do(context.Background(), wire.Request{Verb: wire.Run, Session: "bad name"}); err == nil {
		t.Fatal("expected error")
	}
}

// swallowServer answers a request normally unless its body is "die", in which
// case it counts the frame and hangs up without a reply, as a daemon that
// crashed mid-program would. frames counts every "die" frame it received.
type swallowServer struct {
	sock   string
	frames atomic.Int32
}

func newSwallowServer(t *testing.T) *swallowServer {
	t.Helper()
	dir, err := os.MkdirTemp("", "rs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	s := &swallowServer{sock: filepath.Join(dir, "s")}
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", s.sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				br := bufio.NewReader(c)
				for {
					req, err := wire.ReadRequest(br)
					if err != nil {
						return
					}
					if req.Body == "die" {
						s.frames.Add(1)
						return
					}
					if wire.WriteReply(c, wire.Reply{Body: "ok"}) != nil {
						return
					}
				}
			}()
		}
	}()
	return s
}

func TestViewIsRetriedOnceAfterAStaleConnectionButRunNeverIs(t *testing.T) {
	for _, tc := range []struct {
		verb       string
		wantFrames int32
	}{
		{wire.View, 2}, // first attempt on the stale connection, one retry on a fresh one
		{wire.Run, 1},  // a program may have partly run: sent exactly once
	} {
		t.Run(tc.verb, func(t *testing.T) {
			s := newSwallowServer(t)
			c := NewClient(s.sock)
			defer c.Close()
			ctx := context.Background()
			if _, err := c.Do(ctx, wire.Request{Verb: wire.View, Session: "a", Body: "warm"}); err != nil {
				t.Fatal(err)
			}
			_, err := c.Do(ctx, wire.Request{Verb: tc.verb, Session: "a", Body: "die"})
			if err == nil {
				t.Fatal("the daemon never replied; want an error")
			}
			if got := s.frames.Load(); got != tc.wantFrames {
				t.Errorf("server received %d frames, want %d", got, tc.wantFrames)
			}
			// The client recovers on the next call.
			if got, err := c.Do(ctx, wire.Request{Verb: tc.verb, Session: "a", Body: "again"}); err != nil || got != "ok" {
				t.Errorf("next call = %q, %v", got, err)
			}
		})
	}
}

func TestRetryExhaustionReportsTheReceiveFailure(t *testing.T) {
	s := newSwallowServer(t)
	c := NewClient(s.sock)
	defer c.Close()
	ctx := context.Background()
	if _, err := c.Do(ctx, wire.Request{Verb: wire.View, Session: "a", Body: "warm"}); err != nil {
		t.Fatal(err)
	}
	_, err := c.Do(ctx, wire.Request{Verb: wire.View, Session: "a", Body: "die"})
	if err == nil || !strings.HasPrefix(err.Error(), "riffle: receive:") {
		t.Fatalf("err = %v, want it to start with %q", err, "riffle: receive:")
	}
	if got := s.frames.Load(); got != 2 {
		t.Errorf("a view is retried exactly once, server saw %d frames", got)
	}
}

func TestExchangeReturnsAStoppedReplyWithoutAnError(t *testing.T) {
	s := newFakeServer(t, func(r wire.Request) wire.Reply {
		return wire.Reply{Body: "failed line 2: boom", Stopped: true}
	}, 0)
	c := NewClient(s.sock)
	defer c.Close()
	rep, err := c.Exchange(context.Background(), wire.Request{Verb: wire.Run, Session: "a", Body: "x"})
	if err != nil {
		t.Fatalf("a program that stopped is an answer, not an error: %v", err)
	}
	if !rep.Stopped || rep.Body != "failed line 2: boom" {
		t.Errorf("reply = %+v", rep)
	}
	if body, err := c.Do(context.Background(), wire.Request{Verb: wire.Run, Session: "a", Body: "x"}); err != nil || body != rep.Body {
		t.Errorf("Do must return the body of a stopped reply: %q, %v", body, err)
	}
}

// TestMain lets the test binary stand in for the riffle binary that Client
// spawns when no daemon listens: as "serve" it lingers without listening, a
// daemon that is slow to come up. It exits by itself so no process is left.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		if f := os.Getenv("RIFFLE_TEST_SPAWN_ARGS"); f != "" {
			_ = os.WriteFile(f, []byte(strings.Join(os.Args[1:], " ")), 0o600)
		}
		time.Sleep(3 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCancellingTheContextWhileWaitingForTheDaemonStopsTheConnect(t *testing.T) {
	c := NewClient(filepath.Join(t.TempDir(), "none.sock"))
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Exchange(ctx, wire.Request{Verb: wire.View, Session: "a"})
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2500 * time.Millisecond): // well under the 10s start deadline and the 3s lifetime of the stand-in
		t.Fatal("Exchange kept waiting for the daemon after its context was cancelled")
	}
}

func TestClientRefusesASocketInADirectoryOthersCanWrite(t *testing.T) {
	s := newFakeServer(t, echo, 0)
	if err := os.Chmod(filepath.Dir(s.sock), 0o777); err != nil {
		t.Fatal(err)
	}
	c := NewClient(s.sock)
	defer c.Close()
	_, err := c.Exchange(t.Context(), wire.Request{Verb: wire.View, Session: "a"})
	if err == nil || !strings.Contains(err.Error(), "socket directory") {
		t.Fatalf("err = %v: a socket another user could have planted must not be trusted", err)
	}
	if s.conns.Load() != 0 {
		t.Error("the client must not connect before checking the directory")
	}
}

func TestADaemonStartedAgainGetsThePolicyItLastRanWith(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "riffle.sock")
	hardened := Policy{Origins: []string{"https://shop.example"}, AllowEval: true}
	if err := writeRecord(sock, hardened); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("RIFFLE_TEST_SPAWN_ARGS", argsFile)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	c := NewClient(sock)
	defer c.Close()
	go func() { _, _ = c.Exchange(ctx, wire.Request{Verb: wire.View, Session: "a"}) }()
	var args string
	for ctx.Err() == nil && args == "" {
		b, _ := os.ReadFile(argsFile)
		args = string(b)
		time.Sleep(20 * time.Millisecond)
	}
	if want := "serve -socket " + sock + " -allow-eval -allow-origin https://shop.example"; args != want {
		t.Errorf("a crashed daemon came back as %q, want %q: the operator's restrictions must survive a restart", args, want)
	}
}

func TestAClientThatInsistsOnAPolicyStartsTheDaemonWithIt(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "riffle.sock")
	if err := writeRecord(sock, Policy{AllowPrivate: true}); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("RIFFLE_TEST_SPAWN_ARGS", argsFile)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	c := NewClient(sock).Expect(Policy{})
	defer c.Close()
	go func() { _, _ = c.Exchange(ctx, wire.Request{Verb: wire.View, Session: "a"}) }()
	var args string
	for ctx.Err() == nil && args == "" {
		b, _ := os.ReadFile(argsFile)
		args = string(b)
		time.Sleep(20 * time.Millisecond)
	}
	if want := "serve -socket " + sock; args != want {
		t.Errorf("started as %q, want %q", args, want)
	}
}

func TestAClientThatInsistsOnAPolicyRefusesADaemonWithAnother(t *testing.T) {
	s := newFakeServer(t, echo, 0)
	if err := writeRecord(s.sock, Policy{AllowPrivate: true, AllowEval: true}); err != nil {
		t.Fatal(err)
	}
	c := NewClient(s.sock).Expect(Policy{AllowPrivate: true})
	defer c.Close()
	_, err := c.Exchange(t.Context(), wire.Request{Verb: wire.View, Session: "a"})
	if err == nil || !strings.Contains(err.Error(), "-allow-private -allow-eval") || !strings.Contains(err.Error(), "kill "+strconv.Itoa(os.Getpid())) {
		t.Fatalf("err = %v: the refusal names the daemon's policy and how to stop it", err)
	}

	same := NewClient(s.sock).Expect(Policy{AllowEval: true, AllowPrivate: true})
	defer same.Close()
	if _, err := same.Exchange(t.Context(), wire.Request{Verb: wire.View, Session: "a"}); err != nil {
		t.Errorf("a daemon with the same policy is used: %v", err)
	}
	any := NewClient(s.sock)
	defer any.Close()
	if _, err := any.Exchange(t.Context(), wire.Request{Verb: wire.View, Session: "a"}); err != nil {
		t.Errorf("a client that names no policy uses the daemon as it is: %v", err)
	}
}

func TestAnUnreadablePolicyRecordFailsClosed(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "riffle.sock")
	if err := os.WriteFile(recordPath(sock), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("RIFFLE_TEST_SPAWN_ARGS", argsFile)
	c := NewClient(sock)
	defer c.Close()
	_, err := c.Exchange(t.Context(), wire.Request{Verb: wire.View, Session: "a"})
	if err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(argsFile); err == nil {
		t.Error("no daemon may be started with a guessed policy")
	}
}

func TestServeRecordsItsPolicyBeforeAcceptingClients(t *testing.T) {
	sock := filepath.Join(shortDir(t), "riffle.sock")
	p := Policy{Origins: []string{"https://a.example"}, UploadDir: "/up"}
	d, err := New(Config{Socket: sock, Policy: p})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- d.Serve(ctx) }()
	defer func() { cancel(); <-done }()
	for {
		if conn, err := (&net.Dialer{}).DialContext(ctx, "unix", sock); err == nil {
			_ = conn.Close()
			break
		}
		select {
		case err := <-done:
			t.Fatalf("serve stopped: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	rec, ok, err := readRecord(sock)
	if err != nil || !ok || !same(rec.Policy, p) || rec.PID != os.Getpid() {
		t.Errorf("record = %+v %v %v, want the policy %v of this process", rec, ok, err, p)
	}
}

// shortDir is a private directory with a path short enough for a unix
// socket, which macOS limits to 104 bytes.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
