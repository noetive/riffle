package facts

import (
	"sort"
	"strconv"
	"strings"

	"github.com/noetive/riffle/internal/snapshot"
)

// emphasis derives visual headings, tone, primary buttons and truncation,
// all measured against page norms.
func (a *analyzer) emphasis() {
	a.baseline()
	a.tones()
	a.headings()
	a.truncation()
	a.primaries()
}

// directTexts lists the readable text children of element i.
func (a *analyzer) directTexts(i int32) []int32 {
	var out []int32
	for _, c := range a.s.Children[i] {
		if a.visibleText(c) {
			out = append(out, c)
		}
	}
	return out
}

func (a *analyzer) fontSize(i int32) float64 {
	v, _ := parsePx(a.style(i)[snapshot.FontSize])
	return v
}

func (a *analyzer) fontWeight(i int32) float64 {
	w := a.style(i)[snapshot.FontWeight]
	switch w {
	case "bold":
		return 700
	case "normal", "":
		return 400
	}
	v, err := strconv.ParseFloat(w, 64)
	if err != nil {
		return 400
	}
	return v
}

type baselineText struct {
	color  string
	bg     rgba
	weight int
}

// baseline measures the text-weighted median font size and dominant color.
// The size is the body's: the small print of navigation, footers and asides
// is left out, since on a short page it can outweigh the body and make the
// body read as headings. A page that is all small print is measured whole.
func (a *analyzer) baseline() {
	type sw struct {
		size   float64
		weight int
	}
	aside := make([]bool, a.n)
	for i := int32(0); i < int32(a.n); i++ {
		if p := a.s.Parent[i]; p != snapshot.None {
			aside[i] = aside[p]
		}
		if a.s.Kind[i] == snapshot.KindElement && smallPrint(a.nodes[i].Role) {
			aside[i] = true
		}
	}
	var sizes, all []sw
	total, allTotal := 0, 0
	colors := map[string]*baselineText{}
	for i := int32(0); i < int32(a.n); i++ {
		if !a.visibleText(i) {
			continue
		}
		w := len([]rune(strings.TrimSpace(a.s.Text[i])))
		if fs := a.fontSize(i); fs > 0 {
			all = append(all, sw{fs, w})
			allTotal += w
			if !aside[i] {
				sizes = append(sizes, sw{fs, w})
				total += w
			}
		}
		st := a.style(i)
		c := st[snapshot.Color]
		if b, ok := colors[c]; ok {
			b.weight += w
		} else {
			colors[c] = &baselineText{color: c, bg: a.backgroundOf(i), weight: w}
		}
	}
	if total == 0 {
		sizes, total = all, allTotal
	}
	a.median = 16
	if len(sizes) > 0 {
		sort.Slice(sizes, func(x, y int) bool { return sizes[x].size < sizes[y].size })
		acc := 0
		for _, e := range sizes {
			acc += e.weight
			if acc*2 >= total {
				a.median = e.size
				break
			}
		}
	}
	a.dominant = baselineText{}
	keys := make([]string, 0, len(colors))
	for k := range colors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if colors[k].weight > a.dominant.weight {
			a.dominant = *colors[k]
		}
	}
}

func (a *analyzer) backgroundOf(i int32) rgba {
	for _, k := range []int32{i, a.s.Parent[i]} {
		if k == snapshot.None {
			continue
		}
		if c, ok := parseColor(a.s.Background[k]); ok && c.a > 0 {
			return c.over(white)
		}
	}
	return white
}

// toneOf classifies a text color against its background and the page baseline.
func (a *analyzer) toneOf(colorStr string, bg rgba) Tone {
	c, ok := parseColor(colorStr)
	if !ok || c.a < 0.5 {
		return ToneNone
	}
	c = c.over(bg)
	h, sat, l := c.hsl()
	switch {
	case sat >= 0.5 && l >= 0.2 && l <= 0.7 && (h <= 15 || h >= 340):
		return ToneRed
	case sat >= 0.4 && l >= 0.15 && l <= 0.65 && h >= 85 && h <= 165:
		return ToneGreen
	case sat <= 0.2 && distance(bg, a.dominant.bg) < 24:
		cr := contrast(c, bg)
		base, ok := parseColor(a.dominant.color)
		if !ok {
			return ToneNone
		}
		bcr := contrast(base.over(a.dominant.bg), a.dominant.bg)
		if cr >= 1.5 && cr < 0.4*bcr {
			return ToneMuted
		}
	}
	return ToneNone
}

// tones sets Tone on text nodes and mirrors it onto their element. A class
// shared with the page baseline is not a deviation and stays unset.
func (a *analyzer) tones() {
	baseTone := ToneNone
	if c, ok := parseColor(a.dominant.color); ok {
		h, sat, _ := c.hsl()
		switch {
		case sat >= 0.5 && (h <= 15 || h >= 340):
			baseTone = ToneRed
		case sat >= 0.4 && h >= 85 && h <= 165:
			baseTone = ToneGreen
		}
	}
	for i := int32(0); i < int32(a.n); i++ {
		if !a.visibleText(i) {
			continue
		}
		t := a.toneOf(a.style(i)[snapshot.Color], a.backgroundOf(i))
		if t == baseTone {
			t = ToneNone
		}
		nd := &a.nodes[i]
		nd.Tone = t
		p := a.s.Parent[i]
		if p == snapshot.None {
			continue
		}
		pn := &a.nodes[p]
		if pn.Tone == ToneNone {
			pn.Tone = t
		}
		if nd.Strike {
			pn.Strike = true
		}
	}
}

