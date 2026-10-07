package facts_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/noetive/riffle/internal/facts"
	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/snapshot"
)

const sentence = "A long paragraph of ordinary body text that sets the page norm for size."

// withNorm adds enough 16px body text to make 16px the page median.
func withNorm(b *pb.Builder) {
	for k := 0; k < 3; k++ {
		p := b.El(b.Body(), "div", R(0, float64(400+k*30), 600, 20))
		b.Text(p, sentence)
	}
}

func headingOf(opts ...pb.Opt) int {
	b := pb.New(1280, 800)
	withNorm(b)
	d := b.El(b.Body(), "div", R(0, 0, 300, 40), opts...)
	b.Text(d, "Section title")
	return analyze(b).Nodes[d].Heading
}

func TestVisualHeadingSizeThresholds(t *testing.T) {
	bold := pb.Style(snapshot.FontWeight, "700")
	for _, c := range []struct {
		px   float64
		bold bool
		want int
	}{
		{16, false, 0}, {19, false, 0}, {20, false, 0}, {20.7, false, 0},
		{20.8, false, 3}, {25.5, false, 3}, {25.6, false, 2}, {31.9, false, 2},
		{32, false, 1}, {48, false, 1},
		{18, true, 0}, {18.3, true, 0}, {18.4, true, 4}, {20.7, true, 4},
		{20.8, true, 3}, {16, true, 0},
	} {
		opts := []pb.Opt{pb.FontPx(c.px)}
		if c.bold {
			opts = append(opts, bold)
		}
		if got := headingOf(opts...); got != c.want {
			t.Errorf("%vpx bold=%v: heading %d, want %d", c.px, c.bold, got, c.want)
		}
	}
}

func TestVisualHeadingWeightThreshold(t *testing.T) {
	for w, want := range map[string]int{"400": 0, "599": 0, "600": 4, "700": 4, "bold": 4, "normal": 0, "": 0, "heavy": 0} {
		if got := headingOf(pb.FontPx(19), pb.Style(snapshot.FontWeight, w)); got != want {
			t.Errorf("weight %q: heading %d, want %d", w, got, want)
		}
	}
}

func TestVisualHeadingLengthLimit(t *testing.T) {
	for _, c := range []struct {
		n    int
		want int
	}{{1, 1}, {140, 1}, {141, 0}} {
		b := pb.New(1280, 800)
		withNorm(b)
		d := b.El(b.Body(), "div", R(0, 0, 300, 40), pb.FontPx(36))
		text := ""
		for len([]rune(text)) < c.n {
			text += "x"
		}
		b.Text(d, "  "+text+"  ")
		if got := analyze(b).Nodes[d].Heading; got != c.want {
			t.Errorf("%d runes: heading %d, want %d", c.n, got, c.want)
		}
	}
}

func TestVisualHeadingExclusions(t *testing.T) {
	b := pb.New(1280, 800)
	withNorm(b)
	big := pb.FontPx(36)
	inLink := b.El(b.Body(), "a", R(0, 0, 200, 40), pb.Attr("href", "/"), big)
	linkKid := b.El(inLink, "span", R(0, 0, 100, 40), big)
	b.Text(linkKid, "Link text")
	realH := b.El(b.Body(), "h2", R(0, 50, 200, 40), big)
	hKid := b.El(realH, "span", R(0, 50, 100, 40), big)
	b.Text(hKid, "Inside heading")
	roled := b.El(b.Body(), "nav", R(0, 100, 200, 40), big)
	b.Text(roled, "Nav text")
	hid := b.El(b.Body(), "div", R(0, 150, 200, 40), big, pb.NotLaid())
	b.Text(hid, "Hidden", pb.NotLaid())
	wrapper := b.El(b.Body(), "div", R(0, 200, 200, 40), big)
	inner := b.El(wrapper, "div", R(0, 200, 200, 40), big)
	b.Text(inner, "Nested only")
	unseen := b.El(b.Body(), "div", R(0, 250, 200, 40), big, pb.Style(snapshot.Color, "rgb(255, 255, 255)"))
	b.Text(unseen, "White on white")
	p := analyze(b)
	for name, i := range map[string]int32{"in interactive": linkKid, "inside heading": hKid, "role": roled, "hidden": hid, "no direct text": wrapper, "unseen text": unseen} {
		if p.Nodes[i].Heading != 0 {
			t.Errorf("%s: heading %d, want none", name, p.Nodes[i].Heading)
		}
	}
	if p.Nodes[inner].Heading != 1 {
		t.Errorf("nested text block: heading %d, want 1", p.Nodes[inner].Heading)
	}
}

