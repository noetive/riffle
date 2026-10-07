package chromium

import (
	"context"
	"errors"
	"fmt"
	"net"
	neturl "net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/goccy/go-json"
	"github.com/noetive/riffle/internal/egress"
	"github.com/noetive/riffle/internal/engine"
	"github.com/noetive/riffle/internal/snapshot"
	"github.com/tidwall/gjson"
)

const (
	// loadTimeout bounds real time spent waiting for a navigation to load.
	loadTimeout = 15 * time.Second
	// errorPageTimeout bounds the wait for Chrome's own page after a failed load.
	errorPageTimeout = 2 * time.Second
)

// How a page settles. Pages run on the real clock, as in any browser, so their
// clocks read the real time and they go on running while the agent thinks. A
// page is read once it has had time to answer the last action and has gone
// quiet. Variables, so tests can shorten them.
var (
	// actionSettle is the least time a page gets to answer an action, such as a
	// click or typing, before it is read: a debounce or a panel opened on a
	// short timer shows within it.
	actionSettle = 600 * time.Millisecond
	// loadSettle is the same for a page that has just loaded: banners and
	// redirects a page starts on load come within it.
	loadSettle = 2 * time.Second
	// quietWindow is how long a page must add no visible content, and wait on
	// no request, to count as settled.
	quietWindow = 250 * time.Millisecond
	// busyGrace is how long past its minimum a page that keeps adding content
	// on its own, such as a ticker or a live feed, is waited for.
	busyGrace = 1500 * time.Millisecond
	// settlePoll is how often a settle looks again.
	settlePoll = 50 * time.Millisecond
)

// Page is one tab. It implements the engine port consumed by the program
// interpreter. The page runs on the real clock between actions as during them.
type Page struct {
	// acted and actedMin, guarded by mu, are when the agent last acted on the
	// page and the least time the page gets to answer before it is read.
	acted       time.Time
	conn        *conn
	browser     *Browser      // set when the page owns its browser
	egress      *egress.Proxy // the browser's only route out, when it has a dial guard
	pointer     *[2]float64   // where the last mouse event left the pointer, nil before any; guarded by mu
	loaded      chan struct{}
	byID        map[string]*engine.Request // guarded by mu
	kinds       map[string]string          // guarded by mu
	open        map[string]openRequest     // guarded by mu: requests sent and not yet finished
	clock       func() time.Time           // time.Now; tests set it
	mainFrame   string                     // guarded by mu: id of the top frame
	unreachable string                     // guarded by mu: the address the top frame last failed to load
	topDocument string                     // guarded by mu: the request that carried the top frame's latest document
	loadMoved   chan struct{}              // signalled whenever loading changes
	filter      RequestFilter              // guarded by mu
	override    map[string]any             // the user agent to present; set before adopt, then read-only
	life        context.Context

	session  string
	targetID string

	// events, repeats, requests and dropped are guarded by mu.
	events   []engine.Event
	repeats  []int
	requests []*engine.Request
	// answer, guarded by mu, is how the next dialog is answered; it is used
	// once.
	answer engine.DialogAnswer

	actedMin time.Duration
	commits  int // guarded by mu: documents the top frame has committed

	mu      sync.Mutex
	dropped int
	// logged counts the console.error lines kept since the last Drain, and
	// logHidden the ones left out.
	logged, logHidden int
	width             int
	height            int

	loading bool // guarded by mu: the top frame is loading a document
	// leaving is set, guarded by mu, while a navigation runs and the page it
	// leaves can still act: what that page does is not the new page's news.
	leaving bool
}

func newPage(ctx context.Context, c *conn, session, targetID string) *Page {
	p := &Page{
		conn: c, session: session, targetID: targetID, life: context.WithoutCancel(ctx),
		byID: map[string]*engine.Request{}, kinds: map[string]string{}, open: map[string]openRequest{}, clock: time.Now, loadMoved: make(chan struct{}, 1),
		loaded: make(chan struct{}, 1),
	}
	sub := c.subscribe()
	go func() {
		for {
			select {
			case ev := <-sub:
				p.handle(ctx, ev)
			case <-c.closed:
				return
			}
		}
	}()
	return p
}

// Option configures a page at Open.
type Option func(*options)

type options struct {
	filter  RequestFilter
	guard   egress.Guard
	product string
	state   engine.State
}

// WithRequestFilter makes every request the page issues pass the filter
// before it leaves the browser.
func WithRequestFilter(f RequestFilter) Option {
	return func(o *options) { o.filter = f }
}

// WithProduct appends a product token such as "Riffle/1.0" to the browser's
// own user agent, so servers and scripts can tell who is visiting.
func WithProduct(token string) Option {
	return func(o *options) { o.product = token }
}

// WithDialGuard routes all of the browser's traffic through a proxy that asks
// guard about every address before connecting to it. Unlike a request filter,
// which sees the address as written, the guard sees the address a name
// resolved to, for every frame, popup, worker and socket.
func WithDialGuard(guard egress.Guard) Option {
	return func(o *options) { o.guard = guard }
}

// Open launches Chrome and one page in it. Closing the page closes Chrome.
func Open(ctx context.Context, width, height int, opts ...Option) (*Page, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	var proxy *egress.Proxy
	var extra []string
	if o.guard != nil {
		var err error
		if proxy, err = egress.Start(ctx, o.guard); err != nil {
			return nil, err
		}
		extra = proxyArgs(proxy.Addr())
	}
	b, err := Launch(ctx, extra...)
	if err != nil {
		if proxy != nil {
			_ = proxy.Close()
		}
		return nil, err
	}
	// Restored before the page exists, and before its filter: the tab that
	// restores storage is answered here and never reaches the network.
	var p *Page
	var unrestored []string
	if !o.state.Empty() {
		unrestored, err = b.restore(ctx, o.state)
	}
	if err == nil {
		p, err = b.NewPage(ctx, width, height)
	}
	if err == nil {
		for _, u := range unrestored {
			p.note(engine.Unrestored, u)
		}
	}
	if err == nil && o.filter != nil {
		err = p.filterRequests(ctx, o.filter)
	}
	if err == nil && o.product != "" {
		err = p.identify(ctx, b.userAgent, o.product)
	}
	if err == nil && (o.filter != nil || o.product != "") {
		err = p.adopt(ctx)
	}
	if err != nil {
		b.Close()
		if proxy != nil {
			_ = proxy.Close()
		}
		return nil, err
	}
	p.browser = b
	if proxy != nil {
		p.egress = proxy
		proxy.OnRefused(func(target string, err error) {
			p.note(engine.BlockedFetch, fmt.Sprintf("%s: refused: %s", target, err))
		})
	}
	return p, nil
}

// proxyArgs sends every connection the browser makes through the proxy at
// addr, loopback included, and turns off the paths that would go around it:
// QUIC, and WebRTC over UDP.
func proxyArgs(addr string) []string {
	return []string{
		"--proxy-server=http://" + addr,
		"--proxy-bypass-list=<-loopback>",
		"--disable-quic",
		"--webrtc-ip-handling-policy=disable_non_proxied_udp",
	}
}

