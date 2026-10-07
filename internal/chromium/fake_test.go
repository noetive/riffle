package chromium

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// rec is one command the fake browser received.
type rec struct {
	ID      int64
	Session string
	Method  string
	Params  gjson.Result
	Raw     string
}

// fakeBrowser is the far end of a pipe pair speaking the DevTools framing.
type fakeBrowser struct {
	t     *testing.T
	conn  *conn
	sw    io.WriteCloser
	sr    io.Reader
	wm    sync.Mutex
	mu    sync.Mutex
	calls []rec

	// handle answers one command. ok=false leaves it unanswered.
	handle func(f *fakeBrowser, r rec) (result string, errMsg string, ok bool)

	afterReply []func() // guarded by mu: run once the current reply is sent
}

// thenAfterReply runs fn once the reply to the command being handled is sent,
// before the next command is read: an event the browser sends just after
// answering.
func (f *fakeBrowser) thenAfterReply(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.afterReply = append(f.afterReply, fn)
}

func newFake(t *testing.T) *fakeBrowser {
	t.Helper()
	shortenSettle(t)
	cr, sw := io.Pipe() // server writes, client reads
	sr, cw := io.Pipe() // client writes, server reads
	f := &fakeBrowser{t: t, sw: sw, sr: sr}
	f.conn = newConn(cr, cw)
	go f.serve()
	t.Cleanup(func() { _ = sw.Close(); _ = cw.Close() })
	return f
}

func (f *fakeBrowser) serve() {
	br := bufio.NewReader(f.sr)
	for {
		msg, err := br.ReadBytes(0)
		if err != nil {
			return
		}
		raw := string(bytes.TrimRight(msg, "\x00"))
		p := gjson.Parse(raw)
		r := rec{ID: p.Get("id").Int(), Session: p.Get("sessionId").String(), Method: p.Get("method").String(), Params: p.Get("params"), Raw: raw}
		f.mu.Lock()
		f.calls = append(f.calls, r)
		f.mu.Unlock()
		res, em, ok := "{}", "", true
		if f.handle != nil {
			res, em, ok = f.handle(f, r)
		}
		if !ok {
			continue
		}
		if em != "" {
			f.send(`{"id":` + itoa(r.ID) + `,"error":{"message":"` + em + `"}}`)
		} else {
			f.send(`{"id":` + itoa(r.ID) + `,"result":` + res + `}`)
		}
		f.mu.Lock()
		after := f.afterReply
		f.afterReply = nil
		f.mu.Unlock()
		for _, fn := range after {
			fn()
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func (f *fakeBrowser) send(s string) {
	f.wm.Lock()
	defer f.wm.Unlock()
	_, _ = f.sw.Write(append([]byte(s), 0))
}

func (f *fakeBrowser) emit(session, method, params string) {
	if params == "" {
		params = "{}"
	}
	f.send(`{"sessionId":"` + session + `","method":"` + method + `","params":` + params + `}`)
}

func (f *fakeBrowser) recorded() []rec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]rec(nil), f.calls...)
}

// methods lists the recorded method names in order.
func (f *fakeBrowser) methods() []string {
	var out []string
	for _, r := range f.recorded() {
		out = append(out, r.Method)
	}
	return out
}

// find returns recorded commands with the method.
func (f *fakeBrowser) find(method string) []rec {
	var out []rec
	for _, r := range f.recorded() {
		if r.Method == method {
			out = append(out, r)
		}
	}
	return out
}

// eventually polls cond until it holds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

func newTestPage(t *testing.T) (*Page, *fakeBrowser) {
	t.Helper()
	f := newFake(t)
	return newPage(testCtx(t), f.conn, "S1", "T1"), f
}

// longStill is a page that added visible content long ago, in milliseconds.
const longStill = "60000"

// shortenSettle makes a page's real-time waits short enough for tests, and
// restores them when the test ends. Tests of one wait set that variable after.
func shortenSettle(t *testing.T) {
	t.Helper()
	was := [7]time.Duration{actionSettle, loadSettle, quietWindow, busyGrace, settlePoll, challengeGrace, settleCap}
	actionSettle, loadSettle, quietWindow = 40*time.Millisecond, 80*time.Millisecond, 30*time.Millisecond
	busyGrace, settlePoll, challengeGrace, settleCap = 60*time.Millisecond, 5*time.Millisecond, 300*time.Millisecond, 500*time.Millisecond
	t.Cleanup(func() {
		actionSettle, loadSettle, quietWindow, busyGrace, settlePoll, challengeGrace, settleCap =
			was[0], was[1], was[2], was[3], was[4], was[5], was[6]
	})
	wasStale, wasPump := staleRequest, framePumpTimeout
	t.Cleanup(func() { staleRequest, framePumpTimeout = wasStale, wasPump })
}

// evaluation answers a Runtime.evaluate the way a healthy page does: the
// transient drain reports toasts (a JSON list), the quiet probe reports still
// (milliseconds since content last appeared), animations finish none, and
// anything else is 0.
func evaluation(r rec, toasts, still string) string {
	switch r.Params.Get("expression").String() {
	case transientDrain:
		return `{"result":{"type":"string","value":` + fmt.Sprintf("%q", toasts) + `}}`
	case transientStill:
		return `{"result":{"type":"number","value":` + still + `}}`
	}
	return `{"result":{"type":"number","value":0}}`
}
