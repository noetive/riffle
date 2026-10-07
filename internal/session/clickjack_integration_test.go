//go:build integration

package session_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/program"
	"github.com/noetive/riffle/internal/session"
)

func servePage(t *testing.T, html string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

const rejectButton = `<!doctype html><title>start</title><body style="margin:0">
<button id="r" onclick="document.title='rejected'" style="position:absolute;left:20px;top:20px;width:200px;height:50px">Reject</button>`

func TestAClickIsNeverTakenByALayerOverTheTarget(t *testing.T) {
	for name, layer := range map[string]string{
		"invisible":                           `<div onclick="document.title='hijacked'" style="position:absolute;left:0;top:0;width:400px;height:200px;opacity:0"></div>`,
		"transparent":                         `<div onclick="document.title='hijacked'" style="position:absolute;left:0;top:0;width:400px;height:200px;background:transparent"></div>`,
		"invisible, far down a scrolled page": `<div style="height:3000px"></div><button onclick="document.title='rejected'" style="display:block;width:200px;height:50px">Reject</button><div onclick="document.title='hijacked'" style="position:absolute;left:0;top:2990px;width:400px;height:100px;opacity:0"></div><div style="height:2000px"></div>`,
		"centre only":                         `<div onclick="document.title='hijacked'" style="position:absolute;left:110px;top:35px;width:20px;height:20px;background:rgba(0,0,0,0.01)"></div>`,
		"invisible, inside a painted panel elsewhere": `<div style="position:absolute;left:600px;top:0;width:200px;height:200px;background:#fff"><div onclick="document.title='hijacked'" style="position:fixed;left:0;top:0;width:400px;height:200px;opacity:0"></div></div>`,
		"on mousemove": `<script>document.getElementById('r').addEventListener('mousemove', function once() {
			this.removeEventListener('mousemove', once);
			var d = document.createElement('div');
			d.setAttribute('style', 'position:absolute;left:0;top:0;width:400px;height:200px;opacity:0');
			d.onclick = function () { document.title = 'hijacked'; };
			document.body.appendChild(d);
		});</script>`,
	} {
		t.Run(name, func(t *testing.T) {
			s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
			page := rejectButton + layer
			if strings.HasPrefix(layer, "<div style=\"height:3000px") {
				page = `<!doctype html><title>start</title><body style="margin:0">` + layer
			}
			url := servePage(t, page)
			out := s.Run(ctx, "goto "+url+"\nclick \"Reject\"")
			title := s.Run(ctx, "eval document.title")
			if strings.Contains(title, "hijacked") {
				t.Fatalf("the click landed on the layer:\n%s", out)
			}
			if !strings.Contains(out, "blocked:") && !strings.Contains(title, "rejected") {
				t.Errorf("the click either reaches the button or is reported blocked:\n%s\n%s", out, title)
			}
		})
	}
}

func TestOrdinaryControlsAreStillClicked(t *testing.T) {
	for name, page := range map[string]string{
		"plain":  rejectButton,
		"nested": `<!doctype html><title>start</title><button onclick="document.title='rejected'" style="width:200px;height:50px"><span><svg width="20" height="20"><rect width="20" height="20"/></svg> <b>Reject</b></span></button>`,
		// The label lies over its hidden input; a label with a background is
		// still taken for a cover by the page analysis, a separate limitation.
		"label over its input": `<!doctype html><title>start</title><style>
			.box{position:relative;display:inline-block;width:120px;height:30px}
			.box input{position:absolute;left:0;top:0;width:120px;height:30px;opacity:0;margin:0}
			.box label{position:absolute;left:0;top:0;width:120px;height:30px}</style>
			<div class="box"><input type="checkbox" id="c" onchange="document.title='rejected'"><label for="c">Reject</label></div>`,
		"inside its label": `<!doctype html><title>start</title><label style="display:block;width:200px;padding:10px;background:#eee">Reject <input type="checkbox" onchange="document.title='rejected'"></label>`,
	} {
		t.Run(name, func(t *testing.T) {
			s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
			out := s.Run(ctx, "goto "+servePage(t, page)+"\nclick \"Reject\"")
			if title := s.Run(ctx, "eval document.title"); !strings.Contains(title, "rejected") {
				t.Errorf("an ordinary control must still be clicked:\n%s\n%s", out, title)
			}
		})
	}
}

// A click that lands on the target's container did not act on the target. It
// must not be reported as done.
func TestAClickThatLandsBesideItsTargetIsNotReportedAsDone(t *testing.T) {
	page := `<!doctype html><title>start</title><body style="margin:0">
<div onclick="if (document.title === 'start') document.title='container'" style="width:400px;height:200px;padding:20px">
<button onclick="document.title='hit'" style="width:0;height:0;overflow:hidden;padding:0;border:0">Button</button>
</div>`
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	out := s.Run(ctx, "goto "+servePage(t, page)+"\nclick \"Button\"")
	title := s.Run(ctx, "eval document.title")
	if strings.Contains(title, "container") {
		t.Fatalf("the click went to the container and was reported as done:\n%s\n%s", out, title)
	}
	if !strings.Contains(out, "blocked:") && !strings.Contains(out, "not found") && !strings.Contains(title, "hit") {
		t.Errorf("the click must reach the button or say why it cannot:\n%s\n%s", out, title)
	}
}

// An animation Riffle runs to its end must end for the page too: a handler on
// animationend that puts the target back where it belongs runs before the
// next step aims at it, as it would for a person who waited.
func TestAClickAfterAnAnimationReachesTheTargetWhereItCameToRest(t *testing.T) {
	page := `<!doctype html><title>start</title><style>
@keyframes drift { from { transform: translateX(0) } to { transform: translateX(240px) } }
.go { position:absolute; animation: drift 5s forwards linear }
</style><body style="margin:0">
<div onclick="if (document.title === 'start') document.title='container'" style="padding:40px;height:300px">
<button onclick="document.getElementById('m').className='go'">Animate</button>
<button id="m" onanimationend="this.className=''" onclick="document.title = this.className === '' ? 'hit' : 'hit while moving'">Target</button>
</div>`
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	out := s.Run(ctx, "goto "+servePage(t, page)+"\nclick \"Animate\"\nclick \"Target\"")
	if title := s.Run(ctx, "eval document.title"); !strings.Contains(title, "eval hit\n") && !strings.HasSuffix(strings.TrimSpace(title), "eval hit") {
		t.Errorf("the click after the animation must reach the button at rest:\n%s\n%s", out, title)
	}
}

// A target that moves away as the pointer arrives is not where the click
// lands. The click must not be reported as done.
func TestAClickOnATargetThatMovesAwayFromThePointerIsNotReportedAsDone(t *testing.T) {
	page := `<!doctype html><title>start</title><body style="margin:0">
<div onclick="if (document.title === 'start') document.title='container'" style="position:relative;width:900px;height:300px">
<button onmouseover="this.style.left='600px'" onclick="document.title='hit'" style="position:absolute;left:20px;top:20px;width:200px;height:50px">Shy</button>
</div>`
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	out := s.Run(ctx, "goto "+servePage(t, page)+"\nclick \"Shy\"")
	title := s.Run(ctx, "eval document.title")
	if strings.Contains(title, "container") {
		t.Fatalf("the click went to the container and was reported as done:\n%s\n%s", out, title)
	}
	if !strings.Contains(out, "blocked:") && !strings.Contains(title, "hit") {
		t.Errorf("the click must reach the button or say why it cannot:\n%s\n%s", out, title)
	}
}

// A field whose centre lies under another element takes no typing a person
// could do: the keys would go where the page does not want them. Typing is
// refused with what is in the way, not lost.
func TestTypingIntoAFieldCoveredAtItsCentreIsRefusedNotLost(t *testing.T) {
	page := `<!doctype html><title>start</title><body style="margin:0">
<section style="position:relative;width:420px">
<style>textarea{display:block;width:400px;height:40px;margin:0 0 30px 0;padding:0;border:0;box-sizing:border-box}</style>
<div style="overflow:auto;height:160px"><textarea placeholder="Street"></textarea><textarea id="n" placeholder="City"></textarea><textarea placeholder="Notes"></textarea></div>
<aside style="position:absolute;left:0;top:90px;width:420px;height:40px;background:#1d4ed8;color:#fff">Free shipping this week</aside>
</section>
<script>var n = document.getElementById('n'); n.addEventListener('input', function () {
  var r = n.getBoundingClientRect();
  if (document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2) !== n) n.value = '';
});</script>`
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	out := s.Run(ctx, "goto "+servePage(t, page)+"\nfill \"City\" \"Riffle\"")
	value := s.Run(ctx, "eval document.getElementById('n').value")
	if strings.Contains(value, "Riffle") {
		return // typed where a person could
	}
	if !strings.Contains(out, "blocked:") {
		t.Errorf("typing that the page throws away must be refused with what is in the way:\n%s\n%s", out, value)
	}
}

// A layer a person sees over the target, gone after a few seconds, is waited
// out like a disabled control, and never called invisible.
func TestAVisibleLayerThatGoesByItselfIsWaitedFor(t *testing.T) {
	page := rejectButton + `<button style="position:absolute;left:20px;top:200px" onclick="var o = document.createElement('div');
  o.setAttribute('style', 'position:absolute;left:100px;top:30px;width:40px;height:30px;z-index:10;background-color:rgba(255,0,0,0.5)');
  document.body.appendChild(o); setTimeout(function () { o.remove(); }, 3000);">Arm</button>`
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	out := s.Run(ctx, "goto "+servePage(t, page)+"\nclick \"Arm\"\nclick \"Reject\"")
	if strings.Contains(out, "invisible") {
		t.Errorf("a layer a person sees is not invisible:\n%s", out)
	}
	if title := s.Run(ctx, "eval document.title"); !strings.Contains(title, "rejected") {
		t.Errorf("the click must wait for the layer to go:\n%s\n%s", out, title)
	}
}

// A control that has no size yet, and gets one a few seconds later, is waited
// for like a disabled one.
func TestAControlThatGrowsIntoPlaceIsWaitedFor(t *testing.T) {
	page := `<!doctype html><title>start</title><body style="margin:0">
<button onclick="var b = document.getElementById('b'); b.setAttribute('style', 'width:0;height:0;overflow:hidden;padding:0;border:0;pointer-events:none');
  setTimeout(function () { b.setAttribute('style', 'width:120px;height:40px'); }, 3000);">Shrink</button>
<div style="padding:20px"><button id="b" onclick="document.title='hit'" style="width:120px;height:40px">Grow</button></div>`
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	out := s.Run(ctx, "goto "+servePage(t, page)+"\nclick \"Shrink\"\nclick \"Grow\"")
	if title := s.Run(ctx, "eval document.title"); !strings.Contains(title, "hit") {
		t.Errorf("the click must wait for the control to get its size:\n%s\n%s", out, title)
	}
}

// A control that is hidden for now, and shown a few seconds later, is waited
// for like a disabled one; a name nothing on the page has still fails fast.
func TestAControlThatAppearsShortlyIsWaitedFor(t *testing.T) {
	var s *session.Session
	var ctx context.Context
	for hide, show := range map[string]string{
		"display = 'none'":      "display = ''",
		"visibility = 'hidden'": "visibility = 'visible'",
	} {
		page := `<!doctype html><title>start</title><body style="margin:0">
<button onclick="var b = document.getElementById('b'); b.style.` + hide + `;
  setTimeout(function () { b.style.` + show + `; }, 3000);">Hide</button>
<select><option>Later</option><option>Sooner</option></select>
<button id="b" onclick="document.title='hit'">Later   </button>`
		s, ctx = newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
		out := s.Run(ctx, "goto "+servePage(t, page)+"\nclick \"Hide\"\nclick \"Later\"")
		if title := s.Run(ctx, "eval document.title"); !strings.Contains(title, "hit") {
			t.Errorf("%s: the click must wait for the control to appear:\n%s\n%s", hide, out, title)
		}
	}
	out := s.Run(ctx, `click "Nothing named so"`)
	if !strings.Contains(out, "not found") || strings.Contains(out, "(waited") {
		t.Errorf("a name nothing has fails at once, without a wait:\n%s", out)
	}
}

// A dialog the page opens after the program has moved on, while the next page
// is still on its way, is told as the page left behind's, not as the new page's.
func TestALateDialogIsToldAsThePreviousPages(t *testing.T) {
	// The order is made by events, not by timers: the first page's response
	// starts at once (so the click's settle does not wait on it) and its body
	// is held until the next page's request is in; the next page is held until
	// the first page has opened its dialog and said so.
	entered, fired := make(chan struct{}), make(chan struct{})
	var enter, fire sync.Once
	const patience = 10 * time.Second
	firstMux := http.NewServeMux()
	firstMux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>first</title><button onclick="fetch('/fire').then(r => r.text()).then(() => { alert('late'); fetch('/fired'); })">Later</button>`))
	})
	firstMux.HandleFunc("/fire", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-entered:
			_, _ = w.Write([]byte("go"))
		case <-r.Context().Done():
		case <-time.After(patience):
		}
	})
	firstMux.HandleFunc("/fired", func(http.ResponseWriter, *http.Request) { fire.Do(func() { close(fired) }) })
	firstSrv := httptest.NewServer(firstMux)
	t.Cleanup(firstSrv.Close)
	secondSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enter.Do(func() { close(entered) })
		select {
		case <-fired:
		case <-r.Context().Done():
		case <-time.After(patience):
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>second</title><h1>Second</h1>`))
	}))
	t.Cleanup(secondSrv.Close)
	first, second := firstSrv.URL, secondSrv.URL
	s, ctx := newSessionWith(t, session.Config{})
	if out := s.Run(ctx, "goto "+first+"\nclick \"Later\""); strings.Contains(out, "late") {
		t.Fatalf("the dialog is due after the click's settle, so it opens during the next load:\n%s", out)
	}
	out := s.Run(ctx, "goto "+second)
	told := false
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, `alert: late`) {
			told = true
			if !strings.Contains(l, "previous page") {
				t.Errorf("a dialog the first page opened reads as the second page's:\n%s", out)
			}
		}
	}
	if !told {
		t.Errorf("a dialog the first page opened, and that was answered, must be told:\n%s", out)
	}
}

