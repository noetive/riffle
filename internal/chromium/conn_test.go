package chromium

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/tidwall/gjson"
)

func TestCallReturnsTheResultOfItsOwnCommand(t *testing.T) {
	f := newFake(t)
	f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
		return fmt.Sprintf(`{"echo":%q}`, r.Method), "", true
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := fmt.Sprintf("X.m%d", i)
			res, err := f.conn.call(context.Background(), "", m, nil)
			if err != nil {
				t.Errorf("%s: %v", m, err)
				return
			}
			if got := gjson.GetBytes(res, "echo").String(); got != m {
				t.Errorf("%s got reply for %s", m, got)
			}
		}()
	}
	wg.Wait()
}

func TestCallRepliesMayArriveOutOfOrder(t *testing.T) {
	f := newFake(t)
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		if r.Method == "A.first" {
			return "", "", false
		}
		// answer the second command, then the held first one
		f.send(fmt.Sprintf(`{"id":%d,"result":{"who":"second"}}`, r.ID))
		f.send(fmt.Sprintf(`{"id":%d,"result":{"who":"first"}}`, f.find("A.first")[0].ID))
		return "", "", false
	}
	done := make(chan string, 1)
	go func() {
		res, _ := f.conn.call(context.Background(), "", "A.first", nil)
		done <- gjson.GetBytes(res, "who").String()
	}()
	eventually(t, "first command", func() bool { return len(f.find("A.first")) == 1 })
	res, err := f.conn.call(context.Background(), "", "A.second", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := gjson.GetBytes(res, "who").String(); got != "second" {
		t.Fatalf("second got %q", got)
	}
	if got := <-done; got != "first" {
		t.Fatalf("first got %q", got)
	}
}

func TestCallRequestShape(t *testing.T) {
	f := newFake(t)
	ctx := context.Background()
	if _, err := f.conn.call(ctx, "", "Browser.getVersion", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.call(ctx, "sess", "Page.navigate", map[string]any{"url": "u"}); err != nil {
		t.Fatal(err)
	}
	calls := f.recorded()
	if len(calls) != 2 {
		t.Fatalf("calls = %d", len(calls))
	}
	bare := gjson.Parse(calls[0].Raw)
	if bare.Get("sessionId").Exists() || bare.Get("params").Exists() {
		t.Fatalf("browser-level command carries session or params: %s", calls[0].Raw)
	}
	if calls[1].Session != "sess" || calls[1].Params.Get("url").String() != "u" {
		t.Fatalf("session command malformed: %s", calls[1].Raw)
	}
	if calls[1].ID <= calls[0].ID {
		t.Fatalf("ids must increase: %d then %d", calls[0].ID, calls[1].ID)
	}
	if calls[0].ID < 1 {
		t.Fatalf("first id = %d", calls[0].ID)
	}
}

func TestCallSurfacesProtocolErrors(t *testing.T) {
	f := newFake(t)
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "", "No such node", true }
	_, err := f.conn.call(context.Background(), "", "DOM.focus", nil)
	if err == nil || !strings.Contains(err.Error(), "No such node") || !strings.Contains(err.Error(), "DOM.focus") {
		t.Fatalf("err = %v", err)
	}
}

func TestCallHonoursContextAndForgetsTheRequest(t *testing.T) {
	f := newFake(t)
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "", "", false }
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := f.conn.call(ctx, "", "Runtime.evaluate", nil)
		errc <- err
	}()
	eventually(t, "command sent", func() bool { return len(f.recorded()) == 1 })
	cancel()
	err := <-errc
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	f.conn.mu.Lock()
	n := len(f.conn.pending)
	f.conn.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d requests still pending after the caller gave up", n)
	}
}

func TestLateReplyAfterGivingUpIsIgnored(t *testing.T) {
	f := newFake(t)
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		if r.Method == "A.slow" {
			return "", "", false
		}
		return `{"ok":true}`, "", true
	}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := f.conn.call(ctx, "", "A.slow", nil); errc <- err }()
	eventually(t, "sent", func() bool { return len(f.recorded()) == 1 })
	cancel()
	<-errc
	f.send(fmt.Sprintf(`{"id":%d,"result":{}}`, f.find("A.slow")[0].ID))
	res, err := f.conn.call(context.Background(), "", "A.next", nil)
	if err != nil || !gjson.GetBytes(res, "ok").Bool() {
		t.Fatalf("res=%s err=%v", res, err)
	}
}

func TestClosingTheConnectionReleasesWaiters(t *testing.T) {
	f := newFake(t)
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "", "", false }
	errc := make(chan error, 1)
	go func() { _, err := f.conn.call(context.Background(), "", "A.hang", nil); errc <- err }()
	eventually(t, "sent", func() bool { return len(f.recorded()) == 1 })
	_ = f.sw.Close()
	if err := <-errc; err == nil {
		t.Fatal("waiter was released without an error")
	}
	<-f.conn.closed
	if _, err := f.conn.call(context.Background(), "", "A.after", nil); err == nil {
		t.Fatal("call on a closed connection succeeded")
	}
}

func TestFailKeepsTheFirstCauseAndReleasesPending(t *testing.T) {
	c := &conn{pending: map[int64]chan reply{}, closed: make(chan struct{})}
	ch := make(chan reply, 1)
	c.pending[7] = ch
	first := errors.New("first")
	c.fail(first)
	c.fail(errors.New("second"))
	r := <-ch
	if !errors.Is(r.err, first) || !strings.Contains(r.err.Error(), "connection closed") {
		t.Fatalf("pending release = %v", r.err)
	}
	if !errors.Is(c.err, first) {
		t.Fatalf("err = %v", c.err)
	}
	if len(c.pending) != 0 {
		t.Fatal("pending not cleared")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("pipe broken") }

func TestCallReportsWriteFailure(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	c := newConn(pr, failingWriter{})
	_, err := c.call(context.Background(), "", "A.b", nil)
	if err == nil || !strings.Contains(err.Error(), "pipe broken") {
		t.Fatalf("err = %v", err)
	}
	c.mu.Lock()
	n := len(c.pending)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("pending = %d", n)
	}
}

func TestCallReportsUnmarshalableParams(t *testing.T) {
	f := newFake(t)
	_, err := f.conn.call(context.Background(), "", "A.b", map[string]any{"c": make(chan int)})
	if err == nil {
		t.Fatal("expected a marshal error")
	}
	if len(f.recorded()) != 0 {
		t.Fatal("a malformed command was written")
	}
}

func TestNotificationsFanOutToEverySubscriber(t *testing.T) {
	f := newFake(t)
	a, b := f.conn.subscribe(), f.conn.subscribe()
	f.emit("S9", "Page.loadEventFired", `{"timestamp":1}`)
	f.emit("", "Browser.downloadWillBegin", `{"url":"u"}`)
	for _, ch := range []<-chan event{a, b} {
		ev := <-ch
		if ev.Session != "S9" || ev.Method != "Page.loadEventFired" || ev.Params.Get("timestamp").Int() != 1 {
			t.Fatalf("event = %+v", ev)
		}
		ev = <-ch
		if ev.Session != "" || ev.Method != "Browser.downloadWillBegin" {
			t.Fatalf("event = %+v", ev)
		}
	}
}

func TestEventsDoNotDisturbPendingCommands(t *testing.T) {
	f := newFake(t)
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		f.emit("S", "Noise.event", "")
		return `{"v":1}`, "", true
	}
	res, err := f.conn.call(context.Background(), "", "A.b", nil)
	if err != nil || gjson.GetBytes(res, "v").Int() != 1 {
		t.Fatalf("res=%s err=%v", res, err)
	}
}
