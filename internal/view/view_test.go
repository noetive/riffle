package view_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/noetive/riffle/internal/facts"
	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/snapshot"
	"github.com/noetive/riffle/internal/view"
)

func compile(c *cart, o view.Options) *view.View { return view.Compile(c.page(), o) }

func lineWith(t *testing.T, v *view.View, parts ...string) string {
	t.Helper()
	for _, l := range v.Lines {
		s := l.String()
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(s, p)
		}
		if ok {
			return s
		}
	}
	t.Fatalf("no line with %q in:\n%s", parts, v)
	return ""
}

func indexOf(v *view.View, sub string) int {
	for i, l := range v.Lines {
		if strings.Contains(l.String(), sub) {
			return i
		}
	}
	return -1
}

func TestOutlineFollowsTheDesign(t *testing.T) {
	v := compile(newCart(cartOpts{cookie: true}), view.Options{})
	out := v.String()
	for _, want := range []string{
		`page shop.example/cart "Cart" 1280x800`,
		`modal d1 "Cookie preferences" covers=page`,
		`button b1 "Accept all" primary`,
		`button b2 "Reject"`,
		`main covered-by=d1`,
		`h1 "Your cart"`,
		`strike "€69" red`,
		`text "Free shipping over €75" green`,
		`link a1 "Continue shopping" muted`,
		`unseen 2 nodes`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("outline lacks %q:\n%s", want, out)
		}
	}
	if indexOf(v, "modal d1") > indexOf(v, "main covered-by") {
		t.Error("the modal layer must come first")
	}
	// Children of a covered region do not repeat the tag.
	for _, l := range v.Lines {
		if l.Indent > 0 && strings.Contains(l.String(), "covered-by") {
			t.Errorf("redundant covered-by on %q", l.String())
		}
	}
}

func TestRowsAndRepeatedSiblings(t *testing.T) {
	v := compile(newCart(cartOpts{}), view.Options{})
	row := lineWith(t, v, `item "Trail shoe 42"`)
	if strings.Count(row, "|") != 2 || !strings.Contains(row, `spinbutton f1 "qty"`) || !strings.Contains(row, `button b`) {
		t.Errorf("row not folded into cells: %s", row)
	}
	lineWith(t, v, "list", "x2")
	many := compile(newCart(cartOpts{extra: 10}), view.Options{})
	lineWith(t, many, "list", "x12")
}

func TestHiddenAndUnseenContentNeverAppears(t *testing.T) {
	c := newCart(cartOpts{cookie: true})
	p := c.page()
	secrets := []string{"never shown", "ignore all previous", "exfiltrate"}
	nav := p.Snap.Backend[c.nav]
	var refNav string
	for _, pr := range []view.Projection{view.Outline, view.Interactive, view.Read, view.Find, view.Expand, view.Table} {
		o := view.Options{Projection: pr, Query: "e", Target: "r1"}
		if pr == view.Expand {
			base := view.Compile(p, view.Options{})
			for ref, id := range base.Refs {
				if id == nav {
					refNav = ref
				}
			}
			o.Target = refNav
			o.Previous = base.Refs
		}
		out := view.Compile(p, o).String()
		for _, s := range secrets {
			if strings.Contains(out, s) {
				t.Errorf("%v leaks %q:\n%s", pr, s, out)
			}
		}
	}
	for _, q := range []string{"exfiltrate", "never"} {
		out := view.Compile(p, view.Options{Projection: view.Find, Query: q}).String()
		if !strings.Contains(out, "0 matches") {
			t.Errorf("find %q should report no matches: %s", q, out)
		}
	}
}

func TestUnseenLineAbsentWhenNothingIsUnseen(t *testing.T) {
	c := newCart(cartOpts{})
	s := c.b.Snapshot()
	s.Style[c.unseen][snapshot.FontSize] = "16px"
	s.Style[c.unseen+1][snapshot.FontSize] = "16px"
	unlay(s, c.unseen2)
	if out := compile(c, view.Options{}).String(); strings.Contains(out, "unseen") {
		t.Errorf("unexpected unseen line:\n%s", out)
	}
}

