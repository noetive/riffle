//go:build integration

package chromium

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/snapshot"
	"github.com/tidwall/gjson"
)

// TestGenerateFuzzCorpus writes raw browser captures of varied pages as seed
// files for the analyzer and view fuzzers. It runs only when
// RIFFLE_GEN_CORPUS names the output directory.
func TestGenerateFuzzCorpus(t *testing.T) {
	out := os.Getenv("RIFFLE_GEN_CORPUS")
	if out == "" {
		t.Skip("set RIFFLE_GEN_CORPUS to regenerate the corpus")
	}
	pages := corpusPages()
	mux := http.NewServeMux()
	for name, h := range pages {
		h := h
		mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(h))
		})
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	p, err := Open(ctx, 1280, 800)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	for name := range pages {
		if err := p.Load(ctx, srv.URL+"/"+name); err != nil {
			t.Fatal(err)
		}
		raw, err := p.call(ctx, "DOMSnapshot.captureSnapshot", map[string]any{
			"computedStyles":                 snapshot.PropNames,
			"includePaintOrder":              true,
			"includeDOMRects":                true,
			"includeBlendedBackgroundColors": true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !gjson.ValidBytes(raw) {
			t.Fatalf("%s: capture is not JSON", name)
		}
		body := "go test fuzz v1\n[]byte(" + strconv.Quote(string(raw)) + ")\n"
		if err := os.WriteFile(filepath.Join(out, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("%s %d bytes\n", name, len(raw))
	}
}

func corpusPages() map[string]string {
	return map[string]string{
		"form":   `<!doctype html><title>Form</title><form action=/x><label for=e>Email</label><input id=e type=email required placeholder="you@example.com"><input type=password aria-label=Pw value=hunter2><select aria-label=Color><option>Red<option selected>Green</select><textarea aria-label=Notes>hi</textarea><label><input type=checkbox checked> Agree</label><input type=radio name=r><input type=range><input type=time><input type=file><button disabled>Off</button><input type=submit value="Send it"></form>`,
		"table":  `<!doctype html><table><caption>Stock</caption><thead><tr><th>Name</th><th>Qty</th></tr></thead><tbody><tr><td>Apple</td><td>3</td></tr><tr><td colspan=2>Total <b>3</b></td></tr></tbody></table><table><tr><td><table><tr><td>nested</td></tr></table></td></tr></table>`,
		"shadow": `<!doctype html><div id=h></div><script>const r=h.attachShadow({mode:'open'});r.innerHTML='<button>In shadow</button><slot></slot>'</script><iframe srcdoc="<p>frame</p>"></iframe><iframe src="about:blank"></iframe>`,
		"svgnav": `<!doctype html><nav><a href=/a><svg width=20 height=20><title>Gear</title><circle r=5 /></svg></a><ul><li><a href=/b>Docs</a><li>Two <a href=/c>link</a></ul></nav><img alt="cat" src="data:image/gif;base64,R0lGODlhAQABAAAAACw="><picture><img src=x.png></picture>`,
		"scroll": `<!doctype html><div tabindex=0 style="height:100px;overflow:auto"><div style="height:1000px">log</div><button>End</button></div><div style="position:fixed;top:0;left:0;width:100%;height:100%;background:rgba(0,0,0,.5)"><div style="background:#fff;margin:100px;padding:20px">We use cookies<button>Accept</button></div></div><div style="position:absolute;left:-9999px">off</div><div style="font-size:0">tiny</div><div style="color:#fff;background:#fff">invisible</div>`,
		"dialog": `<!doctype html><dialog open aria-modal=true><h1>Sure?</h1><button>Yes</button><button>No</button></dialog><details open><summary>More</summary><p>detail</p></details><div role=alert>Saved!</div><div role=tablist><button role=tab aria-selected=true>One</button><button role=tab aria-expanded=false>Two</button></div><button aria-pressed=true>On</button><div contenteditable=true role=textbox aria-label=Editor>text</div>`,
		"text": `<!doctype html><article><h1>Title</h1><h2>Sub</h2><p>See <a href=/x>cities</a>, then <b>bold</b>. And <i>it</i>! (<a href=/y>more</a>) <del>$20</del> <ins>$15</ins></p><blockquote>quote</blockquote><pre>pre  formatted
text</pre><p style="text-overflow:ellipsis;overflow:hidden;white-space:nowrap;width:40px">truncated truncated truncated</p><p lang=ar dir=rtl>مرحبا</p><p>emoji 😀 and 日本語</p><style>p::before{content:"★ "}</style></article>`,
		"nav":    `<!doctype html><header><nav aria-label="Main"><a href=/>Home</a> | <a href=/docs>Docs</a> | <a href=/blog>Blog</a></nav></header><main><h1>Docs</h1><aside>side</aside><section><h2>Intro</h2><p>Text</p></section></main><footer><a href=/terms>Terms</a><a href=/privacy>Privacy</a></footer>`,
		"grid":   `<!doctype html><div style="display:grid;grid-template-columns:1fr 1fr"><div>a</div><div>b</div><div style="grid-column:span 2">wide</div></div><div style="display:flex;flex-direction:column"><button>One</button><button>Two</button></div><div style="column-count:2">col text col text col text col text</div>`,
		"rtl":    `<!doctype html><html dir=rtl lang=ar><body><h1>عنوان</h1><p>نص عربي <a href=/x>رابط</a> ثم المزيد.</p><input aria-label="بحث" value="قيمة"><button>إرسال</button><p lang=he dir=rtl>שלום עולם</p></body></html>`,
		"cookie": `<!doctype html><title>Shop</title><div role=dialog aria-modal=true aria-label="Cookie preferences" style="position:fixed;bottom:0;left:0;right:0;background:#eee;padding:10px"><p>We use cookies</p><button>Accept all</button><button>Reject all</button><button>Manage</button></div><main><h1>Shop</h1><button>Buy now</button><p>Price <s>$30</s> <b>$20</b></p></main>`,
		"list":   `<!doctype html><ul><li>One</li><li>Two<ul><li>Nested <a href=/n>link</a></li></ul></li></ul><ol><li>First</li><li>Second</li></ol><dl><dt>Term</dt><dd>Definition</dd></dl><div role=list><div role=listitem>a</div></div>`,
		"menus":  `<!doctype html><div role=menubar><div role=menuitem aria-haspopup=true aria-expanded=false>File</div><div role=menuitem>Edit</div></div><div role=listbox aria-label=Choices><div role=option aria-selected=true>A</div><div role=option>B</div></div><div role=slider aria-valuenow=5 aria-label=Vol tabindex=0></div><div role=switch aria-checked=true aria-label=Dark tabindex=0></div><div role=combobox aria-expanded=true aria-label=Search></div>`,
		"styled": `<!doctype html><p style="color:red">error text</p><p style="color:#0a0">ok text</p><p style="color:#ccc">muted text</p><button style="background:#06f;color:#fff">Primary</button><button style="background:#eee">Secondary</button><a href=/x style="text-decoration:line-through">old</a><p style="font-size:30px;font-weight:700">Big bold</p><p style="font-style:italic">italic</p><span style="text-transform:uppercase">upper</span>`,
		"hidden": `<!doctype html><div hidden>h</div><div style="display:none"><button>nope</button></div><div style="position:absolute;left:-9999px"><a href=/x>offscreen</a></div><div style="clip:rect(0 0 0 0);position:absolute">clipped</div><div style="height:0;overflow:hidden">zero</div><div style="font-size:0">zerofont</div><div style="text-indent:-9999px">indented</div><input type=hidden value=h><button style="opacity:0">clear</button><button aria-hidden=true>aria</button>`,
		"errors": `<!doctype html><label for=z>Zip</label><input id=z aria-invalid=true aria-errormessage=err value=abc><p id=err role=alert style="color:red">Not valid</p><input required aria-label=Req><input readonly value=ro aria-label=RO><input disabled aria-label=Dis><fieldset disabled><input aria-label=InFs></fieldset>`,
		"media":  `<!doctype html><video controls aria-label=Clip></video><audio controls></audio><canvas width=50 height=50></canvas><object data=x></object><embed src=y><map name=m><area shape=rect coords="0,0,5,5" href=/a alt=Area></map><img usemap=#m alt=Mapped src="data:image/gif;base64,R0lGODlhAQABAAAAACw=">`,
		"huge":   `<!doctype html>` + manyLinks(60),
		"weird": `<!doctype html><p>a&nbsp;b&#8203;c&shy;d&zwj;e</p><p>😀🎉 日本語 العربية é́</p><pre>tab	tab
newline</pre><p>` + longWord(3000) + `</p><button title="Title only"></button><a href="javascript:void(0)">js</a><a href="">empty</a><a>no href</a><input type=button value="Val btn"><input type=image alt="Img btn" src=x><button aria-label="Aria"><span>Inner</span></button>`,
		"frames": `<!doctype html><iframe srcdoc="<button>in</button>" title="Pay"></iframe><iframe sandbox src="about:blank"></iframe><frameset></frameset><div id=h></div><script>const r=h.attachShadow({mode:'closed'});r.innerHTML='<button>closed</button>'</script>`,
		"deep":   `<!doctype html>` + deepNest(60) + `<div hidden>gone</div><div style="display:none">none</div><div style="visibility:hidden">invisible</div><div style="opacity:0">clear</div><template><b>t</b></template><noscript>ns</noscript>`,
	}
}

func deepNest(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		s += "<div><span>"
	}
	s += "<button>deep</button>"
	for i := 0; i < n; i++ {
		s += "</span></div>"
	}
	return s
}

func manyLinks(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<p>Item %d <a href=/i/%d>link %d</a> <button>act %d</button></p>`, i, i, i, i)
	}
	return b.String()
}

func longWord(n int) string { return strings.Repeat("w", n) }
