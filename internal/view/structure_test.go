package view_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/noetive/riffle/internal/facts"
	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/view"
)

// cards adds n sibling blocks of a label and a button under parent.
func cards(b *pb.Builder, parent int32, y float64, n int, prefix string) {
	for k := 0; k < n; k++ {
		yy := y + float64(k*30)
		d := b.El(parent, "div", R(0, yy, 300, 20))
		b.Text(b.El(d, "span", R(0, yy, 50, 20), pb.Inline()), fmt.Sprintf("%s%d", prefix, k))
		bt := b.El(d, "button", R(100, yy, 50, 20))
		b.Text(bt, fmt.Sprintf("go%d", k))
	}
}

func outlineOf(b *pb.Builder) *view.View {
	return view.Compile(facts.Analyze(b.Snapshot()), view.Options{})
}

func TestTwoAlikeBlocksAreNotAListButThreeAre(t *testing.T) {
	two := pb.New(1280, 800)
	cards(two, two.Body(), 0, 2, "A")
	sameLines(t, outlineOf(two),
		`page shop.example/cart "Cart" 1280x800`,
		`text "A0"`,
		`button b1 "go0"`,
		`text "A1"`,
		`button b2 "go1"`,
	)
	three := pb.New(1280, 800)
	cards(three, three.Body(), 0, 3, "A")
	sameLines(t, outlineOf(three),
		`page shop.example/cart "Cart" 1280x800`,
		`list r1 x3`,
		`  item "A0" | button b1 "go0"`,
		`  item "A1" | button b2 "go1"`,
		`  item "A2" | button b3 "go2"`,
	)
}

func TestTwoRowsOfAListElementAreAlreadyARun(t *testing.T) {
	b := pb.New(1280, 800)
	ul := b.El(b.Body(), "ul", R(0, 0, 300, 60))
	for k := 0; k < 2; k++ {
		li := b.El(ul, "li", R(0, float64(k*30), 300, 20))
		b.Text(li, fmt.Sprintf("li%d", k))
		a := b.El(li, "a", R(100, float64(k*30), 50, 20), pb.Attr("href", "/x"), pb.Inline())
		b.Text(a, "x")
	}
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`list x2`,
		`  item "li0" link a1 "x"`,
		`  item "li1" link a2 "x"`,
	)
}

func TestTheLongestRunOfAlikeBlocksIsTheList(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	cards(b, body, 0, 3, "A")
	sep := b.El(body, "div", R(0, 95, 300, 20))
	b.Text(sep, "separator")
	cards(b, body, 150, 4, "B")
	v := outlineOf(b)
	lineWith(t, v, "list", "x4")
	if indexOf(v, `"A0"`) < 0 || indexOf(v, `"separator"`) < 0 {
		t.Fatalf("missing content:\n%s", v)
	}
	for _, l := range v.Lines {
		if s := l.String(); len(s) > 4 && s[:4] == "list" && s != "list r1 x4" {
			t.Errorf("only the longest run is a list: %s", s)
		}
	}
}

func TestPlainParagraphsNeverFormAList(t *testing.T) {
	b := pb.New(1280, 800)
	for k := 0; k < 5; k++ {
		p := b.El(b.Body(), "p", R(0, float64(k*30), 300, 20))
		b.Text(p, fmt.Sprintf("Paragraph %d", k))
	}
	v := outlineOf(b)
	if indexOf(v, "list") >= 0 || indexOf(v, "item") >= 0 {
		t.Errorf("prose is not a list:\n%s", v)
	}
}

func TestAListOfFlatRowsIsAddressableAsATable(t *testing.T) {
	b := pb.New(1280, 800)
	cards(b, b.Body(), 0, 3, "A")
	v := outlineOf(b)
	ref := ""
	for _, l := range v.Lines {
		if l.Label == "list r1 x3" {
			ref = l.Ref
		}
	}
	if ref != "r1" {
		t.Fatalf("rows of two or more cells get a table ref:\n%s", v)
	}
}

