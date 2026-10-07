package chromium

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/engine"
	"github.com/tidwall/gjson"
)

func (p *Page) feed(method, params string) {
	if params == "" {
		params = "{}"
	}
	p.handle(context.Background(), event{Session: p.session, Method: method, Params: gjson.Parse(params)})
}

func TestEventsFromOtherSessionsAreIgnored(t *testing.T) {
	p, _ := newTestPage(t)
	p.handle(context.Background(), event{Session: "OTHER", Method: "Runtime.exceptionThrown", Params: gjson.Parse(`{"exceptionDetails":{"text":"boom"}}`)})
	p.handle(context.Background(), event{Session: "OTHER", Method: "Page.javascriptDialogOpening", Params: gjson.Parse(`{"type":"alert","message":"x"}`)})
	if ev := p.Drain(); len(ev) != 0 {
		t.Fatalf("foreign events leaked: %v", ev)
	}
}

func TestBrowserLevelEventsReachEveryPage(t *testing.T) {
	p, _ := newTestPage(t)
	p.handle(context.Background(), event{Session: "", Method: "Browser.downloadWillBegin", Params: gjson.Parse(`{"suggestedFilename":"a.zip","url":"http://x/a.zip"}`)})
	ev := p.Drain()
	if len(ev) != 1 || ev[0].Kind != engine.Download || ev[0].Text != "a.zip http://x/a.zip" {
		t.Fatalf("events = %v", ev)
	}
}

func TestMainFrameNavigationIsReportedSubframesAreNot(t *testing.T) {
	p, _ := newTestPage(t)
	p.feed("Page.frameNavigated", `{"frame":{"url":"http://sub/","parentId":"P"}}`)
	p.feed("Page.frameNavigated", `{"frame":{"url":"http://top/"}}`)
	ev := p.Drain()
	if len(ev) != 1 || ev[0].Kind != engine.Navigated || ev[0].Text != "http://top/" {
		t.Fatalf("events = %v", ev)
	}
}

func TestDialogsAreReportedAndAccepted(t *testing.T) {
	p, f := newTestPage(t)
	p.feed("Page.javascriptDialogOpening", `{"type":"confirm","message":"Sure?"}`)
	ev := p.Drain()
	if len(ev) != 1 || ev[0].Kind != engine.Dialog || ev[0].Text != "confirm: Sure?" {
		t.Fatalf("events = %v", ev)
	}
	eventually(t, "dialog answer", func() bool { return len(f.find("Page.handleJavaScriptDialog")) == 1 })
	r := f.find("Page.handleJavaScriptDialog")[0]
	if !r.Params.Get("accept").Bool() || r.Session != "S1" {
		t.Fatalf("dialog answer = %s", r.Raw)
	}
}

func TestScriptExceptionsAreReportedByFirstLine(t *testing.T) {
	p, _ := newTestPage(t)
	p.feed("Runtime.exceptionThrown", `{"exceptionDetails":{"text":"Uncaught","exception":{"description":"TypeError: x\n    at foo"}}}`)
	p.feed("Runtime.exceptionThrown", `{"exceptionDetails":{"text":"Uncaught SyntaxError"}}`)
	ev := p.Drain()
	if len(ev) != 2 || ev[0].Kind != engine.ConsoleError || ev[0].Text != "TypeError: x" || ev[1].Text != "Uncaught SyntaxError" {
		t.Fatalf("events = %v", ev)
	}
}

func TestOnlyConsoleErrorsAreReported(t *testing.T) {
	p, _ := newTestPage(t)
	p.feed("Runtime.consoleAPICalled", `{"type":"log","args":[{"value":"hello"}]}`)
	p.feed("Runtime.consoleAPICalled", `{"type":"error","args":[{"value":"bad thing\nmore"}]}`)
	p.feed("Runtime.consoleAPICalled", `{"type":"error","args":[{"description":"Error: obj"}]}`)
	ev := p.Drain()
	if len(ev) != 2 || ev[0].Text != "bad thing" || ev[1].Text != "Error: obj" {
		t.Fatalf("events = %v", ev)
	}
	for _, e := range ev {
		if e.Kind != engine.ConsoleError {
			t.Fatalf("kind = %v", e.Kind)
		}
	}
}

func TestRepeatedEventsCountUpInsteadOfPilingUp(t *testing.T) {
	p, _ := newTestPage(t)
	for i := 0; i < 3; i++ {
		p.note(engine.ConsoleError, "same")
	}
	p.note(engine.Dialog, "same") // same text, other kind: distinct
	p.note(engine.ConsoleError, "other")
	ev := p.Drain()
	want := []engine.Event{
		{Kind: engine.ConsoleError, Text: "same (x3)"},
		{Kind: engine.Dialog, Text: "same"},
		{Kind: engine.ConsoleError, Text: "other"},
	}
	if fmt.Sprint(ev) != fmt.Sprint(want) {
		t.Fatalf("events = %v want %v", ev, want)
	}
}

func TestDrainStartsAFreshWindow(t *testing.T) {
	p, _ := newTestPage(t)
	p.note(engine.Toast, "a")
	p.note(engine.Toast, "a")
	if got := p.Drain(); len(got) != 1 || got[0].Text != "a (x2)" {
		t.Fatalf("first drain = %v", got)
	}
	if got := p.Drain(); len(got) != 0 {
		t.Fatalf("second drain = %v", got)
	}
	p.note(engine.Toast, "a")
	if got := p.Drain(); len(got) != 1 || got[0].Text != "a" {
		t.Fatalf("counts leaked across drains: %v", got)
	}
}

func TestNoisyPagesAreCappedAndTheOverflowIsCounted(t *testing.T) {
	p, _ := newTestPage(t)
	for i := 0; i < maxEvents+5; i++ {
		p.note(engine.ConsoleError, fmt.Sprintf("e%d", i))
	}
	// a repeat of something already kept still counts, even when full
	p.note(engine.ConsoleError, "e0")
	ev := p.Drain()
	if len(ev) != maxEvents+1 {
		t.Fatalf("len = %d", len(ev))
	}
	if ev[0].Text != "e0 (x2)" {
		t.Fatalf("first = %q", ev[0].Text)
	}
	last := ev[len(ev)-1]
	if last.Kind != engine.ConsoleError || last.Text != "5 more events not shown" {
		t.Fatalf("last = %+v", last)
	}
	p.note(engine.ConsoleError, "fresh")
	if got := p.Drain(); len(got) != 1 {
		t.Fatalf("drop count leaked: %v", got)
	}
}

func TestLongEventTextIsTruncated(t *testing.T) {
	p, _ := newTestPage(t)
	p.note(engine.ConsoleError, strings.Repeat("a", 300))
	p.note(engine.ConsoleError, strings.Repeat("b", 301))
	ev := p.Drain()
	if ev[0].Text != strings.Repeat("a", 300) {
		t.Fatalf("300 chars were altered: %d", len(ev[0].Text))
	}
	if ev[1].Text != strings.Repeat("b", 300)+"…" {
		t.Fatalf("301 chars not truncated: %d", len(ev[1].Text))
	}
}

func TestRequestsRecordTheExchange(t *testing.T) {
	p, _ := newTestPage(t)
	p.feed("Network.requestWillBeSent", `{"requestId":"1","type":"Document","request":{"method":"GET","url":"http://x/"}}`)
	p.feed("Network.requestWillBeSent", `{"requestId":"2","type":"Script","request":{"method":"GET","url":"http://x/a.js"}}`)
	p.feed("Network.requestWillBeSent", `{"requestId":"3","type":"XHR","request":{"method":"POST","url":"http://x/api"}}`)
	p.feed("Network.requestWillBeSent", `{"requestId":"4","type":"Fetch","request":{"method":"GET","url":"http://x/f"}}`)
	p.feed("Network.responseReceived", `{"requestId":"3","response":{"status":201,"mimeType":"application/json"}}`)
	p.feed("Network.loadingFinished", `{"requestId":"3","encodedDataLength":42}`)
	p.feed("Network.responseReceived", `{"requestId":"unknown","response":{"status":500}}`)
	p.feed("Network.loadingFinished", `{"requestId":"unknown","encodedDataLength":1}`)
	got := p.Requests()
	if len(got) != 3 {
		t.Fatalf("requests = %+v", got)
	}
	want := []engine.Request{
		{Method: "GET", URL: "http://x/", Kind: "Document"},
		{Method: "POST", URL: "http://x/api", Kind: "XHR", Status: 201, Mime: "application/json", Size: 42},
		{Method: "GET", URL: "http://x/f", Kind: "Fetch"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("request %d = %+v want %+v", i, got[i], want[i])
		}
	}
}

func TestRedirectsReuseTheRequestRecord(t *testing.T) {
	p, _ := newTestPage(t)
	p.feed("Network.requestWillBeSent", `{"requestId":"1","type":"Document","request":{"method":"GET","url":"http://x/old"}}`)
	p.feed("Network.requestWillBeSent", `{"requestId":"1","type":"Document","request":{"method":"GET","url":"http://x/new"}}`)
	p.feed("Network.responseReceived", `{"requestId":"1","response":{"status":200,"mimeType":"text/html"}}`)
	got := p.Requests()
	if len(got) != 1 || got[0].URL != "http://x/new" || got[0].Status != 200 {
		t.Fatalf("requests = %+v", got)
	}
}

