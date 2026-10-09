package daemon

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	json "github.com/goccy/go-json"

	"github.com/noetive/riffle/internal/userdir"
	"github.com/noetive/riffle/internal/wire"
)

// DefaultSocket is where the daemon listens unless told otherwise: a private
// per-user directory, so no other user can stand in for the daemon. Each wire
// protocol has its own, so a client never reaches a daemon another Riffle
// left running that would not understand it: that one keeps serving its own
// clients beside this one until it idles out.
func DefaultSocket() string { return socketFor(wire.Protocol) }

// socketFor is the default socket of protocol p. Protocol 2 is the one the
// first releases spoke, before sockets were named after a protocol, so it
// keeps their riffle.sock.
func socketFor(p int) string {
	if p <= 2 {
		return filepath.Join(riffleDir(), "riffle.sock")
	}
	return filepath.Join(riffleDir(), fmt.Sprintf("riffle-p%d.sock", p))
}

// earlierSockets are the default sockets of the protocols before p, newest
// first.
func earlierSockets(p int) []string {
	var socks []string
	for q := p - 1; q >= 2; q-- {
		socks = append(socks, socketFor(q))
	}
	return socks
}

func riffleDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "riffle")
	}
	return filepath.Join(os.TempDir(), "riffle-"+strconv.Itoa(os.Getuid()))
}

// Client talks to a daemon, starting one in the background when none is listening.
type Client struct {
	c  net.Conn      // guarded by mu
	br *bufio.Reader // guarded by mu
	// want is the policy this client was started with, nil when it takes
	// whatever policy the operator gave the daemon.
	want   *Policy
	socket string
	// holding is the session this client holds, announced on each connection.
	holding string
	// kept is where the daemon keeps the held session's state, once it said.
	kept string
	// keep asks for the held session's state to be kept.
	keep bool

	mu sync.Mutex
}

// NewClient returns a client for the socket; the connection is made on first use.
func NewClient(socket string) *Client { return &Client{socket: socket} }

// Expect makes the client insist on policy p: it starts the daemon with p,
// and refuses a running daemon whose policy is any other.
func (c *Client) Expect(p Policy) *Client {
	c.want = &p
	return c
}

// Exchange sends one request and returns the reply. A failed reply is an error
// whose message is the daemon's explanation.
func (c *Client) Exchange(ctx context.Context, req wire.Request) (wire.Reply, error) {
	if err := req.Validate(); err != nil {
		return wire.Reply{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// A connection that died while idle fails the first exchange. Reading is
	// safe to repeat; a program might have partly run, so it is never resent.
	for attempt := 0; ; attempt++ {
		reused := c.c != nil
		if !reused {
			if err := c.connect(ctx); err != nil {
				return wire.Reply{}, err
			}
			if err := c.announce(); err != nil {
				return wire.Reply{}, err
			}
		}
		if err := wire.WriteRequest(c.c, req); err != nil {
			c.drop()
			if reused && attempt == 0 {
				continue
			}
			return wire.Reply{}, fmt.Errorf("riffle: send: %w", err)
		}
		rep, err := wire.ReadReply(c.br)
		if err != nil {
			c.drop()
			if reused && attempt == 0 && req.Verb == wire.View {
				continue
			}
			return wire.Reply{}, fmt.Errorf("riffle: receive: %w", err)
		}
		if rep.Failed {
			return wire.Reply{}, fmt.Errorf("%s", rep.Body)
		}
		return rep, nil
	}
}

func (c *Client) drop() {
	_ = c.c.Close()
	c.c, c.br = nil, nil
}

// Close ends the connection; the daemon and its sessions keep running.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.c != nil {
		c.drop()
	}
}