// Hovering is how a person reveals what a page shows only under the pointer:
// a link that becomes clickable, an action on a row. It may aim at any text
// a person sees, not only at a control.
func TestHoverRevealsWhatThePageShowsUnderThePointer(t *testing.T) {
	page := `<!doctype html><title>start</title><style>
.row{display:flex;gap:20px;padding:10px;width:400px}.act{visibility:hidden}.row:hover .act{visibility:visible}</style>
<a class="link" onmouseenter="this.onclick = function () { document.title = 'link'; }">Reveal</a>
<div class="row"><span>Row text</span><button class="act" onclick="document.title += ' row'">Delete</button></div>`
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	out := s.Run(ctx, "goto "+servePage(t, page)+"\nhover \"Reveal\"\nclick \"Reveal\"\nhover \"Row text\"\nclick \"Delete\"")
	if title := s.Run(ctx, "eval document.title"); !strings.Contains(title, "link row") {
		t.Errorf("hovering must reveal the link and the row's action:\n%s\n%s", out, title)
	}
	if out := s.Run(ctx, `click "Row text"`); !strings.Contains(out, "not found") {
		t.Errorf("a click still needs something that takes clicks:\n%s", out)
	}
}

// select names the selection: several options on a multi-select hold
// together, a later select replaces them, and a single select takes one.
func TestSelectNamesTheWholeSelection(t *testing.T) {
	page := `<!doctype html><title>start</title>
<label for="c">Colors</label><select id="c" multiple size="4"><option>Red</option><option>Green</option><option>Blue</option></select>
<label for="s">Size</label><select id="s"><option>Small</option><option>Large</option></select>`
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	s.Run(ctx, "goto "+servePage(t, page))
	selected := `eval Array.from(document.getElementById('c').selectedOptions, o => o.text).join(',')`
	if out := s.Run(ctx, "select \"Colors\" \"Red\" \"Blue\"\n"+selected); !strings.Contains(out, "eval Red,Blue") {
		t.Errorf("both options must be selected:\n%s", out)
	}
	if out := s.Run(ctx, "select \"Colors\" \"Green\"\n"+selected); !strings.Contains(out, "eval Green\n") && !strings.HasSuffix(strings.TrimSpace(out), "eval Green") {
		t.Errorf("a later select replaces the selection:\n%s", out)
	}
	if out := s.Run(ctx, `select "Size" "Small" "Large"`); !strings.Contains(out, "takes one option") {
		t.Errorf("a single select refuses two options:\n%s", out)
	}
	if out := s.Run(ctx, `select "Size" "Medium"`); !strings.Contains(out, `"Small", "Large"`) {
		t.Errorf("a missing option is refused with the options there are:\n%s", out)
	}
}