func TestVisualHeadingUsesAllDirectText(t *testing.T) {
	b := pb.New(1280, 800)
	withNorm(b)
	d := b.El(b.Body(), "div", R(0, 0, 300, 40), pb.FontPx(36))
	b.Text(d, "Your")
	b.El(d, "span", R(50, 0, 10, 10), pb.Inline())
	b.Text(d, " cart")
	if analyze(b).Nodes[d].Heading != 1 {
		t.Error("text split by an inline element is one heading")
	}
}

func TestMedianFontSizeIsWeightedByTextLength(t *testing.T) {
	build := func(smallLen, bigLen int) float64 {
		b := pb.New(1280, 800)
		s := b.El(b.Body(), "div", R(0, 0, 600, 20), pb.FontPx(12))
		b.Text(s, repeat('a', smallLen))
		l := b.El(b.Body(), "div", R(0, 30, 600, 20), pb.FontPx(20))
		b.Text(l, repeat('b', bigLen))
		return analyze(b).MedianFontSize
	}
	if got := build(30, 10); got != 12 {
		t.Errorf("median = %v, want 12", got)
	}
	if got := build(10, 30); got != 20 {
		t.Errorf("median = %v, want 20", got)
	}
	if got := build(20, 20); got != 12 {
		t.Errorf("equal weight median = %v, want the lower size", got)
	}
	if got := build(19, 20); got != 20 {
		t.Errorf("median = %v, want 20", got)
	}
}

func repeat(r rune, n int) string {
	out := make([]rune, n)
	for i := range out {
		out[i] = r
	}
	return string(out)
}

func TestMedianIgnoresUnreadableTextAndDefaultsTo16(t *testing.T) {
	b := pb.New(1280, 800)
	if analyze(b).MedianFontSize != 16 {
		t.Error("no text: default 16")
	}
	h := b.El(b.Body(), "div", R(0, 0, 600, 20), pb.FontPx(40), pb.NotLaid())
	b.Text(h, repeat('a', 100), pb.NotLaid())
	u := b.El(b.Body(), "div", R(0, 30, 600, 20), pb.FontPx(40), pb.Style(snapshot.Color, "rgb(255, 255, 255)"))
	b.Text(u, repeat('a', 100))
	o := b.El(b.Body(), "div", R(0, 60, 600, 20), pb.FontPx(12))
	b.Text(o, "  hello  ")
	if got := analyze(b).MedianFontSize; got != 12 {
		t.Errorf("median = %v, want 12", got)
	}
}

func hslCSS(h, s, l float64) string {
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return fmt.Sprintf("rgb(%d, %d, %d)", int(math.Round((r+m)*255)), int(math.Round((g+m)*255)), int(math.Round((b+m)*255)))
}

// toneOn returns the tone of a short span of the given color among body text
// in the default dark color on white.
func toneOn(color string, opts ...pb.Opt) facts.Tone {
	b := pb.New(1280, 800)
	for k := 0; k < 3; k++ {
		d := b.El(b.Body(), "p", R(0, float64(k*30), 500, 20))
		b.Text(d, "Ordinary text that forms the baseline of the page.")
	}
	el := b.El(b.Body(), "span", R(0, 200, 50, 20), append([]pb.Opt{pb.Inline(), pb.Style(snapshot.Color, color)}, opts...)...)
	i := b.Text(el, "€69")
	return analyze(b).Nodes[i].Tone
}

