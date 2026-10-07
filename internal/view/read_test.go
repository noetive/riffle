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

// tableOf adds a table of rows, each a list of cell builders, and returns it.
func tableOf(b *pb.Builder, y float64, rows ...[]func(td int32, y float64)) int32 {
	tb := b.El(b.Body(), "table", R(0, y, 600, float64(30*len(rows))))
	for r, cells := range rows {
		ry := y + float64(r*30)
		tr := b.El(tb, "tr", R(0, ry, 600, 30))
		for k, f := range cells {
			f(b.El(tr, "td", R(float64(k*150), ry, 150, 30)), ry)
		}
	}
	return tb
}

func word(b *pb.Builder, s string) func(int32, float64) {
	return func(td int32, _ float64) { b.Text(td, s) }
}

func readOf(b *pb.Builder) *view.View {
	return view.Compile(facts.Analyze(b.Snapshot()), view.Options{Projection: view.Read})
}

func TestReadTablesWithUnevenRowsBecomePlainLines(t *testing.T) {
	b := pb.New(1280, 800)
	w := func(s string) func(int32, float64) { return word(b, s) }
	tableOf(b, 0, []func(int32, float64){w("a"), w("b")}, []func(int32, float64){w("c")}, []func(int32, float64){w("d"), w("e"), w("f")})
	sameLines(t, readOf(b), "a b", "c", "d e f")
}

func TestReadTablesWithOneColumnAreNotMarkdownTables(t *testing.T) {
	b := pb.New(1280, 800)
	w := func(s string) func(int32, float64) { return word(b, s) }
	tableOf(b, 0, []func(int32, float64){w("one")}, []func(int32, float64){w("two")})
	sameLines(t, readOf(b), "one", "two")
}

func TestReadTableCellsKeepLinksDropControlsAndBlankRowsVanish(t *testing.T) {
	b := pb.New(1280, 800)
	w := func(s string) func(int32, float64) { return word(b, s) }
	link := func(td int32, y float64) {
		b.Text(b.El(td, "a", R(0, y, 100, 20), pb.Attr("href", "/z")), "zed")
	}
	button := func(td int32, y float64) {
		b.Text(b.El(td, "button", R(150, y, 50, 20)), "btn")
	}
	tableOf(b, 0,
		[]func(int32, float64){w("H1"), w("H2")},
		[]func(int32, float64){link, button},
		[]func(int32, float64){w(" "), w(" ")},
		[]func(int32, float64){w("p"), w("q")},
	)
	sameLines(t, readOf(b),
		"| H1 | H2 |",
		"| --- | --- |",
		"| [zed](/z) |  |",
		"| p | q |",
	)
}

func TestReadListItemsNestBlocksAndShowImages(t *testing.T) {
	b := pb.New(1280, 800)
	ul := b.El(b.Body(), "ul", R(0, 0, 400, 90))
	li := b.El(ul, "li", R(0, 0, 400, 40))
	b.Text(li, "parent")
	b.Text(b.El(li, "p", R(20, 20, 300, 20)), "nested para")
	li2 := b.El(ul, "li", R(0, 50, 400, 20))
	b.El(li2, "img", R(0, 50, 20, 20), pb.Attr("alt", "icon"))
	b.Text(b.El(b.Body(), "h4", R(0, 100, 300, 20)), "Sub")
	sameLines(t, readOf(b), "- parent nested para", "- icon", "#### Sub")
}

func TestReadSkipsNavigationFootersAsidesAndHeadersButNotTheirSiblings(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	b.Text(b.El(body, "nav", R(0, 0, 300, 20)), "menu")
	b.Text(b.El(body, "header", R(0, 30, 300, 20)), "banner")
	b.Text(b.El(body, "p", R(0, 60, 300, 20)), "story")
	b.Text(b.El(body, "aside", R(0, 90, 300, 20)), "related")
	b.Text(b.El(body, "footer", R(0, 120, 300, 20)), "legal")
	sameLines(t, readOf(b), "story")
}

