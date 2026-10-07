package daemon

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/noetive/riffle/internal/userdir"
	"github.com/noetive/riffle/internal/wire"
)

// DefaultSocket is where the daemon listens unless told otherwise: a private
// per-user directory, so no other user can stand in for the daemon.
func DefaultSocket() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "riffle", "riffle.sock")
	}
	return filepath.Join(os.TempDir(), "riffle-"+strconv.Itoa(os.Getuid()), "riffle.sock")
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
	rec, recorded, err := readRecord(c.socket)
	if err != nil {
		return fmt.Errorf("riffle: %w", err)
	}
	if conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.socket); err == nil {
		if c.want != nil && (!recorded || !same(rec.Policy, *c.want)) {
			_ = conn.Close()
			return c.mismatch(rec, recorded)
		}
		c.c, c.br = conn, bufio.NewReader(conn)
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err // a caller that gave up must not leave a daemon behind
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("riffle: locate own binary: %w", err)
	}
	// A daemon that has to be started again gets the policy it last ran
	// with, unless this client insists on its own.
	policy := rec.Policy
	if c.want != nil {
		policy = *c.want
	}
	// The daemon must outlive this client, so it is not tied to ctx.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), self, append([]string{"serve", "-socket", c.socket}, policy.Args()...)...)
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("riffle: start daemon: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.socket); err == nil {
			c.c, c.br = conn, bufio.NewReader(conn)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return fmt.Errorf("riffle: daemon did not start on %s", c.socket)
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
	if !recorded {
		return fmt.Errorf("riffle: the daemon on %s runs with an unknown policy and this client was started with %s; stop that daemon and retry, and this client starts one with its own policy", c.socket, c.want)
	}
	return fmt.Errorf("riffle: the daemon on %s (pid %d) runs with %s, and this client was started with %s; stop that daemon (kill %d) and retry, and this client starts one with its own policy", c.socket, rec.PID, rec.Policy, c.want, rec.PID)
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

// keepsNoState explains a daemon too old to keep state, and how to replace it.
func (c *Client) keepsNoState() error {
	if rec, recorded, err := readRecord(c.socket); err == nil && recorded {
		return fmt.Errorf("riffle: the daemon on %s (pid %d) cannot keep a session's state; stop it (kill %d) and retry, and this client starts one that can", c.socket, rec.PID, rec.PID)
	}
	return fmt.Errorf("riffle: the daemon on %s cannot keep a session's state; stop it and retry, and this client starts one that can", c.socket)
}