func TestRequestLogIsBounded(t *testing.T) {
	p, _ := newTestPage(t)
	send := func(id int) {
		p.feed("Network.requestWillBeSent", fmt.Sprintf(`{"requestId":"%d","type":"XHR","request":{"method":"GET","url":"http://x/%d"}}`, id, id))
	}
	for i := 0; i < maxRequests; i++ {
		send(i)
	}
	if n := len(p.Requests()); n != maxRequests {
		t.Fatalf("at the cap: %d", n)
	}
	send(maxRequests)
	got := p.Requests()
	if len(got) != maxRequests {
		t.Fatalf("over the cap: %d", len(got))
	}
	if got[0].URL != "http://x/1" || got[len(got)-1].URL != fmt.Sprintf("http://x/%d", maxRequests) {
		t.Fatalf("oldest not dropped: first=%s last=%s", got[0].URL, got[len(got)-1].URL)
	}
	// The dropped exchange is forgotten: its id starts a fresh record.
	send(0)
	got = p.Requests()
	if len(got) != maxRequests || got[len(got)-1].URL != "http://x/0" || got[0].URL != "http://x/2" {
		t.Fatalf("dropped id not forgotten: first=%s last=%s n=%d", got[0].URL, got[len(got)-1].URL, len(got))
	}
	p.mu.Lock()
	n := len(p.byID)
	p.mu.Unlock()
	if n != maxRequests {
		t.Fatalf("byID leaked: %d", n)
	}
}

func TestFailedRequestsAreReportedForDocumentsAndFetches(t *testing.T) {
	p, _ := newTestPage(t)
	for _, k := range []string{"Document", "Fetch", "XHR", "Image", "Script"} {
		p.feed("Network.requestWillBeSent", fmt.Sprintf(`{"requestId":"%s","type":"%s","request":{"method":"GET","url":"http://x/%s"}}`, k, k, k))
		p.feed("Network.loadingFailed", fmt.Sprintf(`{"requestId":"%s","errorText":"net::ERR_FAILED"}`, k))
	}
	p.feed("Network.requestWillBeSent", `{"requestId":"c","type":"Fetch","request":{"method":"GET","url":"http://x/c"}}`)
	p.feed("Network.loadingFailed", `{"requestId":"c","canceled":true,"errorText":"net::ERR_ABORTED"}`)
	p.feed("Network.loadingFailed", `{"requestId":"nope","errorText":"net::ERR_FAILED"}`)
	ev := p.Drain()
	var texts []string
	for _, e := range ev {
		if e.Kind != engine.FailedFetch {
			t.Fatalf("kind = %v", e.Kind)
		}
		texts = append(texts, e.Text)
	}
	want := "GET http://x/Document net::ERR_FAILED|GET http://x/Fetch net::ERR_FAILED|GET http://x/XHR net::ERR_FAILED"
	if strings.Join(texts, "|") != want {
		t.Fatalf("events = %v", texts)
	}
}

func TestFilterAllowsOrBlocksPausedRequests(t *testing.T) {
	p, f := newTestPage(t)
	seen := make(chan string, 4)
	err := p.filterRequests(context.Background(), func(url, typ string) error {
		seen <- url + "|" + typ
		if strings.Contains(url, "evil") {
			return errors.New("denied by policy")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	en := f.find("Fetch.enable")
	if len(en) != 1 || en[0].Session != "S1" || en[0].Params.Get("patterns.0.urlPattern").String() != "*" {
		t.Fatalf("enable = %+v", en)
	}
	p.feed("Fetch.requestPaused", `{"requestId":"r1","resourceType":"Script","request":{"url":"http://good/"}}`)
	p.feed("Fetch.requestPaused", `{"requestId":"r2","resourceType":"Image","request":{"url":"http://evil/"}}`)
	eventually(t, "both decided", func() bool {
		return len(f.find("Fetch.continueRequest")) == 1 && len(f.find("Fetch.failRequest")) == 1
	})
	c := f.find("Fetch.continueRequest")[0]
	if c.Params.Get("requestId").String() != "r1" || c.Session != "S1" {
		t.Fatalf("continue = %s", c.Raw)
	}
	fl := f.find("Fetch.failRequest")[0]
	if fl.Params.Get("requestId").String() != "r2" || fl.Params.Get("errorReason").String() != "BlockedByClient" {
		t.Fatalf("fail = %s", fl.Raw)
	}
	ev := p.Drain()
	if len(ev) != 1 || ev[0].Kind != engine.BlockedFetch || ev[0].Text != "http://evil/: denied by policy" {
		t.Fatalf("events = %v", ev)
	}
	if len(seen) != 2 {
		t.Fatalf("filter saw %d requests", len(seen))
	}
}

func TestPausedRequestsContinueWithoutAFilter(t *testing.T) {
	p, f := newTestPage(t)
	p.feed("Fetch.requestPaused", `{"requestId":"r1","request":{"url":"http://x/"}}`)
	eventually(t, "continue", func() bool { return len(f.find("Fetch.continueRequest")) == 1 })
	if len(f.find("Fetch.failRequest")) != 0 {
		t.Fatal("request failed without a filter")
	}
}

func TestFilterSeesURLAndResourceType(t *testing.T) {
	p, _ := newTestPage(t)
	got := make(chan string, 1)
	_ = p.filterRequests(context.Background(), func(url, typ string) error { got <- url + "|" + typ; return nil })
	p.feed("Fetch.requestPaused", `{"requestId":"r","resourceType":"XHR","request":{"url":"http://u/"}}`)
	if g := <-got; g != "http://u/|XHR" {
		t.Fatalf("filter args = %q", g)
	}
}

// scripted drives the commands a navigation issues and the notifications
// Chrome answers with.
func scripted(f *fakeBrowser, transient string) {
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "Page.navigate", "Page.navigateToHistoryEntry":
			defer f.emit("S1", "Page.loadEventFired", "")
		case "Runtime.evaluate":
			return evaluation(r, transient, longStill), "", true
		case "Page.getNavigationHistory":
			return `{"currentIndex":2,"entries":[{"id":10},{"id":11},{"id":12}]}`, "", true
		}
		return "{}", "", true
	}
}

func TestLoadReportsNavigationErrors(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(*fakeBrowser, rec) (string, string, bool) {
		return `{"errorText":"net::ERR_NAME_NOT_RESOLVED"}`, "", true
	}
	err := p.Load(context.Background(), "http://nope/")
	if err == nil || !strings.Contains(err.Error(), "ERR_NAME_NOT_RESOLVED") || !strings.Contains(err.Error(), "http://nope/") {
		t.Fatalf("err = %v", err)
	}
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "", "boom", true }
	if err := p.Load(context.Background(), "http://x/"); err == nil {
		t.Fatal("protocol error swallowed")
	}
}

func TestLoadDropsStaleSignals(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		if r.Method == "Page.navigate" {
			return `{"errorText":"stop"}`, "", true
		}
		return "{}", "", true
	}
	signal(p.loaded)
	_ = p.Load(context.Background(), "http://x/")
	if len(p.loaded) != 0 {
		t.Fatal("stale signals not cleared")
	}
}

func TestLoadStopsWhenContextEnds(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "{}", "", true } // never fires load
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		eventually(t, "navigate", func() bool { return len(f.find("Page.navigate")) == 1 })
		cancel()
	}()
	if err := p.Load(ctx, "http://x/"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestWaitsEndWhenTheBrowserExits(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		if r.Method == "Runtime.evaluate" {
			go func() { _ = f.sw.Close() }()
		}
		return "{}", "", true
	}
	err := p.Settle(context.Background())
	if err == nil {
		t.Fatal("settle succeeded after the browser exited")
	}
}

