package facts

import (
	"strconv"
	"strings"

	"github.com/noetive/riffle/internal/snapshot"
)

var nonRendered = map[string]bool{
	"script": true, "style": true, "head": true, "meta": true, "link": true,
	"title": true, "template": true, "noscript": true, "base": true,
}

func solidBox(r snapshot.Rect) bool { return !r.Empty() && (r.W > 1 || r.H > 1) }

// visibility sets Hidden, Offscreen and Clipped.
func (a *analyzer) visibility() {
	s := a.s
	n := int32(a.n)
	solid := make([]bool, n)
	for i := n - 1; i >= 0; i-- {
		if s.Laid[i] && solidBox(s.Box[i]) {
			solid[i] = true
		}
		if solid[i] {
			if p := s.Parent[i]; p != snapshot.None {
				solid[p] = true
			}
		}
	}
	a.gone = make([]bool, n)
	for i := int32(0); i < n; i++ {
		g := false
		if p := s.Parent[i]; p != snapshot.None && a.gone[p] {
			g = true
		}
		st := a.style(i)
		switch s.Kind[i] {
		case snapshot.KindElement:
			switch {
			case nonRendered[s.Tag[i]]:
				g = true
			case !s.Laid[i]:
				// No box is display: none, or display: contents (a slot, a wrapper
				// component). Only the second has laid-out content below it.
				if st[snapshot.Display] != "contents" && !solid[i] {
					g = true
				}
			case st[snapshot.Display] == "none":
				g = true
			case opacityZero(st[snapshot.Opacity]) && !a.drawnOver(i):
				g = true
			case s.Tag[i] == "input" && strings.EqualFold(a.attr(i, "type"), "hidden"):
				g = true
			}
		case snapshot.KindText:
			if !s.Laid[i] && strings.TrimSpace(s.Text[i]) != "" {
				g = true
			}
		case snapshot.KindOther:
			g = true
		}
		a.gone[i] = g
		h := g
		if !h && s.Kind[i] != snapshot.KindDocument {
			if v := st[snapshot.Visibility]; v == "hidden" || v == "collapse" {
				h = true
			}
			if s.Kind[i] == snapshot.KindText {
				// Whitespace at a line break or between inline boxes has no box of
				// its own but still separates the words around it.
				if s.Box[i].Empty() && strings.TrimSpace(s.Text[i]) != "" {
					h = true
				}
			} else if !solid[i] && st[snapshot.Display] != "contents" && !a.spaceOnly(i) {
				h = true
			}
		}
		a.nodes[i].Hidden = h
	}
	for i := int32(0); i < n; i++ {
		if a.nodes[i].Hidden || s.Kind[i] == snapshot.KindDocument {
			continue
		}
		if s.Laid[i] {
			a.nodes[i].Offscreen = a.offscreen(s.Box[i]) && !a.scrollsWithin(i)
			a.nodes[i].Clipped = a.clipped(i)
		}
	}
	// A box that holds something drawn on screen is not out of reach, however
	// its own box lies: a fixed dialog belongs to the viewport, not to the
	// empty or clipped wrapper it sits in (CSS 2.1 9.6.1 and 11.1.1; CSS
	// Overflow 3 section 2.2). Words count as drawn too; the node's own words
	// keep their own judgement.
	shows := make([]bool, n)
	for i := n - 1; i >= 0; i-- {
		nd := &a.nodes[i]
		if clipsAway(a.style(i)[snapshot.ClipPath]) {
			shows[i] = false // nothing inside a box clipped away is drawn
		}
		if shows[i] && (nd.Offscreen || nd.Clipped) {
			nd.Offscreen, nd.Clipped = false, false
			nd.Holds = true
		}
		if s.Laid[i] && solidBox(s.Box[i]) && nd.Visible() {
			shows[i] = true
		}
		if p := s.Parent[i]; shows[i] && p != snapshot.None {
			shows[p] = true
		}
	}
}

