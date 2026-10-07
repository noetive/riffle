//go:build integration

package session_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/chromium"
	"github.com/noetive/riffle/internal/program"
	"github.com/noetive/riffle/internal/session"
)

const shop = `<!doctype html><html><head><title>Cart</title><style>
 #cookie{position:fixed;inset:0;background:rgba(0,0,0,.5)} 
 #cookie .box{background:#fff;margin:100px auto;width:400px;padding:20px}
 .strike{text-decoration:line-through}
</style></head><body>
<div id="cookie" role="dialog" aria-modal="true" aria-label="Cookie preferences"><div class="box">
 <p>We use cookies</p><button id="accept">Accept all</button><button id="reject">Reject</button></div></div>
<main><h1>Your cart</h1>
 <p id="count">items: 2</p>
 <label>Email <input id="email" type="email"></label>
 <label>Search <input id="q" type="text"></label>
 <p id="hint"></p>
 <button id="checkout">Checkout</button>
 <p id="result"></p>
</main>
<script>
 var timer;
 document.getElementById('accept').onclick = function(){ document.getElementById('cookie').remove(); };
 document.getElementById('reject').onclick = function(){ document.getElementById('cookie').remove(); };
 document.getElementById('q').addEventListener('input', function(e){
   clearTimeout(timer);
   timer = setTimeout(function(){ document.getElementById('hint').textContent = 'suggest:' + e.target.value; }, 800);
 });
 document.getElementById('checkout').onclick = function(){
   var email = document.getElementById('email').value;
   fetch('/order?email=' + encodeURIComponent(email)).then(r => r.text()).then(t => {
     document.getElementById('result').textContent = 'order:' + t;
   });
 };
</script></body></html>`

func newSession(t *testing.T, url string) (*session.Session, context.Context) {
	t.Helper()
	return newSessionWith(t, session.Config{})
}

func newSessionWith(t *testing.T, cfg session.Config) (*session.Session, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	p, err := chromium.Open(ctx, 1280, 800)
	if err != nil {
		t.Fatal(err)
	}
	s := session.New(p, cfg)
	t.Cleanup(s.Close)
	return s, ctx
}

