//go:build integration

package chromium_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tidwall/gjson"

	"github.com/noetive/riffle/internal/chromium"
)

// identityReport is what a script reports about the identity it runs under.
const identityReport = `JSON.stringify({ua: navigator.userAgent, brands: navigator.userAgentData ? navigator.userAgentData.brands.map(b => b.brand) : null})`

func TestWorkersAndOutOfProcessFramesPresentRiffleIdentity(t *testing.T) {
	var mu sync.Mutex
	reports := map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/report", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reports[r.URL.Query().Get("who")] = r.URL.Query().Get("what")
		mu.Unlock()
	})
	mux.HandleFunc("/frame", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<script>fetch('http://' + location.host + '/report?who=frame&what=' + encodeURIComponent(` + identityReport + `))</script>`))
	})
	mux.HandleFunc("/worker.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = w.Write([]byte(`postMessage(` + identityReport + `)`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<p>probe</p>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	// Another host name is another site, so its frame runs in its own process.
	other := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p, err := chromium.Open(ctx, 1280, 800, chromium.WithProduct("Riffle/9.9.9"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Load(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Eval(ctx, `window.workerSaid = null; const w = new Worker("/worker.js"); w.onmessage = e => { window.workerSaid = e.data }; const f = document.createElement("iframe"); f.src = `+"`"+other+"/frame`"+`; document.body.append(f)`); err != nil {
		t.Fatal(err)
	}
	var worker string
	for i := 0; i < 20 && (worker == "" || worker == "null"); i++ {
		if err := p.Advance(ctx, 500*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		if worker, err = p.Eval(ctx, `window.workerSaid`); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	var frame string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && frame == "" {
		mu.Lock()
		frame = reports["frame"]
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	for who, got := range map[string]string{"worker": worker, "frame": frame} {
		if got == "" {
			t.Errorf("%s reported nothing", who)
			continue
		}
		if strings.Contains(got, "Headless") {
			t.Errorf("%s still presents as headless: %s", who, got)
		}
		if ua := gjson.Get(got, "ua").String(); !strings.HasSuffix(ua, " Riffle/9.9.9") {
			t.Errorf("%s user agent lacks the product: %q", who, ua)
		}
	}
	if gjson.Get(worker, "brands").Type == gjson.Null {
		t.Error("the worker reports no client hints")
	}
}

// Workers run under the request filter: a dedicated worker, a worker it
// starts and a shared worker all run, and every request they make that the
// filter refuses never reaches the server.
func TestWorkersRunAndTheirRequestsPassTheFilter(t *testing.T) {
	var mu sync.Mutex
	reached := map[string]bool{}
	script := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write([]byte(body))
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/worker.js", script(`fetch('/refused-worker').catch(() => 0); importScripts('/imported.js'); new Worker('/inner.js').onmessage = e => postMessage(e.data); postMessage('worker')`))
	mux.HandleFunc("/imported.js", script(`fetch('/refused-imported').catch(() => 0)`))
	mux.HandleFunc("/inner.js", script(`fetch('/refused-inner').catch(() => 0); postMessage('inner')`))
	mux.HandleFunc("/shared.js", script(`fetch('/refused-shared').catch(() => 0); onconnect = e => e.ports[0].postMessage('shared')`))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reached[r.URL.Path] = true
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<p>probe</p>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	refuse := func(u, _ string) error {
		if strings.Contains(u, "/refused-") {
			return errors.New("refused by the test")
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p, err := chromium.Open(ctx, 1280, 800, chromium.WithRequestFilter(refuse), chromium.WithProduct("Riffle/9.9.9"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Load(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Eval(ctx, `window.said = []; new Worker('/worker.js').onmessage = e => said.push(e.data); const s = new SharedWorker('/shared.js'); s.port.onmessage = e => said.push(e.data)`); err != nil {
		t.Fatal(err)
	}
	var said string
	heardAll := func() bool {
		return strings.Contains(said, "worker") && strings.Contains(said, "inner") && strings.Contains(said, "shared")
	}
	for i := 0; i < 20 && !heardAll(); i++ {
		if err := p.Advance(ctx, 500*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		if said, err = p.Eval(ctx, `JSON.stringify(window.said)`); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, who := range []string{"worker", "inner", "shared"} {
		if !strings.Contains(said, `"`+who+`"`) {
			t.Errorf("the %s worker did not run; the page heard %s", who, said)
		}
	}
	for _, e := range p.Drain() {
		if strings.Contains(e.Text, "not started") {
			t.Errorf("a worker was held back: %s", e.Text)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for path := range reached {
		if strings.HasPrefix(path, "/refused-") {
			t.Errorf("%s reached the server past the filter", path)
		}
	}
}