// drawnOver: a checkbox or radio made invisible so a custom control can be
// drawn in its place, as component libraries do. The visible sibling sits over
// it, so a person clicks the real input; a lone invisible one stays hidden.
func (a *analyzer) drawnOver(i int32) bool {
	s := a.s
	if s.Tag[i] != "input" || !s.Laid[i] || !solidBox(s.Box[i]) {
		return false
	}
	if t := strings.ToLower(a.attr(i, "type")); t != "checkbox" && t != "radio" {
		return false
	}
	p := s.Parent[i]
	if p == snapshot.None {
		return false
	}
	b := s.Box[i]
	for _, k := range s.Children[p] {
		if k == i || s.Kind[k] != snapshot.KindElement || !s.Laid[k] || !solidBox(s.Box[k]) || opacityZero(s.Style[k][snapshot.Opacity]) {
			continue
		}
		c := s.Box[k]
		if b.X < c.X+c.W && c.X < b.X+b.W && b.Y < c.Y+c.H && c.Y < b.Y+b.H {
			return true
		}
	}
	return false
}

func opacityZero(v string) bool {
	if v == "" {
		return false
	}
	f, err := strconv.ParseFloat(v, 64)
	return err == nil && f <= 0.01
}

// offscreen: the box cannot be reached by scrolling.
func (a *analyzer) offscreen(b snapshot.Rect) bool {
	s := a.s
	w := max(s.ContentW, s.ViewportW)
	h := max(s.ContentH, s.ViewportH)
	// A zero-width box on the edge, such as a line break at the left margin, is
	// still where the reader is.
	if (b.X+b.W <= 0 && b.W > 0) || b.X+b.W < 0 || b.Y+b.H <= 0 {
		return true
	}
	return (w > 0 && b.X >= w) || (h > 0 && b.Y >= h)
}

// clipsAway: a clip-path that leaves nothing of the box, nor of anything
// inside it, fixed or not (CSS Masking 1, section 5).
func clipsAway(cp string) bool {
	return strings.Contains(cp, "inset(100%") || strings.Contains(cp, "circle(0") || strings.Contains(cp, "inset(50%")
}

func clips(v string) bool { return v == "hidden" || v == "clip" }

func scrolls(v string) bool { return v == "auto" || v == "scroll" }

// scrollsWithin: an ancestor scrolls its own content, so a box beyond the
// page's extent can still be reached by scrolling that ancestor.
func (a *analyzer) scrollsWithin(i int32) bool {
	s := a.s
	for p := s.Parent[i]; p != snapshot.None; p = s.Parent[p] {
		if s.Kind[p] != snapshot.KindElement || !s.Laid[p] || s.Tag[p] == "html" || s.Tag[p] == "body" {
			continue
		}
		if (scrolls(s.Style[p][snapshot.OverflowX]) || scrolls(s.Style[p][snapshot.OverflowY])) && !a.nodes[p].Offscreen {
			return true
		}
	}
	return false
}

// clipped: some overflow-clipping ancestor leaves no part of the box visible.
func (a *analyzer) clipped(i int32) bool {
	s := a.s
	b := s.Box[i]
	if clipsAway(a.style(i)[snapshot.ClipPath]) {
		return true
	}
	// A clip-path clips all that is inside, fixed or not.
	for p := s.Parent[i]; p != snapshot.None; p = s.Parent[p] {
		if s.Kind[p] == snapshot.KindElement && clipsAway(a.style(p)[snapshot.ClipPath]) {
			return true
		}
	}
	cur := i
	for p := s.Parent[i]; p != snapshot.None; cur, p = p, s.Parent[p] {
		if a.style(cur)[snapshot.Position] == "fixed" {
			return false
		}
		if s.Kind[p] != snapshot.KindElement || !s.Laid[p] || s.Tag[p] == "html" || s.Tag[p] == "body" {
			continue
		}
		pb := s.Box[p]
		if clips(s.Style[p][snapshot.OverflowX]) && (b.X >= pb.X+pb.W || b.X+b.W <= pb.X) {
			return true
		}
		if clips(s.Style[p][snapshot.OverflowY]) && (b.Y >= pb.Y+pb.H || b.Y+b.H <= pb.Y) {
			return true
		}
	}
	return false
}

// spaceOnly: a line break, or an inline element that holds nothing but whitespace. At a line
// break it has no box, yet the whitespace still separates the words around it.
func (a *analyzer) spaceOnly(i int32) bool {
	s := a.s
	if s.Laid[i] && (s.Tag[i] == "br" || s.Tag[i] == "wbr") {
		return true // a line break is as wide as nothing and still separates words
	}
	if !s.Laid[i] || !strings.HasPrefix(a.style(i)[snapshot.Display], "inline") || len(s.Children[i]) == 0 {
		return false
	}
	for _, c := range s.Children[i] {
		if s.Kind[c] != snapshot.KindText || strings.TrimSpace(s.Text[c]) != "" {
			return false
		}
	}
	return true
}