func TestCollapsedRegionsCountWhatTheyHide(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	nav := b.El(body, "nav", R(0, 0, 500, 30))
	for k := 0; k < 2; k++ {
		bt := b.El(nav, "button", R(float64(k*60), 0, 50, 20))
		b.Text(bt, fmt.Sprintf("m%d", k))
	}
	one := b.El(body, "nav", R(0, 40, 500, 30))
	b.Text(b.El(one, "a", R(0, 40, 50, 20), pb.Attr("href", "/a")), "A")
	b.El(body, "footer", R(0, 100, 500, 30))
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`nav r1 collapsed 2 nodes`,
		`nav r2 collapsed 1 links`,
		`footer r3 collapsed`,
	)
}

func TestHeadingsWithControlsKeepThemOnTheirOwnLines(t *testing.T) {
	b := pb.New(1280, 800)
	h := b.El(b.Body(), "h3", R(0, 0, 300, 30))
	a := b.El(h, "a", R(0, 0, 100, 30), pb.Attr("href", "/h"))
	b.Text(a, "Heading link")
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`h3`,
		`  link a1 "Heading link"`,
	)
}

func TestRolesDecideTheWordsOfRegions(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	m := b.El(body, "main", R(0, 0, 600, 200))
	art := b.El(m, "article", R(0, 0, 600, 100), pb.Attr("aria-label", "Story"))
	b.Text(b.El(art, "p", R(0, 0, 300, 20)), "Once")
	sec := b.El(m, "section", R(0, 110, 600, 40), pb.Attr("aria-label", "Extras"))
	b.Text(b.El(sec, "p", R(0, 110, 300, 20)), "More")
	f := b.El(m, "form", R(0, 160, 600, 30))
	in := b.El(f, "input", R(0, 160, 100, 20), pb.Attr("type", "text"), pb.Attr("aria-label", "Q"))
	_ = in
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`main`,
		`  article "Story"`,
		`    text "Once"`,
		`  region "Extras"`,
		`    text "More"`,
		`  form`,
		`    textbox f1 "Q"`,
	)
}

func TestRepeatedWrappersAroundOneControlCollapseToTheControl(t *testing.T) {
	b := pb.New(1280, 800)
	for k := 0; k < 3; k++ {
		d := b.El(b.Body(), "div", R(0, float64(k*30), 300, 20))
		b.Text(b.El(d, "button", R(0, float64(k*30), 50, 20)), fmt.Sprintf("B%d", k))
	}
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`list x3`,
		`  button b1 "B0"`,
		`  button b2 "B1"`,
		`  button b3 "B2"`,
	)
}

func TestRepeatedWrappersAroundNestedBlocksCollapseToTheirContent(t *testing.T) {
	b := pb.New(1280, 800)
	for k := 0; k < 4; k++ {
		d := b.El(b.Body(), "div", R(0, float64(k*30), 300, 20))
		in := b.El(d, "div", R(0, float64(k*30), 300, 20))
		b.Text(b.El(in, "a", R(0, float64(k*30), 50, 20), pb.Attr("href", "/x")), fmt.Sprintf("A%d", k))
	}
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`list x4`,
		`  link a1 "A0"`,
		`  link a2 "A1"`,
		`  link a3 "A2"`,
		`  link a4 "A3"`,
	)
}

func TestEqualRunsPickTheFirstAndTheOthersStayFlat(t *testing.T) {
	b := pb.New(1280, 800)
	cards(b, b.Body(), 0, 3, "A")
	b.Text(b.El(b.Body(), "p", R(0, 95, 300, 20)), "sep")
	cards(b, b.Body(), 150, 3, "B")
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`list r1 x3`,
		`  item "A0" | button b1 "go0"`,
		`  item "A1" | button b2 "go1"`,
		`  item "A2" | button b3 "go2"`,
		`text "sep"`,
		`text "B0"`,
		`button b4 "go0"`,
		`text "B1"`,
		`button b5 "go1"`,
		`text "B2"`,
		`button b6 "go2"`,
	)
}

func TestBlocksOfDifferentShapeAreNotARun(t *testing.T) {
	b := pb.New(1280, 800)
	for k := 0; k < 3; k++ {
		d := b.El(b.Body(), "div", R(0, float64(k*30), 300, 20))
		b.Text(b.El(d, "button", R(0, float64(k*30), 50, 20)), fmt.Sprintf("B%d", k))
		if k == 1 {
			b.Text(b.El(d, "a", R(100, float64(k*30), 50, 20), pb.Attr("href", "/x")), "x")
		}
	}
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`button b1 "B0"`,
		`button b2 "B1"`,
		`link a1 "x"`,
		`button b3 "B2"`,
	)
}