func (p *Page) call(ctx context.Context, method string, params any) ([]byte, error) {
	return p.conn.call(ctx, p.session, method, params)
}

func (p *Page) init(ctx context.Context, width, height int) error {
	p.width, p.height = width, height
	for _, m := range []string{"Page.enable", "Network.enable", "Runtime.enable", "DOM.enable"} {
		if _, err := p.call(ctx, m, nil); err != nil {
			return err
		}
	}
	if _, err := p.call(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": width, "height": height, "deviceScaleFactor": 1, "mobile": false,
	}); err != nil {
		return err
	}
	// Downloads are refused and reported; the agent decides what to do next.
	if _, err := p.conn.call(ctx, "", "Browser.setDownloadBehavior", map[string]any{
		"behavior": "deny", "eventsEnabled": true,
	}); err != nil {
		return err
	}
	if err := p.installTransients(ctx); err != nil {
		return err
	}
	if err := p.installSameTab(ctx); err != nil {
		return err
	}
	if err := p.installValidation(ctx); err != nil {
		return err
	}
	_, err := p.call(ctx, "DOM.getDocument", map[string]any{"depth": 0})
	return err
}

// handle records one notification.
func (p *Page) handle(ctx context.Context, ev event) {
	if ev.Session != p.session && !strings.HasPrefix(ev.Method, "Browser.") {
		return
	}
	switch ev.Method {
	case "Fetch.requestPaused":
		go p.decide(context.WithoutCancel(ctx), ev)
	case "Page.loadEventFired", "Page.navigatedWithinDocument":
		signal(p.loaded)
	case "Page.frameNavigated":
		if !ev.Params.Get("frame.parentId").Exists() {
			p.mu.Lock()
			p.mainFrame = ev.Params.Get("frame.id").String()
			p.commits++
			p.leaving = false
			p.mu.Unlock()
			if u := ev.Params.Get("frame.unreachableUrl").String(); u != "" {
				p.mu.Lock()
				p.unreachable = u
				p.mu.Unlock()
				// Chrome committed its own error page in place of the address.
				p.note(engine.Navigated, u+" (did not load)")
				break
			}
			p.note(engine.Navigated, ev.Params.Get("frame.url").String())
		}
	case "Page.domContentEventFired":
		// The document is parsed: what a navigation promised has arrived. The
		// frame may go on loading subresources, even forever.
		p.mu.Lock()
		p.loading = false
		signal(p.loadMoved)
		p.mu.Unlock()
	case "Page.frameStartedLoading", "Page.frameStoppedLoading":
		p.mu.Lock()
		if id := ev.Params.Get("frameId").String(); id != "" && id == p.mainFrame {
			p.loading = ev.Method == "Page.frameStartedLoading"
			signal(p.loadMoved)
		}
		p.mu.Unlock()
	case "Page.javascriptDialogOpening":
		kind := ev.Params.Get("type").String()
		p.mu.Lock()
		a := p.answer
		// A dismissal is for a confirm or a prompt, a typed answer for a
		// prompt. An alert, or a page asking whether to leave, is accepted and
		// leaves the answer for the dialog it was meant for.
		if kind == "prompt" || kind == "confirm" && !a.Typed {
			p.answer = engine.DialogAnswer{}
		} else {
			a = engine.DialogAnswer{}
		}
		p.mu.Unlock()
		text := kind + ": " + ev.Params.Get("message").String()
		// OK on a prompt answers with its default text, unless the program
		// set an answer for this dialog.
		reply := ev.Params.Get("defaultPrompt").String()
		switch {
		case a.Dismiss:
			text += " (dismissed)"
		case a.Typed:
			reply = a.Text
			text += fmt.Sprintf(" (answered %q)", a.Text)
		}
		p.note(engine.Dialog, text)
		go func() {
			dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			// A dialog left open blocks the page; one the browser no longer
			// shows needs no answer.
			_, _ = p.call(dctx, "Page.handleJavaScriptDialog", map[string]any{"accept": !a.Dismiss, "promptText": reply})
		}()
	case "Runtime.exceptionThrown":
		d := ev.Params.Get("exceptionDetails")
		text := d.Get("exception.description").String()
		if text == "" {
			text = d.Get("text").String()
		}
		p.note(engine.ConsoleError, firstLine(text))
	case "Runtime.consoleAPICalled":
		if ev.Params.Get("type").String() == "debug" {
			if msg, ok := invalidReport(ev.Params.Get("args.0.value").String()); ok {
				p.note(engine.Validation, msg)
				break
			}
		}
		if ev.Params.Get("type").String() == "error" {
			a := ev.Params.Get("args.0")
			text := a.Get("value").String()
			if text == "" {
				text = a.Get("description").String()
			}
			p.noteLog(firstLine(text))
		}
	case "Browser.downloadWillBegin":
		p.note(engine.Download, ev.Params.Get("suggestedFilename").String()+" "+ev.Params.Get("url").String())
	case "Network.requestWillBeSent":
		id := ev.Params.Get("requestId").String()
		kind := ev.Params.Get("type").String()
		r := &engine.Request{
			Method: ev.Params.Get("request.method").String(),
			URL:    ev.Params.Get("request.url").String(),
			Kind:   kind,
		}
		p.mu.Lock()
		p.open[id] = openRequest{sent: p.clock()}
		if prev := p.byID[id]; prev == nil { // redirects reuse the id; keep the first record updated below
			p.requests = append(p.requests, r)
			if len(p.requests) > maxRequests {
				p.forgetOldest()
			}
		} else {
			*prev = *r
			r = prev
		}
		p.byID[id] = r
		if kind == "Document" && (p.mainFrame == "" || ev.Params.Get("frameId").String() == p.mainFrame) {
			p.topDocument = id
		}
		p.mu.Unlock()
	case "Network.responseReceived":
		id := ev.Params.Get("requestId").String()
		p.mu.Lock()
		if o, ok := p.open[id]; ok {
			o.started = true
			p.open[id] = o
		}
		if r := p.byID[id]; r != nil {
			r.Status = int(ev.Params.Get("response.status").Int())
			r.Mime = ev.Params.Get("response.mimeType").String()
		}
		p.mu.Unlock()
	case "Network.loadingFinished":
		id := ev.Params.Get("requestId").String()
		p.mu.Lock()
		delete(p.open, id)
		if r := p.byID[id]; r != nil {
			r.Size = ev.Params.Get("encodedDataLength").Int()
		}
		p.mu.Unlock()
	case "Network.loadingFailed":
		id := ev.Params.Get("requestId").String()
		p.mu.Lock()
		delete(p.open, id)
		r := p.byID[id]
		p.mu.Unlock()
		if r != nil && !ev.Params.Get("canceled").Bool() && (r.Kind == "Fetch" || r.Kind == "XHR" || r.Kind == "Document") {
			p.note(engine.FailedFetch, r.Method+" "+r.URL+" "+ev.Params.Get("errorText").String())
		}
	}
}

