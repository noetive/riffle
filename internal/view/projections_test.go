package view_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/noetive/riffle/internal/facts"
	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/snapshot"
	"github.com/noetive/riffle/internal/view"
)

func linksOf(prefix string, first, n int, label string, indent string) []string {
	var out []string
	for k := 0; k < n; k++ {
		out = append(out, fmt.Sprintf("%s%s a%d \"%s%d\"", indent, prefix, first+k, label, k))
	}
	return out
}

func sameLines(t *testing.T, got fmt.Stringer, want ...string) {
	t.Helper()
	if g, w := got.String(), strings.Join(want, "\n"); g != w {
		t.Errorf("got:\n%s\nwant:\n%s", g, w)
	}
}

func TestOutlineOfTheCartPageIsExact(t *testing.T) {
	v := compile(newCart(cartOpts{extra: 3}), view.Options{})
	sameLines(t, v,
		`page shop.example/cart "Cart" 1280x800`,
		`nav r1 "Main" collapsed 14 links`,
		`main`,
		`  h1 "Your cart"`,
		`  text "Review the items below before you check out of this shop today."`,
		`  list r2 x5`,
		`    item "Trail shoe 42" "€89" strike "€69" red | spinbutton f1 "qty" ="1" | button b1 "Remove Trail shoe 42"`,
		`    item "Wool sock €12" | spinbutton f2 "qty" ="2" | button b2 "Remove Wool sock"`,
		`    item "Extra item 0 €5" | spinbutton f3 "qty" ="1" | button b3 "Remove Extra item 0"`,
		`    item "Extra item 1 €5" | spinbutton f4 "qty" ="1" | button b4 "Remove Extra item 1"`,
		`    item "Extra item 2 €5" | spinbutton f5 "qty" ="1" | button b5 "Remove Extra item 2"`,
		`  text "Free shipping over €75" green`,
		`  button b6 "Checkout"`,
		`  link a1 "Continue shopping" muted`,
		`footer r3 collapsed 31 links`,
		`unseen 2 nodes`,
	)
	for _, l := range v.Lines {
		if l.Ref == "b6" && (l.Label != "button b6" || l.Body != `"Checkout"` || l.Indent != 1 || l.Key == 0) {
			t.Errorf("line fields: %+v", l)
		}
	}
}

func TestInteractiveListsActionableNodesInReadingOrder(t *testing.T) {
	v := compile(newCart(cartOpts{extra: 1}), view.Options{Projection: view.Interactive})
	want := []string{`page shop.example/cart "Cart" 1280x800`}
	want = append(want, linksOf("link", 2, 14, "Nav ", "")...)
	want = append(want,
		`spinbutton f1 "qty" ="1"`,
		`button b1 "Remove Trail shoe 42"`,
		`spinbutton f2 "qty" ="2"`,
		`button b2 "Remove Wool sock"`,
		`spinbutton f3 "qty" ="1"`,
		`button b3 "Remove Extra item 0"`,
		`button b4 "Checkout"`,
		`link a1 "Continue shopping" muted`,
	)
	want = append(want, linksOf("link", 16, 31, "F", "")...)
	want = append(want, `unseen 2 nodes`)
	sameLines(t, v, want...)
}

func TestReadProjectionIsExactMarkdown(t *testing.T) {
	v := compile(newCart(cartOpts{extra: 1}), view.Options{Projection: view.Read})
	sameLines(t, v,
		`# Your cart`,
		`Review the items below before you check out of this shop today.`,
		`- Trail shoe 42 ~~€89~~ €69`,
		`- Wool sock €12`,
		`- Extra item 0 €5`,
		`Free shipping over €75`,
		`[Continue shopping](/shop)`,
	)
}

func TestReadWithNothingReadableSaysSo(t *testing.T) {
	b := pb.New(100, 100)
	btn := b.El(b.Body(), "button", R(0, 0, 50, 20))
	b.Text(btn, "Go")
	v := view.Compile(facts.Analyze(b.Snapshot()), view.Options{Projection: view.Read})
	sameLines(t, v, "no readable content")
}