// A wait for text can watch one element, so words elsewhere on the page,
// such as instructions that name the goal, do not end it early.
func TestAWaitCanWatchOneProgressBar(t *testing.T) {
	page := `<!doctype html><title>start</title>
<h2>Export</h2><p>Pause the export at 40% to review it.</p>
<button onclick="go()">Begin</button><button onclick="clearInterval(window.t); document.title = 'paused at ' + v">Pause</button>
<div id="bar" role="progressbar" aria-valuenow="0" aria-valuemin="0" aria-valuemax="100">0%</div>
<script>var v = 0; function go() { window.t = setInterval(function () { v += 5; var b = document.getElementById('bar');
  b.setAttribute('aria-valuenow', v); b.textContent = v + '%'; if (v >= 100) clearInterval(window.t); }, 100); }</script>`
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	out := s.Run(ctx, "goto "+servePage(t, page)+"\nview outline")
	ref := ""
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 1 && f[0] == "progress" {
			ref = f[1]
		}
	}
	if ref == "" {
		t.Fatalf("the progress bar has a ref to watch:\n%s", out)
	}
	out = s.Run(ctx, "click \"Begin\"\nwait text \"40%\" in "+ref+"\nclick \"Pause\"\neval document.title")
	var at int
	i := strings.LastIndex(out, "paused at")
	if i < 0 {
		t.Fatalf("the program must reach Pause:\n%s", out)
	}
	// The bar shows 40% for one step of 100 ms of real time: a wait that
	// looked less often would pass it by.
	if _, err := fmt.Sscanf(out[i:], "paused at %d", &at); err != nil || at != 40 {
		t.Errorf("the wait must end when the bar, not the instructions, reads 40%%:\n%s", out)
	}
}

