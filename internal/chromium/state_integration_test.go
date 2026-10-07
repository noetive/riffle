//go:build integration

package chromium_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/chromium"
	"github.com/noetive/riffle/internal/engine"
	"github.com/tidwall/gjson"
)

// A site that signs in with an HttpOnly session cookie, a script cookie that
// expires later, and a token in localStorage.
const signedIn = `<!doctype html><title>signed in</title><script>
document.cookie = "pref=dark; path=/; max-age=3600";
localStorage.setItem("token", "t-1");
</script>`

func TestABrowserStartedWithTheStateOfAnotherIsSignedInWithoutAskingTheSite(t *testing.T) {
	var hits atomic.Int32
	var sent atomic.Value
	sent.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		sent.Store(r.Header.Get("Cookie"))
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "s-1", Path: "/", HttpOnly: true})
		_, _ = w.Write([]byte(signedIn))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	allow := func(string, string) error { return nil }

	first, err := chromium.Open(ctx, 800, 600, chromium.WithRequestFilter(allow))
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Load(ctx, srv.URL+"/"); err != nil {
		t.Fatal(err)
	}
	st, err := first.State(ctx)
	first.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Storage[srv.URL]; len(got) != 1 || got[0] != [2]string{"token", "t-1"} {
		t.Fatalf("the site's storage is kept: %v", st.Storage)
	}
	names := map[string]bool{}
	for _, c := range st.Cookies {
		names[gjson.GetBytes(c, "name").String()] = true
	}
	if !names["sid"] || !names["pref"] {
		t.Fatalf("both the HttpOnly session cookie and the script cookie are kept: %v", names)
	}

	before := hits.Load()
	second, err := chromium.Open(ctx, 800, 600, chromium.WithRequestFilter(allow), chromium.WithState(st))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if n := hits.Load() - before; n != 0 {
		t.Errorf("restoring asked the site %d times; it must not ask at all", n)
	}
	if err := second.Load(ctx, srv.URL+"/"); err != nil {
		t.Fatal(err)
	}
	if c := sent.Load().(string); !strings.Contains(c, "sid=s-1") || !strings.Contains(c, "pref=dark") {
		t.Errorf("the site is sent the kept cookies: %q", c)
	}
	v, err := second.Eval(ctx, "localStorage.getItem('token')")
	if err != nil || v != "t-1" {
		t.Errorf("the page finds its kept storage: %v %v", v, err)
	}
}

func TestASiteThatClearedItsStorageIsKeptCleared(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><title>signed out</title><script>localStorage.clear()</script>`))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p, err := chromium.Open(ctx, 800, 600, chromium.WithState(engine.State{Storage: map[string][][2]string{srv.URL: {{"token", "old"}}}}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Load(ctx, srv.URL+"/"); err != nil {
		t.Fatal(err)
	}
	st, err := p.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := st.Storage[srv.URL]; !ok || len(got) != 0 {
		t.Errorf("an emptied storage is reported as empty, not left out: %v %v", got, ok)
	}
}

func TestAPageWithNoSiteHasNoStorageToKeep(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p, err := chromium.Open(ctx, 800, 600)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	st, err := p.State(ctx)
	if err != nil || len(st.Storage) != 0 {
		t.Errorf("a blank page keeps no storage: %v %v", st.Storage, err)
	}
}

// Restoring many times over never leaves the connection stalled by a
// listener nobody reads.
func TestRestoringOverAndOverNeverStallsTheBrowser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	storage := map[string][][2]string{}
	for _, o := range []string{"http://a.example.test", "http://b.example.test", "https://c.example.test:8443"} {
		storage[o] = [][2]string{{"k", o}}
	}
	for range 3 {
		p, err := chromium.Open(ctx, 800, 600, chromium.WithState(engine.State{Storage: storage}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Eval(ctx, "1"); err != nil {
			t.Fatal(err)
		}
		p.Close()
	}
}