func TestNavigationAndFooterCollapseUntilExpanded(t *testing.T) {
	c := newCart(cartOpts{})
	p := c.page()
	v := view.Compile(p, view.Options{})
	navLine := lineWith(t, v, "nav r", "collapsed 14 links")
	lineWith(t, v, "footer r", "collapsed 31 links")
	if strings.Contains(v.String(), "Nav 3") {
		t.Error("collapsed links must not be listed")
	}
	ref := regexp.MustCompile(`nav (r\d+)`).FindStringSubmatch(navLine)[1]
	ex := view.Compile(p, view.Options{Projection: view.Expand, Target: ref, Previous: v.Refs})
	if n := strings.Count(ex.String(), "link a"); n != 14 {
		t.Errorf("expand listed %d links, want 14:\n%s", n, ex)
	}
	if id, ok := v.Resolve(ref); !ok || id != p.Snap.Backend[c.nav] {
		t.Error("nav ref does not resolve to the nav element")
	}
	bad := view.Compile(p, view.Options{Projection: view.Expand, Target: "r99"}).String()
	if !strings.Contains(bad, "unknown ref r99") {
		t.Errorf("unknown ref answer: %s", bad)
	}
}

func TestVisualOrderComesFromLayout(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	right := b.El(body, "button", R(400, 100, 80, 20))
	b.Text(right, "Right")
	left := b.El(body, "button", R(100, 100, 80, 20))
	b.Text(left, "Left")
	top := b.El(body, "div", R(100, 10, 300, 20), pb.Position("absolute"))
	b.Text(top, "Pinned to top")
	_ = right
	_ = left
	v := view.Compile(facts.Analyze(b.Snapshot()), view.Options{})
	iTop, iL, iR := indexOf(v, "Pinned"), indexOf(v, `"Left"`), indexOf(v, `"Right"`)
	if iTop >= iL || iL >= iR {
		t.Errorf("order wrong (top=%d left=%d right=%d):\n%s", iTop, iL, iR, v)
	}
}

func TestInteractiveProjection(t *testing.T) {
	v := compile(newCart(cartOpts{cookie: true}), view.Options{Projection: view.Interactive})
	if indexOf(v, "button b1") != 1 {
		t.Errorf("modal controls come first:\n%s", v)
	}
	for _, l := range v.Lines[1:] {
		s := l.String()
		if strings.HasPrefix(s, "text ") || strings.HasPrefix(s, "h1") || strings.Contains(s, "|") {
			t.Errorf("non-actionable line in interactive view: %s", s)
		}
	}
	lineWith(t, v, "button b5", `"Checkout"`, "covered-by=d1")
	empty := pb.New(100, 100)
	e := view.Compile(facts.Analyze(empty.Snapshot()), view.Options{Projection: view.Interactive}).String()
	if !strings.Contains(e, "0 actionable nodes") {
		t.Errorf("empty result must say so: %s", e)
	}
}