// A program answers the dialog its next step opens: it can decline a confirm
// and type into a prompt. A dialog after that is accepted as before.
func TestAProgramAnswersTheDialogItsStepOpens(t *testing.T) {
	page := `<!doctype html><title>start</title><p id="out"></p>
<button onclick="out.textContent += confirm('Sure?') + ','">Confirm</button>
<button onclick="out.textContent += prompt('Colour?', 'red') + ','">Prompt</button>`
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	out := s.Run(ctx, "goto "+servePage(t, page)+"\ndialog dismiss\nclick \"Confirm\"\ndialog accept \"blue\"\nclick \"Prompt\"\nclick \"Prompt\"\neval out.textContent")
	if !strings.Contains(out, "eval false,blue,red,") {
		t.Errorf("the confirm is declined, the prompt answered, the next prompt gets its default:\n%s", out)
	}
	for _, want := range []string{`confirm: Sure? (dismissed)`, `prompt: Colour? (answered \"blue\")`} {
		if !strings.Contains(out, want) {
			t.Errorf("the reply says how the dialog was answered, lacks %s:\n%s", want, out)
		}
	}
}

// An element that only reacts to clicks is shown by the words on it, so it
// answers to those words even when a title names it otherwise.
func TestAClickableAnswersToTheWordsItShows(t *testing.T) {
	page := `<!doctype html><title>start</title><span title="Opens the order summary" onclick="document.title='hit'">Summary</span>`
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	out := s.Run(ctx, "goto "+servePage(t, page)+"\nclick \"Summary\"\neval document.title")
	if !strings.Contains(out, "eval hit") {
		t.Errorf("the words a view shows must reach the element:\n%s", out)
	}
}