func TestCardsWithAHeadingAndTextFormRows(t *testing.T) {
	b := pb.New(1280, 800)
	for k := 0; k < 3; k++ {
		d := b.El(b.Body(), "div", R(0, float64(k*60), 300, 50))
		b.Text(b.El(d, "h3", R(0, float64(k*60), 300, 20)), fmt.Sprintf("T%d", k))
		b.Text(b.El(d, "p", R(0, float64(k*60+25), 300, 20)), fmt.Sprintf("body %d", k))
	}
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`list r1 x3`,
		`  item h3 "T0" | "body 0"`,
		`  item h3 "T1" | "body 1"`,
		`  item h3 "T2" | "body 2"`,
	)
}

func TestReadingOrderGroupsByRowAndThenLeftToRight(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	// Added in scrambled order. B starts inside the first half of A's
	// height so they share a row, C starts at the half so it begins the next.
	add := func(x, y, h float64, s string) {
		b.Text(b.El(body, "button", R(x, y, 50, h)), s)
	}
	add(300, 100, 40, "C")
	add(200, 119, 40, "B")
	add(100, 100, 40, "A")
	add(0, 120, 40, "D")
	v := outlineOf(b)
	pos := func(s string) int { return indexOf(v, `"`+s+`"`) }
	if pos("A") >= pos("B") || pos("B") >= pos("C") || pos("C") >= pos("D") {
		t.Errorf("order must be A B C D:\n%s", v)
	}
}

func TestReadingOrderUsesTheSmallerHeightForTheRowTest(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	b.Text(b.El(body, "button", R(200, 0, 50, 100)), "Tall")
	b.Text(b.El(body, "button", R(0, 20, 50, 10)), "Short")
	v := outlineOf(b)
	// The short button is 20px below the tall one's top: more than half the
	// smaller height, so it is a later row although the tall one is 100 high.
	if indexOf(v, `"Tall"`) > indexOf(v, `"Short"`) {
		t.Errorf("Tall (y=0) reads before Short (y=20):\n%s", v)
	}
	c := pb.New(1280, 800)
	c.Text(c.El(c.Body(), "button", R(200, 0, 50, 100)), "Tall")
	c.Text(c.El(c.Body(), "button", R(0, 4, 50, 10)), "Short")
	w := outlineOf(c)
	if indexOf(w, `"Short"`) > indexOf(w, `"Tall"`) {
		t.Errorf("within half the smaller height both share a row, left first:\n%s", w)
	}
}

// An icon-font glyph is drawn, not read: a control named only by private-use
// characters has no name, and one that has words besides keeps them.
func TestNamesOfOnlyPrivateUseGlyphsAreEmpty(t *testing.T) {
	b := pb.New(1280, 800)
	b.Text(b.El(b.Body(), "button", R(0, 0, 40, 20)), "\uf013")
	b.Text(b.El(b.Body(), "button", R(0, 40, 80, 20)), "\uf013 Settings")
	var bare, named int
	for _, l := range strings.Split(outlineOf(b).String(), "\n") {
		switch {
		case strings.Contains(l, "Settings"):
			named++
		case strings.HasPrefix(l, "button") && !strings.Contains(l, `"`):
			bare++
		case strings.HasPrefix(l, "button"):
			t.Errorf("a glyph-only name is shown: %q", l)
		}
	}
	if bare != 1 || named != 1 {
		t.Errorf("want one nameless button and one named, got %d and %d", bare, named)
	}
}

// A section that holds a heading, prose and a run of alike controls is not
// itself a list: the run is, and the heading and prose stay beside it.
func TestARunInsideASectionIsAListOfItsOwn(t *testing.T) {
	b := pb.New(1280, 800)
	sec := b.El(b.Body(), "div", R(0, 0, 800, 300))
	h := b.El(sec, "h3", R(0, 0, 400, 30), pb.FontPx(24))
	b.Text(h, "Section title")
	p := b.El(sec, "p", R(0, 40, 600, 20))
	b.Text(p, "Some prose that explains the section.")
	for k := 0; k < 3; k++ {
		bt := b.El(sec, "button", R(float64(k*120), 80, 100, 30))
		b.Text(bt, fmt.Sprintf("Choice %d", k))
	}
	v := outlineOf(b)
	sameLines(t, v,
		`page shop.example/cart "Cart" 1280x800`,
		`h3 "Section title"`,
		`text "Some prose that explains the section."`,
		`list x3`,
		`  button b1 "Choice 0"`,
		`  button b2 "Choice 1"`,
		`  button b3 "Choice 2"`,
	)
}