// maxEvents bounds what one reply can carry; noisy pages repeat themselves.
const maxEvents = 30

// maxConsoleErrors bounds the console.error lines one reply carries. A page
// logs telemetry failures by the dozen, and every one would go to the agent;
// the rest are counted. Uncaught exceptions are not logs and are not capped.
const maxConsoleErrors = 3

// noteLog records a console.error line, up to the cap.
func (p *Page) noteLog(text string) {
	p.mu.Lock()
	if p.leaving {
		p.mu.Unlock()
		return // the old page's noise: dropped, and not counted against the new page's room
	}
	if p.logged >= maxConsoleErrors {
		// A repeat of a line already shown only counts up, as note does.
		for i := range p.events {
			if p.events[i].Kind == engine.ConsoleError && p.events[i].Text == truncateEvent(text) {
				p.mu.Unlock()
				p.note(engine.ConsoleError, text)
				return
			}
		}
		p.logHidden++
		p.mu.Unlock()
		return
	}
	p.logged++
	p.mu.Unlock()
	p.note(engine.ConsoleError, text)
}

// truncateEvent is the text note stores for an event.
func truncateEvent(text string) string {
	if len(text) > 300 {
		return text[:300] + "…"
	}
	return text
}

// note records an event once; repeats count up instead of piling up.
func (p *Page) note(k engine.EventKind, text string) {
	p.mu.Lock()
	leaving := p.leaving
	p.mu.Unlock()
	if leaving {
		switch k {
		case engine.ConsoleError, engine.FailedFetch, engine.Toast:
			return // the old page's noise
		case engine.Dialog:
			// Answered on the old page: a side effect the agent must hear of.
			text = "on the previous page: " + text
		}
	}
	if len(text) > 300 {
		text = text[:300] + "…"
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.events {
		if p.events[i].Kind == k && p.events[i].Text == text {
			p.repeats[i]++
			return
		}
	}
	if len(p.events) >= maxEvents {
		p.dropped++
		return
	}
	p.events = append(p.events, engine.Event{Kind: k, Text: text})
	p.repeats = append(p.repeats, 1)
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func clear(ch chan struct{}) {
	select {
	case <-ch:
	default:
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// Drain returns the events seen since the previous call.
func (p *Page) Drain() []engine.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	ev := p.events
	for i, n := range p.repeats {
		if n > 1 {
			ev[i].Text += fmt.Sprintf(" (x%d)", n)
		}
	}
	if p.dropped > 0 {
		ev = append(ev, engine.Event{Kind: engine.ConsoleError, Text: fmt.Sprintf("%d more events not shown", p.dropped)})
	}
	if p.logHidden > 0 {
		ev = append(ev, engine.Event{Kind: engine.ConsoleError, Text: fmt.Sprintf("%d more console errors not shown", p.logHidden)})
	}
	p.events, p.repeats, p.dropped, p.logged, p.logHidden = nil, nil, 0, 0, 0
	return ev
}

// Requests returns the document, fetch and XHR exchanges the page made.
func (p *Page) Requests() []engine.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []engine.Request
	for _, r := range p.requests {
		switch r.Kind {
		case "Document", "Fetch", "XHR":
			out = append(out, *r)
		}
	}
	return out
}

// maxNavigationRounds bounds how many documents in a row one settle follows:
// a page that redirects in script after script still ends.
const maxNavigationRounds = 3

// settle waits until the page has answered the last action and gone quiet:
// the action's minimum since it was taken, then no request the page waits on
// and no new visible content for quietWindow. A page that keeps adding content
// on its own is read busyGrace past its minimum; one that keeps waiting on
// requests is read at settleCap, and the agent is told. A navigation the page
// started meanwhile is followed, and its document gets a load's minimum.
func (p *Page) settle(ctx context.Context) error {
	seen := p.commitCount()
	for round := 0; ; round++ {
		loading, err := p.untilQuiet(ctx)
		if err != nil {
			return err
		}
		// A round trip lets the browser's report that a navigation began,
		// which trails the script that started it, reach us first.
		if _, err := p.Eval(ctx, "0"); err != nil {
			return err
		}
		if round >= maxNavigationRounds || (!loading && !p.isLoading() && p.commitCount() == seen) {
			break
		}
		if !p.awaitLoadingDone(ctx) {
			break // waiting more rounds on a load that will not end only repeats it
		}
		// A new document gets a load's minimum. A load that brought none, as a
		// download, leaves the page with what its action is owed.
		if p.commitCount() != seen {
			seen = p.commitCount()
			p.actedNow(loadSettle)
		}
	}
	if err := p.settleAnimations(ctx); err != nil {
		return err
	}
	return p.drainTransients(ctx, true)
}

// untilQuiet waits, in real time, for the page to answer the last action and
// go quiet, or for as long as a settle may take. A settle that waited on a
// request which never answered tells the agent the page may be incomplete.
// It stops early, reporting loading, when a new document is on its way.
func (p *Page) untilQuiet(ctx context.Context) (loading bool, err error) {
	began := p.clock()
	waited := false
	for {
		since, least := p.sinceActed()
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if p.isLoading() {
			return true, nil // a new document is on its way; the settle follows it
		}
		quiet := p.quiet()
		if since >= least && quiet {
			still, err := p.stillFor(ctx)
			if err != nil {
				return false, err
			}
			if still >= quietWindow || since >= least+busyGrace {
				if waited && p.hung(began.Add(-staleRequest)) {
					p.note(engine.SlowLoad, fmt.Sprintf("a request had not answered after %s; reading the page as it is", staleRequest))
				}
				return false, nil
			}
		}
		waited = waited || !quiet
		// The cap outlasts any minimum and its grace, so a page still unread
		// here is one waiting on a request.
		if p.clock().Sub(began) >= settleCap {
			p.note(engine.SlowLoad, fmt.Sprintf("the page was still waiting on requests after %s; reading it as it is", settleCap))
			return false, nil
		}
		if err := p.pause(ctx, settlePoll); err != nil {
			return false, err
		}
	}
}

// actedNow marks an action taken on the page now, which it gets at least
// least to answer before it is read.
func (p *Page) actedNow(least time.Duration) {
	p.mu.Lock()
	p.acted, p.actedMin = p.clock(), least
	p.mu.Unlock()
}

// sinceActed is how long ago the agent last acted, and the least time that
// action gets; with no action yet, the page has had all the time it needs.
func (p *Page) sinceActed() (time.Duration, time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.acted.IsZero() {
		return settleCap, 0
	}
	return p.clock().Sub(p.acted), p.actedMin
}

// stillFor is how long the page has added no visible content. A page that
// cannot be asked, as while a document is replaced, has just changed; only
// the caller giving up, or the browser going, is an error. It asks without a
// user gesture: a settle polls, and each gesture would let the page open
// another popup.
func (p *Page) stillFor(ctx context.Context) (time.Duration, error) {
	res, err := p.call(ctx, "Runtime.evaluate", map[string]any{
		"expression": transientStill, "returnByValue": true,
	})
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		select {
		case <-p.conn.closed:
			return 0, errors.New("chromium: browser exited")
		default:
		}
		return 0, nil
	}
	ms := gjson.GetBytes(res, "result.value").Float()
	return time.Duration(ms * float64(time.Millisecond)), nil
}

// pause waits d, in real time, unless the action or the browser ends first.
func (p *Page) pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.conn.closed:
		return errors.New("chromium: browser exited")
	}
}

