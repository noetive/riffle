package chromium

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/engine"
)

// stillAnswer is how the fake page answers "how long since you last added
// visible content", in milliseconds.
type stillAnswer struct{ ms atomic.Value }

func (s *stillAnswer) set(ms string) { s.ms.Store(ms) }

// livePage is a page whose content last changed as stillAnswer says, that
// loads whatever it is told to, and whose clock a test can push forward.
func livePage(t *testing.T) (*Page, *fakeBrowser, *stillAnswer, *atomic.Int64) {
	t.Helper()
	p, f := newTestPage(t)
	still := &stillAnswer{}
	still.set(longStill)
	var skew atomic.Int64
	p.clock = func() time.Time { return time.Now().Add(time.Duration(skew.Load())) }
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "Page.navigate", "Page.navigateToHistoryEntry":
			defer f.emit("S1", "Page.loadEventFired", "")
		case "Runtime.evaluate":
			return evaluation(r, "[]", still.ms.Load().(string)), "", true
		}
		return "{}", "", true
	}
	return p, f, still, &skew
}

func timed(fn func() error) (time.Duration, error) {
	start := time.Now()
	err := fn()
	return time.Since(start), err
}

func slowNotes(p *Page) []string {
	var out []string
	for _, e := range p.Drain() {
		if e.Kind == engine.SlowLoad {
			out = append(out, e.Text)
		}
	}
	return out
}

func TestASettleRightAfterAnActionWaitsAtLeastTheActionsMinimum(t *testing.T) {
	p, _, _, _ := livePage(t)
	actionSettle = 300 * time.Millisecond
	took, err := timed(func() error {
		if err := p.Click(context.Background(), 1, 1); err != nil {
			return err
		}
		return p.Settle(context.Background())
	})
	if err != nil {
		t.Fatal(err)
	}
	if took < actionSettle {
		t.Errorf("settled after %s, before the page had %s to answer the click", took, actionSettle)
	}
}

func TestALoadGivesThePageItsOwnLongerMinimum(t *testing.T) {
	p, _, _, _ := livePage(t)
	loadSettle = 300 * time.Millisecond
	took, err := timed(func() error { return p.Load(context.Background(), "http://x/") })
	if err != nil {
		t.Fatal(err)
	}
	if took < loadSettle {
		t.Errorf("a load returned after %s, before the page had %s to start what it starts on load", took, loadSettle)
	}
}

func TestASettleLongAfterTheLastActionReturnsAsSoonAsThePageIsQuiet(t *testing.T) {
	p, _, _, skew := livePage(t)
	fresh, _, _, _ := livePage(t)
	// Set after both pages exist: making a page resets the waits to short ones.
	actionSettle = 30 * time.Second
	if err := p.Click(context.Background(), 1, 1); err != nil {
		t.Fatal(err)
	}
	skew.Store(int64(time.Hour)) // the agent thought about it for a long while
	took, err := timed(func() error { return p.Settle(context.Background()) })
	if err != nil {
		t.Fatal(err)
	}
	if took >= actionSettle/2 {
		t.Errorf("a quiet page that was acted on long ago took %s to read", took)
	}
	took, err = timed(func() error { return fresh.Settle(context.Background()) })
	if err != nil {
		t.Fatal(err)
	}
	if took >= actionSettle/2 {
		t.Errorf("a quiet page nobody acted on took %s to read", took)
	}
}

func TestAPageThatKeepsAddingContentIsReadAfterTheMinimumPlusGraceWithoutASlowNote(t *testing.T) {
	p, _, still, _ := livePage(t)
	still.set("0") // content appears all the time, as in a ticker
	actionSettle, busyGrace, settleCap = 100*time.Millisecond, 150*time.Millisecond, 3*time.Second
	took, err := timed(func() error {
		if err := p.Click(context.Background(), 1, 1); err != nil {
			return err
		}
		return p.Settle(context.Background())
	})
	if err != nil {
		t.Fatal(err)
	}
	if took < actionSettle+busyGrace {
		t.Errorf("a changing page was read after %s, before its minimum and grace (%s)", took, actionSettle+busyGrace)
	}
	if took >= settleCap {
		t.Errorf("a changing page was waited for until the cap: %s", took)
	}
	if notes := slowNotes(p); len(notes) != 0 {
		t.Errorf("a page that merely keeps changing is not a slow load: %v", notes)
	}
}

