package view_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/noetive/riffle/internal/facts"
	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/view"
)

func hasLine(v *view.View, want string) bool {
	for _, l := range strings.Split(v.String(), "\n") {
		if l == want {
			return true
		}
	}
	return false
}

func TestElisionLineNamesCountAndEnclosingRegion(t *testing.T) {
	c := newCart(cartOpts{})
	p := c.page()

	// Eight lines of main are dropped: plural noun, ref of main.
	v := view.Compile(p, view.Options{Budget: 38})
	if !hasLine(v, "  … 8 more lines r4") {
		t.Fatalf("plural elision inside main:\n%s", v)
	}
	if id, ok := v.Resolve("r4"); !ok || id != p.Snap.Backend[c.main] {
		t.Errorf("r4 must name the enclosing region (main), got %d %v", id, ok)
	}

	// A single dropped line is singular and names the footer.
	v = view.Compile(p, view.Options{Budget: 80})
	if !hasLine(v, "… 1 more line r3") {
		t.Fatalf("singular elision at top level:\n%s", v)
	}
	if strings.Contains(v.String(), "1 more lines") {
		t.Errorf("one line is not plural:\n%s", v)
	}
	if id, ok := v.Resolve("r3"); !ok || id != p.Snap.Backend[c.footer] {
		t.Errorf("r3 must name the dropped footer, got %d %v", id, ok)
	}

	// Inside a list the elision sits at the list's depth and names the list.
	v = view.Compile(p, view.Options{Budget: 86})
	if !hasLine(v, "    … 2 more lines r2") {
		t.Fatalf("elision inside the list:\n%s", v)
	}
	if id, ok := v.Resolve("r2"); !ok || id != p.Snap.Backend[c.list] {
		t.Errorf("r2 must name the list, got %d %v", id, ok)
	}
}

func TestViewAtExactlyItsOwnTokensIsNotElided(t *testing.T) {
	p := newCart(cartOpts{cookie: true}).page()
	full := view.Compile(p, view.Options{})
	at := view.Compile(p, view.Options{Budget: full.Tokens(), Previous: full.Refs})
	if at.String() != full.String() {
		t.Errorf("a budget equal to the size must keep everything:\n%s", at)
	}
	under := view.Compile(p, view.Options{Budget: full.Tokens() - 1, Previous: full.Refs})
	if !strings.Contains(under.String(), " more line") {
		t.Errorf("one token under must elide something:\n%s", under)
	}
	if under.Tokens() > full.Tokens()-1 {
		t.Errorf("budget exceeded: %d > %d", under.Tokens(), full.Tokens()-1)
	}
}

