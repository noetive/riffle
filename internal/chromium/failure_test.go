package chromium

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// operation is one thing a caller can ask of a page.
type operation struct {
	name string
	run  func(ctx context.Context, p *Page) error
}

func operations() []operation {
	return []operation{
		{"Load", func(ctx context.Context, p *Page) error { return p.Load(ctx, "http://x/") }},
		{"Settle", func(ctx context.Context, p *Page) error { return p.Settle(ctx) }},
		{"Advance", func(ctx context.Context, p *Page) error { return p.Advance(ctx, 10*time.Millisecond) }},
		{"Back", func(ctx context.Context, p *Page) error { _, err := p.Back(ctx); return err }},
		{"Snapshot", func(ctx context.Context, p *Page) error { _, err := p.Snapshot(ctx); return err }},
		{"Move", func(ctx context.Context, p *Page) error { return p.Move(ctx, 1, 1) }},
		{"Click", func(ctx context.Context, p *Page) error { return p.Click(ctx, 1, 1) }},
		{"Wheel", func(ctx context.Context, p *Page) error { return p.Wheel(ctx, 1, 1, 1) }},
		{"Key", func(ctx context.Context, p *Page) error { return p.Key(ctx, "Enter") }},
		{"Focus", func(ctx context.Context, p *Page) error { return p.Focus(ctx, 1) }},
		{"InsertText", func(ctx context.Context, p *Page) error { return p.InsertText(ctx, "x") }},
		{"SelectOption", func(ctx context.Context, p *Page) error { return p.SelectOption(ctx, 1, []string{"a"}) }},
		{"SetFiles", func(ctx context.Context, p *Page) error { return p.SetFiles(ctx, 1, []string{"/a"}) }},
		{"ScrollIntoView", func(ctx context.Context, p *Page) error { return p.ScrollIntoView(ctx, 1) }},
		{"HitTest", func(ctx context.Context, p *Page) error { _, err := p.HitTest(ctx, 1, 1); return err }},
		{"Eval", func(ctx context.Context, p *Page) error { _, err := p.Eval(ctx, "1"); return err }},
		{"init", func(ctx context.Context, p *Page) error { return p.init(ctx, 10, 10) }},
		{"installTransients", func(ctx context.Context, p *Page) error { return p.installTransients(ctx) }},
		{"filterRequests", func(ctx context.Context, p *Page) error {
			return p.filterRequests(ctx, func(string, string) error { return nil })
		}},
	}
}

// cooperative answers every command like a healthy Chrome would.
func cooperative(f *fakeBrowser, r rec) (string, string, bool) {
	switch r.Method {
	case "Page.getLayoutMetrics":
		return `{"cssVisualViewport":{"pageX":0,"pageY":0,"clientWidth":1280,"clientHeight":800}}`, "", true
	case "Page.navigate", "Page.navigateToHistoryEntry":
		defer f.emit("S1", "Page.loadEventFired", "")
	case "Runtime.evaluate":
		return evaluation(r, "[]", longStill), "", true
	case "Page.getNavigationHistory":
		return `{"currentIndex":1,"entries":[{"id":10},{"id":11}]}`, "", true
	case "DOM.resolveNode":
		return `{"object":{"objectId":"o"}}`, "", true
	case "Runtime.callFunctionOn":
		return `{"result":{"value":true}}`, "", true
	case "DOMSnapshot.captureSnapshot":
		return `{"documents":[{"nodes":{"parentIndex":[]}}],"strings":[]}`, "", true
	case "DOM.getNodeForLocation":
		return `{"backendNodeId":3}`, "", true
	}
	return "{}", "", true
}

func countCommands(t *testing.T, op operation) int {
	t.Helper()
	p, f := newTestPage(t)
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) { return cooperative(f, r) }
	if err := op.run(context.Background(), p); err != nil {
		t.Fatalf("%s fails against a healthy browser: %v", op.name, err)
	}
	return len(f.recorded())
}

func TestEveryCommandFailureSurfacesToTheCaller(t *testing.T) {
	for _, op := range operations() {
		n := countCommands(t, op)
		for k := 1; k <= n; k++ {
			t.Run(fmt.Sprintf("%s/command%d", op.name, k), func(t *testing.T) {
				p, f := newTestPage(t)
				var seen atomic.Int64
				var probe atomic.Bool
				f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
					if int(seen.Add(1)) == k {
						probe.Store(isStillnessProbe(r))
						return "", "Internal failure", true
					}
					return cooperative(f, r)
				}
				if err := op.run(context.Background(), p); err == nil && !probe.Load() {
					t.Fatalf("%s swallowed the failure of command %d", op.name, k)
				}
			})
		}
	}
}