func TestReadHeadingLevelsFollowTheMarkup(t *testing.T) {
	b := pb.New(1280, 800)
	for k, tag := range []string{"h1", "h2", "h3"} {
		b.Text(b.El(b.Body(), tag, R(0, float64(k*30), 300, 20)), "T"+tag)
	}
	sameLines(t, readOf(b), "# Th1", "## Th2", "### Th3")
}

func TestReadOnlyFollowsTheMainLandmarkWhereverItIsNested(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	b.Text(b.El(body, "p", R(0, 0, 300, 20)), "outside")
	w := b.El(body, "div", R(0, 30, 600, 100))
	m := b.El(w, "main", R(0, 30, 600, 100))
	b.Text(b.El(m, "p", R(0, 30, 300, 20)), "inside")
	sameLines(t, readOf(b), "inside")
}

func TestReadNestsListsAndKeepsLeadingLinksOfAnItem(t *testing.T) {
	b := pb.New(1280, 800)
	ul := b.El(b.Body(), "ul", R(0, 60, 400, 200))
	li := b.El(ul, "li", R(0, 60, 400, 80))
	a := b.El(li, "a", R(0, 60, 100, 20), pb.Attr("href", "/first"))
	b.Text(a, "first")
	b.Text(li, " tail")
	b.Text(b.El(li, "button", R(200, 60, 50, 20)), "btn")
	ul2 := b.El(li, "ul", R(20, 90, 300, 50))
	for k := 0; k < 2; k++ {
		b.Text(b.El(ul2, "li", R(20, float64(90+k*25), 300, 20)), fmt.Sprintf("child %d", k))
	}
	b.Text(b.El(ul, "li", R(0, 150, 400, 20)), "second")
	v := readOf(b)
	sameLines(t, v,
		"- [first](/first) tail",
		"  - child 0",
		"  - child 1",
		"- second",
	)
	for i, want := range []int{0, 1, 1, 0} {
		if v.Lines[i].Indent != want {
			t.Errorf("line %d indent %d want %d", i, v.Lines[i].Indent, want)
		}
	}
}

func TestReadFlattensTablesNestedInTables(t *testing.T) {
	b := pb.New(1280, 800)
	outer := b.El(b.Body(), "table", R(0, 300, 600, 100))
	tr := b.El(outer, "tr", R(0, 300, 600, 60))
	td := b.El(tr, "td", R(0, 300, 600, 60))
	in := b.El(td, "table", R(0, 300, 600, 60))
	for r, row := range [][]string{{"k", "v"}, {"x", "y"}} {
		rw := b.El(in, "tr", R(0, 300+float64(r*30), 600, 30))
		for c, s := range row {
			b.Text(b.El(rw, "td", R(float64(c*100), 300+float64(r*30), 100, 30)), s)
		}
	}
	b.Text(b.El(b.El(outer, "tr", R(0, 360, 600, 30)), "td", R(0, 360, 600, 30)), "footnote")
	sameLines(t, readOf(b), "| k | v |", "| --- | --- |", "| x | y |", "footnote")
}

func TestReadStyledTextRunsWrapEachStretch(t *testing.T) {
	b := pb.New(1280, 800)
	p := b.El(b.Body(), "p", R(0, 0, 600, 20))
	b.Text(p, "a ")
	both := b.El(p, "span", R(20, 0, 20, 20), pb.Inline(), pb.Style(snapshot.FontWeight, "700"), pb.Style(snapshot.FontStyle, "italic"), pb.Style(snapshot.TextDecorationLine, "line-through"))
	b.Text(both, "mix")
	b.Text(b.El(p, "span", R(40, 0, 20, 20), pb.Inline(), pb.Style(snapshot.FontWeight, "600")), "semi")
	b.Text(b.El(p, "span", R(60, 0, 20, 20), pb.Inline(), pb.Style(snapshot.FontWeight, "599")), "light")
	sameLines(t, readOf(b), "a ***~~mix~~*** **semi** light")
}