func TestAPageWithARequestPendingIsReadAtTheCapAndTheAgentIsTold(t *testing.T) {
	p, _, _, _ := livePage(t)
	settleCap = 300 * time.Millisecond
	p.feed("Network.requestWillBeSent", `{"requestId":"api","type":"XHR","request":{"method":"GET","url":"http://x/api"}}`)
	took, err := timed(func() error { return p.Settle(context.Background()) })
	if err != nil {
		t.Fatalf("a page that will not settle is still read: %v", err)
	}
	if took < settleCap {
		t.Errorf("read after %s with a request still pending, before the cap %s", took, settleCap)
	}
	notes := slowNotes(p)
	if len(notes) != 1 || !strings.Contains(notes[0], "still waiting on requests") || !strings.Contains(notes[0], "as it is") {
		t.Errorf("notes = %v, want one saying the page was still waiting on requests and is read as it is", notes)
	}
}

func TestARequestThatAnswersLetsTheSettleEndBeforeTheCap(t *testing.T) {
	p, _, _, _ := livePage(t)
	settleCap = 3 * time.Second
	p.feed("Network.requestWillBeSent", `{"requestId":"api","type":"XHR","request":{"method":"GET","url":"http://x/api"}}`)
	took, err := timed(func() error {
		// Armed inside the timing, so the answer cannot come before the clock starts.
		time.AfterFunc(100*time.Millisecond, func() {
			p.feed("Network.loadingFinished", `{"requestId":"api"}`)
		})
		return p.Settle(context.Background())
	})
	if err != nil {
		t.Fatal(err)
	}
	if took < 100*time.Millisecond || took >= settleCap/2 {
		t.Errorf("settle took %s; it waits for the request and not for the cap", took)
	}
	if notes := slowNotes(p); len(notes) != 0 {
		t.Errorf("notes = %v", notes)
	}
}

func TestARequestThatGoesUnansweredWhileTheSettleWaitsIsToldOnce(t *testing.T) {
	p, _, _, _ := livePage(t)
	settleCap, staleRequest = 3*time.Second, 150*time.Millisecond
	p.feed("Network.requestWillBeSent", `{"requestId":"poll","type":"Fetch","request":{"method":"GET","url":"http://x/poll"}}`)
	took, err := timed(func() error { return p.Settle(context.Background()) })
	if err != nil {
		t.Fatal(err)
	}
	// That the request was waited on is the note below; the request aged
	// before the clock started, so only the upper bound is exact.
	if took >= settleCap/2 {
		t.Errorf("settle took %s; it waits for the request until it is taken for hung, not for the cap", took)
	}
	if notes := slowNotes(p); len(notes) != 1 || !strings.Contains(notes[0], "had not answered") {
		t.Errorf("notes = %v, want one saying a request had not answered", notes)
	}
	if err := p.Settle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if notes := slowNotes(p); len(notes) != 0 {
		t.Errorf("a request already taken for hung held no later settle, so it is not told again: %v", notes)
	}
}

func TestHungRequestsDoNotHoldASettle(t *testing.T) {
	p, _, _, skew := livePage(t)
	settleCap = 3 * time.Second
	p.feed("Network.requestWillBeSent", `{"requestId":"beacon","type":"Fetch","request":{"method":"POST","url":"http://x/beacon"}}`)
	skew.Store(int64(2 * staleRequest))
	took, err := timed(func() error { return p.Settle(context.Background()) })
	if err != nil {
		t.Fatal(err)
	}
	if took >= settleCap/2 {
		t.Errorf("settle took %s on a request that has hung", took)
	}
	if notes := slowNotes(p); len(notes) != 0 {
		t.Errorf("notes = %v", notes)
	}
}

func TestASettleReportsToastsThatCameAndWent(t *testing.T) {
	p, f := newTestPage(t)
	scripted(f, `["Saved!","Saved!","Other"]`)
	if err := p.Settle(context.Background()); err != nil {
		t.Fatal(err)
	}
	ev := p.Drain()
	if len(ev) != 2 || ev[0].Text != "Saved! (x2)" || ev[1].Text != "Other" || ev[0].Kind != engine.Toast {
		t.Fatalf("events = %v", ev)
	}
}