func TestToneRedRegion(t *testing.T) {
	for _, c := range []struct {
		name    string
		h, s, l float64
		want    facts.Tone
	}{
		{"red", 0, 0.7, 0.45, facts.ToneRed},
		{"hue 10", 10, 0.7, 0.45, facts.ToneRed},
		{"hue 20", 20, 0.7, 0.45, facts.ToneNone},
		{"hue 350", 350, 0.7, 0.45, facts.ToneRed},
		{"hue 330", 330, 0.7, 0.45, facts.ToneNone},
		{"saturated enough", 0, 0.6, 0.45, facts.ToneRed},
		{"too grey", 0, 0.4, 0.45, facts.ToneNone},
		{"light enough", 0, 0.7, 0.65, facts.ToneRed},
		{"too light", 0, 0.7, 0.8, facts.ToneNone},
		{"dark enough", 0, 0.7, 0.25, facts.ToneRed},
		{"too dark", 0, 0.7, 0.12, facts.ToneNone},
	} {
		if got := toneOn(hslCSS(c.h, c.s, c.l)); got != c.want {
			t.Errorf("%s: tone %q, want %q", c.name, got, c.want)
		}
	}
}

func TestToneGreenRegion(t *testing.T) {
	for _, c := range []struct {
		name    string
		h, s, l float64
		want    facts.Tone
	}{
		{"green", 120, 0.6, 0.4, facts.ToneGreen},
		{"hue 90", 90, 0.6, 0.4, facts.ToneGreen},
		{"hue 160", 160, 0.6, 0.4, facts.ToneGreen},
		{"hue 70", 70, 0.6, 0.4, facts.ToneNone},
		{"hue 175", 175, 0.6, 0.4, facts.ToneNone},
		{"saturated enough", 120, 0.45, 0.4, facts.ToneGreen},
		{"too grey", 120, 0.3, 0.4, facts.ToneNone},
		{"light enough", 120, 0.6, 0.6, facts.ToneGreen},
		{"too light", 120, 0.6, 0.75, facts.ToneNone},
		{"dark enough", 120, 0.6, 0.2, facts.ToneGreen},
		{"too dark", 120, 0.6, 0.08, facts.ToneNone},
	} {
		if got := toneOn(hslCSS(c.h, c.s, c.l)); got != c.want {
			t.Errorf("%s: tone %q, want %q", c.name, got, c.want)
		}
	}
}

func TestToneMutedGreys(t *testing.T) {
	for _, c := range []struct {
		name  string
		color string
		want  facts.Tone
	}{
		{"mid grey", "rgb(120, 120, 120)", facts.ToneMuted},
		{"light grey", "rgb(200, 200, 200)", facts.ToneMuted},
		{"nearly baseline", "rgb(80, 80, 80)", facts.ToneNone},
		{"too faint to be muted", "rgb(235, 235, 235)", facts.ToneNone},
		{"slightly tinted grey", "rgb(160, 120, 120)", facts.ToneMuted},
		{"too colourful for grey", "rgb(160, 100, 100)", facts.ToneNone},
		{"baseline itself", "rgb(33, 33, 33)", facts.ToneNone},
		{"unparseable", "inherit", facts.ToneNone},
	} {
		if got := toneOn(c.color); got != c.want {
			t.Errorf("%s: tone %q, want %q", c.name, got, c.want)
		}
	}
}

func TestToneMutedNeedsBackgroundLikeThePage(t *testing.T) {
	if got := toneOn("rgb(120, 120, 120)", pb.Behind("rgb(255, 255, 255)")); got != facts.ToneMuted {
		t.Errorf("same background: %q", got)
	}
	if got := toneOn("rgb(120, 120, 120)", pb.Behind("rgb(250, 250, 250)")); got != facts.ToneMuted {
		t.Errorf("slightly different background: %q", got)
	}
	if got := toneOn("rgb(120, 120, 120)", pb.Behind("rgb(200, 200, 200)")); got != facts.ToneNone {
		t.Errorf("different background: %q", got)
	}
}

func TestToneAlphaAndBlending(t *testing.T) {
	if got := toneOn("rgba(200, 30, 30, 0.6)"); got != facts.ToneRed {
		t.Errorf("opaque enough: %q", got)
	}
	if got := toneOn("rgba(200, 30, 30, 0.4)"); got != facts.ToneNone {
		t.Errorf("too translucent: %q", got)
	}
	// Strong red over a blue backdrop blends toward purple.
	if got := toneOn("rgba(200, 30, 30, 0.5)", pb.Behind("rgb(0, 0, 255)")); got != facts.ToneNone {
		t.Errorf("blended with backdrop: %q", got)
	}
}