// Headings and controls inside list items are read, the bullet a list marker
// draws is not repeated, and clickable text is not lost.
func TestReadListItemsKeepHeadingsControlsAndClickableText(t *testing.T) {
	b := pb.New(1280, 800)
	ul := b.El(b.Body(), "ul", R(0, 0, 600, 200))
	item := func(y float64) int32 {
		li := b.El(ul, "li", R(0, y, 600, 30))
		b.Text(b.El(li, "span", R(0, y, 10, 20), pb.Pseudo("marker"), pb.Inline()), "• ")
		return li
	}
	b.Text(b.El(item(0), "h3", R(20, 0, 300, 20)), "Heading in list")
	b.Text(b.El(item(40), "p", R(20, 40, 300, 20)), "para in list")
	b.Text(b.El(item(80), "button", R(20, 80, 100, 20)), "Btn in list")
	long := "Clickable card text that runs well past sixty characters in a single line."
	b.Text(b.El(item(120), "div", R(20, 120, 500, 20), pb.Clickable()), long)
	sameLines(t, readOf(b),
		"- ### Heading in list",
		"- para in list",
		"- Btn in list",
		"- "+long,
	)
}

func TestReadListMarkersAreNotTextWhereverTheyAreNested(t *testing.T) {
	b := pb.New(1280, 800)
	ul := b.El(b.Body(), "ul", R(0, 0, 600, 100))
	li := b.El(ul, "li", R(0, 0, 600, 30))
	wrap := b.El(li, "span", R(0, 0, 300, 20), pb.Inline())
	b.Text(b.El(wrap, "span", R(0, 0, 10, 20), pb.Pseudo("marker"), pb.Inline()), "• ")
	b.Text(wrap, "wrapped")
	li2 := b.El(ul, "li", R(0, 40, 600, 30))
	card := b.El(li2, "div", R(0, 40, 500, 20), pb.Clickable())
	b.Text(b.El(card, "span", R(0, 40, 10, 20), pb.Pseudo("marker"), pb.Inline()), "• ")
	b.Text(card, "Clickable card text that runs well past sixty characters in a single line.")
	sameLines(t, readOf(b), "- wrapped", "- Clickable card text that runs well past sixty characters in a single line.")
}

func TestReadHeadingLevelsInListItemsFollowTheMarkup(t *testing.T) {
	b := pb.New(1280, 800)
	ul := b.El(b.Body(), "ul", R(0, 0, 600, 100))
	b.Text(b.El(b.El(ul, "li", R(0, 0, 600, 30)), "h1", R(0, 0, 300, 20)), "Top")
	b.Text(b.El(b.El(ul, "li", R(0, 40, 600, 30)), "h2", R(0, 40, 300, 20)), "Next")
	sameLines(t, readOf(b), "- # Top", "- ## Next")
}

// A form field is not content: an item holding only one has nothing to read,
// where an item holding only a button reads as the button.
func TestReadListItemOfOnlyAFieldHasNoText(t *testing.T) {
	b := pb.New(1280, 800)
	ul := b.El(b.Body(), "ul", R(0, 0, 600, 100))
	b.El(b.El(ul, "li", R(0, 0, 600, 30)), "input", R(0, 0, 200, 20), pb.Attr("type", "text"), pb.Attr("aria-label", "Search"))
	b.Text(b.El(b.El(ul, "li", R(0, 40, 600, 30)), "button", R(0, 40, 100, 20)), "Go")
	sameLines(t, readOf(b), "- Go")
}

func TestReadKeepsTheTextOfAClickableBlockAndSkipsAnEmptyOne(t *testing.T) {
	b := pb.New(1280, 800)
	b.Text(b.El(b.Body(), "p", R(0, 0, 400, 20)), "para")
	b.Text(b.El(b.Body(), "div", R(0, 30, 400, 20), pb.Clickable()), "Open the card")
	b.El(b.Body(), "div", R(0, 60, 400, 20), pb.Clickable())
	b.Text(b.El(b.Body(), "p", R(0, 90, 400, 20)), "last")
	sameLines(t, readOf(b), "para", "Open the card", "last")
}

func TestReadBlocksBesideARepeatedRunAreNotListEntries(t *testing.T) {
	b := pb.New(1280, 800)
	box := b.El(b.Body(), "div", R(0, 0, 600, 300))
	b.Text(b.El(box, "h3", R(0, 0, 300, 30)), "Alerts")
	b.Text(b.El(box, "p", R(0, 40, 600, 20)), "Prose about the controls below.")
	b.Text(b.El(box, "h4", R(0, 70, 300, 24)), "Playground")
	for i, name := range []string{"Alert", "Confirm", "Prompt"} {
		b.Text(b.El(box, "button", R(float64(i)*100, 100, 90, 30), pb.Inline()), name)
	}
	sameLines(t, readOf(b), "### Alerts", "Prose about the controls below.", "#### Playground")
}

