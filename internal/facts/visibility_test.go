package facts_test

import (
	"testing"

	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/snapshot"
)

// elementWith adds one element to a 1280x800 page and returns its facts.
func elementWith(tag string, box snapshot.Rect, opts ...pb.Opt) (nodeFacts, *pb.Builder, int32) {
	b := pb.New(1280, 800)
	i := b.El(b.Body(), tag, box, opts...)
	return nodeFacts{b: b, i: i}, b, i
}

type nodeFacts struct {
	b *pb.Builder
	i int32
}

func (n nodeFacts) hidden() bool    { return analyze(n.b).Nodes[n.i].Hidden }
func (n nodeFacts) offscreen() bool { return analyze(n.b).Nodes[n.i].Offscreen }
func (n nodeFacts) clipped() bool   { return analyze(n.b).Nodes[n.i].Clipped }

func TestHiddenByBoxSize(t *testing.T) {
	for _, c := range []struct {
		name string
		box  snapshot.Rect
		want bool
	}{
		{"normal", R(0, 0, 100, 20), false},
		{"zero size", R(0, 0, 0, 0), true},
		{"1x1 pixel", R(0, 0, 1, 1), true},
		{"2 wide 1 high", R(0, 0, 2, 1), false},
		{"1 wide 2 high", R(0, 0, 1, 2), false},
		{"wide but no height", R(0, 0, 100, 0), true},
		{"tall but no width", R(0, 0, 0, 100), true},
		{"negative width", R(0, 0, -5, 100), true},
	} {
		n, _, _ := elementWith("div", c.box)
		if got := n.hidden(); got != c.want {
			t.Errorf("%s: Hidden = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestContainerWithZeroBoxIsVisibleThroughSolidDescendant(t *testing.T) {
	b := pb.New(1280, 800)
	wrap := b.El(b.Body(), "div", R(0, 0, 0, 0))
	mid := b.El(wrap, "div", R(0, 0, 0, 0))
	b.El(mid, "div", R(0, 0, 50, 20))
	empty := b.El(b.Body(), "div", R(0, 100, 0, 0))
	b.El(empty, "div", R(0, 100, 0, 0))
	// An unlaid descendant has no box and can not carry the container.
	unlaidWrap := b.El(b.Body(), "div", R(0, 200, 0, 0))
	b.El(unlaidWrap, "div", R(0, 200, 50, 20), pb.NotLaid())
	p := analyze(b)
	if p.Nodes[wrap].Hidden || p.Nodes[mid].Hidden {
		t.Error("a zero box that holds a solid descendant is not hidden")
	}
	if !p.Nodes[empty].Hidden {
		t.Error("a zero box with only empty descendants is hidden")
	}
	if !p.Nodes[unlaidWrap].Hidden {
		t.Error("an unlaid descendant must not make its parent solid")
	}
}

func TestHiddenByDisplayAndRenderingRules(t *testing.T) {
	for _, c := range []struct {
		name string
		tag  string
		opts []pb.Opt
		want bool
	}{
		{"display none", "div", []pb.Opt{pb.Style(snapshot.Display, "none")}, true},
		{"not laid", "div", []pb.Opt{pb.NotLaid()}, true},
		{"display contents not laid", "div", []pb.Opt{pb.NotLaid(), pb.Style(snapshot.Display, "contents")}, false},
		{"script", "script", nil, true},
		{"style", "style", nil, true},
		{"head", "head", nil, true},
		{"meta", "meta", nil, true},
		{"link", "link", nil, true},
		{"title", "title", nil, true},
		{"template", "template", nil, true},
		{"noscript", "noscript", nil, true},
		{"base", "base", nil, true},
		{"input hidden", "input", []pb.Opt{pb.Attr("type", "hidden")}, true},
		{"input HIDDEN", "input", []pb.Opt{pb.Attr("type", "HIDDEN")}, true},
		{"input text", "input", []pb.Opt{pb.Attr("type", "text")}, false},
		{"div type hidden", "div", []pb.Opt{pb.Attr("type", "hidden")}, false},
		{"opacity 0", "div", []pb.Opt{pb.Style(snapshot.Opacity, "0")}, true},
		{"opacity 0.01", "div", []pb.Opt{pb.Style(snapshot.Opacity, "0.01")}, true},
		{"opacity 0.02", "div", []pb.Opt{pb.Style(snapshot.Opacity, "0.02")}, false},
		{"opacity half", "div", []pb.Opt{pb.Style(snapshot.Opacity, "0.5")}, false},
		{"opacity negative", "div", []pb.Opt{pb.Style(snapshot.Opacity, "-1")}, true},
		{"opacity unset", "div", []pb.Opt{pb.Style(snapshot.Opacity, "")}, false},
		{"opacity junk", "div", []pb.Opt{pb.Style(snapshot.Opacity, "auto")}, false},
		{"visibility hidden", "div", []pb.Opt{pb.Style(snapshot.Visibility, "hidden")}, true},
		{"visibility collapse", "div", []pb.Opt{pb.Style(snapshot.Visibility, "collapse")}, true},
		{"visibility visible", "div", []pb.Opt{pb.Style(snapshot.Visibility, "visible")}, false},
	} {
		n, _, _ := elementWith(c.tag, R(0, 0, 100, 20), c.opts...)
		if got := n.hidden(); got != c.want {
			t.Errorf("%s: Hidden = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDisplayContentsNeedsNoBox(t *testing.T) {
	n, _, _ := elementWith("div", R(0, 0, 0, 0), pb.Style(snapshot.Display, "contents"))
	if n.hidden() {
		t.Error("display:contents generates no box and is still present")
	}
}

func TestHiddenSubtreeAndVisibilityOverride(t *testing.T) {
	b := pb.New(1280, 800)
	gone := b.El(b.Body(), "div", R(0, 0, 100, 20), pb.Style(snapshot.Display, "none"))
	kid := b.El(gone, "span", R(0, 0, 50, 20))
	kidText := b.Text(kid, "x")
	invis := b.El(b.Body(), "div", R(0, 30, 100, 20), pb.Style(snapshot.Visibility, "hidden"))
	shown := b.El(invis, "span", R(0, 30, 50, 20), pb.Style(snapshot.Visibility, "visible"))
	p := analyze(b)
	if !p.Nodes[kid].Hidden || !p.Nodes[kidText].Hidden {
		t.Error("everything under display:none is hidden")
	}
	if !p.Nodes[invis].Hidden || p.Nodes[shown].Hidden {
		t.Error("visibility:visible inside visibility:hidden shows again")
	}
}

func TestHiddenTextRules(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	d := b.El(body, "div", R(0, 0, 300, 20))
	words := b.Text(d, "hello")
	unlaid := b.Text(d, "ghost", pb.NotLaid())
	emptyBox := b.Text(d, "collapsed", pb.Box(R(0, 0, 0, 0)))
	spaces := b.Text(d, "   ", pb.Box(R(0, 0, 0, 0)))
	unlaidSpaces := b.Text(d, "   ", pb.NotLaid())
	p := analyze(b)
	if p.Nodes[words].Hidden {
		t.Error("ordinary text hidden")
	}
	if !p.Nodes[unlaid].Hidden {
		t.Error("text without layout is hidden")
	}
	if !p.Nodes[emptyBox].Hidden {
		t.Error("words with an empty box are hidden")
	}
	if p.Nodes[spaces].Hidden || p.Nodes[unlaidSpaces].Hidden {
		t.Error("whitespace separates words even without a box")
	}
}

func TestOtherNodeKindsAreHidden(t *testing.T) {
	b := pb.New(1280, 800)
	c := b.El(b.Body(), "div", R(0, 0, 100, 20))
	b.Snapshot().Kind[c] = snapshot.KindOther
	if !analyze(b).Nodes[c].Hidden {
		t.Error("comments and other node kinds never render")
	}
	if analyze(b).Nodes[0].Hidden {
		t.Error("the document itself is not hidden")
	}
}

func TestLineBreaksAndSpaceOnlyInlinesStayPresent(t *testing.T) {
	b := pb.New(1280, 800)
	p0 := b.El(b.Body(), "p", R(0, 0, 300, 40))
	br := b.El(p0, "br", R(0, 0, 0, 0))
	wbr := b.El(p0, "wbr", R(0, 0, 0, 0))
	brUnlaid := b.El(p0, "br", R(0, 0, 0, 0), pb.NotLaid())
	spaceSpan := b.El(p0, "span", R(0, 0, 0, 0), pb.Inline())
	b.Text(spaceSpan, " ", pb.Box(R(0, 0, 0, 0)))
	inlineBlock := b.El(p0, "span", R(0, 0, 0, 0), pb.Style(snapshot.Display, "inline-block"))
	b.Text(inlineBlock, " ", pb.Box(R(0, 0, 0, 0)))
	wordSpan := b.El(p0, "span", R(0, 0, 0, 0), pb.Inline())
	b.Text(wordSpan, "word", pb.Box(R(0, 0, 0, 0)))
	blockSpace := b.El(p0, "div", R(0, 0, 0, 0))
	b.Text(blockSpace, " ", pb.Box(R(0, 0, 0, 0)))
	emptySpan := b.El(p0, "span", R(0, 0, 0, 0), pb.Inline())
	nested := b.El(p0, "span", R(0, 0, 0, 0), pb.Inline())
	b.El(nested, "i", R(0, 0, 0, 0), pb.Inline())
	r := analyze(b)
	for name, i := range map[string]int32{"br": br, "wbr": wbr, "space inline": spaceSpan, "space inline-block": inlineBlock} {
		if r.Nodes[i].Hidden {
			t.Errorf("%s must stay present", name)
		}
	}
	for name, i := range map[string]int32{"unlaid br": brUnlaid, "inline with words but no box": wordSpan, "block with only space": blockSpace, "empty inline": emptySpan, "inline with element child": nested} {
		if !r.Nodes[i].Hidden {
			t.Errorf("%s must be hidden", name)
		}
	}
}

func TestOffscreenBoundaries(t *testing.T) {
	for _, c := range []struct {
		name string
		box  snapshot.Rect
		want bool
	}{
		{"inside", R(10, 10, 100, 20), false},
		{"right edge touches", R(0, 0, 100, 20), false},
		{"starts at viewport width", R(1280, 10, 100, 20), true},
		{"starts just inside right", R(1279, 10, 100, 20), false},
		{"starts at viewport height", R(10, 800, 100, 20), true},
		{"starts just inside bottom", R(10, 799, 100, 20), false},
		{"ends exactly at left edge", R(-100, 10, 100, 20), true},
		{"ends just inside left", R(-99, 10, 100, 20), false},
		{"ends exactly at top", R(10, -20, 100, 20), true},
		{"ends just inside top", R(10, -19, 100, 20), false},
		{"far left", R(-9999, 10, 300, 20), true},
		{"far top", R(10, -9999, 300, 20), true},
	} {
		n, _, _ := elementWith("div", c.box)
		if got := n.offscreen(); got != c.want {
			t.Errorf("%s: Offscreen = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestOffscreenZeroWidthBoxAtEdges(t *testing.T) {
	b := pb.New(1280, 800)
	d := b.El(b.Body(), "div", R(0, 0, 300, 20))
	edge := b.Text(d, "\n", pb.Box(R(0, 0, 0, 16)))
	left := b.Text(d, "\n", pb.Box(R(-1, 0, 0, 16)))
	p := analyze(b)
	if p.Nodes[edge].Offscreen {
		t.Error("a zero-width box on the left margin is where the reader is")
	}
	if !p.Nodes[left].Offscreen {
		t.Error("a zero-width box left of the margin is offscreen")
	}
}

func TestOffscreenUsesScrollableDocument(t *testing.T) {
	b := pb.New(1280, 800)
	b.Snapshot().ContentW, b.Snapshot().ContentH = 3000, 5000
	right := b.El(b.Body(), "div", R(2000, 10, 100, 20))
	below := b.El(b.Body(), "div", R(10, 4000, 100, 20))
	pastRight := b.El(b.Body(), "div", R(3000, 10, 100, 20))
	pastBottom := b.El(b.Body(), "div", R(10, 5000, 100, 20))
	p := analyze(b)
	if p.Nodes[right].Offscreen || p.Nodes[below].Offscreen {
		t.Error("scrollable area is reachable")
	}
	if !p.Nodes[pastRight].Offscreen || !p.Nodes[pastBottom].Offscreen {
		t.Error("beyond the scrollable area is unreachable")
	}
	// A document smaller than the viewport still spans the viewport.
	c := pb.New(1280, 800)
	c.Snapshot().ContentW, c.Snapshot().ContentH = 600, 400
	in := c.El(c.Body(), "div", R(1000, 600, 100, 20))
	if analyze(c).Nodes[in].Offscreen {
		t.Error("the viewport is always reachable")
	}
}

func TestHiddenNodesAreNeverOffscreenOrClipped(t *testing.T) {
	b := pb.New(1280, 800)
	i := b.El(b.Body(), "div", R(-9999, 0, 100, 20), pb.NotLaid())
	n := analyze(b).Nodes[i]
	if n.Offscreen || n.Clipped {
		t.Error("hidden nodes carry no position facts")
	}
}

func TestClippedByOverflowAncestor(t *testing.T) {
	for _, c := range []struct {
		name     string
		overflow snapshot.Prop
		value    string
		child    snapshot.Rect
		want     bool
	}{
		{"right of box", snapshot.OverflowX, "hidden", R(100, 0, 50, 20), true},
		{"just inside right", snapshot.OverflowX, "hidden", R(99, 0, 50, 20), false},
		{"left of box", snapshot.OverflowX, "hidden", R(-50, 0, 50, 20), true},
		{"just inside left", snapshot.OverflowX, "hidden", R(-49, 0, 50, 20), false},
		{"below box", snapshot.OverflowY, "hidden", R(0, 20, 50, 20), true},
		{"just inside bottom", snapshot.OverflowY, "hidden", R(0, 19, 50, 20), false},
		{"above box", snapshot.OverflowY, "hidden", R(0, -20, 50, 20), true},
		{"just inside top", snapshot.OverflowY, "hidden", R(0, -19, 50, 20), false},
		{"overflow clip", snapshot.OverflowX, "clip", R(100, 0, 50, 20), true},
		{"overflow clip y", snapshot.OverflowY, "clip", R(0, 20, 50, 20), true},
		{"overflow scroll does not clip", snapshot.OverflowX, "scroll", R(100, 0, 50, 20), false},
		{"overflow auto does not clip", snapshot.OverflowY, "auto", R(0, 20, 50, 20), false},
		{"overflow visible does not clip", snapshot.OverflowX, "visible", R(100, 0, 50, 20), false},
		{"x clip does not clip vertically", snapshot.OverflowX, "hidden", R(0, 20, 50, 20), false},
		{"y clip does not clip horizontally", snapshot.OverflowY, "hidden", R(100, 0, 50, 20), false},
	} {
		b := pb.New(1280, 800)
		host := b.El(b.Body(), "div", R(0, 0, 100, 20), pb.Style(c.overflow, c.value))
		kid := b.El(host, "span", c.child)
		if got := analyze(b).Nodes[kid].Clipped; got != c.want {
			t.Errorf("%s: Clipped = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestClippingReachesThroughIntermediateAncestors(t *testing.T) {
	b := pb.New(1280, 800)
	host := b.El(b.Body(), "div", R(0, 0, 100, 20), pb.Style(snapshot.OverflowX, "hidden"))
	mid := b.El(host, "div", R(0, 0, 300, 20))
	kid := b.El(mid, "span", R(200, 0, 50, 20))
	if !analyze(b).Nodes[kid].Clipped {
		t.Error("a clipping grandparent hides what is outside its box")
	}
}

func TestFixedAndBodyEscapeClipping(t *testing.T) {
	b := pb.New(1280, 800)
	host := b.El(b.Body(), "div", R(0, 0, 100, 20), pb.Style(snapshot.OverflowX, "hidden"))
	fixed := b.El(host, "span", R(100, 0, 50, 20), pb.Position("fixed"))
	deep := b.El(fixed, "span", R(110, 0, 20, 20))
	mid := b.El(host, "div", R(0, 0, 400, 20), pb.Position("fixed"))
	viaFixedMid := b.El(mid, "span", R(300, 0, 20, 20))
	p := analyze(b)
	if p.Nodes[fixed].Clipped || p.Nodes[deep].Clipped || p.Nodes[viaFixedMid].Clipped {
		t.Error("fixed positioning escapes ancestor overflow clipping")
	}
	// Overflow on the body or html element never clips: the page scrolls.
	c := pb.New(1280, 800)
	c.Snapshot().Style[c.Body()][snapshot.OverflowY] = "hidden"
	kid := c.El(c.Body(), "div", R(0, 790, 100, 20))
	if analyze(c).Nodes[kid].Clipped {
		t.Error("body overflow is the viewport's concern")
	}
}

func TestUnlaidAndNonElementParentsDoNotClip(t *testing.T) {
	b := pb.New(1280, 800)
	host := b.El(b.Body(), "div", R(0, 0, 100, 20), pb.Style(snapshot.OverflowX, "hidden"), pb.Style(snapshot.Display, "contents"), pb.NotLaid())
	kid := b.El(host, "span", R(500, 0, 50, 20))
	if analyze(b).Nodes[kid].Clipped {
		t.Error("an ancestor without a box clips nothing")
	}
}

func TestClipPathThatRemovesEverythingClips(t *testing.T) {
	for clip, want := range map[string]bool{
		"inset(100%)":                  true,
		"inset(50%)":                   true,
		"circle(0)":                    true,
		"circle(0px at 50% 50%)":       true,
		"inset(10%)":                   false,
		"circle(40px)":                 false,
		"none":                         false,
		"":                             false,
		"polygon(0 0, 1px 1px, 0 1px)": false,
	} {
		n, _, _ := elementWith("div", R(0, 0, 100, 20), pb.Style(snapshot.ClipPath, clip))
		if got := n.clipped(); got != want {
			t.Errorf("clip-path %q: Clipped = %v, want %v", clip, got, want)
		}
	}
}

func TestScrollRegionContentIsReachableUnlessTheRegionItselfIsNot(t *testing.T) {
	b := pb.New(1280, 800)
	reach := b.El(b.Body(), "div", R(0, 0, 100, 100), pb.Style(snapshot.OverflowY, "auto"))
	inside := b.El(reach, "button", R(0, 1000, 50, 20))
	drawer := b.El(b.Body(), "div", R(-9999, 0, 100, 100), pb.Style(snapshot.OverflowY, "auto"))
	inDrawer := b.El(drawer, "button", R(-9999, 1000, 50, 20))
	hidden := b.El(b.Body(), "div", R(0, 200, 100, 100), pb.Style(snapshot.OverflowY, "hidden"))
	inHidden := b.El(hidden, "button", R(0, 1000, 50, 20))
	p := analyze(b)
	if p.Nodes[inside].Offscreen {
		t.Error("a control the page cannot scroll to but its scroll region can is reachable")
	}
	if !p.Nodes[inDrawer].Offscreen {
		t.Error("a control in a scroll region that is itself off the page is not reachable")
	}
	if !p.Nodes[inHidden].Offscreen {
		t.Error("a region that does not scroll cannot bring its content into reach")
	}
}

func TestInvisibleCheckboxDrawnOverByAVisibleElementStaysReachable(t *testing.T) {
	b := pb.New(1280, 800)
	li := b.El(b.Body(), "li", R(0, 0, 300, 40))
	toggle := b.El(li, "input", R(0, 0, 40, 40), pb.Attr("type", "checkbox"), pb.Style(snapshot.Opacity, "0"))
	label := b.El(li, "label", R(0, 0, 300, 40))
	b.Text(label, "buy milk")
	lone := b.El(b.Body(), "input", R(0, 100, 40, 40), pb.Attr("type", "checkbox"), pb.Style(snapshot.Opacity, "0"))
	apart := b.El(b.Body(), "div", R(0, 200, 300, 40))
	far := b.El(apart, "input", R(0, 200, 40, 40), pb.Attr("type", "checkbox"), pb.Style(snapshot.Opacity, "0"))
	b.Text(b.El(apart, "span", R(100, 200, 100, 40)), "elsewhere")
	text := b.El(b.Body(), "input", R(0, 300, 40, 40), pb.Attr("type", "text"), pb.Style(snapshot.Opacity, "0"))
	p := analyze(b)
	if p.Nodes[toggle].Hidden {
		t.Error("a checkbox a visible label is drawn over is what a person clicks")
	}
	if !p.Nodes[lone].Hidden {
		t.Error("an invisible checkbox with nothing drawn in its place is hidden")
	}
	if !p.Nodes[far].Hidden {
		t.Error("something elsewhere on the row does not stand in for the checkbox")
	}
	if !p.Nodes[text].Hidden {
		t.Error("only checkboxes and radios are replaced by custom drawing")
	}
}

func TestBoxlessWrapperWithLaidOutContentIsDisplayContentsNotHidden(t *testing.T) {
	b := pb.New(1280, 800)
	wrap := b.El(b.Body(), "slot", R(0, 0, 0, 0), pb.NotLaid())
	btn := b.El(wrap, "button", R(0, 0, 80, 20), pb.Laid())
	b.Text(btn, "Buy now")
	deep := b.El(b.Body(), "div", R(0, 0, 0, 0), pb.NotLaid())
	inner := b.El(deep, "div", R(0, 0, 0, 0), pb.NotLaid())
	live := b.El(inner, "a", R(0, 40, 60, 20), pb.Attr("href", "/x"), pb.Laid())
	none := b.El(b.Body(), "div", R(0, 0, 0, 0), pb.NotLaid())
	gone := b.El(none, "span", R(0, 0, 0, 0), pb.NotLaid())
	b.Text(gone, "display none", pb.NotLaid())
	p := analyze(b)
	if p.Nodes[wrap].Hidden || p.Nodes[btn].Hidden {
		t.Error("a slot has no box of its own; what is slotted into it is on the page")
	}
	if p.Nodes[deep].Hidden || p.Nodes[inner].Hidden || p.Nodes[live].Hidden {
		t.Error("nested box-less wrappers pass their laid-out content through")
	}
	if !p.Nodes[none].Hidden || !p.Nodes[gone].Hidden {
		t.Error("a subtree with nothing laid out is display none")
	}
}

// A fixed box belongs to the viewport, not to the box of the element it sits
// in: a wrapper whose own box is empty above the page, empty past its end, or
// clipped away still holds the dialog drawn on screen, so neither it nor the
// dialog is out of reach.
func TestAWrapperHoldingAFixedDialogIsNotOutOfReach(t *testing.T) {
	for name, wrap := range map[string][]pb.Opt{
		"empty at the top":          nil,
		"empty past the page's end": {pb.Box(R(0, 800, 1280, 0))},
		"clipped to nothing":        {pb.Box(R(0, 300, 1280, 0)), pb.Style(snapshot.OverflowY, "hidden"), pb.Style(snapshot.OverflowX, "hidden")},
	} {
		t.Run(name, func(t *testing.T) {
			b := pb.New(1280, 800)
			root := b.El(b.Body(), "div", R(0, 0, 1280, 0), wrap...)
			// A static dialog box inside the wrapper, clipped with it, and the
			// fixed panel it draws.
			dlg := b.El(root, "div", R(0, 300, 1280, 0), pb.Attr("role", "dialog"), pb.Attr("aria-modal", "true"))
			panel := b.El(dlg, "div", R(320, 160, 640, 300), pb.Position("fixed"))
			btn := b.El(panel, "button", R(340, 400, 100, 30))
			b.Text(btn, "Accept")
			words := b.El(dlg, "p", R(0, 300, 1280, 20))
			b.Text(words, "Behind the clip")
			p := analyze(b)
			for _, i := range []int32{root, dlg, panel, btn} {
				if !p.Nodes[i].Visible() {
					t.Errorf("node %d: offscreen=%v clipped=%v; it holds what is drawn on screen", i, p.Nodes[i].Offscreen, p.Nodes[i].Clipped)
				}
			}
			if name == "clipped to nothing" && p.Nodes[words].Visible() {
				t.Error("the dialog's own static words are still clipped away")
			}
		})
	}
}

// A wrapper with nothing on screen inside it stays out of reach.
func TestAnEmptyWrapperWithNothingDrawnStaysOutOfReach(t *testing.T) {
	b := pb.New(1280, 800)
	root := b.El(b.Body(), "div", R(0, -500, 1280, 100))
	inner := b.El(root, "div", R(0, -500, 1280, 100))
	b.Text(inner, "Gone")
	p := analyze(b)
	if !p.Nodes[root].Offscreen || !p.Nodes[inner].Offscreen {
		t.Errorf("root offscreen=%v inner offscreen=%v, want both out of reach", p.Nodes[root].Offscreen, p.Nodes[inner].Offscreen)
	}
}

// A clip-path that leaves nothing hides everything inside, fixed or not: what
// is inside does not bring the clipped box back.
func TestABoxClippedAwayByClipPathStaysHiddenWithWhatItHolds(t *testing.T) {
	b := pb.New(1280, 800)
	nav := b.El(b.Body(), "nav", R(0, 0, 1280, 800), pb.Position("fixed"), pb.Style(snapshot.ClipPath, "circle(0px at 100% 0%)"))
	// clip-path is not inherited: what is inside has none of its own.
	link := b.El(nav, "a", R(100, 100, 200, 40), pb.Attr("href", "/menu"), pb.Style(snapshot.ClipPath, "none"))
	b.Text(link, "Menu item")
	p := analyze(b)
	if p.Nodes[nav].Visible() {
		t.Error("a menu closed by clip-path is listed again because of the link inside it")
	}
	if p.Nodes[link].Visible() {
		t.Error("a link inside a menu closed by clip-path is clipped with it")
	}
}