// Text cut to the line limit says so with a ref that expand opens, as text
// cut to a budget does.
func TestTextCutToTheLineLimitCanBeExpanded(t *testing.T) {
	b := pb.New(1280, 800)
	long := strings.Repeat("word ", 60) + "end"
	p := b.El(b.Body(), "p", R(0, 0, 900, 80), pb.ID(4242))
	b.Text(p, long)
	v := outlineOf(b)
	var line string
	for _, l := range strings.Split(v.String(), "\n") {
		if strings.HasPrefix(l, "text ") {
			line = l
		}
	}
	i := strings.LastIndex(line, " r")
	if !strings.Contains(line, "…") || i < 0 {
		t.Fatalf("a cut line must end in a ref to expand:\n%s", v)
	}
	if got, ok := v.Resolve(line[i+1:]); !ok || got != 4242 {
		t.Errorf("the ref %q must open the paragraph: got %d %v", line[i+1:], got, ok)
	}
}

// A password field says it is one, empty or filled, and never shows what it
// holds.
func TestAPasswordFieldSaysSoAndNeverShowsItsValue(t *testing.T) {
	b := pb.New(1280, 800)
	b.El(b.Body(), "input", R(0, 0, 200, 24), pb.Attr("type", "password"), pb.Attr("placeholder", "Password"))
	b.El(b.Body(), "input", R(0, 40, 200, 24), pb.Attr("type", "password"), pb.Attr("aria-label", "Repeat"), pb.Value("hunter2"))
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`textbox f1 "Password" password`,
		`textbox f2 "Repeat" =filled password`,
	)
}

// A progress bar says how far along it is, whether it is a native progress
// element or a div with the progressbar role; its own text is not repeated.
func TestAProgressBarSaysHowFarAlongItIs(t *testing.T) {
	b := pb.New(1280, 800)
	d := b.El(b.Body(), "div", R(0, 0, 400, 20), pb.Attr("role", "progressbar"), pb.Attr("aria-valuenow", "35"), pb.Attr("aria-valuemin", "0"), pb.Attr("aria-valuemax", "100"))
	b.Text(d, "35%")
	b.El(b.Body(), "progress", R(0, 40, 400, 20), pb.Attr("value", "3"), pb.Attr("max", "4"), pb.Attr("aria-label", "Upload"))
	b.El(b.Body(), "div", R(0, 80, 400, 20), pb.Attr("role", "meter"), pb.Attr("aria-valuenow", "2"), pb.Attr("aria-valuetext", "2 of 5 steps"))
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`progress r1 =35%`,
		`progress r2 "Upload" =75%`,
		`progress r3 ="2 of 5 steps"`,
	)
}

// Native progress and meter elements read as the browser draws them: a range
// of 0 to 1 unless the page sets one, and never past either end.
func TestProgressValuesFollowHowTheBrowserDrawsThem(t *testing.T) {
	b := pb.New(1280, 800)
	b.El(b.Body(), "meter", R(0, 0, 200, 20), pb.Attr("value", "0.6"))
	b.El(b.Body(), "progress", R(0, 30, 200, 20), pb.Attr("value", "0.5"), pb.Attr("max", "abc"))
	b.El(b.Body(), "progress", R(0, 60, 200, 20), pb.Attr("value", "150"), pb.Attr("max", "100"))
	b.El(b.Body(), "meter", R(0, 90, 200, 20), pb.Attr("value", "5"), pb.Attr("min", "0"), pb.Attr("max", "0"))
	b.El(b.Body(), "div", R(0, 120, 200, 20), pb.Attr("role", "progressbar"), pb.Attr("aria-valuenow", "3"), pb.Attr("aria-valuemax", "0"))
	b.El(b.Body(), "meter", R(0, 150, 200, 20), pb.Attr("value", "15"), pb.Attr("min", "10"), pb.Attr("max", "20"))
	b.El(b.Body(), "progress", R(0, 180, 200, 20), pb.Attr("value", "15"), pb.Attr("min", "10"), pb.Attr("max", "20"))
	b.El(b.Body(), "div", R(0, 210, 200, 20), pb.Attr("role", "progressbar"), pb.Attr("value", "50"))
	b.El(b.Body(), "div", R(0, 240, 200, 20), pb.Attr("role", "progressbar"), pb.Attr("aria-valuenow", "15"), pb.Attr("aria-valuemin", "10"), pb.Attr("aria-valuemax", "20"))
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`progress r1 =60%`,
		`progress r2 =50%`,
		`progress r3 =100%`,
		`progress r4 =100%`,
		`progress r5 =3`,   // no range to measure against: the value as given
		`progress r6 =50%`, // a meter from 10 to 20 at 15
		`progress r7 =75%`, // a progress element has no min: 15 of 20
		`progress r8`,      // value is not ARIA: an indeterminate bar
		`progress r9 =50%`, // ARIA from 10 to 20 at 15
	)
}