func TestToneBaselineGreenAndTies(t *testing.T) {
	b := pb.New(1280, 800)
	g := hslCSS(120, 0.6, 0.4)
	d := b.El(b.Body(), "p", R(0, 0, 500, 20), pb.Style(snapshot.Color, g))
	gt := b.Text(d, "Everything on this page is green, so green is the baseline.")
	r := b.El(b.Body(), "span", R(0, 30, 50, 20), pb.Inline(), pb.Style(snapshot.Color, "rgb(200, 30, 30)"))
	rt := b.Text(r, "Err")
	p := analyze(b)
	if p.Nodes[gt].Tone != facts.ToneNone {
		t.Error("baseline green must not be reported")
	}
	if p.Nodes[rt].Tone != facts.ToneRed {
		t.Error("red still deviates from a green page")
	}
}

func TestToneMirrorsOntoElementOnce(t *testing.T) {
	b := pb.New(1280, 800)
	withNorm(b)
	el := b.El(b.Body(), "p", R(0, 0, 200, 20))
	redSpan := b.El(el, "span", R(0, 0, 20, 20), pb.Inline(), pb.Style(snapshot.Color, "rgb(200, 30, 30)"))
	b.Text(redSpan, "bad")
	greenSpan := b.El(el, "span", R(20, 0, 20, 20), pb.Inline(), pb.Style(snapshot.Color, hslCSS(120, 0.6, 0.4)))
	b.Text(greenSpan, "good")
	p := analyze(b)
	if p.Nodes[redSpan].Tone != facts.ToneRed || p.Nodes[greenSpan].Tone != facts.ToneGreen {
		t.Error("spans carry their own tone")
	}
	if p.Nodes[el].Tone != facts.ToneNone {
		t.Errorf("a text-less parent does not inherit tone from spans")
	}
	direct := b.El(b.Body(), "p", R(0, 100, 200, 20), pb.Style(snapshot.Color, "rgb(200, 30, 30)"))
	b.Text(direct, "first")
	b.Text(direct, "second", pb.Style(snapshot.Color, hslCSS(120, 0.6, 0.4)))
	q := analyze(b)
	if q.Nodes[direct].Tone != facts.ToneRed {
		t.Errorf("first tone sticks on the parent: %q", q.Nodes[direct].Tone)
	}
}

func TestStrikeMirrorsOntoParentElement(t *testing.T) {
	b := pb.New(1280, 800)
	s := b.El(b.Body(), "s", R(0, 0, 50, 20), pb.Inline(), pb.Style(snapshot.TextDecorationLine, "line-through"))
	b.Text(s, "€89")
	plain := b.El(b.Body(), "span", R(0, 30, 50, 20), pb.Inline())
	b.Text(plain, "€99")
	p := analyze(b)
	if !p.Nodes[s].Strike || p.Nodes[plain].Strike {
		t.Errorf("strike s=%v plain=%v", p.Nodes[s].Strike, p.Nodes[plain].Strike)
	}
}