func TestSettleFailsWhenTheBrowserFails(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "", "nope", true }
	if err := p.Settle(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestTransientDrainToleratesGoneContextsOnly(t *testing.T) {
	for _, tc := range []struct {
		name, msg string
		fail      bool
	}{
		{"missing context", "Cannot find context with specified id", false},
		{"destroyed", "Execution context was destroyed.", false},
		{"other", "Session closed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, f := newTestPage(t)
			f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "", tc.msg, true }
			err := p.drainTransients(context.Background(), true)
			if (err != nil) != tc.fail {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestTransientDrainIgnoresScriptExceptions(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(*fakeBrowser, rec) (string, string, bool) {
		return `{"exceptionDetails":{"text":"x"},"result":{"value":"[\"ghost\"]"}}`, "", true
	}
	if err := p.drainTransients(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if ev := p.Drain(); len(ev) != 0 {
		t.Fatalf("events = %v", ev)
	}
}

func TestTransientDrainWithoutReportOnlyStartsAWindow(t *testing.T) {
	p, f := newTestPage(t)
	scripted(f, `["gone"]`)
	if err := p.drainTransients(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if ev := p.Drain(); len(ev) != 0 {
		t.Fatalf("events = %v", ev)
	}
	r := f.find("Runtime.evaluate")
	if len(r) != 1 || !r[0].Params.Get("returnByValue").Bool() {
		t.Fatalf("drain = %+v", r)
	}
}

func TestInstallTransientsRegistersForNewAndCurrentDocuments(t *testing.T) {
	p, f := newTestPage(t)
	if err := p.installTransients(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.methods(); fmt.Sprint(got) != "[Page.addScriptToEvaluateOnNewDocument Runtime.evaluate]" {
		t.Fatalf("methods = %v", got)
	}
	for _, r := range f.recorded() {
		src := r.Params.Get("source").String() + r.Params.Get("expression").String()
		if src != transientScript {
			t.Fatalf("%s did not carry the observer script", r.Method)
		}
	}
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "", "x", true }
	if err := p.installTransients(context.Background()); err == nil {
		t.Fatal("error swallowed")
	}
}

func TestBackStopsAtTheStartOfHistory(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(*fakeBrowser, rec) (string, string, bool) {
		return `{"currentIndex":0,"entries":[{"id":10}]}`, "", true
	}
	ok, err := p.Back(context.Background())
	if ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if len(f.find("Page.navigateToHistoryEntry")) != 0 {
		t.Fatal("navigated at the start of history")
	}
}

func TestBackGoesToThePreviousEntry(t *testing.T) {
	p, f := newTestPage(t)
	scripted(f, `[]`)
	ok, err := p.Back(context.Background())
	if !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	nav := f.find("Page.navigateToHistoryEntry")
	if len(nav) != 1 || nav[0].Params.Get("entryId").Int() != 11 {
		t.Fatalf("nav = %+v", nav)
	}
	ms := f.methods()
	if at := slices.Index(ms, "Page.navigateToHistoryEntry"); at < 0 || !slices.Contains(ms[at:], "Runtime.evaluate") {
		t.Fatalf("the page was not read after the navigation: %v", ms)
	}
}

func TestBackReportsErrors(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "", "x", true }
	if _, err := p.Back(context.Background()); err == nil {
		t.Fatal("history error swallowed")
	}
	scripted(f, `[]`)
	inner := f.handle
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		if r.Method == "Page.navigateToHistoryEntry" {
			return "", "x", true
		}
		return inner(f, r)
	}
	if ok, err := p.Back(context.Background()); ok || err == nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestPointerCommands(t *testing.T) {
	p, f := newTestPage(t)
	ctx := context.Background()
	if err := p.Move(ctx, 3, 4); err != nil {
		t.Fatal(err)
	}
	if err := p.Click(ctx, 10, 20); err != nil {
		t.Fatal(err)
	}
	if err := p.Wheel(ctx, 5, 6, 120); err != nil {
		t.Fatal(err)
	}
	r := f.find("Input.dispatchMouseEvent")
	if len(r) != 4 {
		t.Fatalf("events = %d", len(r))
	}
	type want struct {
		typ, button     string
		x, y            float64
		clicks, buttons int64
	}
	for i, w := range []want{
		{"mouseMoved", "none", 3, 4, 0, 0},
		{"mouseMoved", "none", 10, 20, 0, 0},
		{"mousePressed", "left", 10, 20, 1, 1},
		{"mouseReleased", "left", 10, 20, 1, 0},
	} {
		g := r[i].Params
		if g.Get("type").String() != w.typ || g.Get("button").String() != w.button || g.Get("x").Float() != w.x ||
			g.Get("y").Float() != w.y || g.Get("clickCount").Int() != w.clicks || g.Get("buttons").Int() != w.buttons {
			t.Fatalf("event %d = %s", i, r[i].Raw)
		}
	}
	// The wheel is a scroll gesture: the first raw wheel event sent to a fresh
	// document is dropped.
	g := f.find("Input.synthesizeScrollGesture")
	if len(g) != 1 {
		t.Fatalf("scroll gestures = %d", len(g))
	}
	if wh := g[0].Params; wh.Get("x").Float() != 5 || wh.Get("y").Float() != 6 || wh.Get("yDistance").Float() != -120 || wh.Get("gestureSourceType").String() != "mouse" {
		t.Fatalf("scroll gesture = %s", g[0].Raw)
	}
}

func TestClickStopsAtTheFirstFailure(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "", "x", true }
	if err := p.Click(context.Background(), 1, 1); err == nil {
		t.Fatal("expected error")
	}
	if n := len(f.recorded()); n != 1 {
		t.Fatalf("sent %d events after a failure", n)
	}
	for name, fn := range map[string]func() error{
		"move":  func() error { return p.Move(context.Background(), 1, 1) },
		"wheel": func() error { return p.Wheel(context.Background(), 1, 1, 1) },
	} {
		if fn() == nil {
			t.Fatalf("%s error swallowed", name)
		}
	}
	// failing on the second and third step of a click
	for failAt := 2; failAt <= 3; failAt++ {
		n := 0
		f.handle = func(*fakeBrowser, rec) (string, string, bool) {
			n++
			if n == failAt {
				return "", "x", true
			}
			return "{}", "", true
		}
		if p.Click(context.Background(), 1, 1) == nil {
			t.Fatalf("failure at step %d swallowed", failAt)
		}
	}
}

func TestKeyPressesNamedKeys(t *testing.T) {
	p, f := newTestPage(t)
	type want struct {
		name, down, key, code, text string
		vk                          int64
	}
	for _, w := range []want{
		{"Enter", "keyDown", "Enter", "Enter", "\r", 13},
		{"Tab", "rawKeyDown", "Tab", "Tab", "", 9},
		{"Escape", "rawKeyDown", "Escape", "Escape", "", 27},
		{"Space", "keyDown", " ", "Space", " ", 32},
		{"ArrowLeft", "rawKeyDown", "ArrowLeft", "ArrowLeft", "", 37},
		{"a", "keyDown", "a", "KeyA", "a", 65},
		{"Z", "keyDown", "Z", "KeyZ", "Z", 90},
	} {
		before := len(f.find("Input.dispatchKeyEvent"))
		if err := p.Key(context.Background(), w.name); err != nil {
			t.Fatal(err)
		}
		r := f.find("Input.dispatchKeyEvent")[before:]
		if len(r) != 2 {
			t.Fatalf("%s: %d events", w.name, len(r))
		}
		d, u := r[0].Params, r[1].Params
		if d.Get("type").String() != w.down || d.Get("key").String() != w.key || d.Get("code").String() != w.code ||
			d.Get("text").String() != w.text || d.Get("windowsVirtualKeyCode").Int() != w.vk {
			t.Fatalf("%s down = %s", w.name, r[0].Raw)
		}
		if w.text == "" && d.Get("text").Exists() {
			t.Fatalf("%s: raw key carries text", w.name)
		}
		if u.Get("type").String() != "keyUp" || u.Get("key").String() != w.key || u.Get("code").String() != w.code ||
			u.Get("windowsVirtualKeyCode").Int() != w.vk || u.Get("text").Exists() {
			t.Fatalf("%s up = %s", w.name, r[1].Raw)
		}
	}
}

func TestKeyRejectsUnknownNames(t *testing.T) {
	p, f := newTestPage(t)
	for _, n := range []string{"", "ab", "Blorp"} {
		if err := p.Key(context.Background(), n); err == nil {
			t.Fatalf("%q accepted", n)
		}
	}
	if len(f.recorded()) != 0 {
		t.Fatal("rejected keys were sent")
	}
}

func TestKeyDefinitionsAreConsistent(t *testing.T) {
	for name, d := range keyDefs {
		if d.vk == 0 || d.key == "" || d.code == "" {
			t.Errorf("%s incomplete: %+v", name, d)
		}
	}
}

func TestKeyReportsFailures(t *testing.T) {
	p, f := newTestPage(t)
	for failAt := 1; failAt <= 2; failAt++ {
		n := 0
		f.handle = func(*fakeBrowser, rec) (string, string, bool) {
			n++
			if n == failAt {
				return "", "x", true
			}
			return "{}", "", true
		}
		if p.Key(context.Background(), "Enter") == nil {
			t.Fatalf("failure at %d swallowed", failAt)
		}
	}
}

func TestFocusFocusesThenSelects(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
		if r.Method == "DOM.resolveNode" {
			return `{"object":{"objectId":"obj-1"}}`, "", true
		}
		return "{}", "", true
	}
	if err := p.Focus(context.Background(), 77); err != nil {
		t.Fatal(err)
	}
	if got := f.methods(); fmt.Sprint(got) != "[DOM.focus DOM.resolveNode Runtime.callFunctionOn]" {
		t.Fatalf("methods = %v", got)
	}
	if f.find("DOM.focus")[0].Params.Get("backendNodeId").Int() != 77 || f.find("DOM.resolveNode")[0].Params.Get("backendNodeId").Int() != 77 {
		t.Fatal("wrong node")
	}
	c := f.find("Runtime.callFunctionOn")[0].Params
	if c.Get("objectId").String() != "obj-1" || !strings.Contains(c.Get("functionDeclaration").String(), "select") || !c.Get("returnByValue").Bool() {
		t.Fatalf("call = %s", c.Raw)
	}
	if c.Get("arguments").Raw != "[]" {
		t.Fatalf("arguments = %s", c.Get("arguments").Raw)
	}
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "", "x", true }
	if p.Focus(context.Background(), 1) == nil {
		t.Fatal("focus error swallowed")
	}
}

func TestSelectOptionReportsMissingOptions(t *testing.T) {
	p, f := newTestPage(t)
	answer := "{}"
	f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "DOM.resolveNode":
			return `{"object":{"objectId":"o"}}`, "", true
		case "Runtime.callFunctionOn":
			return `{"result":{"value":` + answer + `}}`, "", true
		}
		return "{}", "", true
	}
	if err := p.SelectOption(context.Background(), 5, []string{"Blue"}); err != nil {
		t.Fatal(err)
	}
	c := f.find("Runtime.callFunctionOn")[0].Params
	if c.Get("arguments.0.value.0").String() != "Blue" || c.Get("objectId").String() != "o" {
		t.Fatalf("call = %s", c.Raw)
	}
	answer = `{"missing":"Red","options":["Blue"]}`
	err := p.SelectOption(context.Background(), 5, []string{"Red"})
	if err == nil || !strings.Contains(err.Error(), "Red") {
		t.Fatalf("err = %v", err)
	}
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "", "x", true }
	if p.SelectOption(context.Background(), 5, []string{"Blue"}) == nil {
		t.Fatal("error swallowed")
	}
}