func TestReadKeepsModalAheadOfMainAndUsesMainLandmark(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	hd := b.El(body, "header", R(0, 0, 1280, 30))
	b.Text(hd, "Site banner")
	m := b.El(body, "main", R(0, 40, 1280, 300))
	p1 := b.El(m, "p", R(0, 40, 300, 20))
	b.Text(p1, "Body text")
	img := b.El(m, "img", R(0, 70, 40, 40), pb.Attr("alt", "Photo"))
	_ = img
	aside := b.El(body, "aside", R(0, 400, 300, 20))
	b.Text(aside, "Side note")
	wrap := b.El(body, "div", R(0, 0, 1280, 800), pb.Position("fixed"), pb.Fill("rgba(0, 0, 0, 0.6)"))
	dlg := b.El(wrap, "div", R(300, 300, 400, 200), pb.Attr("role", "dialog"), pb.Attr("aria-label", "Notice"), pb.Position("fixed"), pb.Fill("rgb(255, 255, 255)"))
	dp := b.El(dlg, "p", R(310, 310, 300, 20))
	b.Text(dp, "Dialog text")
	v := view.Compile(facts.Analyze(b.Snapshot()), view.Options{Projection: view.Read})
	sameLines(t, v, "Dialog text", "Body text", "![](Photo)")
}

func TestReadListsTablesAndInlineLinks(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	ul := b.El(body, "ul", R(0, 0, 400, 60))
	for k, txt := range []string{"alpha", "beta"} {
		li := b.El(ul, "li", R(0, float64(k*30), 400, 30))
		b.Text(li, txt)
		a := b.El(li, "a", R(100, float64(k*30), 40, 20), pb.Inline(), pb.Attr("href", fmt.Sprintf("/%d", k)))
		b.Text(a, fmt.Sprintf("see %d", k))
	}
	tb := b.El(body, "table", R(0, 100, 400, 60))
	for r, row := range [][]string{{"Name", "Qty"}, {"Sock", "2"}} {
		tr := b.El(tb, "tr", R(0, 100+float64(r*30), 400, 30))
		for k, txt := range row {
			td := b.El(tr, "td", R(float64(k*200), 100+float64(r*30), 200, 30))
			b.Text(td, txt)
		}
	}
	para := b.El(body, "p", R(0, 200, 400, 20))
	b.Text(para, "Read ")
	b.Text(b.El(para, "b", R(40, 200, 40, 20), pb.Inline(), pb.Style(snapshot.FontWeight, "700")), "bold")
	b.Text(b.El(para, "i", R(80, 200, 40, 20), pb.Inline(), pb.Style(snapshot.FontStyle, "italic")), "slanted")
	v := view.Compile(facts.Analyze(b.Snapshot()), view.Options{Projection: view.Read})
	sameLines(t, v,
		"- alpha [see 0](/0)",
		"- beta [see 1](/1)",
		"| Name | Qty |",
		"| --- | --- |",
		"| Sock | 2 |",
		"Read **bold** *slanted*",
	)
}

func TestFindReportsMatchesInReadingOrderWithCount(t *testing.T) {
	c := newCart(cartOpts{extra: 1})
	sameLines(t, compile(c, view.Options{Projection: view.Find, Query: "  ITEM 0 "}),
		`find "ITEM 0" 2 matches`,
		`text "Extra item 0"`,
		`button b3 "Remove Extra item 0"`,
	)
	sameLines(t, compile(c, view.Options{Projection: view.Find, Query: "qty"}),
		`find "qty" 3 matches`,
		`spinbutton f1 "qty" ="1"`,
		`spinbutton f2 "qty" ="2"`,
		`spinbutton f3 "qty" ="1"`,
	)
	sameLines(t, compile(c, view.Options{Projection: view.Find, Query: "zebra"}), `find "zebra" 0 matches`)
	sameLines(t, compile(c, view.Options{Projection: view.Find, Query: "  "}), `find "" needs a non-empty query`)
	long := strings.Repeat("q", 70)
	if got := compile(c, view.Options{Projection: view.Find, Query: long}).Lines[0].Body; !strings.HasPrefix(got, `"`+strings.Repeat("q", 60)+`…" 0`) {
		t.Errorf("long query is clipped to 60 characters: %s", got)
	}
}

func TestFindMatchesOnlyTheTextOfOneNodeOnce(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	btn := b.El(body, "button", R(0, 0, 100, 20))
	b.Text(btn, "Alpha ")
	b.Text(btn, "Alpha again")
	p := b.El(body, "p", R(0, 50, 300, 20))
	b.Text(p, "Alpha one")
	b.Text(p, " and Alpha two")
	v := view.Compile(facts.Analyze(b.Snapshot()), view.Options{Projection: view.Find, Query: "alpha"})
	sameLines(t, v,
		`find "alpha" 3 matches`,
		`button b1 "Alpha Alpha again"`,
		`text "Alpha one"`,
		`text "and Alpha two"`,
	)
}