func serve(t *testing.T) string {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(shop)) })
	mux.HandleFunc("/order", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok-" + r.URL.Query().Get("email")))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// pageClock reads the page's own clock, which is what the debounce timer sees.
func pageClock(ctx context.Context, t *testing.T, s *session.Session) float64 {
	t.Helper()
	out := s.Run(ctx, "eval Date.now()")
	m := regexp.MustCompile(`eval (\d+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no page clock in reply:\n%s", out)
	}
	ms, _ := strconv.ParseFloat(m[1], 64)
	return ms
}

func TestModalBlocksThenProgramRunsJavaScript(t *testing.T) {
	url := serve(t)
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})

	first := s.Run(ctx, "goto "+url+"\nview")
	if !strings.Contains(first, "Cookie preferences") || !strings.Contains(first, "covered-by") {
		t.Errorf("modal and occlusion not reported:\n%s", first)
	}

	blocked := s.Run(ctx, `click "Checkout"`)
	if !strings.Contains(blocked, "blocked") || !strings.Contains(blocked, "covered-by") {
		t.Errorf("click behind modal should be blocked with its cause:\n%s", blocked)
	}

	before := pageClock(ctx, t, s)
	out := s.Run(ctx, "click \"Accept all\"\nfill \"Email\" \"a@b.co\"\nfill \"Search\" \"shoe\"\nclick \"Checkout\"\nexpect text \"order:ok-a@b.co\"\nexpect text \"suggest:shoe\"")
	if strings.Contains(out, "failed") {
		t.Fatalf("program failed:\n%s", out)
	}
	if adv := pageClock(ctx, t, s) - before; adv < 800 {
		t.Errorf("page clock advanced %vms; the 800ms debounce must have elapsed on the page", adv)
	}
	if strings.Contains(out, "Cookie preferences") && !strings.Contains(out, "- d1") {
		t.Errorf("delta should report the dialog going away:\n%s", out)
	}
}

func TestGuardFailureStopsProgramAndSaysWhy(t *testing.T) {
	url := serve(t)
	s, ctx := newSession(t, url)
	out := s.Run(ctx, "goto "+url+"\nclick \"Accept all\"\nexpect text \"never on page\"\nclick \"Checkout\"")
	if !strings.Contains(out, "failed line 3") {
		t.Errorf("expected failure at line 3:\n%s", out)
	}
}

func TestEvalIsOffByDefaultAndRunsPageJavaScriptWhenEnabled(t *testing.T) {
	url := serve(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	p, err := chromium.Open(ctx, 1280, 800)
	if err != nil {
		t.Fatal(err)
	}
	off := session.New(p, session.Config{})
	defer off.Close()
	if out := off.Run(ctx, "goto "+url+"\neval 1+1"); !strings.Contains(out, "eval is off") {
		t.Errorf("eval must be refused by default with the next action:\n%s", out)
	}

	p2, err := chromium.Open(ctx, 1280, 800)
	if err != nil {
		t.Fatal(err)
	}
	on := session.New(p2, session.Config{Policy: program.Policy{AllowEval: true}})
	defer on.Close()
	out := on.Run(ctx, "goto "+url+"\neval [1,2,3].map(x => x*2).join('-') + document.title")
	if !strings.Contains(out, "2-4-6Cart") {
		t.Errorf("page JavaScript did not run:\n%s", out)
	}
}

type boundSecret struct{ origin string }

func (b boundSecret) Resolve(name, origin string) (string, error) {
	if origin != b.origin {
		return "", fmt.Errorf("secret %q is bound to %s", name, b.origin)
	}
	return "s3cret-value", nil
}

func TestSecretsAreTypedButNeverShownAndStayOnTheirOrigin(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<input id=pw type=password aria-label="Password"><input id=plain aria-label="Note">
<button onclick="document.getElementById('echo').textContent='typed '+document.getElementById('plain').value">Echo</button><p id=echo></p>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	open := func(origin string) *session.Session {
		p, err := chromium.Open(ctx, 1280, 800)
		if err != nil {
			t.Fatal(err)
		}
		s := session.New(p, session.Config{Policy: program.Policy{Secrets: boundSecret{origin: origin}}})
		t.Cleanup(s.Close)
		return s
	}

	ok := open(srv.URL)
	out := ok.Run(ctx, "goto "+srv.URL+"\nfill \"Password\" $secret:login\nfill \"Note\" $secret:login\nclick \"Echo\"\nview")
	if strings.Contains(out, "s3cret-value") {
		t.Errorf("secret leaked into the reply:\n%s", out)
	}
	if strings.Contains(out, "failed") {
		t.Errorf("fill on the bound origin should work:\n%s", out)
	}

	other := open("http://other.example")
	out = other.Run(ctx, "goto "+srv.URL+"\nfill \"Password\" $secret:login")
	if !strings.Contains(out, "failed line 2") || strings.Contains(out, "s3cret-value") {
		t.Errorf("secret on the wrong origin must fail without the value:\n%s", out)
	}
}

func TestReloadingAPageDoesNotInflateRefsAndReportsThePageAgain(t *testing.T) {
	url := serve(t)
	s, ctx := newSession(t, url)
	first := s.Run(ctx, "goto "+url+"\nclick \"Accept all\"\nview interactive")
	second := s.Run(ctx, "goto "+url+"\nview interactive")
	if !strings.Contains(second, "covered-by=d1") {
		t.Errorf("a reload shows a fresh page, so the reply must describe it:\n%s", second)
	}
	if strings.Contains(first, "failed") || strings.Contains(second, "failed") {
		t.Fatalf("unexpected failure:\n%s\n%s", first, second)
	}
	for _, ref := range []string{"b1", "b2"} {
		if !strings.Contains(second, ref+" ") {
			t.Errorf("refs must restart on a new document, %s missing:\n%s", ref, second)
		}
	}
}

func TestRefSurvivesRemountButAVanishedNodeIsStale(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<div id=root><button id=save onclick="document.getElementById('out').textContent='saved'">Save</button></div>
<button id=remount onclick="var r=document.getElementById('root'); r.innerHTML=r.innerHTML">Remount</button>
<button id=dup onclick="document.getElementById('root').innerHTML='<button>Same</button><button>Same</button>'">Duplicate</button>
<p id=out></p>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	view := s.Run(ctx, "goto "+srv.URL+"\nview interactive")
	var ref string
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, `"Save"`) {
			ref = strings.Fields(l)[1]
		}
	}
	if ref == "" {
		t.Fatalf("no ref for Save:\n%s", view)
	}

	out := s.Run(ctx, "click \"Remount\"\nclick "+ref+"\nexpect text \"saved\"")
	if strings.Contains(out, "failed") {
		t.Errorf("a ref must follow its node through a remount:\n%s", out)
	}

	s.Run(ctx, "click \"Duplicate\"\nview interactive")
	out = s.Run(ctx, "click "+ref)
	if !strings.Contains(out, "stale") {
		t.Errorf("a ref whose node is really gone must be stale, never a guess:\n%s", out)
	}
}

func TestTextTargetsFollowWhatAPersonReadsNotAHiddenLabel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<button aria-label="Confirm payment" onclick="document.getElementById('o').textContent='paid'">Cancel</button><p id=o></p>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	view := s.Run(ctx, "goto "+srv.URL+"\nview interactive")
	if !strings.Contains(view, "name-differs") {
		t.Errorf("the contradicting label must be flagged:\n%s", view)
	}
	if out := s.Run(ctx, `click "Confirm payment"`); !strings.Contains(out, "failed") || strings.Contains(out, "paid") {
		t.Errorf("a hidden label must not be a way to click something a person reads differently:\n%s", out)
	}
}

func TestFillSetsInputsThatTypingCannotReach(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><label>When <input type=time id=t></label><label>Day <input type=date id=d></label><label>Level <input type=range id=r min=0 max=10></label>
<p id=o></p><script>for (const id of ['t','d','r']) document.getElementById(id).addEventListener('change', e => { document.getElementById('o').textContent += id + '=' + e.target.value + ';'; });</script>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nfill \"When\" \"12:30\"\nfill \"Day\" \"2030-01-02\"\nfill \"Level\" \"7\"\nview read")
	for _, want := range []string{"t=12:30;", "d=2030-01-02;", "r=7;"} {
		if !strings.Contains(out, want) {
			t.Errorf("filling must leave %s in the page and fire change:\n%s", want, out)
		}
	}
}

func TestSubmittingAFormDoesNotWaitOnRequestsThatFinished(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><form method=post action=/order><input name=n aria-label=Name><button>Order</button></form>`))
	})
	mux.HandleFunc("/order", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><p>Thanks</p>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nfill \"Name\" \"Ada\"\nclick \"Order\"\nexpect text \"Thanks\"")
	if strings.Contains(out, "slow-load") {
		t.Errorf("a form post that has completed must not be reported as slow:\n%s", out)
	}
}