// A progress bar that says how far along it is only in words reads as those
// words, and one that holds controls keeps them reachable.
func TestAProgressBarKeepsItsWordsAndControls(t *testing.T) {
	b := pb.New(1280, 800)
	d := b.El(b.Body(), "div", R(0, 0, 400, 20), pb.Attr("role", "progressbar"))
	b.Text(b.El(d, "span", R(0, 0, 200, 20), pb.Inline()), "Step 3 of 10")
	s := b.El(b.Body(), "div", R(0, 40, 400, 30), pb.Attr("role", "progressbar"), pb.Attr("aria-valuenow", "2"), pb.Attr("aria-valuemax", "4"))
	b.Text(b.El(s, "a", R(0, 40, 80, 20), pb.Attr("href", "/step/1"), pb.Inline()), "Back")
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`progress r1 "Step 3 of 10"`,
		`link a1 "Back"`,
	)
}

// Cards that each hold a heading and a run of alike buttons, set side by
// side, give every card and every run a line of its own, and runs that grow
// show in the diff.
func TestARunInsideARepeatedCardIsNewsWhenItGrows(t *testing.T) {
	page := func(extra bool) *view.View {
		b := pb.New(1280, 800)
		for k := 0; k < 3; k++ {
			x := float64(k * 300)
			card := b.El(b.Body(), "div", R(x, 0, 280, 200), pb.ID(int64(1000+k)))
			b.Text(b.El(card, "h3", R(x, 0, 200, 30), pb.ID(int64(1100+k))), fmt.Sprintf("Plan %d", k))
			n := 3
			if extra {
				n = 4
			}
			for s := 0; s < n; s++ {
				bt := b.El(card, "button", R(x+float64(s*60), 50, 50, 30), pb.ID(int64(1200+k*10+s)))
				b.Text(bt, fmt.Sprintf("Size %d", s))
			}
		}
		return outlineOf(b)
	}
	old, grown := page(false), page(true)
	for _, v := range []*view.View{old, grown} {
		keys := map[int64]int{}
		for _, l := range v.Lines {
			if l.Key != 0 {
				keys[l.Key]++
			}
		}
		for k, n := range keys {
			if n > 1 {
				t.Errorf("%d lines share the node %d, so a diff can see only one of them:\n%s", n, k, v)
			}
		}
	}
	if d := view.Diff(old, grown).String(); !strings.Contains(d, "x4") {
		t.Errorf("the run that grew must show in the diff:\n%s", d)
	}
}

// The ref that opens cut text follows that text, so it never reads as the
// ref of a link after it, and items of a list get one too.
func TestTheRefForCutTextFollowsTheText(t *testing.T) {
	b := pb.New(1280, 800)
	p := b.El(b.Body(), "p", R(0, 0, 900, 80), pb.ID(4300))
	b.Text(p, strings.Repeat("word ", 60))
	a := b.El(p, "a", R(0, 60, 40, 20), pb.Attr("href", "/more"), pb.Inline())
	b.Text(a, "more")
	got := outlineOf(b).String()
	if !strings.Contains(got, `…" r1 link a1 "more"`) {
		t.Errorf("the ref must follow the cut text, before the link:\n%s", got)
	}
}