// select picks the option it names even when another shares its value, and
// refuses a select a person cannot use.
func TestSelectPicksTheNamedOptionAndRefusesADisabledSelect(t *testing.T) {
	page := `<!doctype html><title>start</title>
<label for="k">Kind</label><select id="k"><option value="">Pick one</option><option value="x">Alpha</option><option value="x">Beta</option></select>
<fieldset disabled><label for="d">Size</label><select id="d"><option>Small</option><option>Large</option></select></fieldset>`
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	out := s.Run(ctx, "goto "+servePage(t, page)+"\nselect \"Kind\" \"Beta\"\neval document.getElementById('k').selectedOptions[0].text")
	if !strings.Contains(out, "eval Beta") {
		t.Errorf("the named option must be chosen, not the first with its value:\n%s", out)
	}
	out = s.Run(ctx, "select \"Size\" \"Large\"\neval document.getElementById('d').value")
	if !strings.Contains(out, "blocked:") && !strings.Contains(out, "disabled") {
		t.Errorf("a disabled select is refused:\n%s", out)
	}
}

// A switch drawn by its label around an input with no size of its own is
// ticked by clicking the label, as a person does.
func TestASwitchDrawnByItsLabelIsTicked(t *testing.T) {
	page := `<!doctype html><title>start</title><style>
.sw{position:relative;display:inline-block;width:60px;height:30px}
.sw input{opacity:0;width:0;height:0;margin:0}
.sw span{position:absolute;inset:0;background:#ccc;border-radius:15px}</style>
<label class="sw"><input type="checkbox" aria-label="Alerts" onchange="document.title = this.checked ? 'on' : 'off'"><span></span></label>`
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	out := s.Run(ctx, "goto "+servePage(t, page)+"\ncheck \"Alerts\"\neval document.title")
	if !strings.Contains(out, "eval on") {
		t.Errorf("the switch must be ticked through its label:\n%s", out)
	}
}