func TestBackReturnsPromptlyToThePreviousPage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>One</title><a href=/two>Next page</a>`))
	})
	mux.HandleFunc("/two", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Two</title><p>second page</p>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nclick \"Next page\"\nback\nexpect text \"Next page\"")
	if strings.Contains(out, "slow-load") || strings.Contains(out, "failed") {
		t.Errorf("going back to a page that loaded fine must not look like a slow load:\n%s", out)
	}
}

func TestViewShowsCheckedStateAfterCheckingAndUnchecking(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><label><input type=checkbox> Agree</label><label><input type=checkbox checked> Preticked</label>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\ncheck \"Agree\"\nview interactive")
	if !strings.Contains(out, `checkbox f1 "Agree" checked`) {
		t.Errorf("a box the agent just ticked must read as checked:\n%s", out)
	}
	out = s.Run(ctx, "click \"Preticked\"\nview interactive")
	if strings.Contains(out, `"Preticked" checked`) {
		t.Errorf("a box a person unticked must not still read as checked:\n%s", out)
	}
}

func TestViewShowsWhatASelectHasChosen(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><select aria-label=Color><option value=r>Red</option><option value=g>Green</option></select>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	if out := s.Run(ctx, "goto "+srv.URL+"\nview interactive"); !strings.Contains(out, `"Color" ="Red"`) {
		t.Errorf("a select must show its starting choice:\n%s", out)
	}
	if out := s.Run(ctx, "select \"Color\" \"Green\"\nview interactive"); !strings.Contains(out, `"Color" ="Green"`) {
		t.Errorf("a select must show the choice the agent made:\n%s", out)
	}
}

func TestLinksThatOpenNewWindowsLoadInThisTab(t *testing.T) {
	mux := http.NewServeMux()
	page := func(h string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(h))
		}
	}
	mux.HandleFunc("/", page(`<!doctype html><title>Home</title><a href=/popup target=_blank>Open popup</a><button onclick="window.open('/other')">Script popup</button>`))
	mux.HandleFunc("/popup", page(`<!doctype html><title>Popup</title><p>popup body</p>`))
	mux.HandleFunc("/other", page(`<!doctype html><title>Other</title><p>other body</p>`))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nclick \"Open popup\"\nexpect text \"popup body\"")
	if strings.Contains(out, "failed") {
		t.Errorf("a link that opens a new window must show its page in this tab:\n%s", out)
	}
	out = s.Run(ctx, "goto "+srv.URL+"\nclick \"Script popup\"\nexpect text \"other body\"")
	if strings.Contains(out, "failed") {
		t.Errorf("a script that opens a window must show its page in this tab:\n%s", out)
	}
}

func TestFocusableScrollRegionDoesNotHideTheControlsInsideIt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Log</title><div tabindex=0 style="height:100px;overflow:auto"><div style="height:1000px">log body</div><button onclick="document.title='end-clicked'">End action</button></div>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nview interactive")
	if !strings.Contains(out, `"End action"`) {
		t.Errorf("a control inside a scrollable region must still be listed:\n%s", out)
	}
	out = s.Run(ctx, "click \"End action\"")
	if !strings.Contains(out, "end-clicked") {
		t.Errorf("the control inside the region must be clickable:\n%s", out)
	}
}

func TestReadViewKeepsTableRowsAndColumns(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><table><tr><th>Name</th><th>Qty</th></tr><tr><td>Apple</td><td>3</td></tr><tr><td>Pear</td><td>5</td></tr></table>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nview read")
	for _, want := range []string{"| **Name** | **Qty** |", "| --- | --- |", "| Apple | 3 |", "| Pear | 5 |"} {
		if !strings.Contains(out, want) {
			t.Errorf("read view must keep one row per line and one column per cell, lacks %q:\n%s", want, out)
		}
	}
}

func TestReadViewKeepsPunctuationNextToLinksAndEmphasis(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><p>See <a href=/x>cities</a>, then <b>bold</b>. And <i>it</i>! (<a href=/y>more</a>)</p>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nview read")
	want := "See [cities](/x), then **bold**. And *it*! ([more](/y))"
	if !strings.Contains(out, want) {
		t.Errorf("read view must not add spaces the page does not have, want %q:\n%s", want, out)
	}
}

func TestBlockedClickNamesTheOverlayInTheWay(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><button>Under overlay</button><div id=ov style="position:fixed;top:0;left:0;width:100%;height:100%;background:rgba(0,0,0,.5)"><div style="background:#fff;margin:100px;padding:20px">We use cookies<button onclick="ov.remove()">Accept all</button></div></div>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nclick \"Under overlay\"")
	if !strings.Contains(out, "blocked") || !strings.Contains(out, `page text is "We use cookies`) {
		t.Errorf("a blocked click must say what is in the way, not name an empty element:\n%s", out)
	}
}

func TestViewShowsWhatWasTypedIntoAnEditableRegion(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><div contenteditable=true role=textbox aria-label=Editor></div>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nfill \"Editor\" \"hello editor\"\nview interactive")
	if !strings.Contains(out, `"Editor" ="hello editor"`) {
		t.Errorf("an editable region must show its content once typed into:\n%s", out)
	}
}

func TestFailedNavigationDoesNotLeakIntoTheNextPage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Fine</title><p>all good</p>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	if out := s.Run(ctx, "goto http://127.0.0.1:1/"); !strings.Contains(out, "failed") {
		t.Fatalf("a refused port must fail the program:\n%s", out)
	}
	out := s.Run(ctx, "goto "+srv.URL)
	if strings.Contains(out, "chrome-error") {
		t.Errorf("the previous failure's error page must not appear in this reply:\n%s", out)
	}
}

func TestViewMarksTheFocusedControl(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><input aria-label=First><input aria-label=Second>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nfill \"First\" \"a\"\nview interactive")
	if !strings.Contains(out, `"First" ="a" focused`) || strings.Contains(out, `"Second" focused`) {
		t.Errorf("the control just typed into has focus and must say so:\n%s", out)
	}
	out = s.Run(ctx, "press Tab\nview interactive")
	if !strings.Contains(out, `"Second" focused`) || strings.Contains(out, `"First" ="a" focused`) {
		t.Errorf("Tab must move the focused mark along:\n%s", out)
	}
}

func TestFillTypesNewlinesIntoATextarea(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><textarea aria-label=Notes oninput="document.title='lines:'+this.value.split('\n').length"></textarea>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nfill \"Notes\" \"one\\ntwo\\nthree\"\nview interactive")
	if !strings.Contains(out, `"lines:3"`) {
		t.Errorf("a newline escape must put a new line in the textarea:\n%s", out)
	}
}

func TestPressUnderstandsModifierCombinations(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><input aria-label=First><input aria-label=Second>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nclick \"Second\"\npress Shift+Tab\nview interactive")
	if !strings.Contains(out, `"First" focused`) {
		t.Errorf("Shift+Tab must move focus back:\n%s", out)
	}
	for _, combo := range []string{"Control+a", "Meta+a"} {
		out = s.Run(ctx, "fill \"First\" \"abc\"\npress "+combo+"\npress x\nview interactive")
		if !strings.Contains(out, `"First" ="x"`) {
			t.Errorf("%s then x must replace everything in the field:\n%s", combo, out)
		}
	}
}

const todoApp = `<!doctype html><title>Todos</title><style>
.toggle{opacity:0;position:absolute;width:40px;height:40px;margin:0}
li{position:relative;height:40px;list-style:none}
.toggle+label{display:block;height:40px;padding-left:50px;background:url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='40' height='40'%3E%3Ccircle cx='20' cy='20' r='10' fill='none' stroke='%23999'/%3E%3C/svg%3E") no-repeat}
.done+label{text-decoration:line-through}
</style><input id=nw aria-label="New Todo Input" autofocus><ul id=list></ul><p id=count>0 items left</p>
<script>
const items=[];const render=()=>{list.innerHTML='';items.forEach((t,i)=>{const li=document.createElement('li');
 li.innerHTML='<input class="toggle'+(t.done?' done':'')+'" type=checkbox '+(t.done?'checked':'')+'><label></label>';
 li.querySelector('label').textContent=t.text;
 li.querySelector('input').onchange=e=>{t.done=e.target.checked;render()};list.appendChild(li)});
 count.textContent=items.filter(t=>!t.done).length+' items left'};
nw.onkeydown=e=>{if(e.key==="Enter"&&nw.value){items.push({text:nw.value,done:false});nw.value="";render()}};
</script>`

