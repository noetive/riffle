package view_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/noetive/riffle/internal/facts"
	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/view"
)

func outline(b *pb.Builder, o view.Options) *view.View {
	return view.Compile(facts.Analyze(b.Snapshot()), o)
}

// A page of a heading and plain paragraphs reads as a heading and text lines,
// never as a list of identical items.
func TestProseParagraphsAreNotAList(t *testing.T) {
	b := pb.New(1280, 800)
	box := b.El(b.Body(), "div", R(100, 100, 600, 400))
	h := b.El(box, "h1", R(100, 100, 600, 40), pb.FontPx(32))
	b.Text(h, "Example Domain")
	for i, txt := range []string{
		"This domain is for use in documentation.",
		"You may use this domain without coordination.",
		"Avoid use in operations.",
	} {
		p := b.El(box, "p", R(100, float64(160+i*40), 600, 20))
		b.Text(p, txt)
	}
	a := b.El(box, "a", R(100, 300, 100, 20), pb.Attr("href", "https://iana.org"))
	b.Text(a, "Learn more")
	out := outline(b, view.Options{}).String()
	if strings.Contains(out, "list") {
		t.Errorf("prose collapsed into a list:\n%s", out)
	}
	for _, want := range []string{`h1 "Example Domain"`, `text "This domain is for use in documentation."`, `text "Avoid use in operations."`, `link a1 "Learn more"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// Line breaks, block boundaries and whitespace that has no box of its own
// (as at a wrapped line) separate words.
func TestWordsAreNeverGlued(t *testing.T) {
	b := pb.New(1280, 800)
	p := b.El(b.Body(), "p", R(0, 0, 600, 40))
	b.Text(p, "documentation examples")
	b.El(p, "br", R(0, 0, 0, 18), pb.Inline())
	b.Text(p, "without needing")
	// one span per word, the space between them a span without a box
	for i, w := range []string{"avoid", "relying", "on"} {
		s := b.El(p, "span", R(float64(i*60), 20, 50, 20), pb.Inline())
		b.Text(s, w)
		sp := b.El(p, "span", R(float64(i*60+50), 20, 0, 0), pb.Inline())
		b.Text(sp, " ", pb.Box(R(0, 0, 0, 0)))
	}
	out := outline(b, view.Options{}).String()
	for _, want := range []string{"documentation examples without needing", "avoid relying on"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}

	b = pb.New(1280, 800)
	l := b.El(b.Body(), "a", R(0, 0, 200, 40), pb.Attr("href", "/x"))
	b.Text(l, "First line")
	b.El(l, "br", R(0, 0, 0, 18), pb.Inline())
	b.Text(l, "second line")
	if n := facts.Analyze(b.Snapshot()).Nodes[l].Name; n != "First line second line" {
		t.Errorf("name = %q", n)
	}
}

// storyPage is a front page whose entries alternate between two shapes, so no
// run of alike siblings exists, with navigation above and a footer below.
func storyPage(n int) *pb.Builder {
	b := pb.New(1280, 800)
	b.Snapshot().ContentH = 6000
	body := b.Body()
	nav := b.El(body, "nav", R(0, 0, 1280, 30), pb.Attr("aria-label", "Site"))
	for k := 0; k < 6; k++ {
		a := b.El(nav, "a", R(float64(k*80), 5, 70, 20), pb.Inline(), pb.Attr("href", fmt.Sprintf("/n%d", k)))
		b.Text(a, fmt.Sprintf("Section %d", k))
	}
	h := b.El(body, "h1", R(10, 40, 300, 30), pb.FontPx(32))
	b.Text(h, "Front page")
	main := b.El(body, "main", R(0, 80, 1280, 5000))
	for i := 0; i < n; i++ {
		y := float64(90 + i*60)
		t := b.El(main, "div", R(10, y, 900, 20))
		a := b.El(t, "a", R(10, y, 800, 20), pb.Inline(), pb.Attr("href", fmt.Sprintf("/s%d", i)))
		b.Text(a, fmt.Sprintf("Story number %d has a rather long headline to take up room in the outline", i))
		m := b.El(main, "div", R(10, y+25, 900, 20))
		b.Text(m, fmt.Sprintf("%d points by", 100+i))
		u := b.El(m, "a", R(200, y+25, 80, 20), pb.Inline(), pb.Attr("href", fmt.Sprintf("/u%d", i)))
		b.Text(u, fmt.Sprintf("user%d", i))
	}
	f := b.El(body, "footer", R(0, 5200, 1280, 30))
	fa := b.El(f, "a", R(0, 5200, 80, 20), pb.Attr("href", "/legal"))
	b.Text(fa, "Legal")
	return b
}

// A tight budget keeps the heading, the navigation line and the first
// entries with their refs. It never drops all of the main content while
// small items remain.
func TestBudgetKeepsTheStartOfLongContent(t *testing.T) {
	b := storyPage(80)
	for _, proj := range []view.Projection{view.Outline, view.Interactive} {
		v := outline(b, view.Options{Projection: proj, Budget: 1500})
		out := v.String()
		if v.Tokens() > 1500 {
			t.Errorf("projection %d: %d tokens over budget", proj, v.Tokens())
		}
		if !regexp.MustCompile(`link a\d+ "Story number 0 `).MatchString(out) || !strings.Contains(out, "Story number 10 ") {
			t.Errorf("projection %d lost the first stories:\n%s", proj, out)
		}
		if strings.Count(out, "Story number") < 15 {
			t.Errorf("projection %d keeps too little of the content:\n%s", proj, out)
		}
		if proj == view.Outline && (!strings.Contains(out, `h1 "Front page"`) || !strings.Contains(out, "nav r")) {
			t.Errorf("heading or navigation dropped:\n%s", out)
		}
	}
}