func TestInsertTextSetFilesScrollAndHitTest(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
		if r.Method == "Page.getLayoutMetrics" {
			return `{"cssVisualViewport":{"pageX":0,"pageY":0,"clientWidth":1280,"clientHeight":800}}`, "", true
		}
		if r.Method == "DOM.getNodeForLocation" {
			return `{"backendNodeId":99}`, "", true
		}
		return "{}", "", true
	}
	ctx := context.Background()
	if err := p.InsertText(ctx, "hi"); err != nil {
		t.Fatal(err)
	}
	if err := p.SetFiles(ctx, 4, []string{"/a", "/b"}); err != nil {
		t.Fatal(err)
	}
	if err := p.ScrollIntoView(ctx, 4); err != nil {
		t.Fatal(err)
	}
	id, err := p.HitTest(ctx, 10.9, 20.2)
	if err != nil || id != 99 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	var typed string
	for _, k := range f.find("Input.dispatchKeyEvent") {
		typed += k.Params.Get("text").String()
	}
	if typed != "hi" {
		t.Fatalf("typed %q, want the text a key at a time", typed)
	}
	sf := f.find("DOM.setFileInputFiles")[0].Params
	if sf.Get("backendNodeId").Int() != 4 || sf.Get("files.1").String() != "/b" {
		t.Fatalf("files = %s", sf.Raw)
	}
	if f.find("DOM.scrollIntoViewIfNeeded")[0].Params.Get("backendNodeId").Int() != 4 {
		t.Fatal("scroll")
	}
	h := f.find("DOM.getNodeForLocation")[0].Params
	if h.Get("x").Int() != 10 || h.Get("y").Int() != 20 || h.Get("includeUserAgentShadowDOM").Bool() {
		t.Fatalf("hit = %s", h.Raw)
	}
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "", "x", true }
	if p.InsertText(ctx, "x") == nil || p.SetFiles(ctx, 1, nil) == nil || p.ScrollIntoView(ctx, 1) == nil {
		t.Fatal("error swallowed")
	}
	if _, err := p.HitTest(ctx, 1, 1); err == nil {
		t.Fatal("hit test error swallowed")
	}
}

func TestEvalDecodesValues(t *testing.T) {
	p, f := newTestPage(t)
	for _, tc := range []struct{ reply, want string }{
		{`{"result":{"type":"undefined"}}`, ""},
		{`{"result":{"type":"string","value":"hi"}}`, "hi"},
		{`{"result":{"type":"number","value":42}}`, "42"},
		{`{"result":{"type":"object","value":{"a":[1,2]}}}`, `{"a":[1,2]}`},
		{`{"result":{"type":"boolean","value":false}}`, "false"},
	} {
		f.handle = func(*fakeBrowser, rec) (string, string, bool) { return tc.reply, "", true }
		got, err := p.Eval(context.Background(), "x")
		if err != nil || got != tc.want {
			t.Fatalf("%s: got %q err %v", tc.reply, got, err)
		}
	}
	e := f.find("Runtime.evaluate")[0].Params
	if e.Get("expression").String() != "x" || !e.Get("returnByValue").Bool() || !e.Get("awaitPromise").Bool() || !e.Get("userGesture").Bool() {
		t.Fatalf("evaluate = %s", e.Raw)
	}
}

func TestEvalReportsScriptErrorsByFirstLine(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(*fakeBrowser, rec) (string, string, bool) {
		return `{"exceptionDetails":{"text":"Uncaught","exception":{"description":"ReferenceError: q\n at <anon>"}}}`, "", true
	}
	_, err := p.Eval(context.Background(), "q")
	if err == nil || err.Error() != "script error: ReferenceError: q" {
		t.Fatalf("err = %v", err)
	}
	f.handle = func(*fakeBrowser, rec) (string, string, bool) {
		return `{"exceptionDetails":{"text":"Uncaught"}}`, "", true
	}
	_, err = p.Eval(context.Background(), "q")
	if err == nil || err.Error() != "script error: Uncaught" {
		t.Fatalf("err = %v", err)
	}
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "", "x", true }
	if _, err := p.Eval(context.Background(), "q"); err == nil {
		t.Fatal("protocol error swallowed")
	}
}

func TestSnapshotCarriesTheViewport(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(*fakeBrowser, rec) (string, string, bool) {
		return `{"documents":[{"nodes":{"parentIndex":[]}}],"strings":[]}`, "", true
	}
	p.width, p.height = 800, 600
	s, err := p.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.ViewportW != 800 || s.ViewportH != 600 {
		t.Fatalf("viewport = %vx%v", s.ViewportW, s.ViewportH)
	}
	c := f.find("DOMSnapshot.captureSnapshot")[0].Params
	if !c.Get("includePaintOrder").Bool() || !c.Get("includeDOMRects").Bool() || !c.Get("includeBlendedBackgroundColors").Bool() || len(c.Get("computedStyles").Array()) == 0 {
		t.Fatalf("capture = %s", c.Raw)
	}
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return `{}`, "", true }
	if _, err := p.Snapshot(context.Background()); err == nil {
		t.Fatal("unparseable snapshot accepted")
	}
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "", "x", true }
	if _, err := p.Snapshot(context.Background()); err == nil {
		t.Fatal("protocol error swallowed")
	}
}

func TestInitPreparesThePage(t *testing.T) {
	p, f := newTestPage(t)
	if err := p.init(context.Background(), 1024, 768); err != nil {
		t.Fatal(err)
	}
	methods := f.methods()
	if got := fmt.Sprint(methods[:6]); got != "[Page.enable Network.enable Runtime.enable DOM.enable Emulation.setDeviceMetricsOverride Browser.setDownloadBehavior]" {
		t.Fatalf("setup began with %s", got)
	}
	if methods[len(methods)-1] != "DOM.getDocument" {
		t.Fatalf("setup ended with %s", methods[len(methods)-1])
	}
	// Each page script is registered for future documents and run in this one.
	scripts := ""
	for _, r := range f.find("Page.addScriptToEvaluateOnNewDocument") {
		scripts += r.Params.Get("source").String()
	}
	for _, marker := range []string{"window.open", "__rf_invalid_5d2b", "__rf_transient_7c1e"} {
		if !strings.Contains(scripts, marker) {
			t.Errorf("no page script contains %q", marker)
		}
	}
	m := f.find("Emulation.setDeviceMetricsOverride")[0]
	if m.Params.Get("width").Int() != 1024 || m.Params.Get("height").Int() != 768 || m.Params.Get("mobile").Bool() || m.Params.Get("deviceScaleFactor").Int() != 1 {
		t.Fatalf("metrics = %s", m.Raw)
	}
	d := f.find("Browser.setDownloadBehavior")[0]
	if d.Session != "" || d.Params.Get("behavior").String() != "deny" || !d.Params.Get("eventsEnabled").Bool() {
		t.Fatalf("download = %s", d.Raw)
	}
	for _, r := range f.recorded() {
		if r.Method != "Browser.setDownloadBehavior" && r.Session != "S1" {
			t.Fatalf("%s sent without the page session", r.Method)
		}
	}
	if f.find("DOM.getDocument")[0].Params.Get("depth").Int() != 0 {
		t.Fatal("depth")
	}
	if p.width != 1024 || p.height != 768 {
		t.Fatal("size not recorded")
	}
}

