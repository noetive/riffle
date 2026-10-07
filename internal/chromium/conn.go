package chromium

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	json "github.com/goccy/go-json"
	"github.com/tidwall/gjson"
)

// event is one protocol notification.
type event struct {
	Session string
	Method  string
	Params  gjson.Result
}

// conn speaks the DevTools protocol over the pipe pair: NUL-terminated JSON
// messages, requests correlated by id, notifications fanned out to subscribers.
type conn struct {
	pending map[int64]chan reply
	closed  chan struct{}
	err     error
	w       io.Writer
	subs    []chan event
	wm      sync.Mutex // serialises writes to w

	mu     sync.Mutex // guards nextID, pending, subs, err
	nextID int64
}

type reply struct {
	err    error
	result []byte
}

func newConn(r io.Reader, w io.Writer) *conn {
	c := &conn{w: w, pending: map[int64]chan reply{}, closed: make(chan struct{})}
	go c.read(r)
	return c
}

func (c *conn) read(r io.Reader) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		msg, err := br.ReadBytes(0)
		if err != nil {
			c.fail(err)
			return
		}
		msg = bytes.TrimRight(msg, "\x00")
		parsed := gjson.ParseBytes(msg)
		if id := parsed.Get("id"); id.Exists() {
			c.mu.Lock()
			ch := c.pending[id.Int()]
			delete(c.pending, id.Int())
			c.mu.Unlock()
			if ch == nil {
				continue
			}
			if e := parsed.Get("error"); e.Exists() {
				ch <- reply{err: fmt.Errorf("cdp: %s", e.Get("message").String())}
			} else {
				ch <- reply{result: []byte(parsed.Get("result").Raw)}
			}
			continue
		}
		ev := event{Session: parsed.Get("sessionId").String(), Method: parsed.Get("method").String(), Params: parsed.Get("params")}
		c.mu.Lock()
		subs := append([]chan event(nil), c.subs...)
		c.mu.Unlock()
		for _, s := range subs {
			select {
			case s <- ev:
			case <-c.closed:
				return
			}
		}
	}
}

// fail terminates the connection and releases every waiter.
func (c *conn) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.closed:
		return
	default:
	}
	c.err = err
	close(c.closed)
	for id, ch := range c.pending {
		ch <- reply{err: fmt.Errorf("cdp: connection closed: %w", err)}
		delete(c.pending, id)
	}
}

// subscribe returns an unbounded-in-practice channel of notifications.
// The buffer is large; a subscriber that stalls blocks the reader, which
// is a bug in the subscriber, not something to paper over.
func (c *conn) subscribe() <-chan event {
	ch := make(chan event, 4096)
	c.mu.Lock()
	c.subs = append(c.subs, ch)
	c.mu.Unlock()
	return ch
}

// unsubscribe stops notifications to ch. The reader may still deliver one it
// was already sending, which the buffer holds.
func (c *conn) unsubscribe(ch <-chan event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, s := range c.subs {
		if s == ch {
			c.subs = append(c.subs[:i], c.subs[i+1:]...)
			return
		}
	}
}

// call sends a command and waits for its result. sessionID may be empty for
// browser-level commands.
func (c *conn) call(ctx context.Context, sessionID, method string, params any) ([]byte, error) {
	// A renderer stuck in a script must not hold a session forever.
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan reply, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	req := map[string]any{"id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	if sessionID != "" {
		req["sessionId"] = sessionID
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("cdp: marshal %s: %w", method, err)
	}
	c.wm.Lock()
	_, err = c.w.Write(append(b, 0))
	c.wm.Unlock()
	if err != nil {
		return nil, fmt.Errorf("cdp: write %s: %w", method, err)
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("%s: %w", method, r.err)
		}
		return r.result, nil
	case <-c.closed:
		return nil, fmt.Errorf("%s: browser exited", method)
	case <-ctx.Done():
		if parent.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) && sessionID != "" {
			return nil, &unanswered{browserAlive: c.alive(parent)}
		}
		return nil, fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

// unanswered is a command the page did not answer within callTimeout. It is
// a deadline: callers that tell a slow site from a stuck page still can.
type unanswered struct{ browserAlive bool }

func (u *unanswered) Error() string {
	if !u.browserAlive {
		return fmt.Sprintf("the browser did not answer within %s; this machine may be overloaded", callTimeout)
	}
	return fmt.Sprintf("the page did not answer within %s; its scripts may be keeping the browser busy. Take a fresh view, or try again", callTimeout)
}

func (u *unanswered) Unwrap() error { return context.DeadlineExceeded }

// alive reports whether the browser itself answers, apart from any page.
func (c *conn) alive(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	_, err := c.call(ctx, "", "Browser.getVersion", nil)
	return err == nil
}

// callTimeout bounds one protocol command, in real time.
var callTimeout = 30 * time.Second