func TestCheckboxesStyledInvisibleByAFrameworkCanStillBeTicked(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(todoApp))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nfill \"New Todo Input\" \"buy milk\"\npress Enter\nfill \"New Todo Input\" \"walk dog\"\npress Enter\nview interactive")
	if strings.Count(out, "checkbox f") != 2 {
		t.Fatalf("each todo's checkbox must be listed:\n%s", out)
	}
	out = s.Run(ctx, "check f2\nview outline")
	if !strings.Contains(out, "1 items left") {
		t.Errorf("ticking a todo must show it done:\n%s", out)
	}
}

func TestFirstScrollAfterNavigatingMovesThePage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>T</title><p>top</p><div style="height:3000px"></div><p>bottom</p>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nscroll down\nview outline budget=40")
	if !strings.Contains(out, "scroll 600/") {
		t.Errorf("one scroll down right after goto must move the page a screenful:\n%s", out)
	}
}

func TestOpenCustomListboxListsItsOptions(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><button id=b aria-haspopup=listbox>Choose color</button><ul id=l role=listbox aria-label=Colors hidden><li role=option>Red</li><li role=option>Green</li></ul><select aria-label=Native><option>One<option>Two</select>
<script>b.onclick=()=>{l.hidden=!l.hidden};l.querySelectorAll('li').forEach(li=>li.onclick=()=>{document.title='picked '+li.textContent;l.hidden=true})</script>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nclick \"Choose color\"\nview interactive")
	if !strings.Contains(out, `option b`) || !strings.Contains(out, `"Green"`) {
		t.Errorf("the options of an opened custom listbox must be listed:\n%s", out)
	}
	if strings.Contains(out, `"Two"`) {
		t.Errorf("a native select stays one control:\n%s", out)
	}
	out = s.Run(ctx, "click \"Green\"")
	if !strings.Contains(out, "picked Green") {
		t.Errorf("an option must be clickable:\n%s", out)
	}
}

func liveServer(t *testing.T, page string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for i := 0; ; i++ {
			_, _ = fmt.Fprintf(w, "data: tick %d\n\n", i)
			fl.Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	})
	mux.HandleFunc("/poll", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	})
	srv := httptest.NewServer(mux)
	// Closed after the session, so open streams end first.
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestAnOpenEventStreamDoesNotMakeEveryActionWaitForTheNetwork(t *testing.T) {
	url := liveServer(t, `<!doctype html><title>Live</title><p id=s>none</p><button onclick="document.title='clicked'">Act</button>
<script>new EventSource('/stream').onmessage=e=>{s.textContent=e.data}</script>`)
	s, ctx := newSession(t, url)

	for _, prog := range []string{"goto " + url + "\nview outline", "click \"Act\"\nview outline"} {
		out := s.Run(ctx, prog)
		if strings.Contains(out, "slow-load") || strings.Contains(out, "failed") {
			t.Errorf("a stream that is answering is not a request in flight:\n%s", out)
		}
	}
}

func TestARequestThatNeverAnswersStillLetsTheActionFinish(t *testing.T) {
	url := liveServer(t, `<!doctype html><title>Poll</title><button onclick="document.title='clicked'">Act</button><script>fetch('/poll')</script>`)
	s, ctx := newSession(t, url)

	out := s.Run(ctx, "goto "+url+"\nclick \"Act\"\nexpect text \"Act\"")
	if strings.Contains(out, "failed") {
		t.Errorf("a long poll must not fail the program:\n%s", out)
	}
	if !strings.Contains(out, "had not answered") {
		t.Errorf("the agent must be told a request had not answered:\n%s", out)
	}
	if !strings.Contains(s.Run(ctx, "view outline"), "clicked") {
		t.Error("the click must have taken effect")
	}
}

func TestFadeInUIIsReachableOnceItsTransitionHasRun(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><style>
#panel{opacity:0;transition:opacity 2s}#panel.on{opacity:1}
@keyframes slide{from{opacity:0}to{opacity:1}}#entry{animation:slide 1.5s both}
@keyframes spin{to{transform:rotate(360deg)}}#spinner{animation:spin 1s linear infinite}
</style><button id=reveal onclick="panel.classList.add('on')">Reveal panel</button>
<div id=panel><button onclick="document.title='acted'">Panel action</button></div>
<div id=entry><button>Entry button</button></div><div id=spinner>working</div>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nview interactive")
	if !strings.Contains(out, `"Entry button"`) {
		t.Errorf("a control that fades in when the page loads must be listed:\n%s", out)
	}
	if strings.Contains(out, `"Panel action"`) {
		t.Errorf("a panel that has not been asked for yet is still hidden:\n%s", out)
	}
	out = s.Run(ctx, "click \"Reveal panel\"\nview interactive")
	if !strings.Contains(out, `"Panel action"`) {
		t.Errorf("a panel that fades in must be reachable after the click:\n%s", out)
	}
	if out = s.Run(ctx, "click \"Panel action\""); !strings.Contains(out, "acted") {
		t.Errorf("the revealed control must be clickable:\n%s", out)
	}
}

func TestAcceptedPromptReturnsItsDefaultText(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Home</title><button onclick="document.title='got:'+prompt('Name?','anon')">Ask</button>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nclick \"Ask\"")
	if !strings.Contains(out, "got:anon") {
		t.Errorf("accepting a prompt answers with its default, as pressing OK does:\n%s", out)
	}
}

func TestSubmittingAnInvalidFormSaysWhichFieldsAreWrong(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><form action=/done><label>Email <input type=email name=e required></label><label>Age <input type=number name=a min=18></label><button>Submit form</button></form>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nfill \"Age\" \"5\"\nclick \"Submit form\"")
	if strings.Count(out, "event validation") != 2 || !strings.Contains(out, "Email:") || !strings.Contains(out, "Age:") {
		t.Errorf("each field the browser refused must be reported with its name:\n%s", out)
	}
}