func TestReadARunOfBareLinksIsAListOfLinks(t *testing.T) {
	b := pb.New(1280, 800)
	box := b.El(b.Body(), "div", R(0, 0, 600, 300))
	b.Text(b.El(box, "h3", R(0, 0, 300, 30)), "Pages")
	for i, name := range []string{"one", "two", "three"} {
		b.Text(b.El(box, "a", R(0, 40+float64(i)*30, 90, 20), pb.Attr("href", "https://example.com/"+name)), name)
	}
	sameLines(t, readOf(b), "### Pages",
		"- [one](https://example.com/one)",
		"- [two](https://example.com/two)",
		"- [three](https://example.com/three)")
}

// A bullet is drawn by the list and not repeated, but the number of an ordered
// list and a symbol that says something are part of what the page says.
func TestReadKeepsMarkersThatCarryMeaning(t *testing.T) {
	for name, c := range map[string]struct {
		marks []string
		texts []string
		want  []string
	}{
		"numbers": {[]string{"1. ", "2. ", "10. "}, []string{"Preheat the oven", "Mix", "Serve"},
			[]string{"- 1. Preheat the oven", "- 2. Mix", "- 10. Serve"}},
		"letters and numerals": {[]string{"a) ", "iv. "}, []string{"first", "second"},
			[]string{"- a) first", "- iv. second"}},
		"symbols": {[]string{"✓ ", "✗ ", "★ "}, []string{"Offline mode", "Teams", "Priority"},
			[]string{"- ✓ Offline mode", "- ✗ Teams", "- ★ Priority"}},
		"bullets": {[]string{"• ", "◦", " ▪ ", "▸ ", "- "}, []string{"a", "b", "c", "d", "e"},
			[]string{"- a", "- b", "- c", "- d", "- e"}},
	} {
		t.Run(name, func(t *testing.T) {
			b := pb.New(1280, 800)
			ul := b.El(b.Body(), "ul", R(0, 0, 600, 300))
			for i, m := range c.marks {
				y := float64(i * 40)
				li := b.El(ul, "li", R(0, y, 600, 30))
				b.Text(b.El(li, "span", R(0, y, 10, 20), pb.Pseudo("marker"), pb.Inline()), m)
				b.Text(b.El(li, "p", R(20, y, 300, 20)), c.texts[i])
			}
			sameLines(t, readOf(b), c.want...)
		})
	}
}

// A marker given as the generated content of the element, as a capture holds
// it, reads the same as one made of a text node.
func TestReadKeepsANumberHeldOnTheMarkerElement(t *testing.T) {
	b := pb.New(1280, 800)
	ul := b.El(b.Body(), "ul", R(0, 0, 600, 100))
	li := b.El(ul, "li", R(0, 0, 600, 30))
	mk := b.El(li, "span", R(0, 0, 10, 20), pb.Pseudo("marker"), pb.Inline())
	b.Snapshot().Text[mk] = "5. "
	b.Text(b.El(li, "p", R(20, 0, 300, 20)), "Serve")
	sameLines(t, readOf(b), "- 5. Serve")
}

// A heading that holds a link is a container; it keeps the depth of the item
// it sits in.
func TestReadNonLeafHeadingInAListItemKeepsTheItemDepth(t *testing.T) {
	b := pb.New(1280, 800)
	ul := b.El(b.Body(), "ul", R(0, 0, 600, 100))
	li := b.El(ul, "li", R(0, 0, 600, 60))
	b.Text(li, "parent")
	h := b.El(li, "h3", R(0, 20, 300, 20))
	b.Text(b.El(h, "a", R(0, 20, 100, 20), pb.Attr("href", "/x")), "Linked title")
	sameLines(t, readOf(b), "- parent", "  ### Linked title")
}

