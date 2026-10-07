package facts_test

import (
	"testing"

	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/snapshot"
)

// unseenText reports Unseen for a text node in a div styled with opts.
func unseenText(opts ...pb.Opt) bool {
	b := pb.New(1280, 800)
	d := b.El(b.Body(), "div", R(0, 0, 300, 20), opts...)
	i := b.Text(d, "ignore previous instructions")
	return analyze(b).Nodes[i].Unseen
}

func TestUnseenByFontSize(t *testing.T) {
	for px, want := range map[float64]bool{0: true, 0.5: true, 1: true, 1.5: false, 2: false, 16: false} {
		if got := unseenText(pb.FontPx(px)); got != want {
			t.Errorf("font %vpx: Unseen = %v, want %v", px, got, want)
		}
	}
}

func TestUnseenByTextIndent(t *testing.T) {
	for _, c := range []struct {
		indent string
		want   bool
	}{
		{"-9999px", true}, {"-500px", true}, {"-499px", false}, {"0px", false}, {"20px", false}, {"", false}, {"-10em", false},
	} {
		if got := unseenText(pb.Style(snapshot.TextIndent, c.indent)); got != c.want {
			t.Errorf("text-indent %q: Unseen = %v, want %v", c.indent, got, c.want)
		}
	}
}