// refusedDocument answers a navigation with a document the site refused, and
// returns when the page was asked to load.
func refusedDocument(f *fakeBrowser, status string) {
	prev := f.handle
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		if r.Method == "Page.navigate" {
			f.emit("S1", "Network.requestWillBeSent", `{"requestId":"doc","type":"Document","request":{"method":"GET","url":"http://x/"}}`)
			f.emit("S1", "Network.responseReceived", `{"requestId":"doc","response":{"status":`+status+`,"mimeType":"text/html"}}`)
			f.emit("S1", "Network.loadingFinished", `{"requestId":"doc"}`)
		}
		return prev(f, r)
	}
}

func TestARefusedDocumentWaitsForANewOneUpToTheGrace(t *testing.T) {
	for _, status := range []string{"403", "429", "503"} {
		t.Run(status, func(t *testing.T) {
			p, f, _, _ := livePage(t)
			challengeGrace = 400 * time.Millisecond
			refusedDocument(f, status)
			took, err := timed(func() error { return p.Load(context.Background(), "http://x/") })
			if err != nil {
				t.Fatal(err)
			}
			if took < challengeGrace {
				t.Errorf("a %s document was read after %s, before a browser check had %s to replace it", status, took, challengeGrace)
			}
		})
	}
}

func TestARefusedDocumentStopsWaitingAsSoonAsANewOneCommits(t *testing.T) {
	p, f, _, _ := livePage(t)
	challengeGrace = 5 * time.Second
	refusedDocument(f, "403")
	time.AfterFunc(100*time.Millisecond, func() {
		p.feed("Page.frameNavigated", `{"frame":{"id":"F","url":"http://x/ok"}}`)
	})
	took, err := timed(func() error { return p.Load(context.Background(), "http://x/") })
	if err != nil {
		t.Fatal(err)
	}
	if took >= challengeGrace/2 {
		t.Errorf("load took %s; the new document committed after 100ms", took)
	}
}

func TestADocumentThatAnsweredIsNotWaitedOnForAReplacement(t *testing.T) {
	for _, status := range []string{"200", "404"} {
		t.Run(status, func(t *testing.T) {
			p, f, _, _ := livePage(t)
			challengeGrace = 5 * time.Second
			refusedDocument(f, status)
			took, err := timed(func() error { return p.Load(context.Background(), "http://x/") })
			if err != nil {
				t.Fatal(err)
			}
			if took >= challengeGrace/2 {
				t.Errorf("a %s document made the load wait %s", status, took)
			}
		})
	}
}

func TestAdvanceWaitsAtLeastTheTimeAskedThenSettles(t *testing.T) {
	p, f, _, _ := livePage(t)
	d := 250 * time.Millisecond
	took, err := timed(func() error { return p.Advance(context.Background(), d) })
	if err != nil {
		t.Fatal(err)
	}
	if took < d {
		t.Errorf("advanced %s, asked for %s", took, d)
	}
	if len(f.find("Runtime.evaluate")) == 0 {
		t.Error("the page was not settled after the wait")
	}
}

func TestAdvanceStopsWhenTheCallerGivesUp(t *testing.T) {
	p, _, _, _ := livePage(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	took, err := timed(func() error { return p.Advance(ctx, time.Minute) })
	if !errors.Is(err, context.DeadlineExceeded) || took > 5*time.Second {
		t.Fatalf("err = %v after %s, want the caller's deadline within seconds", err, took)
	}
}

func TestAFrameThatNeverRendersDoesNotHoldUpTheAction(t *testing.T) {
	p, f := newTestPage(t)
	framePumpTimeout = 50 * time.Millisecond
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "Page.captureScreenshot":
			return "", "", false // the browser never draws
		case "Runtime.evaluate":
			if r.Params.Get("expression").String() == finishAnimations {
				return `{"result":{"type":"number","value":1}}`, "", true
			}
			return evaluation(r, "[]", longStill), "", true
		}
		return "{}", "", true
	}
	took, err := timed(func() error { return p.Settle(context.Background()) })
	if err != nil {
		t.Fatalf("a frame that does not come is not a failed action: %v", err)
	}
	if took > 5*time.Second {
		t.Errorf("settle waited %s on a frame", took)
	}
	if len(f.find("Page.captureScreenshot")) == 0 {
		t.Error("no frame was asked for, so the test did not exercise the wait")
	}
}

