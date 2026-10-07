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

const cartHeader = `page shop.example/cart "Cart" 1280x800`

func TestTinyBudgetsCutTheTailInsteadOfOverflowing(t *testing.T) {
	p := newCart(cartOpts{}).page()
	cases := map[int][]string{
		3:  {`page shop.e`},
		10: {cartHeader},
		11: {cartHeader, `… 11`},
		12: {cartHeader, `… 11 mor`},
		14: {cartHeader, `… 11 more lines `},
	}
	for budget, want := range cases {
		v := view.Compile(p, view.Options{Budget: budget})
		sameLines(t, v, want...)
		if v.Tokens() > budget {
			t.Errorf("budget %d: %d tokens", budget, v.Tokens())
		}
	}
}

func TestBudgetKeepsNavigationAndHeadingsBeforeBodyText(t *testing.T) {
	p := newCart(cartOpts{}).page()
	sameLines(t, view.Compile(p, view.Options{Budget: 20}),
		cartHeader,
		`… 11 more lines r1`,
		`unseen 2 nodes`,
	)
	sameLines(t, view.Compile(p, view.Options{Budget: 30}),
		cartHeader,
		`nav r1 "Main" collapsed 14 links`,
		`… 10 more lines r4`,
		`unseen 2 nodes`,
	)
	sameLines(t, view.Compile(p, view.Options{Budget: 45}),
		cartHeader,
		`nav r1 "Main" collapsed 14 links`,
		`main`,
		`  h1 "Your cart"`,
		`  … 8 more lines r4`,
		`unseen 2 nodes`,
	)
}

func TestBudgetDropsLaterRowsOfARepeatedListBeforeOtherText(t *testing.T) {
	p := newCart(cartOpts{}).page()
	sameLines(t, view.Compile(p, view.Options{Budget: 100}),
		cartHeader,
		`nav r1 "Main" collapsed 14 links`,
		`main`,
		`  h1 "Your cart"`,
		`  text "Review the items below before you check out of this shop today."`,
		`  list r2 x2`,
		`    … 2 more lines r2`,
		`  text "Free shipping over €75" green`,
		`  button b3 "Checkout"`,
		`  link a1 "Continue shopping" muted`,
		`… 1 more line r3`,
		`unseen 2 nodes`,
	)
}

func TestBudgetThatFitsReturnsTheViewUntouched(t *testing.T) {
	p := newCart(cartOpts{extra: 4}).page()
	full := view.Compile(p, view.Options{})
	for _, extra := range []int{0, 1, 3} {
		v := view.Compile(p, view.Options{Budget: full.Tokens() + extra})
		if v.String() != full.String() {
			t.Errorf("a budget of %d tokens holds the %d token view:\n%s", full.Tokens()+extra, full.Tokens(), v)
		}
	}
	tight := view.Compile(p, view.Options{Budget: full.Tokens() - 1})
	if tight.String() == full.String() || tight.Tokens() > full.Tokens()-1 {
		t.Errorf("one token short must elide something:\n%s", tight)
	}
}

func TestElisionKeepsIndentOfTheDroppedRun(t *testing.T) {
	p := newCart(cartOpts{extra: 6}).page()
	var elided *view.Line
	v := view.Compile(p, view.Options{Budget: 120})
	for i, l := range v.Lines {
		if l.Label == "…" && l.Indent == 2 {
			elided = &v.Lines[i]
		}
	}
	if elided == nil {
		t.Fatalf("rows of the list are elided one level below it:\n%s", v)
	}
}

func TestFocusKeepsTheFocusedRowAndMarksIt(t *testing.T) {
	c := newCart(cartOpts{extra: 6})
	p := c.page()
	focus := p.Snap.Backend[c.trash2]
	sameLines(t, view.Compile(p, view.Options{Budget: 90, Focus: focus}),
		cartHeader,
		`nav r1 "Main" collapsed 14 links`,
		`main`,
		`  h1 "Your cart"`,
		`  text "Review the items below before you check out of this shop today."`,
		`  list r2 x8`,
		`    … 1 more line r2`,
		`    item "Wool sock €12" | spinbutton f2 "qty" ="2" | button b2 "Remove Wool sock" focused`,
		`    … 10 more lines r2`,
		`unseen 2 nodes`,
	)
	if out := compile(c, view.Options{Budget: 90}).String(); strings.Contains(out, "focused") {
		t.Errorf("nothing is focused without Focus:\n%s", out)
	}
}

// The read view has no cap on a line, so one text node far longer than the
// budget is cut to what is left of it, and the cut offers the same expand ref
// as dropped blocks do.
func TestReadCutsASingleOverlongLineToTheBudgetWithAnExpandRef(t *testing.T) {
	b := pb.New(1280, 800)
	b.Text(b.El(b.Body(), "h1", R(0, 0, 600, 30)), "Title")
	b.Text(b.El(b.Body(), "pre", R(0, 40, 600, 700)), "{"+strings.Repeat(`"k":"value",`, 2000)+"}")
	v := outline(b, view.Options{Projection: view.Read, Budget: 300})
	if v.Tokens() > 300 {
		t.Errorf("%d tokens over a budget of 300", v.Tokens())
	}
	if len(v.Lines) != 2 || v.Lines[0].String() != "# Title" {
		t.Fatalf("want the heading and one cut line:\n%.600s", v.String())
	}
	cut := v.Lines[1].String()
	if !strings.HasPrefix(cut, `{"k":"value","k":"value"`) || !strings.Contains(cut, "… r") {
		t.Errorf("the long line is not cut with a marker and a ref:\n%.600s", cut)
	}
	ref := cut[strings.LastIndex(cut, " ")+1:]
	if _, ok := v.Resolve(ref); !ok {
		t.Errorf("ref %q on the cut line does not resolve", ref)
	}
}