func TestInitStopsAtTheFirstFailure(t *testing.T) {
	all := []string{"Page.enable", "Network.enable", "Runtime.enable", "DOM.enable", "Emulation.setDeviceMetricsOverride",
		"Browser.setDownloadBehavior", "Page.addScriptToEvaluateOnNewDocument", "DOM.getDocument"}
	for _, bad := range all {
		p, f := newTestPage(t)
		f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
			if r.Method == bad {
				return "", "x", true
			}
			return "{}", "", true
		}
		if p.init(context.Background(), 1, 1) == nil {
			t.Fatalf("%s failure swallowed", bad)
		}
	}
}

func TestCloseClosesTheTargetWhenTheBrowserIsShared(t *testing.T) {
	p, f := newTestPage(t)
	p.Close()
	c := f.find("Target.closeTarget")
	if len(c) != 1 || c[0].Session != "" || c[0].Params.Get("targetId").String() != "T1" {
		t.Fatalf("close = %+v", c)
	}
	if !p.Alive() {
		t.Fatal("a page on a shared browser is alive")
	}
}

func TestAliveFollowsTheOwningBrowser(t *testing.T) {
	p, _ := newTestPage(t)
	exited := make(chan struct{})
	p.browser = &Browser{exited: exited}
	if !p.Alive() {
		t.Fatal("running browser reported dead")
	}
	close(exited)
	if p.Alive() {
		t.Fatal("exited browser reported alive")
	}
}

func TestBrowserCloseRemovesItsProfile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	close(exited)
	b := &Browser{cmd: &exec.Cmd{}, exited: exited, dir: dir}
	b.Close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("profile survives: %v", err)
	}
}

func TestNewPageAttachesAFlattenedSession(t *testing.T) {
	f := newFake(t)
	f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "Target.createTarget":
			return `{"targetId":"TT"}`, "", true
		case "Target.attachToTarget":
			return `{"sessionId":"SS"}`, "", true
		}
		return "{}", "", true
	}
	b := &Browser{conn: f.conn}
	p, err := b.NewPage(context.Background(), 640, 480)
	if err != nil {
		t.Fatal(err)
	}
	if p.session != "SS" || p.targetID != "TT" || p.width != 640 {
		t.Fatalf("page = %+v", p)
	}
	a := f.find("Target.attachToTarget")[0].Params
	if a.Get("targetId").String() != "TT" || !a.Get("flatten").Bool() {
		t.Fatalf("attach = %s", a.Raw)
	}
	if f.find("Target.createTarget")[0].Params.Get("url").String() != "about:blank" {
		t.Fatal("blank")
	}
	if f.find("Page.enable")[0].Session != "SS" {
		t.Fatal("commands must use the attached session")
	}
}

func TestNewPageFailures(t *testing.T) {
	for _, bad := range []string{"Target.createTarget", "Target.attachToTarget", "Page.enable"} {
		f := newFake(t)
		f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
			if r.Method == bad {
				return "", "x", true
			}
			return `{"targetId":"T","sessionId":"S"}`, "", true
		}
		b := &Browser{conn: f.conn}
		if _, err := b.NewPage(context.Background(), 1, 1); err == nil {
			t.Fatalf("%s failure swallowed", bad)
		}
	}
}

func TestBrowserEnvironmentStripsDaemonSecrets(t *testing.T) {
	in := []string{"PATH=/bin", "RIFFLE_SECRET_TOKEN=abc", "RIFFLE_SECRET_=x", "RIFFLE_CHROME=/c", "HOME=/h", "XRIFFLE_SECRET_A=1"}
	got := browserEnvironment(in)
	want := []string{"PATH=/bin", "RIFFLE_CHROME=/c", "HOME=/h", "XRIFFLE_SECRET_A=1"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("env = %v", got)
	}
	if len(browserEnvironment(nil)) != 0 {
		t.Fatal("nil env")
	}
}

func TestFindChromePrefersTheEnvironmentOverride(t *testing.T) {
	t.Setenv("RIFFLE_CHROME", "/opt/my-chrome")
	got, err := FindChrome()
	if err != nil || got != "/opt/my-chrome" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestFirstLine(t *testing.T) {
	for in, want := range map[string]string{"": "", "a": "a", "a\nb": "a", "\nb": "", "a\n": "a"} {
		if got := firstLine(in); got != want {
			t.Errorf("firstLine(%q) = %q", in, got)
		}
	}
}

func TestLoadAndNavigationSignals(t *testing.T) {
	p, _ := newTestPage(t)
	p.feed("Page.navigatedWithinDocument", "")
	if len(p.loaded) != 1 {
		t.Fatal("in-document navigation did not signal load")
	}
	p.feed("Page.loadEventFired", "")
	if len(p.loaded) != 1 {
		t.Fatalf("loaded=%d; a second signal must not pile up", len(p.loaded))
	}
}

func TestLoadOfASilentSiteSaysTheSiteDidNotRespond(t *testing.T) {
	old := callTimeout
	callTimeout = 100 * time.Millisecond
	defer func() { callTimeout = old }()
	p, f := newTestPage(t)
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		if r.Method == "Page.navigate" {
			return "", "", false // the site never answers
		}
		return "{}", "", true
	}
	err := p.Load(context.Background(), "http://silent.example/")
	if err == nil {
		t.Fatal("a load nobody answered reported success")
	}
	for _, want := range []string{"silent.example", "site did not respond"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("error %q leaks an internal timeout message", err)
	}
}

func TestKeyComboSplitsModifiersFromTheKey(t *testing.T) {
	for _, c := range []struct {
		name string
		mods int
		key  string
		bad  bool
	}{
		{name: "Enter", key: "Enter"},
		{name: "Shift+Tab", mods: 8, key: "Tab"},
		{name: "Control+a", mods: 2, key: "a"},
		{name: "ctrl+Shift+a", mods: 10, key: "a"},
		{name: "Meta+a", mods: 4, key: "a"},
		{name: "+", key: "+"},
		{name: "Shift++", mods: 8, key: "+"},
		{name: "Hyper+a", bad: true},
	} {
		mods, key, err := splitCombo(c.name)
		if c.bad {
			if err == nil {
				t.Errorf("%q: unknown modifier accepted", c.name)
			}
			continue
		}
		if err != nil || mods != c.mods || key != c.key {
			t.Errorf("%q: got %d %q %v, want %d %q", c.name, mods, key, err, c.mods, c.key)
		}
	}
}

func TestKeyComboSendsModifiersAndNoTypedText(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "{}", "", true }
	for _, name := range []string{"Control+a", "Shift+Tab"} {
		if err := p.Key(context.Background(), name); err != nil {
			t.Fatal(err)
		}
	}
	ev := f.find("Input.dispatchKeyEvent")
	if len(ev) != 4 {
		t.Fatalf("%d key events, want 4", len(ev))
	}
	ctrl := ev[0].Params
	if ctrl.Get("modifiers").Int() != 2 || ctrl.Get("text").Exists() || ctrl.Get("commands.0").String() != "selectAll" {
		t.Errorf("Control+a down = %s", ev[0].Raw)
	}
	if shift := ev[2].Params; shift.Get("modifiers").Int() != 8 || shift.Get("key").String() != "Tab" || shift.Get("commands").Exists() {
		t.Errorf("Shift+Tab down = %s", ev[2].Raw)
	}
}

func TestShiftedLetterTypesTheCapital(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "{}", "", true }
	if err := p.Key(context.Background(), "Shift+a"); err != nil {
		t.Fatal(err)
	}
	if d := f.find("Input.dispatchKeyEvent")[0].Params; d.Get("text").String() != "A" || d.Get("key").String() != "A" {
		t.Errorf("Shift+a down = %s", d.Raw)
	}
}

func TestAbortedNavigationDoesNotWaitForAnErrorPage(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return `{"errorText":"net::ERR_ABORTED"}`, "", true }
	start := time.Now()
	if err := p.Load(context.Background(), "http://x/file.bin"); err == nil {
		t.Fatal("an aborted navigation reported success")
	}
	if time.Since(start) >= errorPageTimeout {
		t.Errorf("waited %s for an error page Chrome never loads for a download", time.Since(start))
	}
}

func TestStreamingRequestsDoNotCountAsPending(t *testing.T) {
	p, _ := newTestPage(t)
	if !p.quiet() {
		t.Fatal("a page that has sent nothing is quiet")
	}
	p.feed("Network.requestWillBeSent", `{"requestId":"poll","type":"Fetch","request":{"method":"GET","url":"http://x/poll"}}`)
	if p.quiet() {
		t.Error("a request still waiting for its first byte is pending")
	}
	p.feed("Network.responseReceived", `{"requestId":"poll","response":{"status":200,"mimeType":"text/event-stream"}}`)
	if !p.quiet() {
		t.Error("a response that started and keeps streaming is not something to wait for")
	}
	p.feed("Network.requestWillBeSent", `{"requestId":"api","type":"XHR","request":{"method":"GET","url":"http://x/api"}}`)
	if p.quiet() {
		t.Error("a new request is pending even while a stream is open")
	}
	p.feed("Network.loadingFailed", `{"requestId":"api","canceled":true}`)
	if !p.quiet() {
		t.Error("a request that ended is not pending")
	}
	// A redirect reuses the request id and starts over.
	p.feed("Network.requestWillBeSent", `{"requestId":"poll","type":"Fetch","request":{"method":"GET","url":"http://x/poll2"}}`)
	if p.quiet() {
		t.Error("a redirected request waits for its new response")
	}
}

