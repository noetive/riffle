//go:build integration

package chromium_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/chromium"
	"github.com/noetive/riffle/internal/engine"
	"github.com/tidwall/gjson"
)

const transientFixture = `<!doctype html><html><body>
<button id="toast" style="position:absolute;left:10px;top:10px;width:100px;height:40px"
 onclick="var d=document.createElement('div');d.textContent='Saved!';d.id='t';document.body.appendChild(d);setTimeout(function(){d.remove()},300)">toast</button>
<button id="keep" style="position:absolute;left:10px;top:70px;width:100px;height:40px"
 onclick="var d=document.createElement('div');d.textContent='Kept!';d.id='k';document.body.appendChild(d)">keep</button>
<button id="drop" style="position:absolute;left:10px;top:130px;width:100px;height:40px"
 onclick="document.getElementById('k').remove()">drop</button>
<button id="unrendered" style="position:absolute;left:10px;top:190px;width:100px;height:40px"
 onclick="var s=document.createElement('style');s.textContent='body{opacity:0}';document.head.appendChild(s);var j=document.createElement('script');j.textContent='window.x=1';document.body.appendChild(j);var n=document.createElement('noscript');n.textContent='Enable JavaScript';document.body.appendChild(n);var g=document.createElement('dialog');g.textContent='Closed dialog';document.body.appendChild(g);setTimeout(function(){s.remove();j.remove();n.remove();g.remove()},300)">unrendered</button>
<button id="mixed" style="position:absolute;left:10px;top:250px;width:100px;height:40px"
 onclick="var d=document.createElement('div');d.innerHTML='<style>.t{color:red}</style>Copied<span hidden>secret</span>';document.body.appendChild(d);setTimeout(function(){d.remove()},300)">mixed</button>
</body></html>`

func openTransient(t *testing.T) (context.Context, *chromium.Page) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(transientFixture))
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	p, err := chromium.Open(ctx, 1280, 800)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	if err := p.Load(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	p.Drain()
	return ctx, p
}

func clickButton(ctx context.Context, t *testing.T, p *chromium.Page, id string) {
	t.Helper()
	r, err := p.Eval(ctx, `(() => { const r = document.getElementById('`+id+`').getBoundingClientRect(); return {x: r.x + r.width/2, y: r.y + r.height/2}; })()`)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Click(ctx, gjson.Get(r, "x").Float(), gjson.Get(r, "y").Float()); err != nil {
		t.Fatal(err)
	}
	if err := p.Settle(ctx); err != nil {
		t.Fatal(err)
	}
}

func toasts(p *chromium.Page) []string {
	var out []string
	for _, e := range p.Drain() {
		if e.Kind == engine.Toast {
			out = append(out, e.Text)
		}
	}
	return out
}

func TestTransientNodeIsReportedAsToast(t *testing.T) {
	ctx, p := openTransient(t)
	clickButton(ctx, t, p, "toast")
	got := toasts(p)
	if len(got) != 1 || got[0] != "Saved!" {
		t.Fatalf("toasts = %q, want [Saved!]", got)
	}
	s, err := p.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(texts(s), "Saved!") {
		t.Errorf("snapshot still has the toast: %s", texts(s))
	}
}

func TestKeptNodeIsNotAToast(t *testing.T) {
	ctx, p := openTransient(t)
	clickButton(ctx, t, p, "keep")
	if got := toasts(p); len(got) != 0 {
		t.Fatalf("toasts = %q, want none", got)
	}
}

func TestRemovalInLaterWindowIsNotAToast(t *testing.T) {
	ctx, p := openTransient(t)
	clickButton(ctx, t, p, "keep")
	clickButton(ctx, t, p, "drop")
	if got := toasts(p); len(got) != 0 {
		t.Fatalf("toasts = %q, want none", got)
	}
}

// A style or script element is never rendered (HTML, Rendering: display none),
// so one a page adds and removes at once is no message a person saw.
func TestAnUnrenderedElementThatComesAndGoesIsNotAToast(t *testing.T) {
	ctx, p := openTransient(t)
	clickButton(ctx, t, p, "unrendered")
	if got := toasts(p); len(got) != 0 {
		t.Fatalf("toasts = %q, want none", got)
	}
}

// A toast's text is what a person saw: never style rules or hidden text in it.
func TestAToastSaysOnlyWhatWasRendered(t *testing.T) {
	ctx, p := openTransient(t)
	clickButton(ctx, t, p, "mixed")
	if got := toasts(p); len(got) != 1 || got[0] != "Copied" {
		t.Fatalf("toasts = %q, want [Copied]", got)
	}
}