// headings gives non-semantic text blocks a visual heading level when their
// size, or weight and size, stand out from the page median.
func (a *analyzer) headings() {
	s := a.s
	inInteractive := make([]bool, a.n)
	for i := int32(0); i < int32(a.n); i++ {
		if p := s.Parent[i]; p != snapshot.None {
			inInteractive[i] = inInteractive[p] || a.nodes[p].Interactive
		}
	}
	// Parts of a heading are not headings of their own.
	inHeading := make([]bool, a.n)
	for i := int32(0); i < int32(a.n); i++ {
		nd := &a.nodes[i]
		if p := s.Parent[i]; p != snapshot.None {
			inHeading[i] = inHeading[p] || a.nodes[p].Heading != 0
		}
		if s.Kind[i] != snapshot.KindElement || nd.Heading != 0 || nd.Hidden || nd.Interactive || inInteractive[i] || inHeading[i] || nd.Role != "" {
			continue
		}
		texts := a.directTexts(i)
		if len(texts) == 0 {
			continue
		}
		var sb strings.Builder
		for _, t := range texts {
			sb.WriteString(s.Text[t])
		}
		n := len([]rune(words(sb.String())))
		if n == 0 || n > 140 {
			continue
		}
		ratio := a.fontSize(i) / a.median
		switch {
		case ratio >= 2:
			nd.Heading = 1
		case ratio >= 1.6:
			nd.Heading = 2
		case ratio >= 1.3:
			nd.Heading = 3
		case ratio >= 1.15 && a.fontWeight(i) >= 600:
			nd.Heading = 4
		}
	}
}

// truncation flags text cut by ellipsis or line clamp. Without a scrollable
// extent larger than the box the cut cannot be proven and nothing is set.
func (a *analyzer) truncation() {
	s := a.s
	for i := int32(0); i < int32(a.n); i++ {
		if s.Kind[i] != snapshot.KindElement || a.nodes[i].Hidden || !s.Laid[i] {
			continue
		}
		st := &s.Style[i]
		sc, b := s.Scroll[i], s.Box[i]
		if strings.Contains(st[snapshot.TextOverflow], "ellipsis") && sc.W > b.W+1 {
			a.nodes[i].Truncated = true
		}
		if lc := st[snapshot.LineClamp]; lc != "" && lc != "none" && sc.H > b.H+1 {
			a.nodes[i].Truncated = true
		}
		if t := s.Tag[i]; t != "html" && t != "body" {
			y := scrolls(st[snapshot.OverflowY]) && sc.H > b.H+1
			x := scrolls(st[snapshot.OverflowX]) && sc.W > b.W+1
			a.nodes[i].Scrollable = x || y
		}
	}
}

// primaries marks the one button in a sibling group whose fill stands out.
func (a *analyzer) primaries() {
	s := a.s
	groups := map[int32][]int32{}
	var order []int32
	for i := int32(0); i < int32(a.n); i++ {
		nd := &a.nodes[i]
		if nd.Role != "button" || !nd.Visible() || nd.Disabled || !s.Laid[i] {
			continue
		}
		p := s.Parent[i]
		if _, ok := groups[p]; !ok {
			order = append(order, p)
		}
		groups[p] = append(groups[p], i)
	}
	for _, p := range order {
		g := groups[p]
		if len(g) < 2 {
			continue
		}
		fills := make([]rgba, len(g))
		dist := make([]float64, len(g))
		for k, i := range g {
			bg := a.backgroundOf(i)
			fill := bg
			if c, ok := parseColor(s.Style[i][snapshot.BackgroundColor]); ok {
				fill = c.over(bg)
			}
			fills[k] = fill
			dist[k] = distance(fill, bg)
		}
		if w := uniqueFill(fills); len(g) >= 3 && w >= 0 {
			a.nodes[g[w]].Primary = true
			continue
		}
		if len(g) == 2 {
			hi, lo := 0, 1
			if dist[1] > dist[0] {
				hi, lo = 1, 0
			}
			if dist[hi]-dist[lo] >= 80 {
				a.nodes[g[hi]].Primary = true
			}
		}
	}
}

// uniqueFill returns the index of the single fill that differs from all the
// others when those others are equal, else -1.
func uniqueFill(f []rgba) int {
	const tol = 12
	for k := range f {
		other := -1
		ok := true
		for j := range f {
			if j == k {
				continue
			}
			if other == -1 {
				other = j
			}
			if distance(f[j], f[other]) > tol {
				ok = false
				break
			}
		}
		if ok && other >= 0 && distance(f[k], f[other]) > 2*tol {
			return k
		}
	}
	return -1
}

// smallPrint reports whether an element with the role holds a page's small
// print rather than its body: navigation, the page's footer, an aside. A
// footer inside an article belongs to the article and is not small print.
func smallPrint(role string) bool {
	switch role {
	case "navigation", "contentinfo", "complementary":
		return true
	}
	return false
}
