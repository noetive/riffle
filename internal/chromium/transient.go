package chromium

import (
	"context"
	"strings"

	"github.com/noetive/riffle/internal/engine"
	"github.com/tidwall/gjson"
)

// transientScript records elements that appear and vanish within one settle
// window (toasts, snackbars, flash messages). A window ends when the host
// drains: elements still attached then were seen, so they stop being
// candidates and a later removal is an ordinary removal. State lives under a
// non-enumerable property so page scripts do not trip over it.
const transientScript = `(() => {
  const key = '__rf_transient_7c1e';
  if (Object.prototype.hasOwnProperty.call(window, key)) return;
  // changed is when the page last added visible content: what a settle
  // waits to stop. Live regions, tickers and timers change by design and are
  // not waited for.
  const state = { candidates: new Set(), seen: [], changed: performance.now() };
  const noisy = (n) => n.closest && n.closest('[aria-live]:not([aria-live="off"]),[role=marquee],[role=timer],[role=progressbar],[role=status],[role=log]');
  Object.defineProperty(window, key, { value: state, enumerable: false });
  const clean = (s) => (s || '').replace(/\s+/g, ' ').trim().slice(0, 200);
  // Elements that are never rendered (HTML, Rendering: display none): no
  // message a person saw, and no part of one.
  // noscript is not rendered while scripts run, as they do here; a dialog
  // only once it is open.
  const unrendered = new Set(['area', 'base', 'basefont', 'datalist', 'head', 'link', 'meta',
    'noembed', 'noframes', 'noscript', 'param', 'rp', 'script', 'style', 'template', 'title']);
  const shown = (n) => !unrendered.has(n.localName) && !n.hasAttribute('hidden') &&
    !(n.localName === 'dialog' && !n.hasAttribute('open'));
  const text = (n) => {
    if (n.nodeType === 3) return n.data;
    if (n.nodeType !== 1 || !shown(n)) return '';
    let s = '';
    for (const c of n.childNodes) s += text(c);
    return s;
  };
  const process = (records) => {
    for (const r of records) {
      for (const n of r.addedNodes) {
        if (n.nodeType !== 1 || !n.isConnected || !shown(n)) continue;
        if (!noisy(n)) state.changed = performance.now();
        let inside = false;
        for (const c of state.candidates) { if (c.contains(n)) { inside = true; break; } }
        if (!inside) state.candidates.add(n);
      }
      for (const n of r.removedNodes) {
        if (n.nodeType !== 1) continue;
        for (const c of Array.from(state.candidates)) {
          if (!n.contains(c) || c.isConnected) continue;
          state.candidates.delete(c);
          const t = clean(text(c));
          if (t && state.seen.indexOf(t) < 0) state.seen.push(t);
        }
      }
    }
  };
  const observer = new MutationObserver(process);
  observer.observe(document, { childList: true, subtree: true });
  state.flush = () => {
    process(observer.takeRecords());
    const out = state.seen;
    state.seen = [];
    state.candidates.clear();
    return JSON.stringify(out);
  };
})()`

const transientDrain = `(() => {
  const s = window['__rf_transient_7c1e'];
  return s ? s.flush() : '[]';
})()`

// installTransients registers the observer for every future document and
// for the current one.
func (p *Page) installTransients(ctx context.Context) error {
	if _, err := p.call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": transientScript}); err != nil {
		return err
	}
	_, err := p.call(ctx, "Runtime.evaluate", map[string]any{"expression": transientScript})
	return err
}

// drainTransients collects nodes that came and went since the previous drain
// and reports them as toasts when report is set; otherwise it only starts a
// new window.
// A context that is gone (navigated away, no document) is tolerated; any
// other failure propagates.
func (p *Page) drainTransients(ctx context.Context, report bool) error {
	res, err := p.call(ctx, "Runtime.evaluate", map[string]any{
		"expression": transientDrain, "returnByValue": true,
	})
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "Cannot find context") || strings.Contains(msg, "context was destroyed") {
			return nil
		}
		return err
	}
	if gjson.GetBytes(res, "exceptionDetails").Exists() {
		return nil
	}
	gjson.Parse(gjson.GetBytes(res, "result.value").String()).ForEach(func(_, v gjson.Result) bool {
		if report {
			p.note(engine.Toast, v.String())
		}
		return true
	})
	return nil
}

// transientStill is how many milliseconds ago the page last added visible
// content, by the same observer that records toasts; a page without the
// observer yet, as one still loading, has just changed.
const transientStill = `(() => {
  const s = window['__rf_transient_7c1e'];
  return s ? performance.now() - s.changed : 0;
})()`