func TestExpandAndTableOfARepeatedList(t *testing.T) {
	p := newCart(cartOpts{extra: 1}).page()
	base := view.Compile(p, view.Options{})
	ex := view.Compile(p, view.Options{Projection: view.Expand, Target: "r2", Previous: base.Refs})
	sameLines(t, ex,
		`list r2 x3`,
		`  item "Trail shoe 42" "€89" strike "€69" red | spinbutton f1 "qty" ="1" | button b1 "Remove Trail shoe 42"`,
		`  item "Wool sock €12" | spinbutton f2 "qty" ="2" | button b2 "Remove Wool sock"`,
		`  item "Extra item 0 €5" | spinbutton f3 "qty" ="1" | button b3 "Remove Extra item 0"`,
	)
	tb := view.Compile(p, view.Options{Projection: view.Table, Target: "r2", Previous: base.Refs})
	sameLines(t, tb, "Trail shoe 42\t€89\t€69\tqty", "Wool sock\t€12\tqty", "Extra item 0\t€5\tqty")
	// Refs handed out by expanding belong to the same nodes as in the base view.
	for ref, id := range base.Refs {
		if got, ok := ex.Resolve(ref); ok && got != id {
			t.Errorf("ref %s moved from %d to %d", ref, id, got)
		}
	}
}

func TestExpandAndTableAnswerWhenTheTargetIsGone(t *testing.T) {
	p := newCart(cartOpts{}).page()
	known := map[string]int64{"r7": 987654321}
	for _, pr := range []view.Projection{view.Expand, view.Table} {
		got := view.Compile(p, view.Options{Projection: pr, Target: "r7", Previous: known}).String()
		if got != "r7 is no longer on the page; compile a fresh view" {
			t.Errorf("projection %v: %q", pr, got)
		}
		got = view.Compile(p, view.Options{Projection: pr, Target: "zz"}).String()
		if got != "unknown ref zz; refs come from the latest view" {
			t.Errorf("projection %v: %q", pr, got)
		}
	}
}

func TestTargetThatIsATextNodeIsNoLongerAnElement(t *testing.T) {
	c := newCart(cartOpts{})
	p := c.page()
	known := map[string]int64{"r9": p.Snap.Backend[c.price]}
	got := view.Compile(p, view.Options{Projection: view.Table, Target: "r9", Previous: known}).String()
	if got != "r9 is no longer on the page; compile a fresh view" {
		t.Errorf("got %q", got)
	}
}

func TestTableOfARegionWithoutCellsAndExpandOfAnEmptyOne(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	empty := b.El(body, "div", R(0, 0, 100, 100))
	btn := b.El(body, "button", R(0, 200, 100, 20))
	b.Text(btn, "Go")
	p := facts.Analyze(b.Snapshot())
	known := map[string]int64{"r5": p.Snap.Backend[empty]}
	got := view.Compile(p, view.Options{Projection: view.Table, Target: "r5", Previous: known}).String()
	if got != "r5 has no rows" {
		t.Errorf("got %q", got)
	}
	got = view.Compile(p, view.Options{Projection: view.Expand, Target: "r5", Previous: known}).String()
	if got != "r5 has nothing visible" {
		t.Errorf("got %q", got)
	}
}

func TestTableRowsFollowVerticalPositionThenHorizontal(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	g := b.El(body, "div", R(0, 0, 600, 100))
	// Cells added in scrambled order; rows differ by less than half a row in Y.
	add := func(x, y float64, txt string) {
		b.Text(b.El(g, "div", R(x, y, 100, 20)), txt)
	}
	add(300, 40, "c2")
	add(0, 1, "a1")
	add(200, 0, "b1")
	add(0, 40, "a2")
	add(100, 41, "b2")
	p := facts.Analyze(b.Snapshot())
	known := map[string]int64{"r1": p.Snap.Backend[g]}
	got := view.Compile(p, view.Options{Projection: view.Table, Target: "r1", Previous: known})
	sameLines(t, got, "a1\tb1", "a2\tb2\tc2")
}