func TestInvalidFieldReportsAsAValidationEvent(t *testing.T) {
	p, _ := newTestPage(t)
	p.feed("Runtime.consoleAPICalled", `{"type":"debug","args":[{"value":"__rf_invalid__Email: Please include an '@' in the email address."}]}`)
	p.feed("Runtime.consoleAPICalled", `{"type":"debug","args":[{"value":"ordinary debug output"}]}`)
	ev := p.Drain()
	if len(ev) != 1 || ev[0].Kind != engine.Validation || ev[0].Text != "Email: Please include an '@' in the email address." {
		t.Errorf("events = %+v", ev)
	}
}

func TestLongOrUnprintableTextIsInsertedWholeNotTypedKeyByKey(t *testing.T) {
	for name, text := range map[string]string{
		"long":      strings.Repeat("x", maxTypedRunes+1),
		"newline":   "line one\nline two",
		"tab":       "a\tb",
		"empty":     "",
		"nul":       "a\x00b",
		"formatter": "a\u200bb",
	} {
		p, f := newTestPage(t)
		f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "{}", "", true }
		if err := p.InsertText(context.Background(), text); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if n := len(f.find("Input.dispatchKeyEvent")); n != 0 {
			t.Errorf("%s: %d key events, want the text inserted whole", name, n)
		}
		if got := f.find("Input.insertText"); len(got) != 1 || got[0].Params.Get("text").String() != text {
			t.Errorf("%s: insertText calls = %d", name, len(got))
		}
	}
}

func TestTypableTextIsShortAndPrintable(t *testing.T) {
	for text, want := range map[string]bool{
		"hello": true, "with spaces and punctuation!?": true, "åäö 日本語 😀": true, strings.Repeat("y", maxTypedRunes): true,
		strings.Repeat("y", maxTypedRunes+1): false, "": false, "a\nb": false, "a\tb": false, "\x1b": false,
	} {
		if got := typable(text); got != want {
			t.Errorf("typable(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestPunctuationIsNotSentAsAnEditingKey(t *testing.T) {
	// The ASCII value of these characters is the Windows code of a key that
	// edits or moves: . Delete, - Insert, ( Down, % Left, ! PageUp, # End.
	for _, ch := range []string{".", "-", "(", "&", "%", "'", "!", "\"", "#", "$", "@", ",", "/", ";"} {
		p, f := newTestPage(t)
		f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "{}", "", true }
		if err := p.Key(context.Background(), ch); err != nil {
			t.Fatal(err)
		}
		d := f.find("Input.dispatchKeyEvent")[0].Params
		if d.Get("windowsVirtualKeyCode").Int() != 0 || d.Get("text").String() != ch || d.Get("key").String() != ch {
			t.Errorf("%q must type itself and name no editing key: %s", ch, d.Raw)
		}
	}
	for ch, vk := range map[string]int64{"a": 65, "Z": 90, "5": 53} {
		p, f := newTestPage(t)
		f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "{}", "", true }
		if err := p.Key(context.Background(), ch); err != nil {
			t.Fatal(err)
		}
		if got := f.find("Input.dispatchKeyEvent")[0].Params.Get("windowsVirtualKeyCode").Int(); got != vk {
			t.Errorf("%q has key code %d, want %d", ch, got, vk)
		}
	}
}

func TestDoubleClickIsTwoClicksCountedOneAndTwo(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "{}", "", true }
	if err := p.DoubleClick(context.Background(), 7, 8); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range f.find("Input.dispatchMouseEvent") {
		g := r.Params
		got = append(got, fmt.Sprintf("%s/%d/%d", g.Get("type").String(), g.Get("clickCount").Int(), g.Get("buttons").Int()))
	}
	want := "[mouseMoved/0/0 mousePressed/1/1 mouseReleased/1/0 mousePressed/2/1 mouseReleased/2/0]"
	if fmt.Sprint(got) != want {
		t.Errorf("events = %v, want %s", got, want)
	}
}

func TestOnlyRecentUnansweredRequestsAreWorthWaitingFor(t *testing.T) {
	p, _ := newTestPage(t)
	now := time.Unix(1000, 0)
	p.clock = func() time.Time { return now }
	p.feed("Network.requestWillBeSent", `{"requestId":"old","type":"Fetch","request":{"method":"GET","url":"http://x/beacon"}}`)
	if p.quiet() {
		t.Fatal("a request that has only just been sent is pending")
	}
	now = now.Add(staleRequest - time.Millisecond)
	if p.quiet() {
		t.Error("a request a moment short of stale still counts")
	}
	now = now.Add(2 * time.Millisecond)
	if !p.quiet() {
		t.Error("a request unanswered for this long is a hung one, not what the action started")
	}
	p.feed("Network.requestWillBeSent", `{"requestId":"fresh","type":"XHR","request":{"method":"GET","url":"http://x/api"}}`)
	if p.quiet() {
		t.Error("a new request is pending even beside a stale one")
	}
}

func TestOnlyTheTopFrameLoadingCountsAsTheActionNavigating(t *testing.T) {
	p, _ := newTestPage(t)
	p.feed("Page.frameNavigated", `{"frame":{"id":"top","url":"http://x/"}}`)
	p.feed("Page.frameNavigated", `{"frame":{"id":"sub","parentId":"top","url":"http://x/frame"}}`)
	if p.isLoading() {
		t.Fatal("nothing is loading yet")
	}
	p.feed("Page.frameStartedLoading", `{"frameId":"sub"}`)
	if p.isLoading() {
		t.Error("a frame inside the page loading, such as an ad, is not the page navigating")
	}
	p.feed("Page.frameStartedLoading", `{"frameId":"top"}`)
	if !p.isLoading() {
		t.Error("the top frame started loading a document")
	}
	p.feed("Page.frameStoppedLoading", `{"frameId":"sub"}`)
	if !p.isLoading() {
		t.Error("a subframe finishing does not finish the top frame")
	}
	p.feed("Page.frameStoppedLoading", `{"frameId":"top"}`)
	if p.isLoading() {
		t.Error("the top frame finished")
	}
}

func TestAParsedDocumentIsNotStillNavigatingWhateverElseItLoads(t *testing.T) {
	p, _ := newTestPage(t)
	p.feed("Page.frameNavigated", `{"frame":{"id":"top","url":"http://x/"}}`)
	p.feed("Page.frameStartedLoading", `{"frameId":"top"}`)
	if !p.isLoading() {
		t.Fatal("the top frame started loading")
	}
	// A beacon or a stream keeps the frame from ever reporting that it stopped.
	p.feed("Page.domContentEventFired", ``)
	if p.isLoading() {
		t.Error("once the document is parsed an action has what it needs; waiting for every subresource waits forever on pages that never finish")
	}
}

func TestScrollingANodeWithNoBoxGoesThroughItsImage(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "DOM.scrollIntoViewIfNeeded":
			return "", "Node does not have a layout object", true
		case "DOM.resolveNode":
			return `{"object":{"objectId":"o"}}`, "", true
		}
		return `{"result":{"value":true}}`, "", true
	}
	if err := p.ScrollIntoView(context.Background(), 7); err != nil {
		t.Fatalf("a region with no box is scrolled to through its image: %v", err)
	}
	fn := f.find("Runtime.callFunctionOn")
	if len(fn) != 1 || !strings.Contains(fn[0].Params.Get("functionDeclaration").String(), "usemap") {
		t.Errorf("no fallback through the image: %v", f.methods())
	}
	// Other failures are not swallowed.
	f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "", "No node with given id", true }
	if err := p.ScrollIntoView(context.Background(), 7); err == nil {
		t.Error("an unrelated failure was swallowed")
	}
}

func TestSelectingADisabledOptionIsRefused(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "DOM.resolveNode":
			return `{"object":{"objectId":"o"}}`, "", true
		case "Runtime.callFunctionOn":
			return `{"result":{"value":{"disabled":"USA (closed)"}}}`, "", true
		}
		return "{}", "", true
	}
	err := p.SelectOption(context.Background(), 5, []string{"USA (closed)"})
	if err == nil || !strings.Contains(err.Error(), "disabled") || !strings.Contains(err.Error(), "USA (closed)") {
		t.Fatalf("a person cannot choose a disabled option, so neither can the agent: %v", err)
	}
}

