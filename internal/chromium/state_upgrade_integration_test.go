//go:build integration

package chromium

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/engine"
	"github.com/tidwall/gjson"
)

// readStorage reads origin's storage the way it was written: from a tab that
// visits it without reaching it.
func readStorage(ctx context.Context, t *testing.T, b *Browser, origin string) string {
	t.Helper()
	res, err := b.conn.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank", "background": true})
	if err != nil {
		t.Fatal(err)
	}
	res, err = b.conn.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": gjson.GetBytes(res, "targetId").String(), "flatten": true})
	if err != nil {
		t.Fatal(err)
	}
	tab := gjson.GetBytes(res, "sessionId").String()
	sub := b.conn.subscribe()
	defer b.conn.unsubscribe(sub)
	for _, m := range []string{"Page.enable", "DOMStorage.enable"} {
		if _, err := b.conn.call(ctx, tab, m, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.conn.call(ctx, tab, "Fetch.enable", fetchAll); err != nil {
		t.Fatal(err)
	}
	if err := b.visitBlank(ctx, tab, sub, origin); err != nil {
		t.Fatal(err)
	}
	items, err := b.conn.call(ctx, tab, "DOMStorage.getDOMStorageItems", storageOf(origin))
	if err != nil {
		t.Fatal(err)
	}
	return gjson.GetBytes(items, "entries").Raw
}

// A browser upgrades http to https for most names on its own; a kept http
// site's storage must still go back to the http site, and the browser start.
func TestStorageOfAPlainHTTPSiteIsRestoredDespiteTheUpgradeToHTTPS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	st := engine.State{Storage: map[string][][2]string{
		"http://a.example.com":  {{"token", "plain"}},
		"https://b.example.com": {{"token", "secure"}},
	}}
	p, err := Open(ctx, 800, 600, WithState(st))
	if err != nil {
		t.Fatalf("the browser starts: %v", err)
	}
	defer p.Close()
	for _, e := range p.Drain() {
		if e.Kind == engine.Unrestored {
			t.Errorf("every site was restored: %s", e.Text)
		}
	}
	if got := readStorage(ctx, t, p.browser, "http://a.example.com"); !strings.Contains(got, `"plain"`) {
		t.Errorf("the http site has its storage: %s", got)
	}
	if got := readStorage(ctx, t, p.browser, "https://b.example.com"); !strings.Contains(got, `"secure"`) {
		t.Errorf("the https site has its storage: %s", got)
	}
}

// A site whose storage cannot go back does not keep the browser from
// starting; the agent is told.
func TestASiteThatCannotBeRestoredIsReportedAndTheBrowserStarts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	st := engine.State{Storage: map[string][][2]string{
		"http://A.example.test": {{"k", "v"}},
		"http://b.example.test": {{"k", "v"}},
	}}
	p, err := Open(ctx, 800, 600, WithState(st))
	if err != nil {
		t.Fatalf("the browser starts: %v", err)
	}
	defer p.Close()
	var told []string
	for _, e := range p.Drain() {
		if e.Kind == engine.Unrestored {
			told = append(told, e.Text)
		}
	}
	if len(told) != 1 || !strings.HasPrefix(told[0], "http://A.example.test:") {
		t.Errorf("the one site that failed is named: %v", told)
	}
	if got := readStorage(ctx, t, p.browser, "http://b.example.test"); !strings.Contains(got, `"v"`) {
		t.Errorf("the other site is restored: %s", got)
	}
}