func TestClickBehindAShowModalDialogIsRefusedNotSilentlyLost(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>T</title><button onclick="document.title='bg-clicked'">Background action</button><button onclick="dlg.showModal()">Open dialog</button>
<dialog id=dlg><p>Delete?</p><form method=dialog><button onclick="document.title='kept'">Keep</button></form></dialog>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nclick \"Open dialog\"\nclick \"Background action\"")
	if !strings.Contains(out, "blocked") || !strings.Contains(out, "covered-by") {
		t.Errorf("a click the open modal would swallow must be refused with the reason:\n%s", out)
	}
	out = s.Run(ctx, "click \"Keep\"\nclick \"Background action\"")
	if strings.Contains(out, "failed") || !strings.Contains(out, "bg-clicked") {
		t.Errorf("once the dialog is closed the page works again:\n%s", out)
	}
}

func TestTextBoxWithComboboxRoleIsNotCalledASelect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><input role=combobox aria-label="Search city" aria-expanded=false><select aria-label=Native><option>One<option>Two</select><div role=combobox tabindex=0 aria-label="Custom">pick one</div>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nview interactive")
	if !strings.Contains(out, `combobox f1 "Search city"`) {
		t.Errorf("an input that takes typing is a combobox, not a select:\n%s", out)
	}
	if !strings.Contains(out, `select f2 "Native"`) {
		t.Errorf("a native select stays a select:\n%s", out)
	}
	if !strings.Contains(out, `combobox f3 "Custom"`) {
		t.Errorf("a custom combobox is not a native select either:\n%s", out)
	}
}

func TestFillReplacesTheContentOfAnEditableRegion(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><div contenteditable=true role=textbox aria-label=Editor><p>old line one</p><p>old line two</p></div><input aria-label=Plain value="old plain"><textarea aria-label=Area>old area</textarea>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nfill \"Editor\" \"fresh\"\nfill \"Plain\" \"fresh\"\nfill \"Area\" \"fresh\"\nview interactive")
	for _, want := range []string{`"Editor" ="fresh"`, `"Plain" ="fresh"`, `"Area" ="fresh"`} {
		if !strings.Contains(out, want) {
			t.Errorf("fill must replace what was there; lacks %s:\n%s", want, out)
		}
	}
}

func TestFillTypesLikeAPersonForPagesThatListenForKeys(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>keys</title>
<input id=otp aria-label="Code" autocomplete=off><p id=log></p>
<script>const seen=[];otp.addEventListener('keydown',e=>{seen.push(e.key)});otp.addEventListener('input',()=>{log.textContent='keys:'+seen.join('')+' value:'+otp.value});</script>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nfill \"Code\" \"123456\"\nview outline")
	if !strings.Contains(out, "keys:123456 value:123456") {
		t.Errorf("a page listening for keydown must see each key that fill types:\n%s", out)
	}
}

func TestTextSplitAcrossElementsIsStillFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><p>Count: <!-- -->5<!-- --> items</p><p>Total: <b>$12</b>.<span>50</span></p><p>Hello,<br>world</p><div>block one</div><div>block two</div>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	for _, want := range []string{"Count: 5 items", "Total: $12.50", "Hello, world", "COUNT: 5"} {
		out := s.Run(ctx, "goto "+srv.URL+"\nexpect text \""+want+"\"")
		if strings.Contains(out, "failed") {
			t.Errorf("text a person reads as %q must be found:\n%s", want, out)
		}
	}
	if out := s.Run(ctx, "goto "+srv.URL+"\nexpect gone \"Count: 5 items\""); !strings.Contains(out, "failed") {
		t.Errorf("expect gone must fail while the text is there:\n%s", out)
	}
	out := s.Run(ctx, "goto "+srv.URL+"\nexpect text \"block one block two\"")
	if strings.Contains(out, "failed") {
		t.Errorf("neighbouring blocks read as separate words:\n%s", out)
	}
	if out := s.Run(ctx, "goto "+srv.URL+"\nexpect text \"items Total\""); strings.Contains(out, "failed") {
		t.Errorf("paragraphs read as separate words:\n%s", out)
	}
}

func TestFindMatchesAPhraseSplitAcrossElements(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><div><p>Count: <!-- -->5<!-- --> items</p><p>Total: <b>$12</b>.<span>50</span></p><p>Single node phrase</p></div>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nview find \"Count: 5 items\"")
	if !strings.Contains(out, "1 matches") || !strings.Contains(out, "Count:") {
		t.Errorf("a phrase a person reads as one must be found, once, at its paragraph:\n%s", out)
	}
	if out = s.Run(ctx, `view find "total: $12.50"`); !strings.Contains(out, "1 matches") {
		t.Errorf("matching ignores case and follows inline markup:\n%s", out)
	}
	if out = s.Run(ctx, `view find "single node"`); !strings.Contains(out, "1 matches") {
		t.Errorf("a phrase in one text node is still one match:\n%s", out)
	}
	if out = s.Run(ctx, `view find "items total"`); !strings.Contains(out, "0 matches") {
		t.Errorf("a phrase must not match across paragraphs:\n%s", out)
	}
}

