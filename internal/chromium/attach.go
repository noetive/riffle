package chromium

import (
	"context"
	"fmt"
	"strings"

	"github.com/noetive/riffle/internal/engine"
)

// Each attach pauses a new frame or worker until it is adopted. pageAttach,
// for a page or frame, leaves service workers to the browser-level
// browserAttach, because they belong to no page and a worker attached twice
// never starts.
var (
	pageAttach = map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true,
		"filter": []map[string]any{{"type": "service_worker", "exclude": true}, {}}}
	browserAttach = map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true,
		"filter": []map[string]any{{"type": "service_worker"}, {"type": "shared_worker"}}}
)

// adopt holds every frame and worker the page starts to the page's own rules
// before it runs: the request filter and the user agent. Frames and workers
// out of the page's process make their own requests, which the page's filter
// never sees. The page must be the only one in its browser: every target that
// attaches to the connection is adopted.
func (p *Page) adopt(ctx context.Context) error {
	go p.adoptAttached(p.conn.subscribe())
	if _, err := p.call(ctx, "Target.setAutoAttach", pageAttach); err != nil {
		return err
	}
	_, err := p.conn.call(ctx, "", "Target.setAutoAttach", browserAttach)
	return err
}

// adoptAttached adopts each frame or worker as it attaches and answers the
// requests they pause. Each runs in its own goroutine: a call made from here
// would wait on the reader that feeds this subscription.
func (p *Page) adoptAttached(sub <-chan event) {
	for {
		select {
		case ev := <-sub:
			switch {
			case ev.Method == "Target.attachedToTarget" && ev.Params.Get("waitingForDebugger").Bool():
				go p.release(ev.Params.Get("sessionId").String(), ev.Params.Get("targetInfo.type").String(), ev.Params.Get("targetInfo.url").String())
			case ev.Method == "Fetch.requestPaused" && ev.Session != p.session:
				go p.decide(p.life, ev)
			}
		case <-p.conn.closed:
			return
		}
	}
}

// release applies the page's rules to one paused target and resumes it. A
// target that refuses the filter stays paused: running it would let its
// requests past the filter. A dedicated worker has no Fetch domain of its
// own, yet is not past the filter: its requests are paused by the
// interception of the page or worker that started it, so it runs. Chrome's
// own extensions are not the page's and run as they are. A target that
// refuses the user agent stays paused too: it would run as stock headless
// Chrome without the Riffle token. A worker without the Target domain makes
// no children, so only a frame is held back for refusing to hold its own
// children.
func (p *Page) release(session, kind, url string) {
	p.mu.Lock()
	filtered := p.filter != nil
	p.mu.Unlock()
	if !strings.HasPrefix(url, "chrome-extension://") {
		if filtered {
			if _, err := p.conn.call(p.life, session, "Fetch.enable", fetchAll); err != nil && (kind != "worker" || !unsupported(err)) {
				if gone(err) {
					return // closed or navigated away first: nothing was held back
				}
				p.note(engine.BlockedFetch, fmt.Sprintf("%s: not started, its requests could not be checked: %s", url, err))
				return
			}
		}
		if p.override != nil {
			if _, err := p.conn.call(p.life, session, "Network.setUserAgentOverride", p.override); err != nil {
				if gone(err) {
					return // closed or navigated away first: nothing was held back
				}
				p.note(engine.BlockedFetch, fmt.Sprintf("%s: not started, it could not be given Riffle's identity: %s", url, err))
				return
			}
		}
		// A frame starts frames and workers of its own; one that will not hold
		// them for adoption would let them run unfiltered.
		if _, err := p.conn.call(p.life, session, "Target.setAutoAttach", pageAttach); err != nil && filtered && kind == "iframe" {
			p.note(engine.BlockedFetch, fmt.Sprintf("%s: not started, its frames and workers could not be checked: %s", url, err))
			return
		}
	}
	_, _ = p.conn.call(p.life, session, "Runtime.runIfWaitingForDebugger", nil)
}

// unsupported reports a target that offers no Fetch domain at all, as opposed
// to one that refused it.
func unsupported(err error) bool { return strings.Contains(err.Error(), "'Fetch.enable' wasn't found") }

// gone reports a target that closed, or whose document was replaced, before
// a command reached it.
func gone(err error) bool {
	m := err.Error()
	return strings.Contains(m, "Session with given id not found") || strings.Contains(m, "Inspected target navigated or closed")
}