func (p *Page) commitCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.commits
}

func (p *Page) isLoading() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loading
}

// awaitLoadingDone waits for the top frame to stop loading, for as long as a
// load may take. A page that will not finish is read as it is; it reports
// whether the load ended.
func (p *Page) awaitLoadingDone(ctx context.Context) bool {
	t := time.NewTimer(loadTimeout)
	defer t.Stop()
	for p.isLoading() {
		select {
		case <-p.loadMoved:
		case <-t.C:
			p.note(engine.SlowLoad, fmt.Sprintf("load did not finish in %s; page may be incomplete", loadTimeout))
			return false
		case <-ctx.Done():
			return false
		case <-p.conn.closed:
			return false
		}
	}
	return true
}

var errWaitTimeout = errors.New("chromium: wait timed out")

// openRequest is a request sent and not yet finished.
type openRequest struct {
	sent    time.Time
	started bool // the response has started
}

// staleRequest is how long a request may go unanswered before it is taken for
// a hung one: a beacon, a long poll, a frame that never loads. The page was
// not waiting on it when the action began, so the action does not wait either.
var staleRequest = 5 * time.Second

// quiet reports whether the page waits on no request. A response that has
// started and not finished is a stream, a long poll that was answered or a big
// download, and a request unanswered for long is a hung one; none of them is
// something to wait for.
func (p *Page) quiet() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	for _, o := range p.open {
		if !o.started && now.Sub(o.sent) < staleRequest {
			return false
		}
	}
	return true
}

// hung reports whether a request sent after since has gone unanswered past
// staleRequest. One sent before is a poll the page kept open all along.
func (p *Page) hung(since time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock()
	for _, o := range p.open {
		if !o.started && o.sent.After(since) && now.Sub(o.sent) >= staleRequest {
			return true
		}
	}
	return false
}

func (p *Page) await(ctx context.Context, ch chan struct{}, d time.Duration, what string) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ch:
		return nil
	case <-t.C:
		return fmt.Errorf("%w: %s", errWaitTimeout, what)
	case <-ctx.Done():
		return ctx.Err()
	case <-p.conn.closed:
		return errors.New("chromium: browser exited")
	}
}

// Settle waits until the page has answered the last action and gone quiet.
func (p *Page) Settle(ctx context.Context) error {
	return p.settle(ctx)
}

// Load navigates and waits for the load event and the page to settle.
func (p *Page) Load(ctx context.Context, url string) (err error) {
	clear(p.loaded)
	p.mu.Lock()
	p.requests, p.byID = nil, map[string]*engine.Request{}
	p.open = map[string]openRequest{}
	p.mu.Unlock()
	p.leave()
	defer func() {
		if err != nil {
			p.abandon(ctx)
		}
		p.arrived()
	}()
	started := time.Now()
	res, err := p.call(ctx, "Page.navigate", map[string]any{"url": url})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			// Chrome answers once the site starts to respond, so silence is the
			// site's, unless the browser itself has stopped answering.
			if !p.answers(ctx) {
				return fmt.Errorf("navigate %s: the browser did not respond within %s; this machine may be overloaded", url, callTimeout)
			}
			return fmt.Errorf("navigate %s: the site did not respond within %s", url, callTimeout)
		}
		return err
	}
	if e := gjson.GetBytes(res, "errorText").String(); e != "" {
		// Chrome now loads its error page; let that finish here so it is part of
		// this failure and not news in the next reply.
		// A download or a 204 aborts the navigation and loads no error page.
		if e != "net::ERR_ABORTED" {
			_ = p.await(ctx, p.loaded, errorPageTimeout, "error page")
		}
		return fmt.Errorf("navigate %s: %s%s", url, e, p.tunnelCause(url, e, started))
	}
	return p.afterNavigation(ctx)
}

// leave marks the start of a navigation: until the new document commits, what
// the page does is the old page's.
func (p *Page) leave() {
	p.mu.Lock()
	p.leaving = true
	p.mu.Unlock()
}

// arrived ends the leaving a navigation started, whether or not it committed.
func (p *Page) arrived() {
	p.mu.Lock()
	p.leaving = false
	p.mu.Unlock()
}

// probeTimeout bounds the check that the browser still answers at all.
var probeTimeout = 2 * time.Second

// answers reports whether the browser answers a command that costs it nothing.
// A browser starved of CPU is as silent as a site that never responds.
func (p *Page) answers(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	_, err := p.conn.call(ctx, "", "Browser.getVersion", nil)
	return err == nil
}

func (p *Page) afterNavigation(ctx context.Context) error {
	if err := p.await(ctx, p.loaded, loadTimeout, "page load"); err != nil {
		// A page whose subresources hang is still readable. Stop the load,
		// say so, and go on with what arrived.
		if !errors.Is(err, errWaitTimeout) {
			return err
		}
		p.note(engine.SlowLoad, fmt.Sprintf("load did not finish in %s; page may be incomplete", loadTimeout))
		_, _ = p.call(ctx, "Page.stopLoading", nil)
	}
	// The new document is in; what happens from here is its own, and its
	// minimum counts from now: the time a slow load took is not time the page
	// had to run.
	p.arrived()
	p.actedNow(loadSettle)
	// Spinners and placeholders that came and went while the document loaded
	// are not news; the window the agent hears about starts here.
	if err := p.drainTransients(ctx, false); err != nil {
		return err
	}
	if p.documentRefused() {
		// A refused document may be a check the browser passes on its own, as a
		// person's browser does while they wait: it loads the page it guards.
		if p.awaitCommit(ctx, challengeGrace) {
			p.actedNow(loadSettle)
		}
	}
	return p.settle(ctx)
}

// Back navigates one entry back in history. It reports false at the start.
func (p *Page) Back(ctx context.Context) (bool, error) { return p.step(ctx, -1) }

// Forward navigates one entry forward in history. It reports false at the end.
func (p *Page) Forward(ctx context.Context) (bool, error) { return p.step(ctx, 1) }

// step moves through history by delta entries, if there is such an entry.
func (p *Page) step(ctx context.Context, delta int) (moved bool, err error) {
	res, err := p.call(ctx, "Page.getNavigationHistory", nil)
	if err != nil {
		return false, err
	}
	cur := int(gjson.GetBytes(res, "currentIndex").Int())
	to := cur + delta
	if to < 0 || to >= int(gjson.GetBytes(res, "entries.#").Int()) {
		return false, nil
	}
	id := gjson.GetBytes(res, fmt.Sprintf("entries.%d.id", to)).Int()
	clear(p.loaded)
	p.leave()
	defer func() {
		if err != nil {
			p.abandon(ctx)
		}
		p.arrived()
	}()
	if _, err := p.call(ctx, "Page.navigateToHistoryEntry", map[string]any{"entryId": id}); err != nil {
		return false, err
	}
	return true, p.afterNavigation(ctx)
}