func (c *Client) connect(ctx context.Context) error {
	// A socket in a directory another user can write to may be theirs, and
	// so may every reply read from it.
	if err := userdir.Ensure(filepath.Dir(c.socket)); err != nil {
		return fmt.Errorf("riffle: socket directory: %w", err)
	}
	// A record that cannot be read fails here, before a daemon is started
	// that would fail on it out of sight.
	if _, err := LastPolicy(c.socket); err != nil {
		return fmt.Errorf("riffle: %w", err)
	}
	if conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.socket); err == nil {
		return c.accept(conn)
	}
	if err := ctx.Err(); err != nil {
		return err // a caller that gave up must not leave a daemon behind
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("riffle: locate own binary: %w", err)
	}
	// A client that insists on a policy starts the daemon with it. Any other
	// starts one that names none, which reads the policy the socket last ran
	// with once it owns the socket, so a record read here a moment ago is
	// never replayed over a newer one. Either only starts a daemon: one that
	// finds another serving leaves it be, and accept judges that one.
	args := []string{"serve", "-socket", c.socket, "-start-only"}
	if c.want != nil {
		args = append(args, c.want.Args()...)
	}
	// The daemon must outlive this client, so it is not tied to ctx.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), self, args...)
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("riffle: start daemon: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.socket); err == nil {
			// Another client may have started its own daemon first.
			return c.accept(conn)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return fmt.Errorf("riffle: daemon did not start on %s", c.socket)
}

// accept makes conn the client's connection, unless the client insists on a
// policy and the daemon on the other end runs with another.
func (c *Client) accept(conn net.Conn) error {
	if c.want != nil {
		rec, recorded, err := readRecord(c.socket)
		if err != nil {
			_ = conn.Close()
			return fmt.Errorf("riffle: %w", err)
		}
		if !recorded || !same(rec.Policy, *c.want) {
			_ = conn.Close()
			return c.mismatch(rec, recorded)
		}
	}
	c.c, c.br = conn, bufio.NewReader(conn)
	return nil
}

// Do sends one request and returns the reply body, whether or not the
// program it carried stopped early.
func (c *Client) Do(ctx context.Context, req wire.Request) (string, error) {
	rep, err := c.Exchange(ctx, req)
	return rep.Body, err
}

// same reports whether two policies allow exactly the same.
func same(p, q Policy) bool { return p.Within(q) && q.Within(p) }

// mismatch explains why a client that insists on a policy will not use the
// daemon that is running, and what to do about it.
func (c *Client) mismatch(rec record, recorded bool) error {
	next := fmt.Sprintf("run `%s` and run this again, or `%s` to take its place; either ends the sessions open in it", StopCommand(c.socket), serveCommand(c.socket, *c.want))
	if !recorded {
		return fmt.Errorf("riffle: the daemon on %s runs with a policy it did not record, and this command asks for %s; %s", c.socket, c.want, next)
	}
	return fmt.Errorf("riffle: the daemon on %s (pid %d) runs with %s, and this command asks for %s; %s", c.socket, rec.PID, rec.Policy, c.want, next)
}

// StopCommand is the command line that stops the daemon on socket.
func StopCommand(socket string) string {
	if socket == DefaultSocket() {
		return "riffle stop"
	}
	return "riffle stop -socket " + socket
}

// serveCommand is the command line that serves socket with policy p, in the
// place of any daemon running there with another.
func serveCommand(socket string, p Policy) string {
	args := []string{"riffle", "serve"}
	if socket != DefaultSocket() {
		args = append(args, "-socket", socket)
	}
	return strings.Join(append(args, p.Args()...), " ")
}

// stopWait bounds how long stopping a daemon waits for its sessions to end.
const stopWait = 30 * time.Second