func TestAFloodOfConsoleErrorsIsSummarisedNotListed(t *testing.T) {
	p, _ := newTestPage(t)
	for i := 0; i < 12; i++ {
		p.feed("Runtime.consoleAPICalled", fmt.Sprintf(`{"type":"error","args":[{"value":"telemetry failure number %d"}]}`, i))
	}
	p.feed("Runtime.exceptionThrown", `{"exceptionDetails":{"exception":{"description":"TypeError: real bug"}}}`)
	p.note(engine.Navigated, "http://x/next")
	ev := p.Drain()
	var errs, summary int
	var kinds []engine.EventKind
	for _, e := range ev {
		kinds = append(kinds, e.Kind)
		if e.Kind == engine.ConsoleError {
			errs++
			if strings.Contains(e.Text, "more console errors") {
				summary++
				if !strings.Contains(e.Text, "9 more") {
					t.Errorf("summary %q must count the ones left out", e.Text)
				}
			}
		}
	}
	if errs != maxConsoleErrors+2 || summary != 1 { // the shown logs, the exception, the summary
		t.Errorf("%d console lines (%d summary), want %d logs, the exception and one summary: %+v", errs, summary, maxConsoleErrors, ev)
	}
	var sawBug, sawNav bool
	for _, e := range ev {
		sawBug = sawBug || strings.Contains(e.Text, "real bug")
		sawNav = sawNav || e.Kind == engine.Navigated
	}
	if !sawBug || !sawNav {
		t.Errorf("an uncaught exception and other kinds of event are untouched by the cap: %v", kinds)
	}
}

func TestUploadingToAButtonUsesTheFileInputBehindIt(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "DOM.setFileInputFiles":
			if r.Params.Get("backendNodeId").Exists() {
				return "", "Node is not a file input element", true
			}
			return "{}", "", true
		case "DOM.resolveNode":
			return `{"object":{"objectId":"button-obj"}}`, "", true
		case "Runtime.callFunctionOn":
			return `{"result":{"type":"object","objectId":"input-obj"}}`, "", true
		}
		return "{}", "", true
	}
	if err := p.SetFiles(context.Background(), 7, []string{"/a.txt"}); err != nil {
		t.Fatalf("a button that opens a file input is uploaded to through that input: %v", err)
	}
	calls := f.find("DOM.setFileInputFiles")
	if len(calls) != 2 || calls[1].Params.Get("objectId").String() != "input-obj" || calls[1].Params.Get("files.0").String() != "/a.txt" {
		t.Errorf("second call must name the found input: %v", f.methods())
	}
	// With no input to be found the error names what is missing.
	f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "DOM.setFileInputFiles":
			return "", "Node is not a file input element", true
		case "DOM.resolveNode":
			return `{"object":{"objectId":"o"}}`, "", true
		case "Runtime.callFunctionOn":
			return `{"result":{"type":"object","subtype":"null","value":null}}`, "", true
		}
		return "{}", "", true
	}
	err := p.SetFiles(context.Background(), 7, []string{"/a.txt"})
	if err == nil || !strings.Contains(err.Error(), "no file input") {
		t.Errorf("err = %v, want it to say no file input was found", err)
	}
}

func TestFamiliarKeyNamesAreUnderstood(t *testing.T) {
	for alias, want := range map[string]string{
		"Esc": "Escape", "esc": "Escape", "ESCAPE": "Escape", "Return": "Enter", "enter": "Enter",
		"Del": "Delete", "Up": "ArrowUp", "down": "ArrowDown", "Left": "ArrowLeft", "RIGHT": "ArrowRight",
		"PgUp": "PageUp", "PageDown": "PageDown", "pgdn": "PageDown", "Spacebar": "Space", "tab": "Tab", "Backspace": "Backspace",
	} {
		p, f := newTestPage(t)
		f.handle = func(*fakeBrowser, rec) (string, string, bool) { return "{}", "", true }
		if err := p.Key(context.Background(), alias); err != nil {
			t.Errorf("%q: %v", alias, err)
			continue
		}
		got := f.find("Input.dispatchKeyEvent")[0].Params.Get("key").String()
		wantKey := keyDefs[want].key
		if got != wantKey {
			t.Errorf("%q sent key %q, want %q (%s)", alias, got, wantKey, want)
		}
	}
	p, _ := newTestPage(t)
	if err := p.Key(context.Background(), "Blorp"); err == nil || !strings.Contains(err.Error(), "Enter") {
		t.Errorf("an unknown key names some that work: %v", err)
	}
}

func TestForwardGoesToTheNextHistoryEntryAndReportsTheEnd(t *testing.T) {
	p, f := newTestPage(t)
	idx := 1
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "Page.getNavigationHistory":
			return fmt.Sprintf(`{"currentIndex":%d,"entries":[{"id":10},{"id":11},{"id":12}]}`, idx), "", true
		case "Page.navigateToHistoryEntry":
			defer f.emit("S1", "Page.loadEventFired", "")
		case "Runtime.evaluate":
			return evaluation(r, "[]", longStill), "", true
		}
		return "{}", "", true
	}
	ok, err := p.Forward(context.Background())
	if err != nil || !ok {
		t.Fatalf("forward from the middle: %v %v", ok, err)
	}
	if got := f.find("Page.navigateToHistoryEntry")[0].Params.Get("entryId").Int(); got != 12 {
		t.Errorf("went to entry %d, want the next one (12)", got)
	}
	idx = 2
	n := len(f.find("Page.navigateToHistoryEntry"))
	ok, err = p.Forward(context.Background())
	if err != nil || ok {
		t.Errorf("forward at the end must report there is no later page: %v %v", ok, err)
	}
	if len(f.find("Page.navigateToHistoryEntry")) != n {
		t.Error("navigated past the end")
	}
}

func TestTheProductTokenFollowsTheBrowsersOwnUserAgent(t *testing.T) {
	got, err := productUserAgent("Mozilla/5.0 (X11) Chrome/1.0", "Riffle/1.2.3")
	if err != nil || got != "Mozilla/5.0 (X11) Chrome/1.0 Riffle/1.2.3" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := productUserAgent("", "Riffle/1.2.3"); err == nil {
		t.Error("a browser that reports no user agent must be an error, not a bare product token")
	}
}

func TestHitTestAddsTheScrollOffsetChromeExpects(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "Page.getLayoutMetrics":
			return `{"cssVisualViewport":{"pageX":7,"pageY":1500,"clientWidth":1280,"clientHeight":800}}`, "", true
		case "DOM.getNodeForLocation":
			return `{"backendNodeId":42}`, "", true
		}
		return "{}", "", true
	}
	id, err := p.HitTest(context.Background(), 100, 30)
	if err != nil || id != 42 {
		t.Fatalf("HitTest = %d, %v", id, err)
	}
	got := f.find("DOM.getNodeForLocation")
	if len(got) != 1 || got[0].Params.Get("x").Int() != 107 || got[0].Params.Get("y").Int() != 1530 {
		t.Errorf("a viewport point on a page scrolled by 7,1500 is asked for at document 107,1530: %v", got)
	}
}

// Chrome's error page for an address that did not load commits as a frame of
// its own. The reply must not read as if the address loaded.
func TestAnErrorPageIsNotReportedAsTheAddressLoading(t *testing.T) {
	p, _ := newTestPage(t)
	p.feed("Page.frameNavigated", `{"frame":{"id":"F","url":"http://down.example/","unreachableUrl":"http://down.example/"}}`)
	evs := p.Drain()
	if len(evs) != 1 {
		t.Fatalf("events = %v", evs)
	}
	if !strings.Contains(evs[0].Text, "down.example") || !strings.Contains(evs[0].Text, "did not load") {
		t.Errorf("error page reported as %q", evs[0].Text)
	}
}

// A browser starved of CPU is as silent as a site that never responds; the
// error must not send the agent to blame the site.
func TestASilentBrowserIsNotBlamedOnTheSite(t *testing.T) {
	oldCall, oldProbe := callTimeout, probeTimeout
	callTimeout, probeTimeout = 100*time.Millisecond, 50*time.Millisecond
	defer func() { callTimeout, probeTimeout = oldCall, oldProbe }()
	p, f := newTestPage(t)
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		if r.Method == "Page.navigate" || r.Method == "Browser.getVersion" {
			return "", "", false
		}
		return "{}", "", true
	}
	err := p.Load(context.Background(), "http://fine.example/")
	if err == nil {
		t.Fatal("a load nobody answered reported success")
	}
	if strings.Contains(err.Error(), "site did not respond") {
		t.Errorf("error %q blames the site for a browser that did not answer", err)
	}
	if !strings.Contains(err.Error(), "browser") {
		t.Errorf("error %q does not say the browser stopped answering", err)
	}
}