func TestSlottedAndDisplayContentsContentIsOnThePage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><x-card><span slot=title>Pro plan</span><p>Unlimited projects</p><button slot=action onclick="document.title='bought'">Buy now</button></x-card>
<x-btn>Save changes</x-btn><div style="display:contents"><div style="display:contents"><button onclick="document.title='wrapped'">Wrapped action</button></div></div><div style="display:none"><button>Not here</button></div>
<script>
customElements.define('x-card',class extends HTMLElement{constructor(){super();this.attachShadow({mode:'open'}).innerHTML='<section><h2><slot name=title></slot></h2><slot></slot><footer><slot name=action></slot></footer></section>'}});
customElements.define('x-btn',class extends HTMLElement{constructor(){super();const r=this.attachShadow({mode:'open'});r.innerHTML='<button><slot></slot></button>';r.querySelector('button').onclick=()=>document.title='saved'}});
</script>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nview outline")
	for _, want := range []string{`"Pro plan"`, `"Unlimited projects"`, `"Buy now"`, `"Save changes"`, `"Wrapped action"`} {
		if !strings.Contains(out, want) {
			t.Errorf("content slotted into a component or wrapped in display:contents must be listed; lacks %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Not here") {
		t.Errorf("display:none content stays hidden:\n%s", out)
	}
	for _, c := range []struct{ click, title string }{{"Buy now", "bought"}, {"Save changes", "saved"}, {"Wrapped action", "wrapped"}} {
		if o := s.Run(ctx, `click "`+c.click+`"`); !strings.Contains(o, c.title) {
			t.Errorf("clicking %q must reach the page:\n%s", c.click, o)
		}
	}
}

func TestAnimationFrameAndResizeCallbacksRunOnThePage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><p id=o>nothing yet</p><button id=b>Start slide</button><div id=slide style="width:10px;height:10px;background:red"></div>
<script>
let frames=0,resized=0;new ResizeObserver(()=>{resized++;show()}).observe(document.body);
const show=()=>{o.textContent='frames='+(frames>0?'yes':'no')+' resized='+(resized>0?'yes':'no')+' slide='+slide.style.width};
const raf=()=>{frames++;show();requestAnimationFrame(raf)};requestAnimationFrame(raf);
b.onclick=()=>{const t0=performance.now();const step=t=>{const w=Math.min(300,10+(t-t0)/2);slide.style.width=w+'px';show();if(w<300)requestAnimationFrame(step)};requestAnimationFrame(step)};
</script>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nview outline")
	if !strings.Contains(out, "frames=yes resized=yes") {
		t.Errorf("requestAnimationFrame and ResizeObserver callbacks must run:\n%s", out)
	}
	out = s.Run(ctx, "click \"Start slide\"\nwait seconds 1\nview outline")
	if !strings.Contains(out, "slide=300px") {
		t.Errorf("an animation driven by requestAnimationFrame must finish within its time:\n%s", out)
	}
}

func TestDoubleClickStartsEditingInAListApp(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>t</title><ul><li><label id=l>buy milk</label></li></ul>
<script>let clicks=0;l.onclick=()=>{clicks++;document.title='clicks:'+clicks};l.ondblclick=()=>{l.outerHTML='<input aria-label=Edit value="buy milk">'}</script>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nview interactive")
	if !strings.Contains(out, `"buy milk"`) {
		t.Fatalf("the label must be a target:\n%s", out)
	}
	out = s.Run(ctx, "dblclick \"buy milk\"\nview interactive")
	if !strings.Contains(out, `"Edit"`) {
		t.Errorf("a double-click must start editing:\n%s", out)
	}
}

func TestClickableDivIsTargetedByTheTextItShows(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>t</title><div id=card style="cursor:pointer">Show <b>error</b> details</div><div id=tab>Settings</div><p>Settings are below</p>
<script>card.onclick=()=>{document.title='card-clicked'};tab.onclick=()=>{document.title='tab-clicked'}</script>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	if out := s.Run(ctx, "goto "+srv.URL+"\nclick \"Show error details\""); !strings.Contains(out, "card-clicked") {
		t.Errorf("a clickable block is targeted by the text it shows, markup and all:\n%s", out)
	}
	if out := s.Run(ctx, "click \"Settings\""); !strings.Contains(out, "tab-clicked") {
		t.Errorf("a clickable block named by its text:\n%s", out)
	}
}

func TestFocusAndFillWorkInsideAShadowRoot(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>t</title><input aria-label=Outside><x-field></x-field>
<script>customElements.define('x-field',class extends HTMLElement{constructor(){super();const r=this.attachShadow({mode:'open'});r.innerHTML='<label>When <input type=time id=t></label><input aria-label=Inner id=i>';r.getElementById('t').onchange=e=>{document.title='time:'+e.target.value};r.getElementById('i').oninput=e=>{document.title='inner:'+e.target.value}}})</script>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nfill \"Inner\" \"abc\"\nview interactive")
	if !strings.Contains(out, `"Inner" ="abc" focused`) || strings.Contains(out, `"Outside" focused`) {
		t.Errorf("the input inside the shadow root has focus, not its host:\n%s", out)
	}
	if !strings.Contains(out, "inner:abc") {
		t.Errorf("typing must reach the inner input:\n%s", out)
	}
	out = s.Run(ctx, "fill \"When\" \"12:30\"")
	if !strings.Contains(out, "time:12:30") {
		t.Errorf("a time input inside a shadow root is set like any other:\n%s", out)
	}
}

func TestImageReplacedButtonIsNamedByItsHiddenLabelAndFlagged(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>t</title><style>.login{width:90px;height:30px;overflow:hidden;text-indent:-9999px;background:#06f}</style>
<button class=login onclick="document.title='logged-in'"><span>Sign in</span></button><button onclick="document.title='plain'">Plain</button>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nview interactive")
	if !strings.Contains(out, `"Sign in" unseen-name`) || strings.Contains(out, `"Plain" unseen-name`) {
		t.Errorf("a control labelled by image replacement is named by its hidden words and flagged:\n%s", out)
	}
	if out = s.Run(ctx, `click "Sign in"`); !strings.Contains(out, "logged-in") {
		t.Errorf("the control must be reachable by that name:\n%s", out)
	}
}

func TestAnActionThatNavigatesWaitsForTheNewPageToArrive(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(1200 * time.Millisecond)
		_, _ = w.Write([]byte(`<!doctype html><title>Arrived</title><p>slow page body</p>`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>Home</title><input aria-label=Search onkeydown="if(event.key==='Enter')location.href='/slow'"><a href=/slow>Go slow</a><button onclick="location.href='/slow'">Script go</button>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	for _, prog := range []string{
		"goto " + srv.URL + "\nfill \"Search\" \"x\"\npress Enter\nexpect text \"slow page body\"",
		"goto " + srv.URL + "\nclick \"Go slow\"\nexpect text \"slow page body\"",
		"goto " + srv.URL + "\nclick \"Script go\"\nexpect text \"slow page body\"",
	} {
		if out := s.Run(ctx, prog); strings.Contains(out, "failed") {
			t.Errorf("an action that navigates must wait for the page it opens:\n%s\n%s", prog, out)
		}
	}
}

func TestAnActionThatNavigatesFollowsTheNewPagesOwnScriptRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/stub", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>Stub</title><p>redirecting</p><script>setTimeout(()=>location.replace('/results'),1500)</script>`))
	})
	mux.HandleFunc("/results", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>Results</title><p>the real results</p>`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>Home</title><input aria-label=Search onkeydown="if(event.key==='Enter')location.href='/stub'">`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nfill \"Search\" \"x\"\npress Enter\nexpect text \"the real results\"")
	if strings.Contains(out, "failed") {
		t.Errorf("a page the action opened gets time to run its own redirect:\n%s", out)
	}
}