func TestHeaderShowsScrollPositionAndSkipsEmptyTitle(t *testing.T) {
	b := pb.New(1280, 800)
	s := b.Snapshot()
	s.URL = "https://a.example/p/q%20r?x=1&y=2#frag"
	s.Title = ""
	s.ContentH = 2000
	s.ScrollY = 150.9
	got := view.Compile(facts.Analyze(s), view.Options{}).Lines[0]
	if want := `page a.example/p/q%20r?x=1&y=2 1280x800 scroll 150/1200`; got.String() != want {
		t.Errorf("got %q want %q", got, want)
	}
	s.ContentH = 801
	if got := view.Compile(facts.Analyze(s), view.Options{}).Lines[0].String(); strings.Contains(got, "scroll") {
		t.Errorf("one pixel of room is no scrolling: %s", got)
	}
	s.ContentH = 802
	if got := view.Compile(facts.Analyze(s), view.Options{}).Lines[0].String(); !strings.Contains(got, "scroll 150/2") {
		t.Errorf("two pixels of room is scrolling: %s", got)
	}
	s.URL = "not a url"
	s.Title = strings.Repeat("t", 90)
	got2 := view.Compile(facts.Analyze(s), view.Options{}).Lines[0].Body
	if want := `not a url "` + strings.Repeat("t", 80) + `…" 1280x800`; got2 != want {
		t.Errorf("got %q want %q", got2, want)
	}
}

func TestUnseenLineIsSingularForOne(t *testing.T) {
	b := pb.New(1280, 800)
	u := b.El(b.Body(), "div", R(-5000, 0, 100, 20))
	b.Text(u, "off screen")
	v := view.Compile(facts.Analyze(b.Snapshot()), view.Options{})
	if got := v.Lines[len(v.Lines)-1].String(); got != "unseen 1 node" {
		t.Errorf("got %q", got)
	}
}

func TestControlKindsShowTheirFacts(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	b.El(body, "input", R(0, 100, 100, 20), pb.Attr("type", "password"), pb.Value("hunter2"), pb.Attr("aria-label", "Pass"))
	b.El(body, "input", R(0, 130, 100, 20), pb.Attr("type", "text"), pb.Value("hello"), pb.Attr("aria-label", "Name"))
	b.El(body, "input", R(0, 160, 100, 20), pb.Attr("type", "checkbox"), pb.Ticked(), pb.Value("on"), pb.Attr("aria-label", "Agree"))
	b.El(body, "iframe", R(0, 200, 100, 100), pb.Attr("title", "Pay"), pb.Attr("src", "https://pay.example/x"))
	b.El(body, "img", R(0, 310, 50, 50), pb.Attr("alt", "Logo pic"))
	b.El(body, "img", R(0, 370, 50, 50))
	cl := b.El(body, "div", R(0, 430, 100, 20), pb.Clickable())
	b.Text(cl, "Tap me")
	long := b.El(body, "div", R(0, 460, 100, 20), pb.Clickable())
	for i := 0; i < 61; i++ {
		b.Text(long, "y")
	}
	bt := b.El(body, "button", R(0, 500, 100, 20), pb.Attr("disabled", ""))
	b.Text(bt, "Off")
	sameLines(t, view.Compile(facts.Analyze(b.Snapshot()), view.Options{}),
		`page shop.example/cart "Cart" 1280x800`,
		`textbox f1 "Pass" =filled password`,
		`textbox f2 "Name" ="hello"`,
		`checkbox f3 "Agree" checked`,
		`frame "Pay" content-not-shown`,
		`img "Logo pic"`,
		`clickable b1 "Tap me"`,
		`clickable b2`,
		`  text "`+strings.Repeat("y", 61)+`"`,
		`button b3 "Off" disabled`,
	)
}