// What the page being left does while the next one loads is not news about
// the next page. A dialog it opened was answered, so the agent hears of it as
// the old page's; its console noise is dropped.
func TestEventsFromThePageBeingLeftAreNotTheNewPages(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "Runtime.evaluate":
			return evaluation(r, "[]", longStill), "", true
		case "Page.navigate":
			f.emit("S1", "Page.javascriptDialogOpening", `{"type":"alert","message":"late"}`)
			f.emit("S1", "Runtime.consoleAPICalled", `{"type":"error","args":[{"value":"old noise"}]}`)
			f.thenAfterReply(func() {
				f.emit("S1", "Page.frameNavigated", `{"frame":{"id":"F","url":"http://next.example/"}}`)
				f.emit("S1", "Runtime.consoleAPICalled", `{"type":"error","args":[{"value":"new noise"}]}`)
				f.emit("S1", "Page.loadEventFired", "")
			})
		}
		return "{}", "", true
	}
	if err := p.Load(context.Background(), "http://next.example/"); err != nil {
		t.Fatal(err)
	}
	var dialog, oldNoise, newNoise bool
	for _, ev := range p.Drain() {
		switch {
		case ev.Kind == engine.Dialog:
			dialog = true
			if !strings.Contains(ev.Text, "late") || !strings.Contains(ev.Text, "previous page") {
				t.Errorf("a dialog from the page left behind reads as the new page's: %q", ev.Text)
			}
		case ev.Kind == engine.ConsoleError && strings.Contains(ev.Text, "old noise"):
			oldNoise = true
		case ev.Kind == engine.ConsoleError && strings.Contains(ev.Text, "new noise"):
			newNoise = true
		}
	}
	if !dialog {
		t.Error("a dialog the page left behind opened, and was answered, went unreported")
	}
	if oldNoise {
		t.Error("console noise from the page left behind was reported for the new page")
	}
	if !newNoise {
		t.Error("the new page's own events were dropped")
	}
}

// A select that has no such option says which options it has, so the next
// try can name one.
func TestAMissingOptionIsRefusedWithTheOptionsThereAre(t *testing.T) {
	p, f := newTestPage(t)
	var opts []string
	for k := 0; k < 14; k++ {
		opts = append(opts, fmt.Sprintf(`"Version %d"`, k))
	}
	f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "DOM.resolveNode":
			return `{"object":{"objectId":"o"}}`, "", true
		case "Runtime.callFunctionOn":
			return `{"result":{"value":{"missing":"v2","options":[` + strings.Join(opts, ",") + `]}}}`, "", true
		}
		return "{}", "", true
	}
	err := p.SelectOption(context.Background(), 5, []string{"v2"})
	if err == nil {
		t.Fatal("a missing option was chosen")
	}
	if !strings.HasSuffix(err.Error(), "the options are \"Version 0\", \"Version 1\", \"Version 2\", \"Version 3\", \"Version 4\", \"Version 5\", \"Version 6\", \"Version 7\", \"Version 8\", \"Version 9\" and 4 more") {
		t.Errorf("refusal %q must list the first ten options and count the rest", err)
	}
	for _, want := range []string{`"v2"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q lacks %s", err, want)
		}
	}
	if strings.Contains(err.Error(), "Version 10") {
		t.Errorf("refusal %q lists more options than it says", err)
	}
	opts = opts[:0]
	for k := 0; k < maxListedOptions; k++ {
		opts = append(opts, fmt.Sprintf(`"Version %d"`, k))
	}
	if err := p.SelectOption(context.Background(), 5, []string{"v2"}); err == nil || strings.Contains(err.Error(), "more") {
		t.Errorf("a select with exactly as many options as are listed has none more: %v", err)
	}
	opts = nil
	if err := p.SelectOption(context.Background(), 5, []string{"v2"}); err == nil || !strings.Contains(err.Error(), "has no options") {
		t.Errorf("a select with no options says so: %v", err)
	}
}

// The next dialog takes the answer set for it, once; the one after gets the
// default again. The event says how it was answered.
func TestTheNextDialogTakesTheAnswerSetForItOnce(t *testing.T) {
	p, f := newTestPage(t)
	p.AnswerDialog(engine.DialogAnswer{Dismiss: true})
	p.feed("Page.javascriptDialogOpening", `{"type":"alert","message":"Note"}`)
	p.feed("Page.javascriptDialogOpening", `{"type":"beforeunload","message":""}`)
	p.feed("Page.javascriptDialogOpening", `{"type":"confirm","message":"Sure?"}`)
	p.AnswerDialog(engine.DialogAnswer{Text: "blue", Typed: true})
	p.feed("Page.javascriptDialogOpening", `{"type":"prompt","message":"Colour?","defaultPrompt":"red"}`)
	p.feed("Page.javascriptDialogOpening", `{"type":"prompt","message":"Again?","defaultPrompt":"red"}`)
	eventually(t, "five answers", func() bool { return len(f.find("Page.handleJavaScriptDialog")) == 5 })
	answers := map[string]rec{}
	for _, r := range f.find("Page.handleJavaScriptDialog") {
		answers[r.Params.Get("promptText").String()+fmt.Sprint(r.Params.Get("accept").Bool())] = r
	}
	if n := len(answers); n != 4 {
		t.Errorf("an alert and a page leaving are accepted and take no answer: %v", f.find("Page.handleJavaScriptDialog"))
	}
	for _, want := range []string{"false", "bluetrue", "redtrue"} {
		if _, ok := answers[want]; !ok {
			t.Errorf("no answer %q among %v", want, f.find("Page.handleJavaScriptDialog"))
		}
	}
	got := map[string]bool{}
	for _, ev := range p.Drain() {
		got[ev.Text] = true
	}
	for _, want := range []string{`alert: Note`, `beforeunload: `, `confirm: Sure? (dismissed)`, `prompt: Colour? (answered "blue")`, `prompt: Again?`} {
		if !got[want] {
			t.Errorf("no event %q in %v", want, got)
		}
	}
}

// Console noise dropped from the page being left does not use up the room
// the new page's own errors have.
func TestDroppedOldPageErrorsLeaveRoomForTheNewPages(t *testing.T) {
	p, _ := newTestPage(t)
	p.leave()
	for k := 0; k < maxConsoleErrors+2; k++ {
		p.feed("Runtime.consoleAPICalled", fmt.Sprintf(`{"type":"error","args":[{"value":"old %d"}]}`, k))
	}
	p.arrived()
	p.feed("Runtime.consoleAPICalled", `{"type":"error","args":[{"value":"new"}]}`)
	ev := p.Drain()
	if len(ev) != 1 || ev[0].Text != "new" {
		t.Fatalf("the new page's error must be reported, and only it: %v", ev)
	}
}

func TestAHitTestOutsideTheVisiblePageHitsNothing(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "Page.getLayoutMetrics":
			return `{"cssVisualViewport":{"pageX":0,"pageY":2000,"clientWidth":1280,"clientHeight":800}}`, "", true
		case "DOM.getNodeForLocation":
			if r.Params.Get("y").Int() == 2000+799 {
				return "", "No node found at given location", true
			}
			return `{"backendNodeId":42}`, "", true
		}
		return "{}", "", true
	}
	for _, pt := range [][2]float64{{640, 800}, {640, -1}, {1280, 10}, {-1, 10}} {
		if _, err := p.HitTest(testCtx(t), pt[0], pt[1]); !errors.Is(err, engine.ErrOffViewport) {
			t.Errorf("hit test at %v: err = %v, want it off the visible page", pt, err)
		}
	}
	if n := len(f.find("DOM.getNodeForLocation")); n != 0 {
		t.Errorf("points off the page were asked of the browser %d times", n)
	}
	if id, err := p.HitTest(testCtx(t), 640, 400); err != nil || id != 42 {
		t.Errorf("a point on the page hits its node: %d, %v", id, err)
	}
	if got := f.find("DOM.getNodeForLocation"); len(got) != 1 || got[0].Params.Get("y").Int() != 2400 {
		t.Errorf("the point is asked in document coordinates: %v", got)
	}
	// The page shrank between reading its scroll and the hit test.
	if _, err := p.HitTest(testCtx(t), 640, 799); !errors.Is(err, engine.ErrOffViewport) {
		t.Errorf("a hit test the browser finds nothing for: err = %v, want it off the visible page", err)
	}
}

// A page that does not answer in time is reported in words an agent can act
// on, and as a deadline to code that tells a slow site from a stuck page.
func TestAPageThatDoesNotAnswerIsReportedPlainly(t *testing.T) {
	oldCall, oldProbe := callTimeout, probeTimeout
	callTimeout, probeTimeout = 100*time.Millisecond, 50*time.Millisecond
	defer func() { callTimeout, probeTimeout = oldCall, oldProbe }()
	for _, c := range []struct {
		browserAnswers bool
		want           string
	}{
		{true, "the page did not answer within"},
		{false, "the browser did not answer within"},
	} {
		p, f := newTestPage(t)
		f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
			if r.Method == "Runtime.evaluate" || (r.Method == "Browser.getVersion" && !c.browserAnswers) {
				return "", "", false
			}
			return "{}", "", true
		}
		_, err := p.Eval(context.Background(), "1")
		if err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), "context deadline exceeded") {
			t.Errorf("browser answers %v: err = %v, want %q and no internal wording", c.browserAnswers, err, c.want)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("the error is still a deadline: %v", err)
		}
	}
}