func TestEveryCommandStopsWhenTheCallerGivesUp(t *testing.T) {
	for _, op := range operations() {
		n := countCommands(t, op)
		for k := 1; k <= n; k++ {
			t.Run(fmt.Sprintf("%s/command%d", op.name, k), func(t *testing.T) {
				p, f := newTestPage(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var seen atomic.Int64
				var probe atomic.Bool
				f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
					if int(seen.Add(1)) == k {
						probe.Store(isStillnessProbe(r))
						cancel()
						return "", "", false // never answered
					}
					return cooperative(f, r)
				}
				done := make(chan error, 1)
				go func() { done <- op.run(ctx, p) }()
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) && !probe.Load() {
						t.Fatalf("err = %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("%s kept waiting on command %d after its context ended", op.name, k)
				}
			})
		}
	}
}

func TestNewPageStopsWhenTheCallerGivesUp(t *testing.T) {
	for k := 1; k <= 12; k++ {
		t.Run(fmt.Sprintf("command%d", k), func(t *testing.T) {
			f := newFake(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var seen atomic.Int64
			f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
				if int(seen.Add(1)) == k {
					cancel()
					return "", "", false
				}
				if r.Method == "Target.createTarget" {
					return `{"targetId":"T"}`, "", true
				}
				if r.Method == "Target.attachToTarget" {
					return `{"sessionId":"S"}`, "", true
				}
				return cooperative(f, r)
			}
			b := &Browser{conn: f.conn}
			done := make(chan error, 1)
			go func() { _, err := b.NewPage(ctx, 10, 10); done <- err }()
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Fatalf("err = %v", err)
				}
				if err == nil && int(seen.Load()) < k {
					return // fewer commands than k: nothing to cancel
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("NewPage kept waiting on command %d", k)
			}
		})
	}
}

// staysQuiet reports whether no command with the method arrives within a grace period.
func staysQuiet(f *fakeBrowser, method string) bool {
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(f.find(method)) > 0 {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return true
}

func TestBackFromTheSecondEntryGoesToTheFirst(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) { return cooperative(f, r) }
	ok, err := p.Back(context.Background())
	if !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if f.find("Page.navigateToHistoryEntry")[0].Params.Get("entryId").Int() != 10 {
		t.Fatal("went to the wrong entry")
	}
}

func TestBackReportsFailureAsNotMoved(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "", "x", true }
	if ok, err := p.Back(context.Background()); ok || err == nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestDrainReportsASingleOverflow(t *testing.T) {
	p, _ := newTestPage(t)
	for i := 0; i <= maxEvents; i++ {
		p.note("k", fmt.Sprint("e", i))
	}
	ev := p.Drain()
	if got := ev[len(ev)-1].Text; got != "1 more events not shown" {
		t.Fatalf("last = %q", got)
	}
}

func TestNamedKeysUseWindowsVirtualKeyCodes(t *testing.T) {
	p, f := newTestPage(t)
	want := map[string]int64{
		"Enter": 13, "Tab": 9, "Escape": 27, "Backspace": 8, "Delete": 46, "ArrowUp": 38, "ArrowDown": 40,
		"ArrowLeft": 37, "ArrowRight": 39, "Home": 36, "End": 35, "PageUp": 33, "PageDown": 34, "Space": 32,
	}
	for name, vk := range want {
		before := len(f.find("Input.dispatchKeyEvent"))
		if err := p.Key(context.Background(), name); err != nil {
			t.Fatal(err)
		}
		r := f.find("Input.dispatchKeyEvent")[before]
		if r.Params.Get("windowsVirtualKeyCode").Int() != vk {
			t.Errorf("%s vk = %d want %d", name, r.Params.Get("windowsVirtualKeyCode").Int(), vk)
		}
	}
}

func TestBackWaitsForItsOwnLoad(t *testing.T) {
	p, f := newTestPage(t)
	signal(p.loaded) // left over from an earlier navigation
	f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "Page.getNavigationHistory":
			return `{"currentIndex":1,"entries":[{"id":10},{"id":11}]}`, "", true
		case "Runtime.evaluate":
			return evaluation(r, "[]", longStill), "", true
		}
		return "{}", "", true
	}
	done := make(chan error, 1)
	go func() { _, err := p.Back(context.Background()); done <- err }()
	eventually(t, "navigation", func() bool { return len(f.find("Page.navigateToHistoryEntry")) == 1 })
	if !staysQuiet(f, "Runtime.evaluate") {
		t.Fatal("back returned on a stale load signal")
	}
	f.emit("S1", "Page.loadEventFired", "")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// isStillnessProbe is the one command a settle repeats on failure instead of
// reporting: a page that cannot be asked, as while a document is replaced,
// has just changed, and the next look asks again.
func isStillnessProbe(r rec) bool {
	return r.Method == "Runtime.evaluate" && r.Params.Get("expression").String() == transientStill
}