// Stop ends the daemon on socket and every session in it, and returns once
// they have ended, with the daemon's word on what it stopped and what a
// daemon started next would run with. It never starts a daemon: found is
// false when none is running.
func Stop(ctx context.Context, socket string) (out string, found bool, err error) {
	if err := userdir.Ensure(filepath.Dir(socket)); err != nil {
		return "", false, fmt.Errorf("riffle: socket directory: %w", err)
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		if socket == DefaultSocket() {
			return "", false, earlierRunning(ctx, socket)
		}
		return "", false, nil
	}
	out, err = stopOn(ctx, conn, socket, "")
	if err != nil {
		return "", true, err
	}
	// The record outlasts the daemon, so a restricted one never comes back
	// unrestricted; it also keeps a widened one wide.
	if p, err := LastPolicy(socket); err == nil && !same(p, Policy{}) {
		out += fmt.Sprintf("\na Riffle started with no policy named runs with %s again; name -allow-private=false for the default policy", p)
	}
	return out, true, nil
}

// earlierRunning explains a daemon of an earlier Riffle that runs beside
// this one's default socket, which this Riffle's stop does not reach.
func earlierRunning(ctx context.Context, socket string) error {
	for _, s := range earlierSockets(wire.Protocol) {
		if conn, err := (&net.Dialer{}).DialContext(ctx, "unix", s); err == nil {
			_ = conn.Close()
			stop := "stop its process"
			if rec, recorded, err := readRecord(s); err == nil && recorded {
				stop = fmt.Sprintf("stop its process (kill %d)", rec.PID)
			}
			return fmt.Errorf("riffle: no daemon of this Riffle is running on %s, but one from an earlier Riffle runs on %s; %s", socket, s, stop)
		}
	}
	return nil
}

// Replace stops the daemon on socket so that one with policy p can take its
// place. The stopping daemon records p, with this process as the one to run
// it, while it still owns the socket: a daemon that starts after it, this
// one or one a client starts in the gap, runs with p, and the record never
// names a policy no daemon runs with. found is false when no daemon is
// running.
func Replace(ctx context.Context, socket string, p Policy) (out string, found bool, err error) {
	next, err := json.Marshal(record{PID: os.Getpid(), Policy: p})
	if err != nil {
		return "", false, fmt.Errorf("riffle: record policy: %w", err)
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return "", false, nil
	}
	out, err = stopOn(ctx, conn, socket, string(next))
	return out, true, err
}

// stopOn asks the daemon on conn to stop, handing it next, the record of the
// daemon to take its place, if any; then it waits until the daemon has ended
// its sessions, when it closes the connection.
func stopOn(ctx context.Context, conn net.Conn, socket, next string) (string, error) {
	defer func() { _ = conn.Close() }()
	defer context.AfterFunc(ctx, func() { _ = conn.Close() })()
	_ = conn.SetDeadline(time.Now().Add(stopWait))
	br := bufio.NewReader(conn)
	sendErr := wire.WriteRequest(conn, wire.Request{Verb: wire.Stop, Session: wire.AllSessions, Body: next})
	var rep wire.Reply
	var err error
	if sendErr == nil {
		rep, err = wire.ReadReply(br)
	}
	switch {
	case ctx.Err() != nil:
		return "", ctx.Err()
	case sendErr != nil, dropped(err):
		// A daemon already ending drops new clients, before or after they
		// write; it is gone once it has let go of its socket.
		return gone(ctx, socket)
	case err != nil:
		return "", fmt.Errorf("riffle: stop: %w", err)
	case rep.Failed && strings.HasPrefix(rep.Body, "unknown verb"):
		return "", unstoppable(socket)
	case rep.Failed:
		return "", fmt.Errorf("%s", rep.Body)
	}
	if _, err := io.Copy(io.Discard, br); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("riffle: the daemon on %s agreed to stop and had not ended its sessions after %s: %w", socket, stopWait, err)
	}
	return rep.Body, nil
}

