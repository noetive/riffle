//go:build integration

package chromium_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/chromium"
)

// target is a server on [::1] that counts each way a page reached it.
type target struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits map[string]int
	udp  int // port of the UDP listener
}

func newTarget(t *testing.T) *target {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	}
	tg := &target{hits: map[string]int{}}
	tg.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tg.mu.Lock()
		tg.hits[strings.TrimPrefix(r.URL.Path, "/")]++
		tg.mu.Unlock()
		_, _ = w.Write([]byte("internal"))
	}))
	tg.srv.Listener = ln
	tg.srv.Start()
	t.Cleanup(tg.srv.Close)
	// WebRTC reaches out over UDP, which no HTTP proxy carries.
	pc, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback UDP: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	tg.udp = pc.LocalAddr().(*net.UDPAddr).Port
	go func() {
		buf := make([]byte, 2048)
		for {
			if _, _, err := pc.ReadFrom(buf); err != nil {
				return
			}
			tg.mu.Lock()
			tg.hits["webrtc"]++
			tg.mu.Unlock()
		}
	}()
	return tg
}

func (tg *target) reached() []string {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	var out []string
	for k := range tg.hits {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// escapePage tries every route a page has to another host: a subresource,
// a fetch, a cross-site frame, a WebSocket, and a link opening a new window.
func escapePage(t *testing.T, to string, udp int) *httptest.Server {
	t.Helper()
	page := fmt.Sprintf(`<!doctype html><body style="margin:0">
<a id="pop" href="%[1]s/popup" target="_blank" style="position:absolute;left:0;top:0;width:200px;height:50px;display:block">open</a>
<img src="%[1]s/img">
<iframe src="%[1]s/frame"></iframe>
<script>
fetch('%[1]s/fetch', {mode: 'no-cors'}).catch(function(){});
try { new WebSocket('%[2]s/ws'); } catch (e) {}
// Chrome now and then starts gathering candidates and never finds one;
// a fresh connection does, so try again until one turns up.
var gathered = false;
function rtc(left) {
  try {
    var pc = new RTCPeerConnection({iceServers: [{urls: 'stun:[::1]:%[3]d'}, {urls: 'turn:[::1]:%[3]d?transport=udp', username: 'u', credential: 'p'}]});
    pc.onicecandidate = function (e) { if (e.candidate) gathered = true; };
    pc.createDataChannel('x');
    pc.createOffer().then(function (o) { return pc.setLocalDescription(o); }).catch(function(){});
  } catch (e) {}
  if (left > 0) setTimeout(function () { if (!gathered) rtc(left - 1); }, 500);
}
rtc(20);
</script></body>`, to, strings.Replace(to, "http://", "ws://", 1), udp)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func tryEscapes(t *testing.T, opts ...chromium.Option) []string {
	t.Helper()
	tg := newTarget(t)
	srv := escapePage(t, tg.srv.URL, tg.udp)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p, err := chromium.Open(ctx, 1280, 800, opts...)
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
	// Frames, sockets, windows and WebRTC connect on their own time, and
	// WebRTC only once the page clock has run for a while.
	for range 5 {
		_ = p.Settle(ctx)
		time.Sleep(time.Second)
	}
	return tg.reached()
}

func TestTheDialGuardStopsEveryRouteOut(t *testing.T) {
	guard := func(ip net.IP) error {
		if ip.Equal(net.IPv6loopback) {
			return errors.New("private")
		}
		return nil
	}
	if got := tryEscapes(t, chromium.WithDialGuard(guard)); len(got) != 0 {
		t.Errorf("reached the refused host by %v", got)
	}
}

func TestWithoutAGuardEveryRouteIsTried(t *testing.T) {
	got := tryEscapes(t)
	for _, want := range []string{"img", "fetch", "frame", "ws", "webrtc"} {
		found := false
		for _, g := range got {
			found = found || g == want
		}
		if !found {
			t.Errorf("the page never tried %s, so the other tests prove nothing about it; reached %v", want, got)
		}
	}
	t.Logf("reached with no guard: %v", got)
}

// Chrome reports every failed tunnel as ERR_TUNNEL_CONNECTION_FAILED; the
// navigation error names where the connection went and why it failed.
func TestANavigationThroughTheGuardThatCannotConnectSaysWhy(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // nothing listens there now
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p, err := chromium.Open(ctx, 1280, 800, chromium.WithDialGuard(func(net.IP) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	err = p.Load(ctx, "https://"+addr+"/")
	if err == nil {
		t.Fatal("a navigation to a closed port must fail")
	}
	for _, want := range []string{"ERR_TUNNEL_CONNECTION_FAILED", addr, "refused"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}
