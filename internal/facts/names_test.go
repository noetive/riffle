package facts_test

import (
	"testing"

	"github.com/noetive/riffle/internal/facts"
	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/snapshot"
)

// nameOfNew builds one element with optional text and returns role and name.
func nameOfNew(tag, text string, opts ...pb.Opt) (string, string, string) {
	b := pb.New(1280, 800)
	i := b.El(b.Body(), tag, R(0, 0, 100, 20), opts...)
	if text != "" {
		b.Text(i, text)
	}
	n := analyze(b).Nodes[i]
	return n.Role, n.Name, n.Shown
}

func TestAccessibleNameSources(t *testing.T) {
	for _, c := range []struct {
		name string
		tag  string
		text string
		opts []pb.Opt
		want string
	}{
		{"button content", "button", "  Buy   now ", nil, "Buy now"},
		{"aria-label beats content", "button", "Buy", []pb.Opt{pb.Attr("aria-label", "  Purchase  item ")}, "Purchase item"},
		{"blank aria-label falls to content", "button", "Buy", []pb.Opt{pb.Attr("aria-label", "   ")}, "Buy"},
		{"title fallback on div", "div", "", []pb.Opt{pb.Attr("title", " Tip ")}, "Tip"},
		{"div content is not a name", "div", "words", nil, ""},
		{"img alt", "img", "", []pb.Opt{pb.Attr("alt", "Logo")}, "Logo"},
		{"img title when no alt", "img", "", []pb.Opt{pb.Attr("title", "Pic")}, "Pic"},
		{"img alt beats title", "img", "", []pb.Opt{pb.Attr("alt", "A"), pb.Attr("title", "B")}, "A"},
		{"submit default", "input", "", []pb.Opt{pb.Attr("type", "submit")}, "Submit"},
		{"submit value", "input", "", []pb.Opt{pb.Attr("type", "submit"), pb.Attr("value", "Send it")}, "Send it"},
		{"button input value", "input", "", []pb.Opt{pb.Attr("type", "button"), pb.Attr("value", "Go")}, "Go"},
		{"reset has no default", "input", "", []pb.Opt{pb.Attr("type", "reset")}, ""},
		{"image input alt", "input", "", []pb.Opt{pb.Attr("type", "image"), pb.Attr("alt", "Search")}, "Search"},
		{"text input title", "input", "", []pb.Opt{pb.Attr("title", "Your name")}, "Your name"},
		{"text input placeholder", "input", "", []pb.Opt{pb.Attr("placeholder", "Search...")}, "Search..."},
		{"title beats placeholder", "input", "", []pb.Opt{pb.Attr("title", "T"), pb.Attr("placeholder", "P")}, "T"},
		{"textarea placeholder", "textarea", "", []pb.Opt{pb.Attr("placeholder", "Message")}, "Message"},
		{"select title", "select", "", []pb.Opt{pb.Attr("title", "Size")}, "Size"},
	} {
		_, got, _ := nameOfNew(c.tag, c.text, c.opts...)
		if got != c.want {
			t.Errorf("%s: name = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSubmitValueFromLiveFormState(t *testing.T) {
	b := pb.New(1280, 800)
	i := b.El(b.Body(), "input", R(0, 0, 100, 20), pb.Attr("type", "button"), pb.Value("  Live   value "))
	if got := analyze(b).Nodes[i].Name; got != "Live value" {
		t.Errorf("name = %q", got)
	}
}

func TestNameFromAriaLabelledby(t *testing.T) {
	b := pb.New(1280, 800)
	h1 := b.El(b.Body(), "span", R(0, 0, 50, 20), pb.Attr("id", "a"))
	b.Text(h1, "First")
	h2 := b.El(b.Body(), "span", R(0, 30, 50, 20), pb.Attr("id", "b"))
	b.Text(h2, "Second")
	both := b.El(b.Body(), "div", R(0, 60, 50, 20), pb.Attr("aria-labelledby", "a b"))
	missing := b.El(b.Body(), "div", R(0, 90, 50, 20), pb.Attr("aria-labelledby", "nope b"))
	allMissing := b.El(b.Body(), "button", R(0, 120, 50, 20), pb.Attr("aria-labelledby", "nope"), pb.Attr("aria-label", "Fallback"))
	p := analyze(b)
	if p.Nodes[both].Name != "First Second" {
		t.Errorf("both = %q", p.Nodes[both].Name)
	}
	if p.Nodes[missing].Name != "Second" {
		t.Errorf("missing = %q", p.Nodes[missing].Name)
	}
	if p.Nodes[allMissing].Name != "Fallback" {
		t.Errorf("allMissing = %q", p.Nodes[allMissing].Name)
	}
}

func TestLabelledbyEmptyTargetFallsThrough(t *testing.T) {
	b := pb.New(1280, 800)
	b.El(b.Body(), "span", R(0, 0, 50, 20), pb.Attr("id", "e"))
	btn := b.El(b.Body(), "button", R(0, 30, 50, 20), pb.Attr("aria-labelledby", "e"))
	b.Text(btn, "Own text")
	if got := analyze(b).Nodes[btn].Name; got != "Own text" {
		t.Errorf("name = %q", got)
	}
}

func TestFirstDuplicateIDWins(t *testing.T) {
	b := pb.New(1280, 800)
	one := b.El(b.Body(), "span", R(0, 0, 50, 20), pb.Attr("id", "x"))
	b.Text(one, "one")
	two := b.El(b.Body(), "span", R(0, 30, 50, 20), pb.Attr("id", "x"))
	b.Text(two, "two")
	d := b.El(b.Body(), "div", R(0, 60, 50, 20), pb.Attr("aria-labelledby", "x"))
	if got := analyze(b).Nodes[d].Name; got != "one" {
		t.Errorf("name = %q", got)
	}
}

func TestControlLabelFromLabelElements(t *testing.T) {
	b := pb.New(1280, 800)
	l1 := b.El(b.Body(), "label", R(0, 0, 50, 20), pb.Attr("for", "q"))
	b.Text(l1, "First")
	l2 := b.El(b.Body(), "label", R(0, 30, 50, 20), pb.Attr("for", "q"))
	b.Text(l2, "Second")
	in := b.El(b.Body(), "input", R(60, 0, 80, 20), pb.Attr("id", "q"))
	hiddenL := b.El(b.Body(), "label", R(0, 60, 50, 20), pb.Attr("for", "r"), pb.NotLaid())
	b.Text(hiddenL, "Ghost", pb.NotLaid())
	in2 := b.El(b.Body(), "input", R(60, 60, 80, 20), pb.Attr("id", "r"), pb.Attr("placeholder", "ph"))
	wrap := b.El(b.Body(), "label", R(0, 100, 300, 20))
	b.Text(wrap, "Wrapped")
	in3 := b.El(wrap, "input", R(100, 100, 80, 20))
	wrapBtn := b.El(wrap, "button", R(200, 100, 40, 20))
	b.Text(wrapBtn, "Skip")
	p := analyze(b)
	if p.Nodes[in].Name != "First Second" {
		t.Errorf("for-labels = %q", p.Nodes[in].Name)
	}
	if p.Nodes[in2].Name != "ph" {
		t.Errorf("hidden label must not name: %q", p.Nodes[in2].Name)
	}
	if p.Nodes[in3].Name != "Wrapped" {
		t.Errorf("wrapping label = %q (controls inside the label are left out)", p.Nodes[in3].Name)
	}
}

func TestForLabelBeatsWrappingLabel(t *testing.T) {
	b := pb.New(1280, 800)
	outer := b.El(b.Body(), "label", R(0, 0, 300, 20))
	b.Text(outer, "Wrapping")
	in := b.El(outer, "input", R(100, 0, 80, 20), pb.Attr("id", "z"))
	f := b.El(b.Body(), "label", R(0, 30, 50, 20), pb.Attr("for", "z"))
	b.Text(f, "Explicit")
	if got := analyze(b).Nodes[in].Name; got != "Explicit" {
		t.Errorf("name = %q", got)
	}
}

func TestDialogNamedByFirstVisibleHeading(t *testing.T) {
	b := pb.New(1280, 800)
	d := b.El(b.Body(), "div", R(0, 0, 400, 300), pb.Attr("role", "dialog"))
	hid := b.El(d, "h3", R(0, 0, 100, 20), pb.NotLaid())
	b.Text(hid, "Hidden heading", pb.NotLaid())
	h := b.El(d, "h2", R(0, 30, 100, 20))
	b.Text(h, "Real title")
	h2 := b.El(d, "h2", R(0, 60, 100, 20))
	b.Text(h2, "Second title")
	al := b.El(b.Body(), "div", R(0, 400, 400, 300), pb.Attr("role", "alertdialog"))
	ah := b.El(al, "h1", R(0, 400, 100, 20))
	b.Text(ah, "Alert!")
	p := analyze(b)
	if p.Nodes[d].Name != "Real title" || p.Nodes[al].Name != "Alert!" {
		t.Errorf("dialog names %q, %q", p.Nodes[d].Name, p.Nodes[al].Name)
	}
}

func TestIconOnlyControlNamedByInnerTitle(t *testing.T) {
	b := pb.New(1280, 800)
	link := b.El(b.Body(), "a", R(0, 0, 30, 20), pb.Attr("href", "/"))
	b.El(link, "span", R(0, 0, 10, 10), pb.NotLaid(), pb.Attr("title", "Ghost"))
	b.El(link, "svg", R(0, 0, 20, 20), pb.Attr("title", "Home"))
	btn := b.El(b.Body(), "button", R(0, 30, 30, 20))
	b.El(btn, "svg", R(0, 30, 20, 20), pb.Attr("title", "Close"))
	h := b.El(b.Body(), "h2", R(0, 60, 30, 20))
	b.El(h, "svg", R(0, 60, 20, 20), pb.Attr("title", "Not used for headings"))
	p := analyze(b)
	if p.Nodes[link].Name != "Home" || p.Nodes[btn].Name != "Close" {
		t.Errorf("icon names %q %q", p.Nodes[link].Name, p.Nodes[btn].Name)
	}
	if p.Nodes[h].Name != "" {
		t.Errorf("heading name = %q", p.Nodes[h].Name)
	}
}

func TestOwnTitleOnContentRoleFallsBack(t *testing.T) {
	_, got, _ := nameOfNew("button", "", pb.Attr("title", "Hint"))
	if got != "Hint" {
		t.Errorf("name = %q", got)
	}
}

func TestContentRulesForNames(t *testing.T) {
	b := pb.New(1280, 800)
	btn := b.El(b.Body(), "button", R(0, 0, 200, 20))
	b.Text(btn, "Add")
	b.El(btn, "img", R(0, 0, 10, 10), pb.Attr("alt", "icon"), pb.Inline())
	b.Text(btn, "to cart")
	hid := b.El(btn, "span", R(0, 0, 10, 10), pb.NotLaid())
	b.Text(hid, "HIDDEN", pb.NotLaid())
	blocks := b.El(b.Body(), "button", R(0, 30, 200, 40))
	d1 := b.El(blocks, "div", R(0, 30, 100, 20))
	b.Text(d1, "Line")
	d2 := b.El(blocks, "div", R(0, 50, 100, 20))
	b.Text(d2, "two")
	inl := b.El(b.Body(), "button", R(0, 80, 200, 20))
	i1 := b.El(inl, "span", R(0, 80, 20, 20), pb.Inline())
	b.Text(i1, "Fo")
	i2 := b.El(inl, "span", R(20, 80, 20, 20), pb.Inline())
	b.Text(i2, "rm")
	br := b.El(b.Body(), "button", R(0, 110, 200, 40))
	b.Text(br, "a")
	b.El(br, "br", R(0, 110, 0, 0), pb.Inline())
	b.Text(br, "b")
	p := analyze(b)
	if got := p.Nodes[btn].Name; got != "Add icon to cart" {
		t.Errorf("inline image alt: %q", got)
	}
	if got := p.Nodes[blocks].Name; got != "Line two" {
		t.Errorf("block boundaries: %q", got)
	}
	if got := p.Nodes[inl].Name; got != "Form" {
		t.Errorf("inline elements join without space: %q", got)
	}
	if got := p.Nodes[br].Name; got != "a b" {
		t.Errorf("br separates: %q", got)
	}
}

func TestGeneratedTextContributesToName(t *testing.T) {
	b := pb.New(1280, 800)
	btn := b.El(b.Body(), "button", R(0, 0, 100, 20))
	pse := b.El(btn, "span", R(0, 0, 20, 20), pb.Pseudo("before"), pb.Inline())
	b.Snapshot().Text[pse] = "★"
	b.Text(btn, "Rate")
	if got := analyze(b).Nodes[btn].Name; got != "★Rate" {
		t.Errorf("name = %q", got)
	}
}

func TestUnnamedRegionIsNotARegion(t *testing.T) {
	b := pb.New(1280, 800)
	sec := b.El(b.Body(), "section", R(0, 0, 100, 20), pb.Attr("aria-labelledby", "missing"))
	named := b.El(b.Body(), "section", R(0, 30, 100, 20), pb.Attr("aria-label", "Deals"))
	p := analyze(b)
	if p.Nodes[sec].Role != "" || p.Nodes[named].Role != "region" {
		t.Errorf("roles %q %q", p.Nodes[sec].Role, p.Nodes[named].Role)
	}
}

func TestHiddenElementsHaveNoName(t *testing.T) {
	b := pb.New(1280, 800)
	i := b.El(b.Body(), "button", R(0, 0, 100, 20), pb.Attr("aria-label", "Secret"), pb.NotLaid())
	if got := analyze(b).Nodes[i].Name; got != "" {
		t.Errorf("name = %q", got)
	}
}

func TestShownTextWhenLabelContradictsContent(t *testing.T) {
	for _, c := range []struct {
		name         string
		tag          string
		text         string
		opts         []pb.Opt
		wantShown    string
		wantNameSame string
	}{
		{"link", "a", "Cancel", []pb.Opt{pb.Attr("href", "/"), pb.Attr("aria-label", "Confirm payment")}, "Cancel", "Confirm payment"},
		{"tab", "div", "Cancel", []pb.Opt{pb.Attr("role", "tab"), pb.Attr("aria-label", "Confirm payment")}, "Cancel", "Confirm payment"},
		{"heading", "h2", "Cancel", []pb.Opt{pb.Attr("aria-label", "Confirm payment")}, "Cancel", "Confirm payment"},
		{"textbox is not content-named", "input", "", []pb.Opt{pb.Attr("aria-label", "Confirm payment")}, "", "Confirm payment"},
		{"list is not content-named", "ul", "Cancel", []pb.Opt{pb.Attr("aria-label", "Confirm payment")}, "", "Confirm payment"},
		{"title only is not explicit", "button", "Cancel", []pb.Opt{pb.Attr("title", "Confirm payment")}, "", "Cancel"},
	} {
		_, name, shown := nameOfNew(c.tag, c.text, c.opts...)
		if shown != c.wantShown || name != c.wantNameSame {
			t.Errorf("%s: name=%q shown=%q", c.name, name, shown)
		}
	}
}

func TestShownIgnoresUnseenText(t *testing.T) {
	b := pb.New(1280, 800)
	btn := b.El(b.Body(), "button", R(0, 0, 100, 20), pb.Attr("aria-label", "Pay now"))
	b.Text(btn, "Wire money", pb.Style(snapshot.FontSize, "0px"))
	if got := analyze(b).Nodes[btn].Shown; got != "" {
		t.Errorf("Shown = %q", got)
	}
}

func TestListMarkerIsNotPartOfTheName(t *testing.T) {
	b := pb.New(1280, 800)
	sum := b.El(b.Body(), "summary", R(0, 0, 100, 20))
	mk := b.El(sum, "span", R(0, 0, 20, 20), pb.Pseudo("marker"), pb.Inline())
	b.Snapshot().Text[mk] = "▸ "
	b.Text(sum, "More")
	if got := analyze(b).Nodes[sum].Name; got != "More" {
		t.Errorf("name = %q, a disclosure marker is decoration, not a label", got)
	}
}

func TestSVGTitleNamesAnIconLink(t *testing.T) {
	b := pb.New(1280, 800)
	a := b.El(b.Body(), "a", R(0, 0, 20, 20), pb.Attr("href", "/settings"))
	svg := b.El(a, "svg", R(0, 0, 20, 20))
	title := b.El(svg, "title", R(0, 0, 0, 0))
	b.Text(title, "Settings gear")
	if got := analyze(b).Nodes[a].Name; got != "Settings gear" {
		t.Errorf("name = %q, want the svg title", got)
	}
}

func TestSVGWithoutTitleAddsNothingToTheName(t *testing.T) {
	b := pb.New(1280, 800)
	a := b.El(b.Body(), "a", R(0, 0, 60, 20), pb.Attr("href", "/x"))
	b.El(a, "svg", R(0, 0, 20, 20))
	b.Text(a, "Open")
	if got := analyze(b).Nodes[a].Name; got != "Open" {
		t.Errorf("name = %q", got)
	}
}

func TestControlWithOnlyImageReplacedTextIsNamedByItAndSaysSo(t *testing.T) {
	b := pb.New(1280, 800)
	hidden := b.El(b.Body(), "button", R(0, 0, 80, 30))
	sp := b.El(hidden, "span", R(0, 0, 80, 30), pb.FontPx(0))
	b.Text(sp, "Sign in")
	shown := b.El(b.Body(), "button", R(0, 40, 80, 30))
	b.Text(shown, "Visible name")
	both := b.El(b.Body(), "button", R(0, 80, 80, 30))
	b.Text(b.El(both, "span", R(0, 80, 80, 30), pb.FontPx(0)), "hidden part")
	b.Text(both, "Seen part")
	p := analyze(b)
	if got := p.Nodes[hidden].Name; got != "Sign in" || !p.Nodes[hidden].NameUnseen {
		t.Errorf("name = %q unseen=%v, want the site's own hidden label, flagged", got, p.Nodes[hidden].NameUnseen)
	}
	if p.Nodes[shown].NameUnseen || p.Nodes[shown].Name != "Visible name" {
		t.Errorf("a control with a name a person reads is not flagged: %q %v", p.Nodes[shown].Name, p.Nodes[shown].NameUnseen)
	}
	if got := p.Nodes[both].Name; got != "Seen part" || p.Nodes[both].NameUnseen {
		t.Errorf("hidden text never joins a name a person can read: %q %v", got, p.Nodes[both].NameUnseen)
	}
}

func TestIconFontGlyphIsNotAName(t *testing.T) {
	for _, c := range []struct {
		name, text, title, want string
	}{
		{"bmp private use", "\uf013", "", ""},
		{"spaced glyphs", " \uf013 \ue000 ", "", ""},
		{"supplementary plane", "\U000F0000", "", ""},
		{"own title", "\uf013", "Settings", "Settings"},
		{"glyph with words", "\uf013 Settings", "", "Settings"},
		{"words", "Settings", "", "Settings"},
	} {
		var opts []pb.Opt
		if c.title != "" {
			opts = append(opts, pb.Attr("title", c.title))
		}
		if _, got, _ := nameOfNew("button", c.text, opts...); got != c.want {
			t.Errorf("%s: name = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestIconFontGlyphFallsThroughToInnerTitle(t *testing.T) {
	b := pb.New(1280, 800)
	btn := b.El(b.Body(), "button", R(0, 0, 30, 20))
	ic := b.El(btn, "i", R(0, 0, 10, 10), pb.Attr("title", "Close"))
	b.Text(ic, "\uf00d")
	if got := analyze(b).Nodes[btn].Name; got != "Close" {
		t.Errorf("name = %q", got)
	}
}

func TestClickableBlockOfIconGlyphIsUnnamed(t *testing.T) {
	b := pb.New(1280, 800)
	d := b.El(b.Body(), "div", R(0, 0, 30, 20), pb.Clickable())
	b.Text(d, "\uf013")
	if got := analyze(b).Nodes[d].Name; got != "" {
		t.Errorf("name = %q", got)
	}
}

// The name of a control and the text a view reads agree on which markers are
// decoration: a bullet is dropped, a number or a symbol is kept.
func TestMarkerTextIsPartOfTheNameUnlessItIsABullet(t *testing.T) {
	for _, c := range []struct{ marker, want string }{
		{"▸ ", "More"},
		{"• ", "More"},
		{"", "More"},
		{"1. ", "1. More"},
		{"✓ ", "✓ More"},
		{"iv) ", "iv) More"},
	} {
		b := pb.New(1280, 800)
		sum := b.El(b.Body(), "summary", R(0, 0, 100, 20))
		mk := b.El(sum, "span", R(0, 0, 20, 20), pb.Pseudo("marker"), pb.Inline())
		b.Text(mk, c.marker)
		b.Text(sum, "More")
		if got := analyze(b).Nodes[sum].Name; got != c.want {
			t.Errorf("marker %q: name = %q, want %q", c.marker, got, c.want)
		}
	}
}

func TestDecorativeMarker(t *testing.T) {
	for text, want := range map[string]bool{
		"": true, " ": true, "•": true, "• ": true, "◦": true, "▪": true, "▫": true, "‣": true, "⁃": true,
		"·": true, "●": true, "○": true, "■": true, "□": true, "–": true, "-": true, "▸": true, "▹": true,
		"►": true, "▶": true, "▾": true, "▿": true, "▼": true, " • ": true,
		"1.": false, "1. ": false, "10.": false, "a)": false, "iv.": false, "✓": false, "✗": false, "★": false,
		"• •": false, "--": false, "•1": false,
	} {
		b := pb.New(1280, 800)
		mk := b.El(b.Body(), "span", R(0, 0, 10, 10), pb.Pseudo("marker"))
		b.Snapshot().Text[mk] = text
		if got := facts.DecorativeMarker(b.Snapshot(), mk); got != want {
			t.Errorf("DecorativeMarker(%q) = %v, want %v", text, got, want)
		}
	}
	b := pb.New(1280, 800)
	before := b.El(b.Body(), "span", R(0, 0, 10, 10), pb.Pseudo("before"))
	b.Snapshot().Text[before] = "•"
	if facts.DecorativeMarker(b.Snapshot(), before) {
		t.Error("only a marker can be decoration; ::before content is read")
	}
}

// Accessible Name Computation 1.2, step 2D inside 2F: a descendant's aria-label is
// its text alternative, as for an icon button labelled on its svg.
func TestADescendantsAriaLabelIsItsPartOfTheName(t *testing.T) {
	b := pb.New(1280, 800)
	icon := b.El(b.Body(), "button", R(0, 0, 24, 24))
	b.El(icon, "svg", R(0, 0, 24, 24), pb.Attr("aria-label", " Next  slide "))
	mixed := b.El(b.Body(), "a", R(0, 40, 100, 20), pb.Attr("href", "/cart"))
	b.Text(mixed, "Cart")
	b.El(mixed, "span", R(40, 40, 20, 20), pb.Attr("aria-label", "3 items"), pb.Inline())
	p := analyze(b)
	if got := p.Nodes[icon].Name; got != "Next slide" {
		t.Errorf("icon button name = %q, want the svg's aria-label", got)
	}
	if got := p.Nodes[mixed].Name; got != "Cart 3 items" {
		t.Errorf("link name = %q, want its text and its child's label", got)
	}
}

// The node an aria-labelledby reference reaches gives its own aria-label
// (step 2D applied to the referenced node).
func TestALabelledbyReferenceGivesItsOwnAriaLabel(t *testing.T) {
	b := pb.New(1280, 800)
	b.El(b.Body(), "svg", R(0, 0, 20, 20), pb.Attr("id", "ico"), pb.Attr("aria-label", "Search"))
	btn := b.El(b.Body(), "button", R(0, 30, 20, 20), pb.Attr("aria-labelledby", "ico"))
	if got := analyze(b).Nodes[btn].Name; got != "Search" {
		t.Errorf("name = %q, want the referenced node's aria-label", got)
	}
}

// Step 2A: aria-hidden content is no part of a name computed from content,
// unless an aria-labelledby reference reaches it.
func TestAriaHiddenContentIsLeftOutOfANameUnlessReferenced(t *testing.T) {
	b := pb.New(1280, 800)
	link := b.El(b.Body(), "a", R(0, 0, 200, 20), pb.Attr("href", "/news"))
	b.Text(link, "News ")
	glyph := b.El(link, "span", R(60, 0, 20, 20), pb.Attr("aria-hidden", "true"), pb.Inline())
	b.Text(glyph, "arrow")
	ref := b.El(b.Body(), "span", R(0, 30, 50, 20), pb.Attr("id", "lbl"), pb.Attr("aria-hidden", "true"))
	inner := b.El(ref, "span", R(0, 30, 50, 20), pb.Attr("aria-hidden", "true"), pb.Inline())
	b.Text(inner, "Weather")
	btn := b.El(b.Body(), "button", R(0, 60, 50, 20), pb.Attr("aria-labelledby", "lbl"))
	shown := b.El(b.Body(), "a", R(0, 90, 200, 20), pb.Attr("href", "/x"), pb.Attr("aria-label", "Go"))
	b.Text(shown, "Open ")
	hid := b.El(shown, "span", R(60, 90, 20, 20), pb.Attr("aria-hidden", "true"), pb.Inline())
	b.Text(hid, "now")
	p := analyze(b)
	if got := p.Nodes[link].Name; got != "News" {
		t.Errorf("link name = %q, want the aria-hidden words left out", got)
	}
	if got := p.Nodes[btn].Name; got != "Weather" {
		t.Errorf("labelledby name = %q, want the referenced hidden words", got)
	}
	if got := p.Nodes[shown].Shown; got != "Open now" {
		t.Errorf("shown = %q; the words on the control are what a person sees, hidden from assistive technology or not", got)
	}
}

// A control a person sees is named by its words even when the site hid it
// from assistive technology: the rule for hidden content applies to what is
// inside the control, not to the control the agent sees.
func TestAnAriaHiddenControlAPersonSeesKeepsItsName(t *testing.T) {
	_, name, _ := nameOfNew("button", "Previous", pb.Attr("aria-hidden", "true"))
	if name != "Previous" {
		t.Errorf("name = %q, want the words on the control", name)
	}
}

// A label inside the content is a second channel: when it says something
// other than the words a person reads, the view says so.
func TestALabelInsideTheContentThatContradictsItIsShown(t *testing.T) {
	b := pb.New(1280, 800)
	btn := b.El(b.Body(), "button", R(0, 0, 100, 30))
	in := b.El(btn, "span", R(0, 0, 100, 30), pb.Attr("aria-label", "Delete account"), pb.Inline())
	b.Text(in, "Cancel")
	n := analyze(b).Nodes[btn]
	if n.Name != "Delete account" || n.Shown != "Cancel" {
		t.Errorf("name=%q shown=%q, want the label as name and the words as shown", n.Name, n.Shown)
	}
}

// Step 2C: a field inside a name's content gives its value, not its label.
func TestAFieldInsideANameGivesItsValue(t *testing.T) {
	b := pb.New(1280, 800)
	lbl := b.El(b.Body(), "span", R(0, 0, 300, 20), pb.Attr("id", "f"))
	b.Text(lbl, "Flash the screen ")
	b.El(lbl, "input", R(120, 0, 40, 20), pb.Attr("type", "number"), pb.Attr("aria-label", "Number of times"), pb.Value("3"), pb.Inline())
	b.Text(lbl, " times")
	size := b.El(lbl, "select", R(200, 0, 60, 20), pb.Attr("aria-label", "Size"), pb.Inline())
	small := b.El(size, "option", R(0, 0, 0, 0))
	b.Text(small, "small")
	large := b.El(size, "option", R(0, 0, 0, 0), pb.Ticked())
	b.Text(large, "large")
	box := b.El(b.Body(), "input", R(0, 30, 20, 20), pb.Attr("type", "checkbox"), pb.Attr("aria-labelledby", "f"))
	if got := analyze(b).Nodes[box].Name; got != "Flash the screen 3 times large" {
		t.Errorf("name = %q, want the fields' values", got)
	}
}

// Step 2A: a reference to a hidden node reads it whole; a reference to a
// shown node still leaves out what inside it is hidden.
func TestAReferenceReadsAHiddenTargetWholeButNotTheHiddenPartsOfAShownOne(t *testing.T) {
	b := pb.New(1280, 800)
	gone := b.El(b.Body(), "span", R(0, 0, 0, 0), pb.Attr("id", "g"), pb.NotLaid())
	b.Text(gone, "Close", pb.NotLaid())
	closeBtn := b.El(b.Body(), "button", R(0, 30, 30, 30), pb.Attr("aria-labelledby", "g"))
	h := b.El(b.Body(), "h2", R(0, 60, 300, 30), pb.Attr("id", "t"))
	icon := b.El(h, "span", R(0, 60, 20, 30), pb.Attr("aria-hidden", "true"), pb.Inline())
	b.Text(icon, "warning ")
	b.Text(h, "Delete file?")
	dlg := b.El(b.Body(), "div", R(0, 100, 400, 200), pb.Attr("role", "dialog"), pb.Attr("aria-labelledby", "t"))
	p := analyze(b)
	if got := p.Nodes[closeBtn].Name; got != "Close" {
		t.Errorf("button name = %q, want the hidden target's words", got)
	}
	if got := p.Nodes[dlg].Name; got != "Delete file?" {
		t.Errorf("dialog name = %q, want the shown heading without its hidden icon word", got)
	}
}

func TestEachKindOfFieldGivesItsValueInsideAName(t *testing.T) {
	for _, c := range []struct {
		name string
		tag  string
		opts []pb.Opt
		want string
	}{
		{"typed text", "input", []pb.Opt{pb.Attr("value", "old"), pb.Value("typed")}, "Pay typed now"},
		{"text attribute only", "input", []pb.Opt{pb.Attr("value", "given")}, "Pay given now"},
		{"slider text", "div", []pb.Opt{pb.Attr("role", "slider"), pb.Attr("aria-valuetext", "medium"), pb.Attr("aria-valuenow", "5")}, "Pay medium now"},
		{"spinbutton number", "div", []pb.Opt{pb.Attr("role", "spinbutton"), pb.Attr("aria-valuenow", "7")}, "Pay 7 now"},
		{"range input", "input", []pb.Opt{pb.Attr("type", "range"), pb.Value("40")}, "Pay 40 now"},
	} {
		b := pb.New(1280, 800)
		lbl := b.El(b.Body(), "span", R(0, 0, 300, 20), pb.Attr("id", "f"))
		b.Text(lbl, "Pay ")
		b.El(lbl, c.tag, R(40, 0, 60, 20), append([]pb.Opt{pb.Attr("aria-label", "Field"), pb.Inline()}, c.opts...)...)
		b.Text(lbl, " now")
		box := b.El(b.Body(), "input", R(0, 30, 20, 20), pb.Attr("type", "checkbox"), pb.Attr("aria-labelledby", "f"))
		if got := analyze(b).Nodes[box].Name; got != c.want {
			t.Errorf("%s: name = %q, want %q", c.name, got, c.want)
		}
	}
}

// With no name by the rules, the words a person reads on a control name it.
func TestAControlWhoseOnlyWordsAreAriaHiddenIsNamedByThem(t *testing.T) {
	b := pb.New(1280, 800)
	x := b.El(b.Body(), "button", R(0, 0, 30, 30))
	cross := b.El(x, "span", R(0, 0, 30, 30), pb.Attr("aria-hidden", "true"), pb.Inline())
	b.Text(cross, "×")
	cart := b.El(b.Body(), "a", R(0, 40, 30, 30), pb.Attr("href", "/cart"))
	svg := b.El(cart, "svg", R(0, 40, 30, 30), pb.Attr("aria-hidden", "true"))
	title := b.El(svg, "title", R(0, 0, 0, 0))
	b.Text(title, "Cart")
	p := analyze(b)
	if got := p.Nodes[x].Name; got != "×" {
		t.Errorf("button name = %q, want the cross a person sees", got)
	}
	if !p.Nodes[x].Unlabelled || !p.Nodes[cart].Unlabelled {
		t.Errorf("unlabelled: button=%v link=%v; neither has an accessible name", p.Nodes[x].Unlabelled, p.Nodes[cart].Unlabelled)
	}
	if got := p.Nodes[cart].Name; got != "Cart" {
		t.Errorf("link name = %q, want the icon's title", got)
	}
}

// What a hidden reference holds is read, but never script or style source.
func TestAHiddenReferenceNeverReadsScriptOrStyle(t *testing.T) {
	b := pb.New(1280, 800)
	gone := b.El(b.Body(), "div", R(0, 0, 0, 0), pb.Attr("id", "g"), pb.NotLaid())
	b.Text(gone, "Help ", pb.NotLaid())
	sc := b.El(gone, "script", R(0, 0, 0, 0), pb.NotLaid())
	b.Text(sc, "track()", pb.NotLaid())
	btn := b.El(b.Body(), "button", R(0, 30, 30, 30), pb.Attr("aria-labelledby", "g"))
	if got := analyze(b).Nodes[btn].Name; got != "Help" {
		t.Errorf("name = %q, want the words without script source", got)
	}
}

// An editable box inside a name has no value of its own: its words count.
func TestAnEditableBoxInsideANameGivesItsWords(t *testing.T) {
	b := pb.New(1280, 800)
	h := b.El(b.Body(), "h2", R(0, 0, 300, 30), pb.Attr("id", "t"))
	ed := b.El(h, "div", R(0, 0, 300, 30), pb.Attr("role", "textbox"), pb.Attr("contenteditable", "true"))
	b.Text(ed, "Quarterly report")
	dlg := b.El(b.Body(), "div", R(0, 40, 400, 200), pb.Attr("role", "dialog"), pb.Attr("aria-labelledby", "t"))
	if got := analyze(b).Nodes[dlg].Name; got != "Quarterly report" {
		t.Errorf("name = %q, want the editable box's words", got)
	}
}

// A password is never part of a name, wherever the field sits.
func TestAPasswordNeverGoesIntoAName(t *testing.T) {
	b := pb.New(1280, 800)
	row := b.El(b.Body(), "div", R(0, 0, 300, 20), pb.Attr("id", "row"))
	b.Text(row, "Password ")
	b.El(row, "input", R(80, 0, 100, 20), pb.Attr("type", "password"), pb.Attr("value", "hunter2"), pb.Value("hunter2"), pb.Inline())
	btn := b.El(b.Body(), "button", R(0, 30, 60, 20), pb.Attr("aria-labelledby", "row"))
	if got := analyze(b).Nodes[btn].Name; got != "Password" {
		t.Errorf("name = %q, want no password in it", got)
	}
}

// The control's own title is a name the rules give; the words the site hid
// from assistive technology come only after it.
func TestAControlsOwnTitleComesBeforeItsHiddenWords(t *testing.T) {
	b := pb.New(1280, 800)
	x := b.El(b.Body(), "button", R(0, 0, 30, 30), pb.Attr("title", "Close"))
	cross := b.El(x, "span", R(0, 0, 30, 30), pb.Attr("aria-hidden", "true"), pb.Inline())
	b.Text(cross, "×")
	n := analyze(b).Nodes[x]
	if n.Name != "Close" || n.Unlabelled {
		t.Errorf("name = %q unlabelled = %v, want the control's title", n.Name, n.Unlabelled)
	}
}
