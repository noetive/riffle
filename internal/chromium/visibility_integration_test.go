//go:build integration

package chromium_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/chromium"
)

// A page on the real clock is only as timely as its timers: a tab the browser
// took for hidden would run them once a second, or once a minute, and short
// ones would miss the settle.
func TestAPageIsVisibleAndItsTimersRunAtFullRate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>ticks</title><script>window.ticks = 0; setInterval(() => window.ticks++, 50)</script>`))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p, err := chromium.Open(ctx, 1280, 800)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Load(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	if v, err := p.Eval(ctx, "document.visibilityState"); err != nil || v != "visible" {
		t.Fatalf("visibilityState = %q, %v; want visible", v, err)
	}
	before := ticks(ctx, t, p)
	if err := p.Advance(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	if n := ticks(ctx, t, p) - before; n < 10 {
		t.Errorf("a 50 ms interval ticked %d times in over a second; the page's timers are throttled", n)
	}
}

func ticks(ctx context.Context, t *testing.T, p *chromium.Page) int {
	t.Helper()
	v, err := p.Eval(ctx, "String(window.ticks)")
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
