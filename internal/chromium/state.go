package chromium

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"slices"
	"time"

	json "github.com/goccy/go-json"
	"github.com/noetive/riffle/internal/engine"
	"github.com/tidwall/gjson"
)

// WithState starts the browser with the cookies and storage of an earlier
// one. They are put in place before the page exists, so nothing the page
// runs sees the browser without them.
func WithState(st engine.State) Option {
	return func(o *options) { o.state = st }
}

// State is the browser's cookies and the localStorage of the top page's
// site. Storage of a site is readable only while a page of it is open, so a
// site left behind is not in it. A frame of another site has storage of its
// own, kept apart for each site it is embedded in, and is left out.
func (p *Page) State(ctx context.Context) (engine.State, error) {
	res, err := p.call(ctx, "Storage.getCookies", map[string]any{})
	if err != nil {
		return engine.State{}, err
	}
	var st engine.State
	for _, c := range gjson.GetBytes(res, "cookies").Array() {
		st.Cookies = append(st.Cookies, json.RawMessage(c.Raw))
	}
	// The cookies are kept whatever happens to the storage read: a page that
	// moved on between the two reads has its storage read on the next save.
	tree, err := p.call(ctx, "Page.getFrameTree", nil)
	if err != nil {
		return st, nil
	}
	origin, ok := webOrigin(gjson.GetBytes(tree, "frameTree.frame.securityOrigin").String())
	if !ok {
		return st, nil
	}
	res, err = p.call(ctx, "DOMStorage.getDOMStorageItems", storageOf(origin))
	if err != nil {
		return st, nil
	}
	// An empty list is kept too: a site that cleared its storage, as on
	// signing out, stays cleared.
	items := [][2]string{}
	for _, e := range gjson.GetBytes(res, "entries").Array() {
		items = append(items, [2]string{e.Get("0").String(), e.Get("1").String()})
	}
	st.Storage = map[string][][2]string{origin: items}
	return st, nil
}

// webOrigin is the origin of an http or https address as scheme://host[:port],
// or false for any other: an opaque or local origin has no storage to keep.
func webOrigin(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return u.Scheme + "://" + u.Host, true
}

func storageOf(origin string) map[string]any {
	return map[string]any{"storageId": map[string]any{"securityOrigin": origin, "isLocalStorage": true}}
}

// restore puts the cookies and storage in place. A cookie the browser will not
// take back is left out: it would be refused again on every start. A site
// whose storage cannot be put back is skipped and named in what it returns,
// so one site never keeps the browser from starting. Cookies go in last: a
// request the browser makes of its own while storage is put back, outside the
// tab's interception, carries none of them.
func (b *Browser) restore(ctx context.Context, st engine.State) ([]string, error) {
	var skipped []string
	if len(st.Storage) > 0 {
		var err error
		if skipped, err = b.restoreStorage(ctx, st.Storage); err != nil {
			return nil, err
		}
	}
	if len(st.Cookies) > 0 {
		if _, err := b.conn.call(ctx, "", "Storage.setCookies", map[string]any{"cookies": st.Cookies}); err != nil {
			for _, c := range st.Cookies {
				_, _ = b.conn.call(ctx, "", "Storage.setCookies", map[string]any{"cookies": []json.RawMessage{c}})
			}
		}
	}
	return skipped, nil
}

// restoreStorage writes each site's localStorage from a background tab that
// visits the site. Storage is writable only while a page of the site is
// open, so the tab opens one, but every request it makes is answered here
// with blankPage: nothing reaches the site.
func (b *Browser) restoreStorage(ctx context.Context, storage map[string][][2]string) (skipped []string, err error) {
	res, err := b.conn.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank", "background": true})
	if err != nil {
		return nil, err
	}
	target := gjson.GetBytes(res, "targetId").String()
	defer func() {
		_, _ = b.conn.call(context.WithoutCancel(ctx), "", "Target.closeTarget", map[string]any{"targetId": target})
	}()
	sub := b.conn.subscribe()
	defer b.conn.unsubscribe(sub)
	res, err = b.conn.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target, "flatten": true})
	if err != nil {
		return nil, err
	}
	tab := gjson.GetBytes(res, "sessionId").String()
	for _, m := range []string{"Page.enable", "DOMStorage.enable"} {
		if _, err := b.conn.call(ctx, tab, m, nil); err != nil {
			return nil, err
		}
	}
	if _, err := b.conn.call(ctx, tab, "Fetch.enable", fetchAll); err != nil {
		return nil, err
	}
	origins := make([]string, 0, len(storage))
	for o := range storage {
		origins = append(origins, o)
	}
	slices.Sort(origins)
	for _, origin := range origins {
		if _, ok := webOrigin(origin); !ok {
			continue
		}
		if err := b.restoreSite(ctx, tab, sub, origin, storage[origin]); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			skipped = append(skipped, fmt.Sprintf("%s: its kept storage could not be restored and stays kept: %v", origin, err))
		}
	}
	return skipped, nil
}