func TestImageMapRegionsCanBeClicked(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>Home</title><map name=m><area shape=rect coords="0,0,50,50" href="/two" alt="Area link"></map><img usemap=#m width=60 height=60 alt=Map src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7">`))
	})
	mux.HandleFunc("/two", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>Two</title><p>second page</p>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nclick \"Area link\"\nexpect text \"second page\"")
	if strings.Contains(out, "failed") {
		t.Errorf("a region of an image map is a link that can be clicked:\n%s", out)
	}
}

func TestCheckLeavesABoxCheckedAndIsSafeToRepeat(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><label><input type=checkbox id=a> Agree</label><label><input type=checkbox checked> Already</label><label><input type=radio name=r> One</label><label><input type=radio name=r checked> Two</label>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\ncheck \"Agree\"\ncheck \"Agree\"\ncheck \"Already\"\ncheck \"Two\"\nview interactive")
	for _, want := range []string{`"Agree" checked`, `"Already" checked`, `"Two" checked`} {
		if !strings.Contains(out, want) {
			t.Errorf("check must leave the control checked however often it runs; lacks %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"One" checked`) {
		t.Errorf("the other radio must not be checked:\n%s", out)
	}
}

func TestSelectRefusesDisabledOptionsAndMatchesLoosely(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>sel</title><label>Country <select onchange="document.title='c:'+this.value"><option value="">Choose</option><optgroup label=Nordic><option value=se>Sweden</option></optgroup><option value=us disabled>USA (closed)</option><optgroup label=Closed disabled><option value=cu>Cuba</option></optgroup>
<option value=x>
   Extra   spaces
</option></select></label>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	for _, name := range []string{"USA (closed)", "Cuba", "us", "cu"} {
		out := s.Run(ctx, "goto "+srv.URL+"\nselect \"Country\" \""+name+"\"")
		if !strings.Contains(out, "failed") || !strings.Contains(out, "disabled") || strings.Contains(out, "c:us") || strings.Contains(out, "c:cu") {
			t.Errorf("a disabled option, alone or in a disabled group, cannot be chosen (%s):\n%s", name, out)
		}
	}
	for want, title := range map[string]string{"sweden": "c:se", "SWEDEN": "c:se", "extra spaces": "c:x", "EXTRA   Spaces": "c:x"} {
		if out := s.Run(ctx, "goto "+srv.URL+"\nselect \"Country\" \""+want+"\""); !strings.Contains(out, title) {
			t.Errorf("an option is found whatever its case and spacing (%s):\n%s", want, out)
		}
	}
}

func TestFillIsHonestAboutFieldsThatCannotTakeTheText(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>f</title><input aria-label=Readonly value="locked" readonly><input aria-label=Num type=number><input aria-label=Max maxlength=3><input aria-label=Fine>
<input aria-label=Box type=checkbox><button>Plain button</button><select aria-label=Sel><option>a</option></select><input aria-label=Upload type=file><textarea aria-label=Ro readonly>fixed</textarea>
<input aria-label=Pw type=password>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	for name, why := range map[string]string{"Readonly": "read-only", "Box": "check", "Plain button": "click", "Sel": "use select", "Upload": "use upload", "Ro": "read-only"} {
		out := s.Run(ctx, "goto "+srv.URL+"\nfill \""+name+"\" \"x\"")
		if !strings.Contains(out, "failed") || !strings.Contains(out, why) {
			t.Errorf("fill into %q must fail and say why (%s):\n%s", name, why, out)
		}
	}
	out := s.Run(ctx, "goto "+srv.URL+"\nfill \"Num\" \"abc\"")
	if strings.Contains(out, "failed") || !strings.Contains(out, `note: "Num" holds "" after typing "abc"`) {
		t.Errorf("a number field that took no letters is a note, not a failure:\n%s", out)
	}
	out = s.Run(ctx, "fill \"Max\" \"abcdef\"")
	if !strings.Contains(out, `holds "abc" after typing "abcdef"`) {
		t.Errorf("text cut off by a length limit is reported:\n%s", out)
	}
	if out = s.Run(ctx, "fill \"Fine\" \"hello\""); strings.Contains(out, "note:") || strings.Contains(out, "failed") {
		t.Errorf("a field that took the text says nothing:\n%s", out)
	}
	if out = s.Run(ctx, "fill \"Pw\" \"hunter2\""); strings.Contains(out, "note:") || strings.Contains(out, "hunter2") {
		t.Errorf("a password field is never compared or echoed:\n%s", out)
	}
}

func TestAScrollingRegionCanBeScrolledByItsNameOrRef(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>s</title><div id=box role=region aria-label=Log style="height:100px;overflow:auto"><div style="height:800px">top of log</div><p>bottom of log</p></div>
<div id=plain style="height:60px;overflow:auto"><div style="height:500px">unnamed scroller</div></div><p>after</p>
<script>box.addEventListener('scroll',()=>{document.title='log:'+Math.round(box.scrollTop)});plain.addEventListener('scroll',()=>{document.title='plain:'+Math.round(plain.scrollTop)})</script>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nview outline")
	if strings.Count(out, "scrollable") != 2 {
		t.Errorf("both scrolling regions must say so and carry a ref:\n%s", out)
	}
	if out = s.Run(ctx, `scroll "Log" down`); !strings.Contains(out, "log:") {
		t.Errorf("a named scrolling region is scrolled by its name, not the page:\n%s", out)
	}
	ref := regexp.MustCompile(`scroll (r\d+) .*scrollable`).FindStringSubmatch(s.Run(ctx, "view outline"))
	if ref == nil {
		t.Fatal("the unnamed scroller has no ref in the view")
	}
	if out = s.Run(ctx, "scroll "+ref[1]+" down"); !strings.Contains(out, "plain:") {
		t.Errorf("an unnamed scrolling region is scrolled by its ref:\n%s", out)
	}
}

func TestClickableBlockWithOnlyAnImageIsNamedByItsAlt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>t</title><div id=a style="cursor:pointer"><img alt="Example News" width=24 height=24 src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"></div>
<div id=b style="cursor:pointer"><img alt="" width=24 height=24 src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"></div>
<script>a.onclick=()=>{document.title='a-clicked'};b.onclick=()=>{document.title='b-clicked'}</script>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nview interactive")
	if !strings.Contains(out, `clickable b1 "Example News"`) {
		t.Errorf("a clickable image is named by its alt text:\n%s", out)
	}
	if strings.Contains(out, `b2 ""`) {
		t.Errorf("an image with no alt gives no name:\n%s", out)
	}
	if out = s.Run(ctx, `click "Example News"`); !strings.Contains(out, "a-clicked") {
		t.Errorf("and can be clicked by it:\n%s", out)
	}
}