// Snapshot captures the document after cascade, layout and paint order.
func (p *Page) Snapshot(ctx context.Context) (*snapshot.Snapshot, error) {
	res, err := p.call(ctx, "DOMSnapshot.captureSnapshot", map[string]any{
		"computedStyles":                 snapshot.PropNames,
		"includePaintOrder":              true,
		"includeDOMRects":                true,
		"includeBlendedBackgroundColors": true,
	})
	if err != nil {
		return nil, err
	}
	s, err := snapshot.Parse(res)
	if err != nil {
		return nil, err
	}
	s.ViewportW, s.ViewportH = float64(p.width), float64(p.height)
	if s.Focus, err = p.focused(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// focused is the backend id of the element holding keyboard focus, 0 when the
// page itself holds it.
func (p *Page) focused(ctx context.Context) (int64, error) {
	res, err := p.call(ctx, "Runtime.evaluate", map[string]any{
		"expression": "(() => { const e = " + deepActiveElement + "; return e && e !== document.body && e !== document.documentElement ? e : null; })()",
	})
	if err != nil {
		return 0, err
	}
	oid := gjson.GetBytes(res, "result.objectId").String()
	if oid == "" {
		return 0, nil
	}
	node, err := p.call(ctx, "DOM.describeNode", map[string]any{"objectId": oid})
	if err != nil {
		return 0, err
	}
	return gjson.GetBytes(node, "node.backendNodeId").Int(), nil
}

// Move places the pointer at viewport coordinates.
func (p *Page) Move(ctx context.Context, x, y float64) error {
	p.actedNow(actionSettle)
	return p.mouse(ctx, "mouseMoved", x, y, "none", 0, 0)
}

// approach moves the pointer to x, y unless it is there already. A press
// right after a check of what lies under the pointer must not give the page
// another mousemove in which to slide something new beneath it.
func (p *Page) approach(ctx context.Context, x, y float64) error {
	p.mu.Lock()
	there := p.pointer != nil && *p.pointer == [2]float64{x, y}
	p.mu.Unlock()
	if there {
		return nil
	}
	return p.Move(ctx, x, y)
}

// Click performs the full pointer sequence at viewport coordinates.
func (p *Page) Click(ctx context.Context, x, y float64) error {
	p.actedNow(actionSettle)
	if err := p.approach(ctx, x, y); err != nil {
		return err
	}
	if err := p.mouse(ctx, "mousePressed", x, y, "left", 1, 1); err != nil {
		return err
	}
	return p.mouse(ctx, "mouseReleased", x, y, "left", 1, 0)
}

// DoubleClick presses and releases twice at the same point, the second counted
// as the second click, so the page sees click, click and dblclick.
func (p *Page) DoubleClick(ctx context.Context, x, y float64) error {
	p.actedNow(actionSettle)
	if err := p.approach(ctx, x, y); err != nil {
		return err
	}
	for n := 1; n <= 2; n++ {
		if err := p.mouse(ctx, "mousePressed", x, y, "left", n, 1); err != nil {
			return err
		}
		if err := p.mouse(ctx, "mouseReleased", x, y, "left", n, 0); err != nil {
			return err
		}
	}
	return nil
}

// Wheel scrolls under the pointer by dy CSS pixels. A scroll gesture is used
// because the first raw wheel event sent to a fresh document is dropped.
func (p *Page) Wheel(ctx context.Context, x, y, dy float64) error {
	p.actedNow(actionSettle)
	_, err := p.call(ctx, "Input.synthesizeScrollGesture", map[string]any{
		"x": x, "y": y, "yDistance": -dy, "gestureSourceType": "mouse", "speed": 100000,
	})
	return err
}

func (p *Page) mouse(ctx context.Context, typ string, x, y float64, button string, clicks, buttons int) error {
	_, err := p.call(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type": typ, "x": x, "y": y, "button": button, "clickCount": clicks, "buttons": buttons,
	})
	p.mu.Lock()
	if err == nil {
		p.pointer = &[2]float64{x, y}
	} else {
		p.pointer = nil // unknown: the next click moves again
	}
	p.mu.Unlock()
	return err
}

// keyDefs maps key names to DOM key, code and Windows virtual key code.
var keyDefs = map[string]struct {
	key, code string
	text      string
	vk        int
}{
	"Enter":      {"Enter", "Enter", "\r", 13},
	"Tab":        {"Tab", "Tab", "", 9},
	"Escape":     {"Escape", "Escape", "", 27},
	"Backspace":  {"Backspace", "Backspace", "", 8},
	"Delete":     {"Delete", "Delete", "", 46},
	"ArrowUp":    {"ArrowUp", "ArrowUp", "", 38},
	"ArrowDown":  {"ArrowDown", "ArrowDown", "", 40},
	"ArrowLeft":  {"ArrowLeft", "ArrowLeft", "", 37},
	"ArrowRight": {"ArrowRight", "ArrowRight", "", 39},
	"Home":       {"Home", "Home", "", 36},
	"End":        {"End", "End", "", 35},
	"PageUp":     {"PageUp", "PageUp", "", 33},
	"PageDown":   {"PageDown", "PageDown", "", 34},
	"Space":      {" ", "Space", " ", 32},
}

// modifierBits are the Input.dispatchKeyEvent modifier flags.
var modifierBits = map[string]int{
	"alt": 1, "option": 1,
	"control": 2, "ctrl": 2,
	"meta": 4, "cmd": 4, "command": 4,
	"shift": 8,
}

// splitCombo separates modifiers from the key in names such as Shift+Tab or
// Control+a. A lone "+" is the plus key, as is the last one in "Shift++".
func splitCombo(name string) (mods int, key string, err error) {
	key = name
	for {
		i := strings.Index(key, "+")
		if i <= 0 || i == len(key)-1 {
			return mods, key, nil
		}
		bit, ok := modifierBits[strings.ToLower(key[:i])]
		if !ok {
			return 0, "", fmt.Errorf("unknown modifier %q in %q; use Control, Alt, Shift or Meta", key[:i], name)
		}
		mods |= bit
		key = key[i+1:]
	}
}

// Key presses and releases one named key or one printable character,
// optionally held with modifiers: Enter, a, Shift+Tab, Control+a.
func (p *Page) Key(ctx context.Context, name string) error {
	p.actedNow(actionSettle)
	mods, key, err := splitCombo(name)
	if err != nil {
		return err
	}
	d, ok := keyDefs[canonicalKey(key)]
	if !ok {
		if len([]rune(key)) != 1 {
			return fmt.Errorf("unknown key %q; use a single character or a name such as Enter, Tab, Escape, ArrowDown, PageDown, Backspace, Delete, Space", name)
		}
		d.key, d.text = key, key
		// Only letters and digits have a key code equal to their ASCII value.
		// For punctuation that value is another key (. is Delete, - is Insert,
		// ( is Down), so it names none and the text is typed as it is.
		if r := []rune(strings.ToUpper(key))[0]; r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			d.code = "Key" + string(r)
			if r >= '0' && r <= '9' {
				d.code = "Digit" + string(r)
			}
			d.vk = int(r)
		}
	}
	if mods == 8 && len([]rune(key)) == 1 {
		// Shift alone types the capital.
		d.key, d.text = strings.ToUpper(key), strings.ToUpper(key)
	}
	down := map[string]any{"type": "keyDown", "key": d.key, "code": d.code, "windowsVirtualKeyCode": d.vk, "modifiers": mods}
	// A shortcut does not type its letter, and headless Chrome has no menu to
	// turn Control+a into select-all, so the editing command is named.
	if mods&7 != 0 {
		d.text = ""
		if mods&(2|4) != 0 && strings.EqualFold(key, "a") {
			down["commands"] = []string{"selectAll"}
		}
	}
	if d.text != "" {
		down["text"] = d.text
	} else {
		down["type"] = "rawKeyDown"
	}
	if _, err := p.call(ctx, "Input.dispatchKeyEvent", down); err != nil {
		return err
	}
	_, err = p.call(ctx, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyUp", "key": d.key, "code": d.code, "windowsVirtualKeyCode": d.vk, "modifiers": mods,
	})
	return err
}