// gone waits until a daemon that was already ending has let go of socket:
// it takes no connections and no longer owns it.
func gone(ctx context.Context, socket string) (string, error) {
	for deadline := time.Now().Add(stopWait); time.Now().Before(deadline); {
		if conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket); err == nil {
			_ = conn.Close()
		} else if free, err := unowned(socket); err != nil {
			return "", fmt.Errorf("riffle: %w", err)
		} else if free {
			return fmt.Sprintf("the daemon on %s was already stopping, and has stopped", socket), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("riffle: the daemon on %s was stopping and had not ended after %s", socket, stopWait)
}

// unstoppable explains a daemon from an earlier Riffle, which has no stop.
func unstoppable(socket string) error {
	stop := "stop its process"
	if rec, recorded, err := readRecord(socket); err == nil && recorded {
		stop = fmt.Sprintf("stop its process (kill %d)", rec.PID)
	}
	if socket == DefaultSocket() {
		return fmt.Errorf("riffle: the daemon on %s is from an earlier Riffle that cannot be stopped this way; %s and retry", socket, stop)
	}
	return fmt.Errorf("riffle: the daemon on %s is from an earlier Riffle that cannot be stopped this way; %s and retry, or leave out -socket to use this Riffle's own daemon", socket, stop)
}

// Hold makes the client hold session for as long as its connection lasts,
// sent again on every new connection: the session is never closed for being
// idle, and ends when its last holder disconnects.
func (c *Client) Hold(session string) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.holding = session
	return c
}

// KeepState makes the session the client holds keep its cookies and storage
// between its browsers.
func (c *Client) KeepState() *Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keep = true
	return c
}

// Kept connects now, so a daemon that will not keep the state says so at
// once, and returns where the daemon keeps it.
func (c *Client) Kept(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.c == nil {
		if err := c.connect(ctx); err != nil {
			return "", err
		}
		if err := c.announce(); err != nil {
			return "", err
		}
	}
	return c.kept, nil
}

// announce tells a new connection which session this client holds.
func (c *Client) announce() error {
	if c.holding == "" {
		return nil
	}
	req := wire.Request{Verb: wire.Hold, Session: c.holding}
	if c.keep {
		req.Body = wire.KeepState
	}
	if err := wire.WriteRequest(c.c, req); err != nil {
		c.drop()
		return fmt.Errorf("riffle: send: %w", err)
	}
	rep, err := wire.ReadReply(c.br)
	if err != nil {
		c.drop()
		return fmt.Errorf("riffle: receive: %w", err)
	}
	if rep.Failed {
		c.drop()
		if strings.HasPrefix(rep.Body, "unknown verb") {
			return c.older()
		}
		return fmt.Errorf("%s", rep.Body)
	}
	if c.keep {
		path, ok := strings.CutPrefix(rep.Body, wire.HeldKeepingState)
		if !ok {
			c.drop()
			return c.keepsNoState()
		}
		c.kept = path
	}
	return nil
}

// older explains a daemon from an earlier Riffle that does not know a verb
// this client sends first: one on a socket named by hand, or one built before
// the first release on the default socket.
func (c *Client) older() error {
	stop := "stop it"
	if rec, recorded, err := readRecord(c.socket); err == nil && recorded {
		stop = fmt.Sprintf("stop it (kill %d)", rec.PID)
	}
	if c.socket == DefaultSocket() {
		return fmt.Errorf("riffle: the daemon on %s is from an earlier Riffle that does not speak protocol %d; %s and retry, and this client starts one that does", c.socket, wire.Protocol, stop)
	}
	return fmt.Errorf("riffle: the daemon on %s is from an earlier Riffle that does not speak protocol %d; %s and retry, or leave out -socket to use this Riffle's own daemon", c.socket, wire.Protocol, stop)
}

// keepsNoState explains a daemon too old to keep state, and how to replace it.
func (c *Client) keepsNoState() error {
	if rec, recorded, err := readRecord(c.socket); err == nil && recorded {
		return fmt.Errorf("riffle: the daemon on %s (pid %d) cannot keep a session's state; stop it (kill %d) and retry, and this client starts one that can", c.socket, rec.PID, rec.PID)
	}
	return fmt.Errorf("riffle: the daemon on %s cannot keep a session's state; stop it and retry, and this client starts one that can", c.socket)
}

// dropped reports a reply the daemon closed the connection before giving.
func dropped(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET)
}