// A control that lets clicks fall through never receives one: the click goes
// to what holds it, which may do something else entirely.
func TestAClickOnAControlThatTakesNoClicksIsRefused(t *testing.T) {
	page := `<!doctype html><title>start</title><body style="margin:0">
<div onclick="document.title='holder'" style="padding:30px;width:300px">
<button onclick="document.title='hit'" style="pointer-events:none;width:120px;height:40px">Pass</button></div>`
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	out := s.Run(ctx, "goto "+servePage(t, page)+"\nclick \"Pass\"")
	if title := s.Run(ctx, "eval document.title"); strings.Contains(title, "holder") {
		t.Errorf("the click went to what holds the control and was reported as done:\n%s\n%s", out, title)
	}
}

// A layer a person cannot see is refused at once, wherever in the page it
// sits: it is never taken for one that is loading.
func TestAnInvisibleLayerInsideAPaintedPanelIsRefusedAtOnce(t *testing.T) {
	page := rejectButton + `<div style="position:absolute;left:600px;top:0;width:200px;height:200px;background:#fff"><div onclick="document.title='hijacked'" style="position:fixed;left:0;top:0;width:400px;height:200px;opacity:0.001"></div></div>`
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	out := s.Run(ctx, "goto "+servePage(t, page)+"\nclick \"Reject\"")
	if !strings.Contains(out, "invisible layer") || strings.Contains(out, "(waited") {
		t.Errorf("an invisible layer is refused at once and named for what it is:\n%s", out)
	}
}

// A field a page makes read-only for a moment is waited for, as a disabled
// one is, and typed into once it takes text again.
func TestAFieldThatIsReadOnlyForNowIsWaitedFor(t *testing.T) {
	page := `<!doctype html><title>start</title>
<button onclick="var f = document.getElementById('f'); f.readOnly = true; setTimeout(function () { f.readOnly = false; }, 3000);">Lock</button>
<label for="f">Note</label><input id="f">`
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	out := s.Run(ctx, "goto "+servePage(t, page)+"\nclick \"Lock\"\nfill \"Note\" \"later\"\neval document.getElementById('f').value")
	if !strings.Contains(out, "eval later") {
		t.Errorf("the fill must wait for the field to take text:\n%s", out)
	}
	if out := s.Run(ctx, "eval document.getElementById('f').readOnly = true\nfill \"Note\" \"never\""); !strings.Contains(out, "read-only") {
		t.Errorf("a field read-only for good is refused, saying so:\n%s", out)
	}
}

// A name two controls share is ambiguous for a hover too: it is reported with
// the refs to choose from, not settled by aiming at some other text.
func TestAHoverOnASharedNameIsAmbiguous(t *testing.T) {
	page := `<!doctype html><title>start</title>
<button aria-label="Edit">Edit item</button><button aria-label="Edit">Edit item</button><p>Edit</p>`
	s, ctx := newSessionWith(t, session.Config{})
	if out := s.Run(ctx, "goto "+servePage(t, page)+"\nhover \"Edit\""); !strings.Contains(out, "ambiguous") {
		t.Errorf("two controls named alike must be reported as ambiguous:\n%s", out)
	}
}