// Focus moves focus to the node and selects its contents, in a field or an
// editable region, so typing replaces them.
func (p *Page) Focus(ctx context.Context, backend int64) error {
	if _, err := p.call(ctx, "DOM.focus", map[string]any{"backendNodeId": backend}); err != nil {
		return err
	}
	_, err := p.onNode(ctx, backend, `function(){
		if (this.select) { this.select(); return; }
		if (this.isContentEditable) {
			const r = document.createRange();
			r.selectNodeContents(this);
			const s = getSelection();
			s.removeAllRanges();
			s.addRange(r);
		}
	}`, nil)
	return err
}

// InsertText types text into the focused element as real input events.
// Inputs that keystrokes cannot reach, such as time, date, colour and range,
// get the value set directly with the same input and change events.
func (p *Page) InsertText(ctx context.Context, text string) error {
	p.actedNow(actionSettle)
	arg, err := json.Marshal(text)
	if err != nil {
		return err
	}
	set, err := p.Eval(ctx, `(function(t){
		const e = `+deepActiveElement+`;
		if (!e || e.tagName !== 'INPUT' || !/^(time|date|datetime-local|month|week|color|range)$/.test(e.type)) return false;
		Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(e, t);
		e.dispatchEvent(new Event('input', {bubbles:true}));
		e.dispatchEvent(new Event('change', {bubbles:true}));
		return true;
	})(`+string(arg)+`)`)
	if err != nil {
		return err
	}
	if set == "true" {
		return nil
	}
	if typable(text) {
		// Pages listen for keydown and keypress as well as input, so a short
		// text is typed a key at a time, as a person would.
		for _, r := range text {
			if err := p.Key(ctx, string(r)); err != nil {
				return err
			}
		}
		return nil
	}
	_, err = p.call(ctx, "Input.insertText", map[string]any{"text": text})
	return err
}

// maxTypedRunes bounds the text typed key by key; longer text is inserted.
const maxTypedRunes = 200

// typable reports whether text is short and made only of printable
// characters, so each can be sent as a key press.
func typable(text string) bool {
	n := 0
	for _, r := range text {
		if n++; n > maxTypedRunes || !unicode.IsPrint(r) {
			return false
		}
	}
	return n > 0
}

// SelectOption makes the options whose value or label equals the wanted ones
// the selection: one for a select, any number for a multi-select.
func (p *Page) SelectOption(ctx context.Context, backend int64, want []string) error {
	p.actedNow(actionSettle)
	res, err := p.onNode(ctx, backend, `function(want){
		const norm = (s) => (s || '').replace(/\s+/g, ' ').trim().toLowerCase();
		const off = (o) => o.disabled || (o.parentElement && o.parentElement.tagName === 'OPTGROUP' && o.parentElement.disabled);
		const opts = Array.from(this.options||[]);
		const names = () => opts.map((o) => (o.label || o.text || o.value).replace(/\s+/g, ' ').trim());
		if (this.disabled || (this.matches && this.matches(':disabled'))) return {off: true};
		if (want.length > 1 && !this.multiple) return {single: true};
		const chosen = [];
		for (const w of want) {
			const exact = (o) => o.value === w || o.label === w || o.text.trim() === w;
			const loose = (o) => norm(o.value) === norm(w) || norm(o.label) === norm(w) || norm(o.text) === norm(w);
			const o = opts.find(exact) || opts.find(loose);
			if (!o) return {missing: w, options: names()};
			if (off(o)) return {disabled: w};
			chosen.push(o);
		}
		if (this.multiple) {
			for (const o of opts) o.selected = chosen.includes(o);
		} else {
			this.selectedIndex = chosen[0].index; // the option named, not the first with its value
		}
		this.dispatchEvent(new Event('input', {bubbles:true}));
		this.dispatchEvent(new Event('change', {bubbles:true}));
		return {};
	}`, []any{want})
	if err != nil {
		return err
	}
	v := gjson.GetBytes(res, "result.value")
	switch {
	case v.Get("off").Bool():
		return fmt.Errorf("the control is disabled; a person cannot choose in it either, so fill in what the form still needs first")
	case v.Get("single").Bool():
		return fmt.Errorf("this select takes one option, not %d", len(want))
	case v.Get("disabled").Exists():
		return fmt.Errorf("option %q is disabled; a person cannot choose it either", v.Get("disabled").String())
	case v.Get("missing").Exists():
		return fmt.Errorf("no option %q; %s", v.Get("missing").String(), optionList(v.Get("options").Array()))
	}
	return nil
}

// SetFiles attaches files to a file input.
func (p *Page) SetFiles(ctx context.Context, backend int64, paths []string) error {
	p.actedNow(actionSettle)
	_, err := p.call(ctx, "DOM.setFileInputFiles", map[string]any{"files": paths, "backendNodeId": backend})
	if err == nil || !strings.Contains(err.Error(), "not a file input") {
		return err
	}
	// A styled button or link usually opens a file input kept out of sight.
	// The input belongs to the target when it sits inside it or beside it in
	// the same field; failing that, a page with just one is unambiguous.
	oid, err := p.fileInputNear(ctx, backend)
	if err != nil {
		return err
	}
	_, err = p.call(ctx, "DOM.setFileInputFiles", map[string]any{"files": paths, "objectId": oid})
	return err
}