func (b *Browser) restoreSite(ctx context.Context, tab string, sub <-chan event, origin string, items [][2]string) error {
	if err := b.visitBlank(ctx, tab, sub, origin); err != nil {
		return err
	}
	for _, kv := range items {
		params := storageOf(origin)
		params["key"], params["value"] = kv[0], kv[1]
		if _, err := b.conn.call(ctx, tab, "DOMStorage.setDOMStorageItem", params); err != nil {
			return err
		}
	}
	return nil
}

// blankPage answers a restore visit. Its icon is inline: a page without one
// has the browser ask the site for /favicon.ico, a request that does not always
// pass through the tab's interception and would reach the site with the kept
// cookies.
var blankPage = base64.StdEncoding.EncodeToString([]byte(`<!doctype html><link rel="icon" href="data:,">`))

// restoreVisit bounds one blank visit: it is answered here, so it is quick.
const restoreVisit = 10 * time.Second

// visitBlank opens origin in the tab and returns once its page has loaded.
// A request to the origin is answered with blankPage; any other is
// refused, so a browser that would upgrade http to https falls back to the
// origin asked for, and nothing else is visited. A request that ended before
// it was answered, such as an earlier visit's, needs no answer.
func (b *Browser) visitBlank(ctx context.Context, tab string, sub <-chan event, origin string) error {
	ctx, cancel := context.WithTimeout(ctx, restoreVisit)
	defer cancel()
	navigated := make(chan error, 1)
	// Navigation answers only once its request is, which happens below.
	go func() {
		res, err := b.conn.call(ctx, tab, "Page.navigate", map[string]any{"url": origin + "/"})
		if failed := gjson.GetBytes(res, "errorText").String(); err == nil && failed != "" {
			err = fmt.Errorf("navigate: %s", failed)
		}
		navigated <- err
	}()
	for {
		select {
		case ev := <-sub:
			if ev.Session != tab {
				continue
			}
			switch ev.Method {
			case "Fetch.requestPaused":
				id := ev.Params.Get("requestId").String()
				if o, ok := webOrigin(ev.Params.Get("request.url").String()); ok && o == origin {
					_, _ = b.conn.call(ctx, tab, "Fetch.fulfillRequest", map[string]any{
						"requestId": id, "responseCode": 200, "body": blankPage,
						"responseHeaders": []map[string]string{{"name": "Content-Type", "value": "text/html"}},
					})
				} else {
					_, _ = b.conn.call(ctx, tab, "Fetch.failRequest", map[string]any{"requestId": id, "errorReason": "ConnectionRefused"})
				}
			case "Page.loadEventFired":
				if navigated != nil {
					if err := <-navigated; err != nil {
						return err
					}
				}
				return b.landedOn(ctx, tab, origin)
			}
		case err := <-navigated:
			if err != nil {
				return err
			}
			navigated = nil // answered; the load is still to come
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// landedOn checks the tab's page is of origin, not one it was sent on to.
func (b *Browser) landedOn(ctx context.Context, tab, origin string) error {
	tree, err := b.conn.call(ctx, tab, "Page.getFrameTree", nil)
	if err != nil {
		return err
	}
	if got := gjson.GetBytes(tree, "frameTree.frame.securityOrigin").String(); got != origin {
		return fmt.Errorf("the page opened as %s", got)
	}
	return nil
}