// scrollsAway is a page that scrolls back to the top the first time it is
// scrolled, as pages that restore a reading position do: the link it moved
// is off the visible page when the click looks for it.
const scrollsAway = `<!doctype html><title>start</title><body style="margin:0">
<div style="height:3000px"></div><a href="#x" onclick="event.preventDefault();document.title='reached'">Far link</a><div style="height:2000px"></div>
<script>
var once = true;
addEventListener('scroll', function () { if (once) { once = false; scrollTo(0, 0); } });
</script>`

func TestALinkThePageScrollsAwayIsBroughtBackAndClicked(t *testing.T) {
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	out := s.Run(ctx, "goto "+servePage(t, scrollsAway)+"\nclick \"Far link\"")
	if strings.Contains(out, "covered-by") || strings.Contains(out, "cannot tell what is under the pointer") {
		t.Fatalf("a link the page scrolled away is not covered by anything:\n%s", out)
	}
	if title := s.Run(ctx, "eval document.title"); !strings.Contains(title, "reached") {
		t.Errorf("the link was not clicked:\n%s\n%s", out, title)
	}
}

// movesUnderALayer moves its link when the pointer arrives and lays an
// invisible layer over the place it moved to: a target that moved is looked
// for again, and found covered, so the click is refused, never hijacked.
const movesUnderALayer = `<!doctype html><title>start</title><body style="margin:0">
<a id="l" href="#x" onclick="event.preventDefault();document.title='reached'" style="position:absolute;left:20px;top:20px;width:200px;height:40px;display:block">Pay</a>
<script>
var l = document.getElementById('l');
l.addEventListener('mousemove', function once() {
	l.removeEventListener('mousemove', once);
	l.style.top = '300px';
	var d = document.createElement('div');
	d.setAttribute('style', 'position:absolute;left:0;top:280px;width:400px;height:100px;opacity:0');
	d.onclick = function () { document.title = 'hijacked'; };
	document.body.appendChild(d);
});
</script>`

func TestATargetThatMovesUnderAnInvisibleLayerIsStillRefused(t *testing.T) {
	s, ctx := newSessionWith(t, session.Config{Policy: program.Policy{AllowEval: true}})
	out := s.Run(ctx, "goto "+servePage(t, movesUnderALayer)+"\nclick \"Pay\"")
	title := s.Run(ctx, "eval document.title")
	if strings.Contains(title, "hijacked") || strings.Contains(title, "reached") {
		t.Fatalf("the click went through:\n%s\n%s", out, title)
	}
	if !strings.Contains(out, "covered-by an invisible layer") {
		t.Errorf("the layer over the moved target is named:\n%s", out)
	}
}

// A gradient heading draws its words in its background (background-clip:
// text, here only in its prefixed form), with a transparent text colour: a
// person reads it, so the read view keeps it.
func TestTextPaintedThroughItsGlyphsIsRead(t *testing.T) {
	s, ctx := newSessionWith(t, session.Config{})
	page := `<!doctype html><title>start</title><style>
.grad{color:transparent;background-image:linear-gradient(90deg,#c00,#00c);-webkit-background-clip:text}
.gone{color:transparent}
</style><h1>Ending risk with <span class="grad">Example Product</span></h1><p>Visible <span class="gone">hidden words</span> end.</p>`
	out := s.Run(ctx, "goto "+servePage(t, page)+"\nview read")
	if !strings.Contains(out, "Ending risk with Example Product") {
		t.Errorf("the gradient words are read:\n%s", out)
	}
	if strings.Contains(out, "hidden words") {
		t.Errorf("transparent text with no background to show stays unread:\n%s", out)
	}
}

// An archive is the page as the browser holds it, in one MHTML document.
func TestAnArchiveIsTheWholePageInOneDocument(t *testing.T) {
	s, ctx := newSessionWith(t, session.Config{})
	_ = s.Run(ctx, "goto "+servePage(t, `<!doctype html><title>Kept</title><style>h1{color:rgb(1,2,3)}</style><h1>Archived heading</h1>`))
	page, err := s.Archive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"MIME-Version", "Archived heading", "rgb(1, 2, 3)"} {
		if !strings.Contains(page, want) {
			t.Errorf("the archive lacks %q", want)
		}
	}
	if out := s.Run(ctx, "view read"); !strings.Contains(out, "Archived heading") {
		t.Errorf("the session goes on as before after an archive:\n%s", out)
	}
}