// fileInputNear returns a handle to the file input a node stands for.
func (p *Page) fileInputNear(ctx context.Context, backend int64) (string, error) {
	r, err := p.call(ctx, "DOM.resolveNode", map[string]any{"backendNodeId": backend})
	if err != nil {
		return "", err
	}
	res, err := p.call(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": gjson.GetBytes(r, "object.objectId").String(),
		"functionDeclaration": `function(){
			const inputs = (root) => root.querySelectorAll('input[type=file]');
			let found = inputs(this);
			for (let el = this.parentElement, hops = 0; found.length === 0 && el && hops < 4; el = el.parentElement, hops++) {
				const here = inputs(el);
				if (here.length === 1) found = here;
				else if (here.length > 1) break;
			}
			if (found.length === 0) {
				const all = inputs(document);
				if (all.length === 1) found = all;
			}
			return found.length === 1 ? found[0] : null;
		}`,
	})
	if err != nil {
		return "", err
	}
	oid := gjson.GetBytes(res, "result.objectId").String()
	if oid == "" {
		return "", errors.New("no file input belongs to this control; name the file input itself, or the page has several")
	}
	return oid, nil
}

// ScrollIntoView scrolls the node into the viewport if it is not already.
func (p *Page) ScrollIntoView(ctx context.Context, backend int64) error {
	_, err := p.call(ctx, "DOM.scrollIntoViewIfNeeded", map[string]any{"backendNodeId": backend})
	if err != nil && strings.Contains(err.Error(), "layout object") {
		// A node with no box of its own, such as an image-map region, is
		// scrolled to through the image it is drawn on.
		_, err = p.onNode(ctx, backend, `function(){
			let el = this;
			if (this.tagName === 'AREA' && this.parentElement) {
				const name = this.parentElement.getAttribute('name') || this.parentElement.id;
				el = document.querySelector('img[usemap="#' + name + '"]') || this.parentElement;
			}
			if (el.scrollIntoView) el.scrollIntoView({block: 'nearest', inline: 'nearest'});
		}`, nil)
	}
	return err
}

// HitTest returns the backend id of the topmost node at viewport coordinates.
// Chrome hit-tests in document coordinates, so the scroll offset is added.
func (p *Page) HitTest(ctx context.Context, x, y float64) (int64, error) {
	m, err := p.call(ctx, "Page.getLayoutMetrics", nil)
	if err != nil {
		return 0, err
	}
	vp := gjson.GetBytes(m, "cssVisualViewport")
	w, h := vp.Get("clientWidth").Float(), vp.Get("clientHeight").Float()
	if w <= 0 || h <= 0 {
		return 0, fmt.Errorf("the browser reported no visible page to hit test (%gx%g)", w, h)
	}
	// Nothing is hit outside the visible page (CSSOM View, elementFromPoint).
	if x < 0 || y < 0 || x >= w || y >= h {
		return 0, engine.ErrOffViewport
	}
	res, err := p.call(ctx, "DOM.getNodeForLocation", map[string]any{
		"x": int(x + vp.Get("pageX").Float()), "y": int(y + vp.Get("pageY").Float()), "includeUserAgentShadowDOM": false,
	})
	if err != nil {
		if strings.Contains(err.Error(), "No node found at given location") {
			// The page scrolled or shrank between the two calls.
			return 0, fmt.Errorf("%w: %w", engine.ErrOffViewport, err)
		}
		return 0, err
	}
	return gjson.GetBytes(res, "backendNodeId").Int(), nil
}

// Eval runs a JavaScript expression in the page and returns its value as JSON text.
func (p *Page) Eval(ctx context.Context, expr string) (string, error) {
	res, err := p.call(ctx, "Runtime.evaluate", map[string]any{
		"expression": expr, "returnByValue": true, "awaitPromise": true, "userGesture": true,
	})
	if err != nil {
		return "", err
	}
	if d := gjson.GetBytes(res, "exceptionDetails"); d.Exists() {
		text := d.Get("exception.description").String()
		if text == "" {
			text = d.Get("text").String()
		}
		return "", fmt.Errorf("script error: %s", firstLine(text))
	}
	v := gjson.GetBytes(res, "result")
	if v.Get("type").String() == "undefined" {
		return "", nil
	}
	if v.Get("type").String() == "string" {
		return v.Get("value").String(), nil
	}
	return v.Get("value").Raw, nil
}

// onNode calls a function with the node as `this`.
func (p *Page) onNode(ctx context.Context, backend int64, fn string, args []any) ([]byte, error) {
	r, err := p.call(ctx, "DOM.resolveNode", map[string]any{"backendNodeId": backend})
	if err != nil {
		return nil, err
	}
	oid := gjson.GetBytes(r, "object.objectId").String()
	ca := []map[string]any{}
	for _, a := range args {
		ca = append(ca, map[string]any{"value": a})
	}
	return p.call(ctx, "Runtime.callFunctionOn", map[string]any{
		"objectId": oid, "functionDeclaration": fn, "arguments": ca, "returnByValue": true,
	})
}

// Close ends the page, and the browser when the page owns it.
func (p *Page) Close() {
	if p.egress != nil {
		defer func() { _ = p.egress.Close() }()
	}
	if p.browser != nil {
		p.browser.Close()
		return
	}
	ctx, cancel := context.WithTimeout(p.life, 5*time.Second)
	defer cancel()
	_, _ = p.conn.call(ctx, "", "Target.closeTarget", map[string]any{"targetId": p.targetID})
}

// Advance waits d in real time, as a person waits, then for the page to settle.
func (p *Page) Advance(ctx context.Context, d time.Duration) error {
	if err := p.pause(ctx, d); err != nil {
		return err
	}
	return p.settle(ctx)
}

// Alive reports whether the browser behind the page is still running.
func (p *Page) Alive() bool {
	return p.browser == nil || p.browser.Alive()
}

// RequestFilter decides whether the page may make a request. It returns nil
// to allow, or an error whose text is reported to the agent.
type RequestFilter func(url, resourceType string) error

// filterRequests makes every request the page issues, subresources and
// redirects included, pass through the filter before it leaves the browser.
func (p *Page) filterRequests(ctx context.Context, f RequestFilter) error {
	p.mu.Lock()
	p.filter = f
	p.mu.Unlock()
	_, err := p.call(ctx, "Fetch.enable", fetchAll)
	return err
}

// fetchAll pauses every request until decide answers it.
var fetchAll = map[string]any{"patterns": []map[string]any{{"urlPattern": "*"}}}

// decide answers one paused request, on the session that paused it: the page's or
// that of a frame or worker it started.
func (p *Page) decide(ctx context.Context, ev event) {
	p.mu.Lock()
	f := p.filter
	p.mu.Unlock()
	id := ev.Params.Get("requestId").String()
	url := ev.Params.Get("request.url").String()
	if f != nil {
		if err := f(url, ev.Params.Get("resourceType").String()); err != nil {
			p.note(engine.BlockedFetch, fmt.Sprintf("%s: %s", url, err))
			_, _ = p.conn.call(ctx, ev.Session, "Fetch.failRequest", map[string]any{"requestId": id, "errorReason": "BlockedByClient"})
			return
		}
	}
	_, _ = p.conn.call(ctx, ev.Session, "Fetch.continueRequest", map[string]any{"requestId": id})
}