// Headings survive a budget that cuts the body text, wherever they sit.
func TestBudgetKeepsLaterHeadings(t *testing.T) {
	b := pb.New(1280, 800)
	b.Snapshot().ContentH = 9000
	main := b.El(b.Body(), "main", R(0, 0, 1280, 9000))
	for s := 0; s < 8; s++ {
		y := float64(s * 1000)
		h := b.El(main, "h2", R(10, y, 300, 30), pb.FontPx(24))
		b.Text(h, fmt.Sprintf("Section %d", s))
		for k := 0; k < 12; k++ {
			p := b.El(main, "p", R(10, y+40+float64(k*30), 900, 20))
			b.Text(p, fmt.Sprintf("Paragraph %d of section %d with enough words to need a few tokens of budget each.", k, s))
		}
	}
	v := outline(b, view.Options{Budget: 600})
	out := v.String()
	if v.Tokens() > 600 {
		t.Errorf("%d tokens over budget", v.Tokens())
	}
	for s := 0; s < 8; s++ {
		if !strings.Contains(out, fmt.Sprintf(`h2 "Section %d"`, s)) {
			t.Errorf("heading of section %d dropped:\n%s", s, out)
		}
	}
	if !strings.Contains(out, "Paragraph 0 of section 0") {
		t.Errorf("no text kept at all:\n%s", out)
	}
}

// Links inside a sentence are actionable, so they have refs and appear in
// the interactive view.
func TestInlineLinksHaveRefs(t *testing.T) {
	b := pb.New(1280, 800)
	p := b.El(b.Body(), "p", R(0, 0, 600, 20))
	b.Text(p, "Read the ")
	a := b.El(p, "a", R(80, 0, 60, 20), pb.Inline(), pb.Attr("href", "/spec"))
	b.Text(a, "specification")
	b.Text(p, " today.")
	sup := b.El(p, "sup", R(160, 0, 20, 20), pb.Inline())
	c := b.El(sup, "a", R(160, 0, 20, 20), pb.Inline(), pb.Attr("href", "#cite"))
	b.Text(c, "[1]")
	out := outline(b, view.Options{}).String()
	if !regexp.MustCompile(`link a\d+ "specification"`).MatchString(out) || !regexp.MustCompile(`link a\d+ "\[1\]"`).MatchString(out) {
		t.Errorf("inline links lack refs:\n%s", out)
	}
	if strings.Contains(out, "\n  link") || strings.Count(out, "text") != 1 {
		t.Errorf("citation link split from its sentence:\n%s", out)
	}
	in := outline(b, view.Options{Projection: view.Interactive}).String()
	if !strings.Contains(in, `"specification"`) {
		t.Errorf("interactive view lacks the inline link:\n%s", in)
	}
}

// A click handler on a wrapper is delegation, not a control: only the
// actionable nodes inside get refs, and empty headings say nothing.
func TestDelegatedClicksAndEmptyHeadings(t *testing.T) {
	b := pb.New(1280, 800)
	wrap := b.El(b.Body(), "div", R(0, 0, 1280, 800), pb.Clickable())
	card := b.El(wrap, "div", R(10, 10, 300, 40), pb.Clickable())
	a := b.El(card, "a", R(10, 10, 200, 20), pb.Attr("href", "/story"))
	b.Text(a, "A story")
	b.El(wrap, "h3", R(10, 100, 100, 20))
	out := outline(b, view.Options{}).String()
	if strings.Contains(out, "clickable") {
		t.Errorf("wrapper got a control line:\n%s", out)
	}
	if strings.Contains(out, "h3") {
		t.Errorf("empty heading kept:\n%s", out)
	}
	if !strings.Contains(out, `link a1 "A story"`) {
		t.Errorf("link lost:\n%s", out)
	}
}

// Text that sits inside a real heading is part of it, however large.
func TestHeadingPartsAreNotHeadings(t *testing.T) {
	b := pb.New(1280, 800)
	h := b.El(b.Body(), "h1", R(0, 0, 400, 40), pb.FontPx(32))
	s := b.El(h, "span", R(0, 0, 400, 40), pb.Inline(), pb.FontPx(48))
	b.Text(s, "Web browser")
	out := outline(b, view.Options{}).String()
	if !strings.Contains(out, `h1 "Web browser"`) || strings.Contains(out, "h2") {
		t.Errorf("nested heading:\n%s", out)
	}
}

// Tables used to lay out a page do not show up as tables, and their rows
// read as lines in the read view.
func TestLayoutTablesAreTransparent(t *testing.T) {
	b := pb.New(1280, 800)
	outer := b.El(b.Body(), "table", R(0, 0, 1280, 200))
	tr := b.El(outer, "tr", R(0, 0, 1280, 200))
	td := b.El(tr, "td", R(0, 0, 1280, 200))
	inner := b.El(td, "table", R(0, 0, 1280, 100))
	for i, s := range []string{"First story", "Second story"} {
		r := b.El(inner, "tr", R(0, float64(i*40), 1280, 40))
		c1 := b.El(r, "td", R(0, float64(i*40), 100, 40))
		b.Text(c1, fmt.Sprintf("%d.", i+1))
		c2 := b.El(r, "td", R(100, float64(i*40), 400, 40))
		a := b.El(c2, "a", R(100, float64(i*40), 300, 20), pb.Attr("href", "/s"))
		b.Text(a, s)
	}
	o := outline(b, view.Options{}).String()
	if strings.Count(o, "table") != 1 {
		t.Errorf("layout wrapper kept as a table:\n%s", o)
	}
	r := outline(b, view.Options{Projection: view.Read}).String()
	if !strings.Contains(r, "[First story](/s)") {
		t.Errorf("read view of layout table:\n%s", r)
	}
}