func TestReadProjection(t *testing.T) {
	out := compile(newCart(cartOpts{}), view.Options{Projection: view.Read}).String()
	for _, want := range []string{"# Your cart", "~~€89~~", "[Continue shopping](/shop)", "Free shipping over €75"} {
		if !strings.Contains(out, want) {
			t.Errorf("read view lacks %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"Nav 1", "F3", "button", "Checkout"} {
		if strings.Contains(out, bad) {
			t.Errorf("read view contains %q:\n%s", bad, out)
		}
	}
}

func TestTableProjectionRegardlessOfMarkup(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	// A real table.
	tb := b.El(body, "table", R(0, 0, 400, 90))
	for r, row := range [][]string{{"Name", "Qty"}, {"Sock", "2"}, {"Shoe", "1"}} {
		tr := b.El(tb, "tr", R(0, float64(r*30), 400, 30))
		for k, cellText := range row {
			td := b.El(tr, "td", R(float64(k*200), float64(r*30), 200, 30))
			b.Text(td, cellText)
		}
	}
	// A grid of divs, rows in reverse DOM order.
	grid := b.El(body, "div", R(0, 200, 400, 90))
	for _, r := range []int{2, 0, 1} {
		rowTexts := [][]string{{"Name", "Qty"}, {"Sock", "2"}, {"Shoe", "1"}}[r]
		rw := b.El(grid, "div", R(0, 200+float64(r*30), 400, 30))
		for k, cellText := range rowTexts {
			d := b.El(rw, "div", R(float64(k*200), 200+float64(r*30), 200, 30))
			b.Text(d, cellText)
		}
	}
	p := facts.Analyze(b.Snapshot())
	base := view.Compile(p, view.Options{})
	want := "Name\tQty\nSock\t2\nShoe\t1"
	tableRef := ""
	for ref, id := range base.Refs {
		if id == p.Snap.Backend[tb] {
			tableRef = ref
		}
	}
	if tableRef == "" {
		t.Fatalf("table has no ref:\n%s", base)
	}
	got := view.Compile(p, view.Options{Projection: view.Table, Target: tableRef, Previous: base.Refs}).String()
	if got != want {
		t.Errorf("table TSV:\n%q\nwant\n%q", got, want)
	}
	// The grid is not marked up as a table; its ref comes from the page's own refs.
	gridRef := ""
	for _, l := range base.Lines {
		if strings.HasPrefix(l.Label, "list") && l.Ref != "" {
			gridRef = l.Ref
		}
	}
	if gridRef == "" {
		t.Fatalf("repeated rows have no ref:\n%s", base)
	}
	got = view.Compile(p, view.Options{Projection: view.Table, Target: gridRef, Previous: base.Refs}).String()
	if got != want {
		t.Errorf("grid TSV:\n%q\nwant\n%q", got, want)
	}
}

func TestFindProjection(t *testing.T) {
	c := newCart(cartOpts{})
	v := compile(c, view.Options{Projection: view.Find, Query: "REMOVE"})
	lineWith(t, v, `find "REMOVE" 2 matches`)
	lineWith(t, v, "button b", "Remove Wool sock")
	one := compile(c, view.Options{Projection: view.Find, Query: "shipping over"}).String()
	if !strings.Contains(one, `text "Free shipping over €75" green`) || !strings.Contains(one, "1 matches") {
		t.Errorf("text match: %s", one)
	}
	// A match inside a control is reported as the control, with its ref.
	ctl := compile(c, view.Options{Projection: view.Find, Query: "continue"}).String()
	if !strings.Contains(ctl, `link a1 "Continue shopping"`) {
		t.Errorf("control match: %s", ctl)
	}
}

func TestResolveAndRefKinds(t *testing.T) {
	c := newCart(cartOpts{cookie: true})
	p := c.page()
	v := view.Compile(p, view.Options{})
	for ref, want := range map[string]int32{"b1": c.accept, "b2": c.reject, "d1": c.dialog, "a1": c.shop, "b5": c.checkout} {
		id, ok := v.Resolve(ref)
		if !ok || id != p.Snap.Backend[want] {
			t.Errorf("Resolve(%s) = %d,%v want %d", ref, id, ok, p.Snap.Backend[want])
		}
	}
	if _, ok := v.Resolve("b99"); ok {
		t.Error("unknown ref resolved")
	}
}

func TestRefsAreStableAcrossRecompiles(t *testing.T) {
	a := newCart(cartOpts{cookie: true})
	pa := a.page()
	v1 := view.Compile(pa, view.Options{})
	again := view.Compile(pa, view.Options{Previous: v1.Refs})
	if fmt.Sprint(again.Refs) != fmt.Sprint(v1.Refs) || again.String() != v1.String() {
		t.Error("recompiling the same page must not change anything")
	}
	// The same page plus a new button that reads first: nothing renumbers.
	b := newCart(cartOpts{cookie: true, mutated: func(c *cart) {
		nb := c.b.El(c.b.Body(), "button", R(5, 2, 20, 10))
		c.b.Text(nb, "Help")
	}})
	pb2 := b.page()
	v2 := view.Compile(pb2, view.Options{Previous: v1.Refs})
	for ref, id := range v1.Refs {
		if got, ok := v2.Resolve(ref); !ok || got != id {
			t.Errorf("ref %s moved: %d -> %d (%v)", ref, id, got, ok)
		}
	}
	fresh := view.Compile(pb2, view.Options{})
	moved := 0
	for ref, id := range v1.Refs {
		if got, _ := fresh.Resolve(ref); got != id {
			moved++
		}
	}
	if moved == 0 {
		t.Error("test is vacuous: a fresh compile would have kept the numbering anyway")
	}
	newRef := lineWith(t, v2, `"Help"`)
	if !strings.Contains(newRef, "button b") {
		t.Fatal(newRef)
	}
	for ref := range v1.Refs {
		if strings.Contains(newRef, " "+ref+" ") {
			t.Errorf("new node reused ref %s", ref)
		}
	}
}

func TestDiff(t *testing.T) {
	a := newCart(cartOpts{cookie: true})
	pa := a.page()
	v1 := view.Compile(pa, view.Options{})
	if d := view.Diff(v1, view.Compile(pa, view.Options{Previous: v1.Refs})); !d.Empty() {
		t.Errorf("identical views differ: %s", d)
	}
	// The user accepts the dialog and the price changes.
	b := newCart(cartOpts{cookie: true, mutated: func(c *cart) {
		s := c.b.Snapshot()
		unlay(s, c.wrap)
		s.Text[c.sale] = "€59 "
	}})
	v2 := view.Compile(b.page(), view.Options{Previous: v1.Refs})
	d := view.Diff(v1, v2)
	out := d.String()
	for _, want := range []string{"- d1", "- b1", "- b2", "~ main -covered-by=d1"} {
		if !strings.Contains(out, want) {
			t.Errorf("delta lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "~ item") || !strings.Contains(out, "€59") {
		t.Errorf("changed row not reported:\n%s", out)
	}
	for _, l := range d.Lines {
		if strings.HasPrefix(l, "+ ") {
			t.Errorf("nothing was added: %s", l)
		}
	}
	back := view.Diff(v2, v1).String()
	if !strings.Contains(back, "+ modal d1") {
		t.Errorf("reverse delta should add the modal:\n%s", back)
	}
}

func kept(v *view.View) map[int64]bool {
	m := map[int64]bool{}
	for _, l := range v.Lines {
		if l.Key != 0 {
			m[l.Key] = true
		}
	}
	return m
}

func TestBudgetIsNeverExceededAndModalSurvives(t *testing.T) {
	c := newCart(cartOpts{cookie: true, extra: 20})
	p := c.page()
	full := view.Compile(p, view.Options{})
	for _, pr := range []view.Projection{view.Outline, view.Interactive, view.Read, view.Find} {
		for budget := 5; budget <= full.Tokens()+20; budget += 7 {
			v := view.Compile(p, view.Options{Projection: pr, Budget: budget, Query: "e"})
			if got := v.Tokens(); got > budget {
				t.Fatalf("projection %v budget %d: used %d tokens\n%s", pr, budget, got, v)
			}
			if pr == view.Outline && budget >= 60 {
				out := v.String()
				if !strings.Contains(out, "modal d1") || !strings.Contains(out, `"Accept all"`) || !strings.Contains(out, `"Reject"`) {
					t.Fatalf("budget %d lost the modal layer:\n%s", budget, out)
				}
			}
		}
	}
}

func TestBudgetDropsInDesignOrderAndLeavesCounts(t *testing.T) {
	c := newCart(cartOpts{extra: 20})
	p := c.page()
	full := view.Compile(p, view.Options{})
	prev := map[int64]bool{}
	sawFooterGoFirst := false
	for budget := 30; budget <= full.Tokens(); budget++ {
		v := view.Compile(p, view.Options{Budget: budget, Previous: full.Refs})
		cur := kept(v)
		for id := range prev {
			if !cur[id] {
				t.Fatalf("budget %d dropped a node kept at %d", budget, budget-1)
			}
		}
		prev = cur
		out := v.String()
		if budget > 40 && strings.Contains(out, "Checkout") && !strings.Contains(out, "footer") {
			sawFooterGoFirst = true
		}
		if strings.Contains(out, " more line") {
			m := regexp.MustCompile(`(\d+) more lines? (\w+)`).FindStringSubmatch(out)
			if m == nil {
				t.Fatalf("dropped region without count and ref:\n%s", out)
			}
			if _, ok := v.Resolve(m[2]); !ok {
				t.Fatalf("elision ref %s does not resolve:\n%s", m[2], out)
			}
		}
	}
	if !sawFooterGoFirst {
		t.Error("the footer should be dropped before main content")
	}
}

func TestFocusedRegionOutlivesOtherContent(t *testing.T) {
	c := newCart(cartOpts{extra: 20})
	p := c.page()
	full := view.Compile(p, view.Options{})
	focus := p.Snap.Backend[c.trash2]
	ref := ""
	for r, id := range full.Refs {
		if id == focus {
			ref = r
		}
	}
	found := false
	for budget := full.Tokens(); budget >= 20; budget-- {
		without := view.Compile(p, view.Options{Budget: budget, Previous: full.Refs}).String()
		with := view.Compile(p, view.Options{Budget: budget, Previous: full.Refs, Focus: focus}).String()
		if !strings.Contains(without, ref+" ") && strings.Contains(with, ref+" ") {
			found = true
			break
		}
	}
	if !found {
		t.Error("focus never kept its region at a budget that dropped it otherwise")
	}
}

func TestCoveredTagsOnlyWhenCovered(t *testing.T) {
	if out := compile(newCart(cartOpts{}), view.Options{}).String(); strings.Contains(out, "covered") {
		t.Errorf("nothing covers the page:\n%s", out)
	}
}

// unlay removes the boxes of a node and everything under it, as display:none
// does; a box-less node with laid-out content is display:contents instead.
func unlay(s *snapshot.Snapshot, i int32) {
	s.Laid[i] = false
	for _, k := range s.Children[i] {
		unlay(s, k)
	}
}