// maxRequests bounds the exchanges kept for the net view: a page that polls
// forever must not grow the daemon.
const maxRequests = 500

// forgetOldest drops the oldest exchange. The caller holds p.mu.
func (p *Page) forgetOldest() {
	old := p.requests[0]
	p.requests = p.requests[1:]
	for id, r := range p.byID {
		if r == old {
			delete(p.byID, id)
			return
		}
	}
}

// keyAliases are the spellings other tools and keyboards use for named keys.
var keyAliases = map[string]string{
	"esc": "Escape", "return": "Enter", "del": "Delete", "up": "ArrowUp", "down": "ArrowDown",
	"left": "ArrowLeft", "right": "ArrowRight", "pgup": "PageUp", "pgdn": "PageDown",
	"spacebar": "Space",
}

// canonicalKey maps a key name to the one keyDefs uses: aliases and any case.
func canonicalKey(name string) string {
	if _, ok := keyDefs[name]; ok {
		return name
	}
	lower := strings.ToLower(name)
	if c, ok := keyAliases[lower]; ok {
		return c
	}
	for k := range keyDefs {
		if strings.ToLower(k) == lower {
			return k
		}
	}
	return name
}

// settleCap is how long a settle waits in all before it reads a page that
// will not settle, as one still waiting on a request. More waiting does not
// help: the page is readable now, and it runs on until the next action.
var settleCap = 5 * time.Second

// SetSecret puts a secret into the node without a keystroke, then sends the
// input and change events a page listens for. Keys go wherever the focus is,
// and a frame from another origin can take the focus between two of them; a
// value set on the node goes nowhere else.
func (p *Page) SetSecret(ctx context.Context, backend int64, secret string) error {
	p.actedNow(actionSettle)
	res, err := p.onNode(ctx, backend, `function(t){
		const proto = this instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype
			: this instanceof HTMLInputElement ? HTMLInputElement.prototype : null;
		if (proto) {
			Object.getOwnPropertyDescriptor(proto, 'value').set.call(this, t);
		} else if (this.isContentEditable) {
			this.textContent = t;
		} else {
			return false;
		}
		this.dispatchEvent(new InputEvent('input', {bubbles:true, inputType:'insertText', data:t}));
		this.dispatchEvent(new Event('change', {bubbles:true}));
		return true;
	}`, []any{secret})
	if err != nil {
		return err
	}
	if !gjson.GetBytes(res, "result.value").Bool() {
		return errors.New("the field does not take text")
	}
	return nil
}

// maxListedOptions is how many options a refused select lists.
const maxListedOptions = 10

// optionList says which options a select has, the first few by name, for a
// choice that named none of them.
func optionList(opts []gjson.Result) string {
	if len(opts) == 0 {
		return "the control has no options"
	}
	var names []string
	for _, o := range opts[:min(len(opts), maxListedOptions)] {
		names = append(names, strconv.Quote(o.String()))
	}
	s := "the options are " + strings.Join(names, ", ")
	if more := len(opts) - maxListedOptions; more > 0 {
		s += fmt.Sprintf(" and %d more", more)
	}
	return s
}

// AnswerDialog sets how the next confirm or prompt the page opens is
// answered; a typed answer waits for a prompt. It is used once: the dialog
// after it is accepted as before. The zero answer clears one not yet used.
func (p *Page) AnswerDialog(a engine.DialogAnswer) {
	p.mu.Lock()
	p.answer = a
	p.mu.Unlock()
}

// tunnelCause is why the egress proxy could not connect, for a navigation
// Chrome reports as ERR_TUNNEL_CONNECTION_FAILED: Chrome gives that one code
// whatever the cause, refused by policy, timed out or unreachable. The
// connection is the one to the host of target, or of the address a redirect
// took the navigation to. It is empty when there is no proxy, or no such
// connection failed since the navigation started: a failure elsewhere, such
// as a tracker on the page being left, is not this one.
func (p *Page) tunnelCause(target, errorText string, since time.Time) string {
	if errorText != "net::ERR_TUNNEL_CONNECTION_FAILED" || p.egress == nil {
		return ""
	}
	p.mu.Lock()
	where := []string{target, p.unreachable}
	p.mu.Unlock()
	for _, u := range where {
		if cause := p.connectionTo(u, since); cause != "" {
			return cause
		}
	}
	return ""
}

// connectionTo is why the connection to the host of address could not be
// made since since, or empty.
func (p *Page) connectionTo(address string, since time.Time) string {
	u, err := neturl.Parse(address)
	if err != nil || u.Host == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	f, ok := p.egress.FailureTo(net.JoinHostPort(u.Hostname(), port))
	if !ok || f.At.Before(since) {
		return ""
	}
	if f.Refused {
		return fmt.Sprintf(": the connection to %s was refused: %v", f.Target, f.Err)
	}
	return fmt.Sprintf(": the connection to %s failed: %v", f.Target, f.Err)
}

// Archive is the page as one MHTML document: its markup with the styles,
// images and frames it shows, as the browser holds them now. Nothing is
// fetched again.
func (p *Page) Archive(ctx context.Context) (string, error) {
	res, err := p.call(ctx, "Page.captureSnapshot", map[string]any{"format": "mhtml"})
	if err != nil {
		return "", err
	}
	return gjson.GetBytes(res, "data").String(), nil
}

// challengeGrace is how long a refused document gets to replace itself, as an
// automatic browser check does once it is satisfied.
var challengeGrace = 8 * time.Second

// documentRefused reports that the top document's latest answer was a refusal
// that a browser check gives: 403, 429 or 503.
func (p *Page) documentRefused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.byID[p.topDocument]
	return r != nil && (r.Status == 403 || r.Status == 429 || r.Status == 503)
}

// awaitCommit waits, for at most d, for the top frame to commit a new
// document, then for it to load. It reports whether one came.
func (p *Page) awaitCommit(ctx context.Context, d time.Duration) bool {
	seen := p.commitCount()
	deadline := p.clock().Add(d)
	for p.commitCount() == seen && p.clock().Before(deadline) {
		if p.pause(ctx, settlePoll) != nil {
			return false
		}
	}
	if p.commitCount() == seen {
		return false
	}
	p.awaitLoadingDone(ctx)
	return true
}

// abandonTimeout bounds the call that stops a failed navigation.
const abandonTimeout = 5 * time.Second

// abandon stops a navigation that failed, such as one to a site that never
// answers: left going, it keeps the tab busy and may land later, after the
// agent was told it failed. It runs on a failure already being reported, so a
// browser too far gone to answer adds nothing to say.
func (p *Page) abandon(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abandonTimeout)
	defer cancel()
	_, _ = p.call(ctx, "Page.stopLoading", nil)
}
