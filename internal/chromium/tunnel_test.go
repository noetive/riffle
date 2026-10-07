package chromium

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/egress"
	"github.com/noetive/riffle/internal/engine"
)

// failTo makes the proxy fail to connect to a closed port, as a navigation
// through it would, and returns that host:port.
func failTo(t *testing.T, p *egress.Proxy) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // nothing listens there now
	proxyURL, _ := url.Parse("http://" + p.Addr())
	c := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+addr+"/", nil)
	if resp, err := c.Do(req); err == nil {
		_ = resp.Body.Close()
	}
	return addr
}

func TestAFailedTunnelIsExplainedOnlyByAFailureToItsOwnHost(t *testing.T) {
	proxy, err := egress.Start(t.Context(), func(net.IP) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	p, _ := newTestPage(t)
	p.egress = proxy
	const tunnel = "net::ERR_TUNNEL_CONNECTION_FAILED"

	before := failTo(t, proxy)
	started := time.Now()
	down := failTo(t, proxy)
	failTo(t, proxy) // another host fails during the navigation too

	if got := p.tunnelCause("https://"+down+"/", tunnel, started); !strings.Contains(got, down) || !strings.Contains(got, "refused") {
		t.Errorf("the cause names where the navigation went and why: %q", got)
	}
	if got := p.tunnelCause("https://"+before+"/", tunnel, started); got != "" {
		t.Errorf("a failure from before the navigation is not its cause: %q", got)
	}
	unrelated := "https://127.0.0.1:1/"
	if got := p.tunnelCause(unrelated, tunnel, started); got != "" {
		t.Errorf("failures to other hosts are not this navigation's cause: %q", got)
	}
	if got := p.tunnelCause("https://"+down+"/", "net::ERR_CONNECTION_REFUSED", started); got != "" {
		t.Errorf("only a tunnel failure needs the proxy's cause: %q", got)
	}
}

func TestAHitTestOnAPageWithNoVisibleSizeFailsInsteadOfWaiting(t *testing.T) {
	p, f := newTestPage(t)
	f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
		if r.Method == "Page.getLayoutMetrics" {
			return `{"cssVisualViewport":{"pageX":0,"pageY":0,"clientWidth":0,"clientHeight":0}}`, "", true
		}
		return "{}", "", true
	}
	_, err := p.HitTest(testCtx(t), 10, 10)
	if err == nil || errors.Is(err, engine.ErrOffViewport) {
		t.Errorf("err = %v, want a plain failure: a page with no size is not one the target moved off", err)
	}
}

func TestAFailedTunnelIsExplainedByARefusalAndByWhereARedirectWent(t *testing.T) {
	proxy, err := egress.Start(t.Context(), func(ip net.IP) error {
		if ip.IsLoopback() {
			return errors.New("127.0.0.1 is a private address")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	p, _ := newTestPage(t)
	p.egress = proxy
	const tunnel = "net::ERR_TUNNEL_CONNECTION_FAILED"
	started := time.Now()
	refused := failTo(t, proxy) // a loopback address: the guard refuses it

	got := p.tunnelCause("https://"+refused+"/", tunnel, started)
	if !strings.Contains(got, "was refused") || !strings.Contains(got, "private address") {
		t.Errorf("a refused connection says it was refused and why: %q", got)
	}
	// The navigation began elsewhere and was sent on to the refused host.
	p.mu.Lock()
	p.unreachable = "https://" + refused + "/landing"
	p.mu.Unlock()
	if got := p.tunnelCause("https://start.example/", tunnel, started); !strings.Contains(got, refused) {
		t.Errorf("a redirect's failed connection explains the navigation: %q", got)
	}
}
