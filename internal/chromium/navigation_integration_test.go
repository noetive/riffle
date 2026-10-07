//go:build integration

package chromium_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/chromium"
	"github.com/noetive/riffle/internal/engine"
)

// heldFetch outlasts the longest a settle waits on a pending request.
var heldFetch = chromium.SettleCap + time.Second

// spinnerPage shows an icon in a web font only when its button starts a slow
// fetch, so the font loads while the settle is still waiting on that fetch.
const spinnerPage = `<!doctype html><title>Spinner</title>
<style>@font-face{font-family:Icons;src:url('/icons.woff2') format('woff2')}
#spin{font-family:Icons;display:none}
#go{position:fixed;left:0;top:0;width:200px;height:50px}</style>
<button id="go" onclick="document.getElementById('spin').style.display='inline';fetch('/slow').then(r=>r.text()).then(t=>{document.getElementById('out').textContent=t})">Load</button>
<i id="spin">&#xf110;</i><p id="out"></p>`

func TestANavigationAfterAFontLoadedBehindAHeldFetchStillLoads(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(spinnerPage)) })
	mux.HandleFunc("/icons.woff2", func(w http.ResponseWriter, r *http.Request) {
		// Not a valid font: the stall needs the font request, not the glyphs.
		_, _ = w.Write(bytes.Repeat([]byte{0x5a}, 2048))
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(heldFetch):
			_, _ = w.Write([]byte("arrived"))
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/next", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><title>Next</title><h1>Next page</h1>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	p, err := chromium.Open(ctx, 1280, 800)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if err := p.Load(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	if err := p.Click(ctx, 100, 25); err != nil {
		t.Fatal(err)
	}
	if err := p.Settle(ctx); err != nil {
		t.Fatal(err)
	}
	// Let the held fetch answer so nothing of the first page is still open.
	time.Sleep(heldFetch)
	if err := p.Settle(ctx); err != nil {
		t.Fatal(err)
	}
	p.Drain()

	began := time.Now()
	if err := p.Load(ctx, srv.URL+"/next"); err != nil {
		t.Fatal(err)
	}
	took := time.Since(began)
	for _, ev := range p.Drain() {
		if ev.Kind == engine.SlowLoad {
			t.Errorf("the next page did not load: %s", ev.Text)
		}
	}
	s, err := p.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(s); !strings.Contains(got, "Next page") {
		t.Errorf("the next page is empty after %s: %s", took, got)
	}
}

// A navigation to a site that never answers fails and is stopped, so it cannot
// land later over the page the agent was left on; that page keeps its real
// clock and its own address.
func TestAFailedNavigationIsStoppedAndThePageKeepsRealTime(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><title>Start</title><h1>Start</h1>`))
	}))
	defer srv.Close()
	// Accepts connections and never answers them.
	silent, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	held := make(chan net.Conn, 16)
	go func() {
		for {
			c, err := silent.Accept()
			if err != nil {
				close(held)
				return
			}
			held <- c
		}
	}()
	defer func() {
		_ = silent.Close()
		for c := range held {
			_ = c.Close()
		}
	}()

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
	restore := chromium.SetCallTimeout(2 * time.Second)
	err = p.Load(ctx, "http://"+silent.Addr().String()+"/")
	restore()
	if err == nil {
		t.Fatal("a site that never answered loaded")
	}
	// Long enough for a navigation left going to commit.
	time.Sleep(500 * time.Millisecond)
	now, err := p.Eval(ctx, `Date.now()`)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := strconv.ParseFloat(strings.TrimSpace(now), 64)
	if err != nil {
		t.Fatalf("page clock %q: %v", now, err)
	}
	if skew := time.Since(time.UnixMilli(int64(ms))); skew > 5*time.Second || skew < -5*time.Second {
		t.Errorf("the page clock is %s away from real time", skew)
	}
	href, err := p.Eval(ctx, `location.href`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(href, silent.Addr().String()) {
		t.Errorf("the failed navigation landed later: page is at %s", href)
	}
}