func TestTokensAreFourRunesRoundedUpPlusNewlines(t *testing.T) {
	line := func(indent int, label, body string) view.Line {
		return view.Line{Indent: indent, Label: label, Body: body}
	}
	cases := []struct {
		name  string
		lines []view.Line
		want  int
	}{
		{"empty", nil, 0},
		{"three runes plus newline fill one token", []view.Line{line(0, "", "abc")}, 1},
		{"four runes spill into a second", []view.Line{line(0, "", "abcd")}, 2},
		{"label and body join with a space", []view.Line{line(0, "text", `"abc"`)}, 3},
		{"indent counts two per level", []view.Line{line(2, "text", `"abc"`)}, 4},
		{"every line costs a newline", []view.Line{line(0, "", "abcdef"), line(0, "", "abcdef"), line(0, "", "abcdef")}, 6},
		{"multibyte runes count once", []view.Line{line(0, "", "€€€")}, 1},
	}
	for _, c := range cases {
		v := &view.View{Lines: c.lines}
		if got := v.Tokens(); got != c.want {
			t.Errorf("%s: Tokens = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestTokensOfACompiledViewMatchTheRenderedText(t *testing.T) {
	empty := view.Compile(facts.Analyze(pb.New(100, 100).Snapshot()), view.Options{})
	if empty.String() != `page shop.example/cart "Cart" 100x100` || empty.Tokens() != 10 {
		t.Errorf("%q = %d tokens, want 10", empty.String(), empty.Tokens())
	}
	for _, o := range []cartOpts{{}, {cookie: true, extra: 5}} {
		v := compile(newCart(o), view.Options{})
		want := (utf8.RuneCountInString(v.String()) + 1 + 3) / 4
		if got := v.Tokens(); got != want {
			t.Errorf("Tokens = %d, rendered text implies %d", got, want)
		}
	}
}

func TestRefOfAnotherKindIsNotReusedAndItsNumberStaysRetired(t *testing.T) {
	c := newCart(cartOpts{})
	p := c.page()
	shop, checkout := p.Snap.Backend[c.shop], p.Snap.Backend[c.checkout]

	// The link was a button before; "b1" must not be handed to a link.
	v := view.Compile(p, view.Options{Previous: map[string]int64{"b1": shop}})
	shopRef := ""
	for ref, id := range v.Refs {
		if id == shop {
			shopRef = ref
		}
	}
	if !strings.HasPrefix(shopRef, "a") {
		t.Errorf("link kept a button ref: %q", shopRef)
	}
	if id, ok := v.Refs["b1"]; ok {
		t.Errorf("b1 was retired with the node it named, now %d", id)
	}

	// The button was a link before: it gets a fresh button ref, and the
	// link number it had is not handed to the next link.
	v = view.Compile(p, view.Options{Previous: map[string]int64{"a7": checkout}})
	ref := ""
	for r, id := range v.Refs {
		if id == checkout {
			ref = r
		}
	}
	if !strings.HasPrefix(ref, "b") {
		t.Errorf("button kept a link ref: %q", ref)
	}
	if got := v.Refs["a8"]; got != shop {
		t.Errorf("new links continue after the highest previous number; a8 = %d, want the shop link", got)
	}
}

func TestSameKindKeepsItsPreviousRef(t *testing.T) {
	c := newCart(cartOpts{})
	p := c.page()
	checkout := p.Snap.Backend[c.checkout]
	v := view.Compile(p, view.Options{Previous: map[string]int64{"b41": checkout}})
	if id, ok := v.Resolve("b41"); !ok || id != checkout {
		t.Errorf("b41 = %d %v, want the checkout button", id, ok)
	}
	for ref := range v.Refs {
		if n, err := strconv.Atoi(strings.TrimPrefix(ref, "b")); strings.HasPrefix(ref, "b") && ref != "b41" && (err != nil || n <= 41) {
			t.Errorf("fresh buttons must number past the previous maximum, got %s", ref)
		}
	}
}

func TestResolveOnlyKnowsRefsHandedOutInThisView(t *testing.T) {
	v := compile(newCart(cartOpts{}), view.Options{})
	if _, ok := v.Resolve("b999"); ok {
		t.Error("unknown ref resolved")
	}
	if _, ok := v.Resolve(""); ok {
		t.Error("empty ref resolved")
	}
}

func TestTargetRefKnownOnlyFromPreviousViewStillResolves(t *testing.T) {
	c := newCart(cartOpts{})
	p := c.page()
	nav := p.Snap.Backend[c.nav]

	ex := view.Compile(p, view.Options{Projection: view.Expand, Target: "r9", Previous: map[string]int64{"r9": nav}})
	if n := strings.Count(ex.String(), "link a"); n != 14 {
		t.Errorf("a ref only the previous view knew must reach its node (%d links):\n%s", n, ex)
	}

	for _, target := range []string{"r8", "r99"} {
		bad := view.Compile(p, view.Options{Projection: view.Expand, Target: target, Previous: map[string]int64{"r9": nav}})
		if want := fmt.Sprintf("unknown ref %s", target); !strings.Contains(bad.String(), want) {
			t.Errorf("%s: want %q in:\n%s", target, want, bad)
		}
	}
}

func TestHigherPaintedModalComesFirstAndCoversTheOther(t *testing.T) {
	build := func(alphaPaint, betaPaint int32) *view.View {
		b := pb.New(1280, 800)
		m := b.El(b.Body(), "main", R(0, 0, 1280, 800))
		b.Text(m, "Page body")
		for _, d := range []struct {
			name  string
			paint int32
		}{{"Alpha", alphaPaint}, {"Beta", betaPaint}} {
			w := b.El(b.Body(), "div", R(0, 0, 1280, 800), pb.Position("fixed"), pb.Fill("rgba(0, 0, 0, 0.6)"), pb.Paint(d.paint))
			dlg := b.El(w, "div", R(300, 200, 500, 300), pb.Attr("role", "dialog"), pb.Position("fixed"), pb.Fill("rgb(255, 255, 255)"), pb.Paint(d.paint+1))
			h := b.El(dlg, "h2", R(310, 210, 100, 20), pb.Paint(d.paint+2))
			b.Text(h, d.name)
			bt := b.El(dlg, "button", R(310, 400, 80, 30), pb.Paint(d.paint+2))
			b.Text(bt, "Close "+d.name)
		}
		return view.Compile(facts.Analyze(b.Snapshot()), view.Options{})
	}
	// Document order must not decide: swap which dialog paints on top.
	for _, c := range []struct {
		alpha, beta  int32
		top, beneath string
	}{{500, 100, "Alpha", "Beta"}, {100, 500, "Beta", "Alpha"}} {
		v := build(c.alpha, c.beta)
		if !hasLine(v, fmt.Sprintf(`modal d1 %q covers=page`, c.top)) {
			t.Errorf("top dialog %s must be d1:\n%s", c.top, v)
		}
		if !hasLine(v, fmt.Sprintf(`modal d2 %q covers=page covered-by=d1`, c.beneath)) {
			t.Errorf("%s must be covered by d1:\n%s", c.beneath, v)
		}
		if indexOf(v, `"`+c.top+`"`) > indexOf(v, `"`+c.beneath+`"`) {
			t.Errorf("higher paint must be listed first:\n%s", v)
		}
		if !hasLine(v, "main covered-by=d1") {
			t.Errorf("page is covered by the top modal:\n%s", v)
		}
	}
}

// pageWithText is a header, one button and one text line of the given length.
func pageWithText(n int) *facts.Page {
	b := pb.New(100, 100)
	m := b.El(b.Body(), "main", R(0, 0, 100, 100))
	bt := b.El(m, "button", R(0, 0, 40, 20))
	b.Text(bt, "Go")
	p := b.El(m, "p", R(0, 30, 90, 20))
	b.Text(p, strings.Repeat("x", n))
	return facts.Analyze(b.Snapshot())
}

func TestBudgetEqualToTheSizeKeepsEverythingWhateverTheRemainder(t *testing.T) {
	for n := 1; n <= 12; n++ {
		p := pageWithText(n)
		full := view.Compile(p, view.Options{})
		at := view.Compile(p, view.Options{Budget: full.Tokens(), Previous: full.Refs})
		if at.String() != full.String() {
			t.Errorf("text of %d: budget %d (its exact size) elided:\n%s", n, full.Tokens(), at)
		}
	}
}

func TestNoBudgetIsWasted(t *testing.T) {
	pages := map[string]*facts.Page{"cart": newCart(cartOpts{}).page(), "cookie": newCart(cartOpts{cookie: true, extra: 4}).page()}
	for n := 1; n <= 12; n++ {
		pages[fmt.Sprintf("text%d", n)] = pageWithText(n)
	}
	for name, p := range pages {
		full := view.Compile(p, view.Options{})
		for b := 5; b < full.Tokens(); b++ {
			smaller := view.Compile(p, view.Options{Budget: b, Previous: full.Refs})
			larger := view.Compile(p, view.Options{Budget: b + 1, Previous: full.Refs})
			if larger.Tokens() <= b && smaller.String() != larger.String() {
				t.Fatalf("%s: budget %d holds the view made for %d but dropped more:\n%s\n--- versus\n%s", name, b, b+1, smaller, larger)
			}
		}
	}
}