func TestReadAGridOfCardsHasNoEmptyEntries(t *testing.T) {
	b := pb.New(1280, 800)
	grid := b.El(b.Body(), "div", R(0, 0, 1200, 400))
	n := 0
	for r := 0; r < 3; r++ {
		row := b.El(grid, "div", R(0, float64(r)*120, 1200, 110))
		for c := 0; c < 3; c++ {
			n++
			x, y := float64(c)*400, float64(r)*120
			col := b.El(row, "div", R(x, y, 390, 110))
			h := b.El(col, "h3", R(x, y, 300, 24))
			name := fmt.Sprintf("Card %d", n)
			b.Text(b.El(h, "a", R(x, y, 100, 24), pb.Inline(), pb.Attr("href", fmt.Sprintf("/c%d", n))), name)
			b.Text(b.El(col, "p", R(x, y+40, 390, 20)), "About "+name+".")
		}
	}
	got := readOf(b).String()
	for _, l := range strings.Split(got, "\n") {
		if strings.TrimSpace(l) == "-" {
			t.Fatalf("an entry with no text:\n%s", got)
		}
	}
	for i := 1; i <= 9; i++ {
		h, p := fmt.Sprintf("### Card %d\n", i), fmt.Sprintf("About Card %d.", i)
		hi, pi := strings.Index(got, h), strings.Index(got, p)
		if hi < 0 || pi < 0 || hi > pi {
			t.Fatalf("card %d must read heading then text:\n%s", i, got)
		}
	}
}

// A heading in a list item is a line of its own, and the text after it, such
// as the answer under a question, continues the item on the next line. On one
// line, "- ### Question? Answer" would make the answer part of the heading,
// and "- New #### Notes" would be no heading at all.
func TestReadAHeadingInAListItemIsNotRunIntoTheTextAfterIt(t *testing.T) {
	b := pb.New(1280, 800)
	ul := b.El(b.Body(), "ul", R(0, 0, 600, 300))
	faq := b.El(ul, "li", R(0, 0, 600, 60))
	b.Text(b.El(faq, "h3", R(0, 0, 300, 20)), "Is it free?")
	b.Text(b.El(faq, "p", R(0, 25, 300, 20)), "Yes, always.")
	card := b.El(ul, "li", R(0, 80, 600, 90))
	b.Text(b.El(card, "span", R(0, 80, 100, 20), pb.Inline()), "New")
	b.Text(b.El(card, "h4", R(0, 100, 300, 20)), "Release notes")
	b.Text(b.El(card, "p", R(0, 125, 300, 20)), "What changed.")
	two := b.El(ul, "li", R(0, 200, 600, 90))
	b.Text(b.El(two, "h3", R(0, 200, 300, 20)), "First")
	b.Text(b.El(two, "p", R(0, 225, 300, 20)), "one")
	b.Text(b.El(two, "h3", R(0, 250, 300, 20)), "Second")
	b.Text(b.El(two, "p", R(0, 275, 300, 20)), "two")
	got := readOf(b).String()
	for _, want := range []string{
		"- ### Is it free?\n  Yes, always.",
		"- New\n  #### Release notes\n  What changed.",
		"- ### First\n  one\n  ### Second\n  two",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("read view lacks %q:\n%s", want, got)
		}
	}
}

// The line under a list item's heading reads the node it starts with, so an
// answer that changes is one change, not a line gone and another come.
func TestReadAChangedAnswerUnderAListHeadingIsOneChange(t *testing.T) {
	faq := func(answer string) *view.View {
		b := pb.New(1280, 800)
		li := b.El(b.El(b.Body(), "ul", R(0, 0, 600, 100)), "li", R(0, 0, 600, 60))
		b.Text(b.El(li, "h3", R(0, 0, 300, 20)), "Is it free?")
		b.Text(b.El(li, "p", R(0, 25, 300, 20)), answer)
		return readOf(b)
	}
	d := view.Diff(faq("Yes, always."), faq("No, not any more.")).String()
	if !strings.HasPrefix(d, "~ ") || !strings.Contains(d, "No, not any more.") || strings.Contains(d, "Yes, always.") {
		t.Errorf("a changed answer is one changed line:\n%s", d)
	}
}