// A navigation that failed is stopped, or it keeps the tab busy and can land
// after the agent was told it failed; one that succeeded is left alone.
func TestAFailedNavigationIsStoppedAndASuccessfulOneIsNot(t *testing.T) {
	was := callTimeout
	callTimeout = 100 * time.Millisecond
	t.Cleanup(func() { callTimeout = was })
	history := `{"currentIndex":1,"entries":[{"id":1},{"id":2}]}`
	for _, c := range []struct {
		name   string
		answer func(f *fakeBrowser, r rec, cancel func()) (string, string, bool)
		run    func(ctx context.Context, p *Page) error
		stops  bool
	}{
		{"loaded", func(f *fakeBrowser, r rec, _ func()) (string, string, bool) {
			return cooperative(f, r)
		}, func(ctx context.Context, p *Page) error { return p.Load(ctx, "http://x/") }, false},
		{"silent site", func(f *fakeBrowser, r rec, _ func()) (string, string, bool) {
			if r.Method == "Page.navigate" {
				return "", "", false
			}
			return cooperative(f, r)
		}, func(ctx context.Context, p *Page) error { return p.Load(ctx, "http://x/") }, true},
		{"refused address", func(f *fakeBrowser, r rec, _ func()) (string, string, bool) {
			if r.Method == "Page.navigate" {
				return `{"errorText":"net::ERR_ABORTED"}`, "", true
			}
			return cooperative(f, r)
		}, func(ctx context.Context, p *Page) error { return p.Load(ctx, "http://x/") }, true},
		{"history entry refused", func(f *fakeBrowser, r rec, _ func()) (string, string, bool) {
			switch r.Method {
			case "Page.getNavigationHistory":
				return history, "", true
			case "Page.navigateToHistoryEntry":
				return "", "no such entry", true
			}
			return cooperative(f, r)
		}, func(ctx context.Context, p *Page) error { _, err := p.Back(ctx); return err }, true},
		{"caller gave up", func(f *fakeBrowser, r rec, cancel func()) (string, string, bool) {
			if r.Method == "Page.navigate" {
				cancel()
				return "", "", false
			}
			return cooperative(f, r)
		}, func(ctx context.Context, p *Page) error { return p.Load(ctx, "http://x/") }, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, f := newTestPage(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.handle = func(f *fakeBrowser, r rec) (string, string, bool) { return c.answer(f, r, cancel) }
			err := c.run(ctx, p)
			if (err != nil) == (c.name == "loaded") {
				t.Fatalf("err = %v", err)
			}
			if stopped := len(f.find("Page.stopLoading")) > 0; stopped != c.stops {
				t.Errorf("navigation stopped = %v, want %v (err = %v)", stopped, c.stops, err)
			}
		})
	}
}

// A load starts from nothing: the previous page's requests are gone, the
// navigation goes to the address asked for, and a toast that came and went
// while the document loaded is not news while one after it is.
func TestALoadForgetsThePreviousPageAndReportsOnlyWhatFollowsTheDocument(t *testing.T) {
	p, f := newTestPage(t)
	var drains atomic.Int64
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "Page.navigate":
			defer f.emit("S1", "Page.loadEventFired", "")
		case "Runtime.evaluate":
			if r.Params.Get("expression").String() == transientDrain {
				if drains.Add(1) == 1 {
					return evaluation(r, `["Loading..."]`, longStill), "", true
				}
				return evaluation(r, `["Hello toast"]`, longStill), "", true
			}
			return evaluation(r, "[]", longStill), "", true
		}
		return "{}", "", true
	}
	p.feed("Network.requestWillBeSent", `{"requestId":"old","type":"Document","request":{"method":"GET","url":"http://old/"}}`)
	if len(p.Requests()) == 0 {
		t.Fatal("the previous page's request was not recorded")
	}
	if err := p.Load(context.Background(), "http://x/"); err != nil {
		t.Fatal(err)
	}
	nav := f.find("Page.navigate")
	if len(nav) != 1 || nav[0].Params.Get("url").String() != "http://x/" {
		t.Fatalf("navigate = %+v", nav)
	}
	if n := len(p.Requests()); n != 0 {
		t.Errorf("%d requests of the previous page survived the load", n)
	}
	ev := p.Drain()
	if len(ev) != 1 || ev[0].Kind != engine.Toast || ev[0].Text != "Hello toast" {
		t.Fatalf("events = %v, want only the toast that followed the document", ev)
	}
}