func TestTruncationBoundaries(t *testing.T) {
	for _, c := range []struct {
		name string
		opts func(w, h float64) []pb.Opt
		want bool
	}{
		{"ellipsis overflow", func(w, h float64) []pb.Opt {
			return []pb.Opt{pb.Style(snapshot.TextOverflow, "ellipsis"), pb.Scroll(R(0, 0, w+2, h))}
		}, true},
		{"ellipsis one pixel over is rounding", func(w, h float64) []pb.Opt {
			return []pb.Opt{pb.Style(snapshot.TextOverflow, "ellipsis"), pb.Scroll(R(0, 0, w+1, h))}
		}, false},
		{"ellipsis exact fit", func(w, h float64) []pb.Opt {
			return []pb.Opt{pb.Style(snapshot.TextOverflow, "ellipsis"), pb.Scroll(R(0, 0, w, h))}
		}, false},
		{"clip is not truncation", func(w, h float64) []pb.Opt {
			return []pb.Opt{pb.Style(snapshot.TextOverflow, "clip"), pb.Scroll(R(0, 0, w+50, h))}
		}, false},
		{"clamp overflow", func(w, h float64) []pb.Opt {
			return []pb.Opt{pb.Style(snapshot.LineClamp, "3"), pb.Scroll(R(0, 0, w, h+2))}
		}, true},
		{"clamp one pixel over is rounding", func(w, h float64) []pb.Opt {
			return []pb.Opt{pb.Style(snapshot.LineClamp, "3"), pb.Scroll(R(0, 0, w, h+1))}
		}, false},
		{"clamp none", func(w, h float64) []pb.Opt {
			return []pb.Opt{pb.Style(snapshot.LineClamp, "none"), pb.Scroll(R(0, 0, w, h+50))}
		}, false},
		{"clamp empty", func(w, h float64) []pb.Opt {
			return []pb.Opt{pb.Style(snapshot.LineClamp, ""), pb.Scroll(R(0, 0, w, h+50))}
		}, false},
		{"ellipsis with only vertical overflow", func(w, h float64) []pb.Opt {
			return []pb.Opt{pb.Style(snapshot.TextOverflow, "ellipsis"), pb.Scroll(R(0, 0, w, h+50))}
		}, false},
		{"clamp with only horizontal overflow", func(w, h float64) []pb.Opt {
			return []pb.Opt{pb.Style(snapshot.LineClamp, "2"), pb.Scroll(R(0, 0, w+50, h))}
		}, false},
		{"no scroll extent known", func(w, h float64) []pb.Opt {
			return []pb.Opt{pb.Style(snapshot.TextOverflow, "ellipsis")}
		}, false},
		{"hidden", func(w, h float64) []pb.Opt {
			return []pb.Opt{pb.Style(snapshot.TextOverflow, "ellipsis"), pb.Scroll(R(0, 0, w+50, h)), pb.NotLaid()}
		}, false},
	} {
		b := pb.New(1280, 800)
		i := b.El(b.Body(), "div", R(0, 0, 100, 20), c.opts(100, 20)...)
		if got := analyze(b).Nodes[i].Truncated; got != c.want {
			t.Errorf("%s: Truncated = %v, want %v", c.name, got, c.want)
		}
	}
}

func buttonRow(fills ...string) (*facts.Page, []int32) {
	b := pb.New(1280, 800)
	row := b.El(b.Body(), "div", R(0, 0, 800, 40))
	var ids []int32
	for k, f := range fills {
		opts := []pb.Opt{}
		if f != "" {
			opts = append(opts, pb.Fill(f))
		}
		ids = append(ids, b.El(row, "button", R(float64(k*100), 0, 90, 30), opts...))
	}
	return analyze(b), ids
}

func primaries(p *facts.Page, ids []int32) []bool {
	out := make([]bool, len(ids))
	for k, i := range ids {
		out[k] = p.Nodes[i].Primary
	}
	return out
}

func grey(v int) string { return fmt.Sprintf("rgb(%d, %d, %d)", v, v, v) }