func TestReadCutLineFillsTheBudgetAndStubsAreNotKept(t *testing.T) {
	page := func() *pb.Builder {
		b := pb.New(1280, 800)
		b.Text(b.El(b.Body(), "h1", R(0, 0, 600, 30)), "Title")
		b.Text(b.El(b.Body(), "pre", R(0, 40, 600, 700)), strings.Repeat("word ", 400))
		return b
	}
	v := outline(page(), view.Options{Projection: view.Read, Budget: 100})
	// The cut falls at a space, so it may stop a word short of the budget.
	if got := v.Tokens(); got > 100 || got < 95 {
		t.Errorf("the cut line takes %d tokens of a budget of 100", got)
	}
	// Too little room for a useful piece of the line: the count of dropped
	// lines says more than a stub would.
	sameLines(t, outline(page(), view.Options{Projection: view.Read, Budget: 8}), "# Title", "… 1 more line r2")
}

// The ref on a cut line opens the block the line belongs to, and what it
// shows goes on where the cut stopped.
func TestReadCutRefExpandsToTheRestOfTheBlock(t *testing.T) {
	b := pb.New(1280, 800)
	b.Text(b.El(b.Body(), "h1", R(0, 0, 600, 30)), "Title")
	var words []string
	for i := range 300 {
		words = append(words, fmt.Sprintf("w%03d", i))
	}
	b.Text(b.El(b.Body(), "p", R(0, 40, 600, 700)), strings.Join(words, " "))
	v := outline(b, view.Options{Projection: view.Read, Budget: 100})
	cut := v.Lines[len(v.Lines)-1].String()
	i := strings.LastIndex(cut, "… ")
	if i < 0 {
		t.Fatalf("the long paragraph is not cut:\n%s", v.String())
	}
	ref := cut[i+len("… "):]
	if _, ok := v.Resolve(ref); !ok {
		t.Fatalf("ref %q on the cut line does not resolve", ref)
	}
	last := strings.Fields(cut[:i])
	kept := last[len(last)-1]
	full := view.Compile(facts.Analyze(b.Snapshot()), view.Options{Projection: view.Expand, Target: ref, Previous: v.Refs})
	got := full.String()
	if strings.Contains(got, "no longer on the page") || strings.Contains(got, "unknown ref") {
		t.Fatalf("expand %s answers:\n%s", ref, got)
	}
	for _, w := range []string{kept, "w299", words[len(last)]} {
		if !strings.Contains(got, w) {
			t.Errorf("expand %s lacks %q, the text after the cut:\n%.300s", ref, w, got)
		}
	}
	// The cut stops between words.
	if strings.HasSuffix(strings.TrimSuffix(cut[:i], " "), "w") || len(kept) != 4 {
		t.Errorf("the cut splits a word: %q", kept)
	}
}

// A cut never leaves half a link or an emphasis that is not closed.
func TestReadCutNeverEndsInsideMarkdown(t *testing.T) {
	cuts := 0
	for budget := 30; budget < 90; budget++ {
		b := pb.New(1280, 800)
		p := b.El(b.Body(), "p", R(0, 0, 600, 700))
		for i := range 12 {
			b.Text(b.El(p, "b", R(0, float64(i*20), 100, 20), pb.Inline(), pb.Style(snapshot.FontWeight, "700")), fmt.Sprintf("bold part %d", i))
			a := b.El(p, "a", R(0, float64(i*20), 100, 20), pb.Attr("href", fmt.Sprintf("/page/%d", i)))
			b.Text(a, fmt.Sprintf("link name %d", i))
		}
		v := outline(b, view.Options{Projection: view.Read, Budget: budget})
		for _, l := range v.Lines {
			s := l.String()
			i := strings.LastIndex(s, "… r")
			if i < 0 || strings.Contains(s, "more line") {
				continue
			}
			cuts++
			kept := s[:i]
			if strings.Count(kept, "**")%2 != 0 {
				t.Errorf("budget %d: cut inside bold: %q", budget, s)
			}
			if strings.Count(kept, "[") != strings.Count(kept, "](") || strings.Count(kept, "](") != strings.Count(kept, ")") {
				t.Errorf("budget %d: cut inside a link: %q", budget, s)
			}
			if v.Tokens() > budget {
				t.Errorf("budget %d: %d tokens", budget, v.Tokens())
			}
		}
	}
	if cuts < 10 {
		t.Errorf("only %d budgets cut a line; the test checks nothing", cuts)
	}
}

// The outline cuts a long text line the same way, and its ref opens the block.
func TestOutlineCutRefOpensTheBlockOfTheText(t *testing.T) {
	b := pb.New(1280, 800)
	b.Text(b.El(b.Body(), "h1", R(0, 0, 600, 30)), "Title")
	b.Text(b.El(b.Body(), "pre", R(0, 40, 600, 700)), "{"+strings.Repeat(`"k":"value",`, 2000)+"}")
	v := outline(b, view.Options{Budget: 40})
	var ref string
	for _, l := range v.Lines {
		if s := l.String(); strings.Contains(s, "… r") && !strings.Contains(s, "more line") {
			ref = s[strings.LastIndex(s, " ")+1:]
		}
	}
	if ref == "" {
		t.Fatalf("no cut line:\n%.500s", v.String())
	}
	got := outline(b, view.Options{Projection: view.Expand, Target: ref, Previous: v.Refs}).String()
	if strings.Contains(got, "no longer on the page") || strings.Count(got, "value") < 1500 {
		t.Errorf("expand %s does not show the whole text:\n%.300s", ref, got)
	}
}