func TestLinksThatLeaveTheWebSayWhereTheyGo(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>t</title><a href="mailto:a@b.co">Mail us</a><a href="tel:+4612345">Call</a><a href="javascript:void(0)">Script</a><a href="/page">Plain</a><a href="https://example.com/x">Away</a><a href="#top">Jump</a><a href="/f.pdf" download>File</a><a href="sms:123">Text us</a>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nview interactive")
	for _, want := range []string{`"Mail us" href=mailto:a@b.co`, `"Call" href=tel:+4612345`, `"Script" href=javascript:void(0)`, `"Text us" href=sms:123`, `"File" download`} {
		if !strings.Contains(out, want) {
			t.Errorf("a link that does not open a web page says where it goes; lacks %s:\n%s", want, out)
		}
	}
	for _, not := range []string{`"Plain" href`, `"Away" href`, `"Jump" href`} {
		if strings.Contains(out, not) {
			t.Errorf("ordinary links stay short; has %s:\n%s", not, out)
		}
	}
}

func TestBlankLinesBetweenTextBlocksAreKeptInTheReadView(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><div>First paragraph, written the old way.<br><br>Second paragraph after a blank line.<br>Still second paragraph.<br><br><br>Third after two blanks.</div><p>Real paragraph.</p>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nview read")
	lines := strings.Split(out, "\n")
	var texts []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" && !strings.HasPrefix(l, "event") {
			texts = append(texts, strings.TrimSpace(l))
		}
	}
	got := strings.Join(texts, " | ")
	for _, want := range []string{"First paragraph, written the old way.", "Second paragraph after a blank line. Still second paragraph.", "Third after two blanks."} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q in\n%s", want, out)
		}
	}
	if strings.Contains(got, "way. Second paragraph") || strings.Contains(got, "paragraph. Third") {
		t.Errorf("a blank line made by <br><br> is a paragraph break, not a space:\n%s", out)
	}
}

func TestUploadThroughAStyledButtonThatOpensAHiddenFileInput(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "cv.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>up</title><div class=field><input type=file id=f style="display:none" onchange="document.title='file:'+this.files[0].name"><button onclick="f.click()">Choose file</button></div>
<form><input type=file style="display:none"><input type=file style="display:none"><button type=button>Ambiguous</button></form>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowUpload: func(string) error { return nil }}})

	out := s.Run(ctx, "goto "+srv.URL+"\nupload \"Choose file\" \""+file+"\"")
	if strings.Contains(out, "failed") || !strings.Contains(out, "file:cv.txt") {
		t.Errorf("a button that opens a hidden file input takes the upload:\n%s", out)
	}
}

func TestWaitTextOutlastsASlowButFiniteApiCall(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(7 * time.Second)
		_, _ = w.Write([]byte("slow answer"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>t</title><button onclick="fetch('/api').then(r=>r.text()).then(t=>{out.textContent=t})">Load</button><p id=out>waiting</p>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nclick \"Load\"\nwait text \"slow answer\"")
	if strings.Contains(out, "failed") {
		t.Errorf("wait text must outlast a request that answers after seven seconds:\n%s", out)
	}
}

func TestClickingAnElementScrolledUnderAStickyHeaderStillReachesIt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		var rows strings.Builder
		for i := 1; i <= 60; i++ {
			fmt.Fprintf(&rows, `<p style="height:60px;margin:0"><a href="#" onclick="document.title='item-%d';return false">Item %d</a></p>`, i, i)
		}
		_, _ = w.Write([]byte(`<!doctype html><title>t</title><header style="position:sticky;top:0;height:90px;background:#fff;border-bottom:1px solid">Sticky header</header>` + rows.String()))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nclick \"Item 55\"\nclick \"Item 2\"\nclick \"Item 30\"\nclick \"Item 1\"")
	if strings.Contains(out, "failed") || !strings.Contains(out, "item-1") {
		t.Errorf("an element above the viewport must be brought out from under the sticky header:\n%s", out)
	}
}

func TestForwardReturnsToThePageBackLeft(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>One</title><a href=/two>Next page</a>`))
	})
	mux.HandleFunc("/two", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><title>Two</title><p>second page</p>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	out := s.Run(ctx, "goto "+srv.URL+"\nclick \"Next page\"\nback\nexpect text \"Next page\"\nforward\nexpect text \"second page\"")
	if strings.Contains(out, "failed") {
		t.Errorf("forward must undo back:\n%s", out)
	}
	if out = s.Run(ctx, "forward"); !strings.Contains(out, "no later page") {
		t.Errorf("forward at the end says so:\n%s", out)
	}
}

func TestADeltaNeverReportsContentAsRemovedThatIsStillOnThePage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		var b strings.Builder
		for i := 0; i < 160; i++ {
			fmt.Fprintf(&b, "<p>Paragraph number %d carries its own distinctive sentence about topic %d.</p>", i, i*7)
		}
		_, _ = w.Write([]byte(`<!doctype html><title>t</title><button onclick="const u=document.createElement('ul');u.id='sug';for(let i=0;i<300;i++){const li=document.createElement('li');li.textContent='Suggestion '+i+' with some words';u.appendChild(li)}document.body.insertBefore(u,document.querySelector('p'))">Open list</button>` + b.String()))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	s, ctx := newSession(t, srv.URL)

	s.Run(ctx, "goto "+srv.URL)
	out := s.Run(ctx, "click \"Open list\"")
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "- ") && strings.Contains(l, "Paragraph") {
			t.Errorf("a paragraph the size limit pushed out of view is still on the page, yet the delta says it was removed:\n%s", l)
		}
	}
	if !strings.Contains(out, "more changes") {
		t.Errorf("three hundred new items cannot all be listed; the reply must say it left some out:\n%.600s", out)
	}
	if len(out) > 1500*4*2 {
		t.Errorf("the reply is %d characters, far over the view budget", len(out))
	}
}