func TestPrimaryOfTwoNeedsClearDifference(t *testing.T) {
	for _, c := range []struct {
		name  string
		fills []string
		want  []bool
	}{
		{"filled vs plain", []string{"rgb(13, 110, 253)", ""}, []bool{true, false}},
		{"plain vs filled", []string{"", "rgb(13, 110, 253)"}, []bool{false, true}},
		{"clear difference", []string{grey(208), ""}, []bool{true, false}},
		{"too close", []string{grey(210), ""}, []bool{false, false}},
		{"both filled alike", []string{"rgb(13, 110, 253)", "rgb(13, 110, 253)"}, []bool{false, false}},
		{"both plain", []string{"", ""}, []bool{false, false}},
		{"strong vs mild", []string{"rgb(13, 110, 253)", grey(230)}, []bool{true, false}},
	} {
		p, ids := buttonRow(c.fills...)
		got := primaries(p, ids)
		for k := range got {
			if got[k] != c.want[k] {
				t.Errorf("%s: primaries = %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}

func TestPrimaryOfManyNeedsOneOddFill(t *testing.T) {
	for _, c := range []struct {
		name  string
		fills []string
		want  []bool
	}{
		{"odd last", []string{grey(230), grey(230), grey(200)}, []bool{false, false, true}},
		{"odd first", []string{grey(200), grey(230), grey(230)}, []bool{true, false, false}},
		{"odd middle of four", []string{grey(230), grey(230), grey(200), grey(230)}, []bool{false, false, true, false}},
		{"odd just far enough", []string{grey(230), grey(230), grey(216)}, []bool{false, false, true}},
		{"odd not far enough", []string{grey(230), grey(230), grey(218)}, []bool{false, false, false}},
		{"others agree within tolerance", []string{grey(230), grey(236), grey(200)}, []bool{false, false, true}},
		{"others disagree", []string{grey(230), grey(246), grey(200)}, []bool{false, false, false}},
		{"two odd ones", []string{grey(230), grey(200), grey(150)}, []bool{false, false, false}},
		{"all the same", []string{grey(230), grey(230), grey(230)}, []bool{false, false, false}},
		{"odd is transparent", []string{grey(150), grey(150), ""}, []bool{false, false, true}},
	} {
		p, ids := buttonRow(c.fills...)
		got := primaries(p, ids)
		for k := range got {
			if got[k] != c.want[k] {
				t.Errorf("%s: primaries = %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}

func TestPrimaryIgnoresDisabledHiddenAndLoneButtons(t *testing.T) {
	b := pb.New(1280, 800)
	row := b.El(b.Body(), "div", R(0, 0, 800, 40))
	a := b.El(row, "button", R(0, 0, 90, 30), pb.Fill(grey(230)))
	c := b.El(row, "button", R(100, 0, 90, 30), pb.Fill(grey(230)))
	dis := b.El(row, "button", R(200, 0, 90, 30), pb.Fill("rgb(13, 110, 253)"), pb.Attr("disabled", ""))
	hid := b.El(row, "button", R(300, 0, 90, 30), pb.Fill("rgb(13, 110, 253)"), pb.NotLaid())
	off := b.El(row, "button", R(-500, 0, 90, 30), pb.Fill("rgb(13, 110, 253)"))
	lone := b.El(b.Body(), "div", R(0, 100, 200, 40))
	only := b.El(lone, "button", R(0, 100, 90, 30), pb.Fill("rgb(13, 110, 253)"))
	rowA := b.El(b.Body(), "div", R(0, 200, 200, 40))
	splitA := b.El(rowA, "button", R(0, 200, 90, 30), pb.Fill("rgb(13, 110, 253)"))
	rowB := b.El(b.Body(), "div", R(0, 250, 200, 40))
	splitB := b.El(rowB, "button", R(0, 250, 90, 30))
	p := analyze(b)
	for name, i := range map[string]int32{"a": a, "c": c, "disabled": dis, "hidden": hid, "offscreen": off, "lone": only, "split a": splitA, "split b": splitB} {
		if p.Nodes[i].Primary {
			t.Errorf("%s must not be primary", name)
		}
	}
}

func TestPrimaryBlendsTranslucentFillOverBackdrop(t *testing.T) {
	b := pb.New(1280, 800)
	row := b.El(b.Body(), "div", R(0, 0, 800, 40), pb.Fill("rgb(20, 20, 20)"))
	a := b.El(row, "button", R(0, 0, 90, 30), pb.Fill("rgba(255, 255, 255, 0.02)"))
	c := b.El(row, "button", R(100, 0, 90, 30), pb.Fill("rgba(255, 255, 255, 0.02)"))
	d := b.El(row, "button", R(200, 0, 90, 30), pb.Fill("rgba(255, 255, 255, 0.9)"))
	p := analyze(b)
	if !p.Nodes[d].Primary || p.Nodes[a].Primary || p.Nodes[c].Primary {
		t.Error("only the opaque light fill stands out on a dark row")
	}
}

func TestAnElementWhoseContentOverflowsAndScrollsIsScrollable(t *testing.T) {
	b := pb.New(1280, 800)
	log := b.El(b.Body(), "div", R(0, 0, 300, 100), pb.Style(snapshot.OverflowY, "auto"), pb.Scroll(R(0, 0, 300, 900)))
	fits := b.El(b.Body(), "div", R(0, 200, 300, 100), pb.Style(snapshot.OverflowY, "auto"), pb.Scroll(R(0, 0, 300, 100)))
	hidden := b.El(b.Body(), "div", R(0, 400, 300, 100), pb.Style(snapshot.OverflowY, "hidden"), pb.Scroll(R(0, 0, 300, 900)))
	wide := b.El(b.Body(), "div", R(0, 600, 200, 50), pb.Style(snapshot.OverflowX, "scroll"), pb.Scroll(R(0, 0, 1500, 50)))
	visible := b.El(b.Body(), "div", R(0, 700, 300, 50), pb.Scroll(R(0, 0, 300, 400)))
	p := analyze(b)
	if !p.Nodes[log].Scrollable || !p.Nodes[wide].Scrollable {
		t.Error("an overflowing auto or scroll box scrolls")
	}
	if p.Nodes[fits].Scrollable || p.Nodes[hidden].Scrollable || p.Nodes[visible].Scrollable {
		t.Error("a box whose content fits, or that clips without scrolling, or does not clip, is not scrollable")
	}
}

// The small print of a footer or a navigation bar is not the page's body
// text. On a short page it can outweigh the body, and the body would then
// read as headings.
func TestSmallPrintDoesNotSetTheBodySize(t *testing.T) {
	b := pb.New(1280, 800)
	title := b.El(b.Body(), "div", R(0, 0, 400, 40), pb.FontPx(28))
	b.Text(title, "Sample form")
	p := b.El(b.Body(), "p", R(0, 50, 600, 20), pb.FontPx(16))
	b.Text(p, "Enter your details below to continue.")
	lbl := b.El(b.Body(), "label", R(0, 80, 200, 20), pb.FontPx(16))
	b.Text(lbl, "Session saved.")
	nav := b.El(b.Body(), "nav", R(0, 700, 1280, 20), pb.FontPx(11))
	b.Text(nav, "Shop Journal Stockists Care Returns")
	foot := b.El(b.Body(), "footer", R(0, 740, 1280, 40), pb.FontPx(11))
	for k := 0; k < 3; k++ {
		d := b.El(foot, "div", R(0, float64(740+k*12), 1280, 12), pb.FontPx(11))
		b.Text(d, "Made in small batches. All rights reserved. Prices include tax where it applies.")
	}
	page := analyze(b)
	for _, n := range []int32{p, lbl} {
		if h := page.Nodes[n].Heading; h != 0 {
			t.Errorf("16px body text reads as heading level %d", h)
		}
	}
	if page.Nodes[title].Heading == 0 {
		t.Error("the 28px title is still a heading")
	}
}

// Each kind of small print is left out of the body size on its own, and a
// page that is nothing but small print is measured whole.
func TestEachKindOfSmallPrintIsLeftOutOfTheBodySize(t *testing.T) {
	for _, tag := range []string{"nav", "footer", "aside"} {
		b := pb.New(1280, 800)
		p := b.El(b.Body(), "p", R(0, 50, 600, 20), pb.FontPx(16))
		b.Text(p, "Enter your details below to continue.")
		box := b.El(b.Body(), tag, R(0, 700, 1280, 60), pb.FontPx(11))
		for k := 0; k < 3; k++ {
			d := b.El(box, "div", R(0, float64(700+k*12), 1280, 12), pb.FontPx(11))
			b.Text(d, "Made in small batches. All rights reserved. Prices include tax where it applies.")
		}
		if h := analyze(b).Nodes[p].Heading; h != 0 {
			t.Errorf("%s: 16px body text reads as heading level %d", tag, h)
		}
	}
	b := pb.New(1280, 800)
	foot := b.El(b.Body(), "footer", R(0, 0, 1280, 200), pb.FontPx(11))
	title := b.El(foot, "div", R(0, 0, 400, 40), pb.FontPx(16))
	b.Text(title, "Closed")
	for k := 0; k < 3; k++ {
		d := b.El(foot, "div", R(0, float64(50+k*12), 1280, 12), pb.FontPx(11))
		b.Text(d, "Made in small batches. All rights reserved. Prices include tax where it applies.")
	}
	if analyze(b).Nodes[title].Heading == 0 {
		t.Error("on a page of only small print, text larger than it is still a heading")
	}
}