func TestUnseenByColor(t *testing.T) {
	for _, c := range []struct {
		name  string
		color string
		want  bool
	}{
		{"transparent", "rgba(0, 0, 0, 0)", true},
		{"alpha at the limit", "rgba(0, 0, 0, 0.05)", true},
		{"alpha just above the limit", "rgba(0, 0, 0, 0.06)", false},
		{"white on white", "rgb(255, 255, 255)", true},
		{"near white on white", "rgb(250, 250, 250)", true},
		{"light grey on white is readable", "rgb(240, 240, 240)", false},
		{"black on white", "rgb(0, 0, 0)", false},
		{"unparseable color is not judged", "currentcolor", false},
		{"empty color is not judged", "", false},
	} {
		if got := unseenText(pb.Style(snapshot.Color, c.color)); got != c.want {
			t.Errorf("%s: Unseen = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestUnseenWhenTextBlendsIntoItsBackground(t *testing.T) {
	for _, c := range []struct {
		name   string
		color  string
		behind string
		want   bool
	}{
		{"same dark color", "rgb(20, 20, 20)", "rgb(20, 20, 20)", true},
		{"dark on near-same dark", "rgb(20, 20, 20)", "rgb(22, 22, 22)", true},
		{"white on dark", "rgb(255, 255, 255)", "rgb(20, 20, 20)", false},
		{"black on dark", "rgb(0, 0, 0)", "rgb(60, 60, 60)", false},
		{"faint translucent white on black", "rgba(255, 255, 255, 0.055)", "rgb(0, 0, 0)", true},
		{"translucent background is blended over white", "rgb(128, 128, 128)", "rgba(0, 0, 0, 0.5)", true},
		{"translucent background keeps contrast with white text", "rgb(255, 255, 255)", "rgba(0, 0, 0, 0.5)", false},
	} {
		if got := unseenText(pb.Style(snapshot.Color, c.color), pb.Behind(c.behind)); got != c.want {
			t.Errorf("%s: Unseen = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestUnseenBackgroundFallsBackToParentThenWhite(t *testing.T) {
	build := func(textBg, parentBg string) bool {
		b := pb.New(1280, 800)
		d := b.El(b.Body(), "div", R(0, 0, 300, 20), pb.Style(snapshot.Color, "rgb(20, 20, 20)"))
		i := b.Text(d, "hidden words")
		b.Snapshot().Background[i] = textBg
		b.Snapshot().Background[d] = parentBg
		return analyze(b).Nodes[i].Unseen
	}
	if !build("rgba(0, 0, 0, 0)", "rgb(20, 20, 20)") {
		t.Error("a see-through text node takes the parent's background")
	}
	if build("rgba(0, 0, 0, 0)", "rgba(0, 0, 0, 0)") {
		t.Error("dark text over no background sits on white and is readable")
	}
	if build("rgb(255, 255, 255)", "rgb(20, 20, 20)") {
		t.Error("the text node's own background wins over the parent's")
	}
	if !build("rgb(20, 20, 20)", "rgb(255, 255, 255)") {
		t.Error("own dark background hides dark text")
	}
	if build("not a color", "not a color") {
		t.Error("unparseable backgrounds fall back to white")
	}
}

func TestUnseenTextInheritsStyleFromParentWhenItHasNone(t *testing.T) {
	b := pb.New(1280, 800)
	d := b.El(b.Body(), "div", R(0, 0, 300, 20), pb.FontPx(0))
	i := b.Text(d, "tiny")
	b.Snapshot().Style[i][snapshot.FontSize] = ""
	if !analyze(b).Nodes[i].Unseen {
		t.Error("a text node without its own style reads the parent's")
	}
}

func TestUnseenByPosition(t *testing.T) {
	b := pb.New(1280, 800)
	off := b.El(b.Body(), "div", R(-9999, 0, 300, 20))
	offText := b.Text(off, "offscreen words")
	host := b.El(b.Body(), "div", R(0, 100, 100, 20), pb.Style(snapshot.OverflowY, "hidden"))
	kid := b.El(host, "div", R(0, 400, 100, 20))
	clippedText := b.Text(kid, "clipped words")
	p := analyze(b)
	if !p.Nodes[offText].Unseen || !p.Nodes[clippedText].Unseen {
		t.Error("offscreen and clipped text is unseen")
	}
	if p.UnseenCount != 2 {
		t.Errorf("UnseenCount = %d", p.UnseenCount)
	}
}

func TestWhitespaceAndHiddenTextAreNeverUnseen(t *testing.T) {
	b := pb.New(1280, 800)
	d := b.El(b.Body(), "div", R(0, 0, 300, 20), pb.FontPx(0))
	space := b.Text(d, "   ")
	gone := b.El(b.Body(), "div", R(0, 50, 300, 20), pb.FontPx(0), pb.NotLaid())
	goneText := b.Text(gone, "words", pb.NotLaid())
	p := analyze(b)
	if p.Nodes[space].Unseen {
		t.Error("whitespace has nothing to read")
	}
	if p.Nodes[goneText].Unseen || !p.Nodes[goneText].Hidden {
		t.Error("hidden text is Hidden, not Unseen")
	}
	if p.UnseenCount != 0 {
		t.Errorf("UnseenCount = %d", p.UnseenCount)
	}
}

func TestElementsAreNeverUnseen(t *testing.T) {
	b := pb.New(1280, 800)
	d := b.El(b.Body(), "div", R(-9999, 0, 300, 20), pb.FontPx(0))
	b.Text(d, "x")
	if analyze(b).Nodes[d].Unseen {
		t.Error("Unseen is set on text nodes only")
	}
}

func TestTextIsWhitespaceCollapsed(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"plain", "plain"},
		{"a   b", "a b"},
		{"a\n\tb", "a b"},
		{"  lead", " lead"},
		{"trail  ", "trail "},
		{"\nboth\t", " both "},
		{"   ", " "},
		{"\n", " "},
		{" x", "x"},
		{"x ", "x"},
		{"", ""},
	} {
		b := pb.New(1280, 800)
		d := b.El(b.Body(), "div", R(0, 0, 300, 20))
		i := b.Text(d, c.in)
		if got := analyze(b).Nodes[i].Text; got != c.want {
			t.Errorf("Text(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestElementsCarryNoText(t *testing.T) {
	b := pb.New(1280, 800)
	d := b.El(b.Body(), "div", R(0, 0, 300, 20))
	b.Snapshot().Text[d] = "leaked"
	if got := analyze(b).Nodes[d].Text; got != "" {
		t.Errorf("element Text = %q", got)
	}
}

func TestGeneratedContent(t *testing.T) {
	b := pb.New(1280, 800)
	before := b.El(b.Body(), "div", R(0, 0, 50, 20), pb.Pseudo("before"))
	b.Snapshot().Text[before] = "  >>  "
	inner := b.Text(before, "1.")
	real := b.El(b.Body(), "div", R(0, 30, 50, 20))
	realText := b.Text(real, "real")
	p := analyze(b)
	if !p.Nodes[before].Generated || !p.Nodes[inner].Generated {
		t.Error("pseudo element and its text are generated")
	}
	if got := p.Nodes[before].Text; got != " >> " {
		t.Errorf("pseudo element Text = %q, want its collapsed content", got)
	}
	if p.Nodes[real].Generated || p.Nodes[realText].Generated {
		t.Error("ordinary content is not generated")
	}
}

func TestStrikePropagation(t *testing.T) {
	line := pb.Style(snapshot.TextDecorationLine, "line-through")
	for _, c := range []struct {
		name  string
		build func(b *pb.Builder) int32
		want  bool
	}{
		{"own element", func(b *pb.Builder) int32 {
			s := b.El(b.Body(), "s", R(0, 0, 50, 20), line)
			return b.Text(s, "€89")
		}, true},
		{"combined decoration list", func(b *pb.Builder) int32 {
			s := b.El(b.Body(), "span", R(0, 0, 50, 20), pb.Style(snapshot.TextDecorationLine, "underline line-through"))
			return b.Text(s, "€89")
		}, true},
		{"underline only", func(b *pb.Builder) int32 {
			s := b.El(b.Body(), "span", R(0, 0, 50, 20), pb.Style(snapshot.TextDecorationLine, "underline"))
			return b.Text(s, "€89")
		}, false},
		{"through inline wrapper", func(b *pb.Builder) int32 {
			s := b.El(b.Body(), "div", R(0, 0, 50, 20), line)
			w := b.El(s, "span", R(0, 0, 50, 20), pb.Inline())
			return b.Text(w, "€89")
		}, true},
		{"through block wrapper", func(b *pb.Builder) int32 {
			s := b.El(b.Body(), "div", R(0, 0, 50, 20), line)
			w := b.El(s, "div", R(0, 0, 50, 20))
			return b.Text(w, "€89")
		}, true},
		{"stops at inline-block", func(b *pb.Builder) int32 {
			s := b.El(b.Body(), "div", R(0, 0, 50, 20), line)
			w := b.El(s, "span", R(0, 0, 50, 20), pb.Style(snapshot.Display, "inline-block"))
			return b.Text(w, "€89")
		}, false},
		{"stops at absolute", func(b *pb.Builder) int32 {
			s := b.El(b.Body(), "div", R(0, 0, 50, 20), line)
			w := b.El(s, "span", R(0, 0, 50, 20), pb.Position("absolute"))
			return b.Text(w, "€89")
		}, false},
		{"stops at fixed", func(b *pb.Builder) int32 {
			s := b.El(b.Body(), "div", R(0, 0, 50, 20), line)
			w := b.El(s, "span", R(0, 0, 50, 20), pb.Position("fixed"))
			return b.Text(w, "€89")
		}, false},
		{"stops at float", func(b *pb.Builder) int32 {
			s := b.El(b.Body(), "div", R(0, 0, 50, 20), line)
			w := b.El(s, "span", R(0, 0, 50, 20), pb.Style(snapshot.Float, "left"))
			return b.Text(w, "€89")
		}, false},
		{"float none propagates", func(b *pb.Builder) int32 {
			s := b.El(b.Body(), "div", R(0, 0, 50, 20), line)
			w := b.El(s, "span", R(0, 0, 50, 20), pb.Style(snapshot.Float, "none"))
			return b.Text(w, "€89")
		}, true},
		{"relative position propagates", func(b *pb.Builder) int32 {
			s := b.El(b.Body(), "div", R(0, 0, 50, 20), line)
			w := b.El(s, "span", R(0, 0, 50, 20), pb.Position("relative"))
			return b.Text(w, "€89")
		}, true},
		{"decoration on the stopping element itself still counts", func(b *pb.Builder) int32 {
			w := b.El(b.Body(), "span", R(0, 0, 50, 20), pb.Position("absolute"), line)
			return b.Text(w, "€89")
		}, true},
		{"no decoration anywhere", func(b *pb.Builder) int32 {
			w := b.El(b.Body(), "span", R(0, 0, 50, 20))
			return b.Text(w, "€89")
		}, false},
	} {
		b := pb.New(1280, 800)
		i := c.build(b)
		if got := analyze(b).Nodes[i].Strike; got != c.want {
			t.Errorf("%s: Strike = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestUnseenBackgroundComesFromAncestors(t *testing.T) {
	build := func(text string, layers ...string) bool {
		b := pb.New(1280, 800)
		parent := b.Body()
		for _, bg := range layers {
			parent = b.El(parent, "div", R(0, 0, 300, 20), pb.Style(snapshot.BackgroundColor, bg))
		}
		p := b.El(parent, "div", R(0, 0, 300, 20), pb.Style(snapshot.Color, text))
		a := b.El(p, "p", R(0, 0, 300, 20))
		i := b.Text(a, "x")
		noBlended(b)
		return analyze(b).Nodes[i].Unseen
	}
	if build("white", "rgb(34, 34, 34)") {
		t.Error("white text on a dark ancestor is readable")
	}
	if !build("white", "rgb(255, 255, 255)") {
		t.Error("white text on a white ancestor is hidden")
	}
	if !build("rgb(34, 34, 34)", "rgb(34, 34, 34)", "rgba(0, 0, 0, 0)") {
		t.Error("a see-through layer over a dark one still hides dark text")
	}
	if !build("rgb(128, 128, 128)", "rgb(255, 255, 255)", "rgba(0, 0, 0, 0.5)") {
		t.Error("a translucent layer composites over the layer beneath it")
	}
	if build("white", "rgb(255, 255, 255)", "rgb(0, 0, 0)") {
		t.Error("the nearest opaque layer decides")
	}
}

func TestUnseenMultiChildElementTakesAncestorBackground(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	b.Snapshot().Style[body][snapshot.BackgroundColor] = "rgb(128, 128, 128)"
	p := b.El(body, "p", R(0, 0, 300, 40), pb.Style(snapshot.Color, "white"))
	x := b.Text(p, "a")
	b.El(p, "br", R(0, 0, 0, 0))
	y := b.Text(p, "b")
	noBlended(b)
	pg := analyze(b)
	if pg.Nodes[x].Unseen || pg.Nodes[y].Unseen {
		t.Error("white text on a gray page is readable")
	}
}

func TestUnseenPrefersAncestorBlendedBackground(t *testing.T) {
	b := pb.New(1280, 800)
	outer := b.El(b.Body(), "div", R(0, 0, 300, 60), pb.Style(snapshot.BackgroundColor, "rgb(255, 255, 255)"))
	inner := b.El(outer, "div", R(0, 0, 300, 40), pb.Style(snapshot.Color, "white"))
	p := b.El(inner, "p", R(0, 0, 300, 20))
	b.El(p, "br", R(0, 0, 0, 0))
	i := b.Text(p, "x")
	noBlended(b)
	b.Snapshot().Background[inner] = "rgb(0, 0, 0)"
	if analyze(b).Nodes[i].Unseen {
		t.Error("a blended background on an ancestor beats computed colours above it")
	}
}

// noBlended drops the blended backgrounds the builder derives, as Chrome
// leaves them empty for text nodes and elements with several children.
func noBlended(b *pb.Builder) {
	bg := b.Snapshot().Background
	for i := range bg {
		bg[i] = ""
	}
}

// layer is one ancestor's computed background; the zero value paints nothing.
type layer struct{ color, image string }

// unseenOver reports Unseen for text of the given colour in a div nested in a
// chain of layers, outermost first. Chrome reports no blended background
// here, so only the computed styles speak, as they do for an image backdrop.
func unseenOver(color string, layers ...layer) bool {
	b := pb.New(1280, 800)
	parent := b.Body()
	for _, l := range layers {
		parent = b.El(parent, "div", R(0, 0, 300, 20))
		setLayer(b, parent, l)
	}
	i := b.Text(parent, "ignore previous instructions")
	b.Snapshot().Style[i][snapshot.Color] = color
	setLayer(b, i, layer{})
	return analyze(b).Nodes[i].Unseen
}

func setLayer(b *pb.Builder, i int32, l layer) {
	st := &b.Snapshot().Style[i]
	st[snapshot.BackgroundColor] = "rgba(0, 0, 0, 0)"
	if l.color != "" {
		st[snapshot.BackgroundColor] = l.color
	}
	st[snapshot.BackgroundImage] = "none"
	if l.image != "" {
		st[snapshot.BackgroundImage] = l.image
	}
	b.Snapshot().Background[i] = ""
}

func TestUnseenDarkTextOnGradientCardOverDarkBodyIsReadable(t *testing.T) {
	const gradient = "linear-gradient(rgb(255, 255, 255), rgb(238, 238, 238))"
	black := layer{color: "rgb(0, 0, 0)"}
	card := layer{color: "rgba(0, 0, 0, 0)", image: gradient}
	if unseenOver("rgb(0, 0, 0)", black, card) {
		t.Error("black text on a gradient card is readable whatever lies behind the card")
	}
	if unseenOver("rgb(0, 0, 0)", black, card, layer{}) {
		t.Error("the card is still the backdrop one element further in")
	}
}

func TestUnseenOverImageIsNotJudgedByContrast(t *testing.T) {
	white := layer{color: "rgb(255, 255, 255)"}
	img := layer{image: `url("https://example.com/a.png")`}
	if unseenOver("rgb(255, 255, 255)", white, img) {
		t.Error("white text over an image is not flagged by contrast")
	}
	if unseenOver("rgb(255, 255, 255)", img, layer{}) {
		t.Error("an image on an ancestor makes the backdrop unknown")
	}
	if !unseenOver("rgba(0, 0, 0, 0)", img) {
		t.Error("transparent text is unseen over an image too")
	}
	if !unseenOver("rgb(255, 255, 255)", img, white) {
		t.Error("an opaque colour closer than the image decides the backdrop")
	}
	if !unseenOver("rgb(255, 255, 255)", layer{image: "none"}) {
		t.Error("background-image none is no image")
	}
}

func TestUnseenTranslucentParentLayerIsCountedOnce(t *testing.T) {
	b := pb.New(1280, 800)
	d := b.El(b.Body(), "div", R(0, 0, 300, 20))
	setLayer(b, d, layer{color: "rgba(0, 0, 0, 0.3)"})
	i := b.Text(d, "grey on a shaded card")
	// Chrome hands a text node its parent's computed style, background included.
	setLayer(b, i, layer{color: "rgba(0, 0, 0, 0.3)"})
	b.Snapshot().Style[i][snapshot.Color] = "rgb(125, 125, 125)"
	if analyze(b).Nodes[i].Unseen {
		t.Error("a translucent layer counted once leaves grey text readable")
	}
}

func TestUnseenTranslucentInnerLayerIsCompositedOverAncestor(t *testing.T) {
	dark := layer{color: "rgb(0, 0, 0)"}
	veil := layer{color: "rgba(255, 255, 255, 0.5)"}
	// 50% white over black is mid grey: grey text sinks, black stays readable.
	if !unseenOver("rgb(128, 128, 128)", dark, veil) {
		t.Error("the veil lightens the dark ancestor; grey text sinks into it")
	}
	if unseenOver("rgb(0, 0, 0)", dark, veil) {
		t.Error("black text on mid grey is readable")
	}
	if !unseenOver("rgb(255, 255, 255)", veil) {
		t.Error("a veil over the white page stays white")
	}
}

func TestUnseenDarkAncestorStillShowsWhiteText(t *testing.T) {
	dark := layer{color: "rgb(0, 0, 0)"}
	if unseenOver("rgb(255, 255, 255)", dark, layer{}) {
		t.Error("white text on a dark ancestor is readable")
	}
	if !unseenOver("rgb(0, 0, 0)", dark, layer{}) {
		t.Error("black text on a dark ancestor is unseen")
	}
}

// Text painted through its glyphs (CSS Backgrounds 4, background-clip: text)
// is drawn in its element's background, however transparent its own colour:
// a gradient heading is read. Without a background to show, it stays unseen.
func TestTextPaintedThroughItsGlyphsIsSeen(t *testing.T) {
	transparent := pb.Style(snapshot.Color, "rgba(0, 0, 0, 0)")
	clipped := pb.Style(snapshot.BackgroundClip, "text")
	gradient := pb.Style(snapshot.BackgroundImage, "linear-gradient(90deg, rgb(204, 0, 0), rgb(0, 0, 204))")
	for _, c := range []struct {
		name string
		opts []pb.Opt
		want bool
	}{
		{"gradient clipped to the text", []pb.Opt{transparent, clipped, gradient}, false},
		{"colour clipped to the text", []pb.Opt{transparent, clipped, pb.Style(snapshot.BackgroundColor, "rgb(204, 0, 0)")}, false},
		{"clipped to the text with nothing to show", []pb.Opt{transparent, clipped}, true},
		{"gradient not clipped to the text", []pb.Opt{transparent, gradient}, true},
	} {
		if got := unseenText(c.opts...); got != c.want {
			t.Errorf("%s: Unseen = %v, want %v", c.name, got, c.want)
		}
	}
	// The clip on an ancestor reaches the text of its descendants.
	b := pb.New(1280, 800)
	h := b.El(b.Body(), "h1", R(0, 0, 400, 40), clipped, gradient)
	span := b.El(h, "span", R(0, 0, 200, 40), transparent, pb.Inline())
	i := b.Text(span, "Example Product")
	if analyze(b).Nodes[i].Unseen {
		t.Error("text under an ancestor whose background is clipped to text is seen")
	}
}