func TestControlFactsAreTrailingTags(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	b.Text(b.El(body, "label", R(0, 0, 80, 20), pb.Inline()), "Email address")
	b.El(body, "input", R(100, 0, 100, 20), pb.Attr("type", "text"), pb.Attr("aria-label", "Mail"))
	e := b.El(body, "div", R(100, 25, 200, 20), pb.Style(snapshot.Color, red), pb.Attr("id", "err"))
	b.Text(e, "Not valid")
	zip := b.El(body, "input", R(100, 50, 100, 20), pb.Attr("type", "text"), pb.Attr("aria-label", "Zip"), pb.Attr("aria-invalid", "true"), pb.Attr("aria-errormessage", "err"))
	b.Text(b.El(body, "div", R(0, 100, 100, 20), pb.Attr("role", "tab"), pb.Attr("aria-selected", "true")), "Tab1")
	b.Text(b.El(body, "div", R(0, 130, 50, 20), pb.Style(snapshot.TextOverflow, "ellipsis"), pb.Scroll(R(0, 130, 200, 20)), pb.Attr("role", "button")), "Truncated text here")
	b.Text(b.El(body, "a", R(0, 160, 100, 20), pb.Attr("href", "/s"), pb.Style(snapshot.TextDecorationLine, "line-through"), pb.Style(snapshot.Color, green)), "Old link")
	b.Text(b.El(body, "button", R(0, 190, 100, 20), pb.Attr("aria-label", "Close dialog")), "x")
	p := facts.Analyze(b.Snapshot())
	sameLines(t, view.Compile(p, view.Options{Focus: p.Snap.Backend[zip]}),
		`page shop.example/cart "Cart" 1280x800`,
		`text "Email address"`,
		`textbox f1 "Mail" error="Not valid"`,
		`text "Not valid" red`,
		`textbox f2 "Zip" invalid error="Not valid" focused`,
		`tab b1 "Tab1" selected`,
		`button b2 "Truncated text here" truncated`,
		`link a1 "Old link" strike green`,
		`button b3 "Close dialog" name-differs`,
	)
}

func TestInteractiveWithoutControlsSaysSo(t *testing.T) {
	b := pb.New(100, 100)
	b.Text(b.El(b.Body(), "p", R(0, 0, 50, 20)), "Nothing to click")
	sameLines(t, view.Compile(facts.Analyze(b.Snapshot()), view.Options{Projection: view.Interactive}),
		`page shop.example/cart "Cart" 100x100`,
		`interactive 0 actionable nodes`,
	)
}

func TestSelectShowsEveryOptionItHasChosenEvenWhenTheOptionsAreNotLaidOut(t *testing.T) {
	b := pb.New(1280, 800)
	sel := b.El(b.Body(), "select", R(0, 0, 100, 20), pb.Attr("aria-label", "Toppings"), pb.Attr("multiple", ""))
	for i, c := range []struct {
		label string
		on    bool
	}{{"Ham", true}, {"Cheese", false}, {"Olives", true}} {
		opts := []pb.Opt{pb.NotLaid()}
		if c.on {
			opts = append(opts, pb.Ticked())
		}
		o := b.El(sel, "option", R(0, float64(20*i), 100, 20), opts...)
		b.Text(o, c.label)
	}
	out := view.Compile(facts.Analyze(b.Snapshot()), view.Options{Projection: view.Interactive}).String()
	if !strings.Contains(out, `"Toppings" ="Ham, Olives"`) {
		t.Errorf("a select must list what it has chosen, in order:\n%s", out)
	}
}

func TestEditableRegionShowsItsText(t *testing.T) {
	b := pb.New(1280, 800)
	e := b.El(b.Body(), "div", R(0, 0, 100, 20), pb.Attr("contenteditable", "true"), pb.Attr("role", "textbox"), pb.Attr("aria-label", "Editor"))
	b.Text(e, "typed words")
	off := b.El(b.Body(), "div", R(0, 30, 100, 20), pb.Attr("contenteditable", "false"), pb.Attr("role", "textbox"), pb.Attr("aria-label", "Locked"))
	b.Text(off, "fixed")
	out := view.Compile(facts.Analyze(b.Snapshot()), view.Options{Projection: view.Interactive}).String()
	if !strings.Contains(out, `"Editor" ="typed words"`) {
		t.Errorf("an editable region shows its content:\n%s", out)
	}
	if strings.Contains(out, `"Locked" =`) {
		t.Errorf("contenteditable=false is not editable:\n%s", out)
	}
}

func TestReadTableLooksThroughRowGroupsButNotThroughOtherLists(t *testing.T) {
	b := pb.New(1280, 800)
	tb := b.El(b.Body(), "table", R(0, 0, 200, 100))
	body := b.El(tb, "tbody", R(0, 0, 200, 100))
	for i, row := range [][2]string{{"Name", "Qty"}, {"Apple", "3"}} {
		tr := b.El(body, "tr", R(0, float64(20*i), 200, 20))
		for j, c := range row {
			td := b.El(tr, "td", R(float64(100*j), float64(20*i), 100, 20))
			b.Text(td, c)
		}
	}
	out := view.Compile(facts.Analyze(b.Snapshot()), view.Options{Projection: view.Read}).String()
	for _, want := range []string{"| Name | Qty |", "| --- | --- |", "| Apple | 3 |"} {
		if !strings.Contains(out, want) {
			t.Errorf("read table lacks %q:\n%s", want, out)
		}
	}
}
