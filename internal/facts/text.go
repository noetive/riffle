package facts

import (
	"strings"

	"github.com/noetive/riffle/internal/snapshot"
)

// textFacts sets Text, Unseen, Generated and Strike on text nodes, and
// mirrors Generated onto pseudo-element nodes.
func (a *analyzer) textFacts() {
	s := a.s
	for i := int32(0); i < int32(a.n); i++ {
		nd := &a.nodes[i]
		if s.Pseudo[i] != "" {
			nd.Generated = true
		}
		if s.Kind[i] != snapshot.KindText {
			if s.Pseudo[i] != "" && s.Text[i] != "" {
				nd.Text = collapse(s.Text[i])
			}
			continue
		}
		nd.Text = collapse(s.Text[i])
		if p := s.Parent[i]; p != snapshot.None && s.Pseudo[p] != "" {
			nd.Generated = true
		}
		if nd.Hidden || strings.TrimSpace(s.Text[i]) == "" {
			continue
		}
		nd.Unseen = nd.Offscreen || nd.Clipped || a.unreadable(i)
		nd.Strike = a.struck(i)
	}
}

// unreadable: the text is rendered but cannot be read.
func (a *analyzer) unreadable(i int32) bool {
	st := a.style(i)
	if fs, ok := parsePx(st[snapshot.FontSize]); ok && fs <= 1 {
		return true
	}
	if ti, ok := parsePx(st[snapshot.TextIndent]); ok && ti <= -500 {
		return true
	}
	if a.paintedThrough(i) {
		// Drawn in its background, whatever its own colour: readable.
		return false
	}
	fg, ok := parseColor(st[snapshot.Color])
	if !ok {
		return false
	}
	if fg.a <= 0.05 {
		return true
	}
	bg, known := a.backdrop(i)
	if !known {
		return false // an image sits behind the text, whose colours are not measured
	}
	return contrast(fg.over(bg), bg) < 1.1
}

// backdrop is the colour painted at node i, which is what its children sit
// on, and false when a background image is involved so the colour cannot be
// known. Chrome blends a background only for elements with a single text
// child and leaves it empty when an image is involved, so the node and its
// parent are asked first, then each ancestor's blended colour, else its own
// computed colour is composited over the layers further out until one is
// opaque. A text node's computed style is its parent's, so the computed walk
// starts at the parent element; counting both would stack a translucent
// layer twice. The page itself is taken as white.
func (a *analyzer) backdrop(i int32) (rgba, bool) {
	s := a.s
	var layers []rgba // translucent, innermost first
	for k := i; k != snapshot.None; k = s.Parent[k] {
		if c, ok := parseColor(s.Background[k]); ok && c.a > 0 {
			return stack(layers, c.over(white)), true
		}
		if s.Kind[k] == snapshot.KindText {
			continue
		}
		if img := s.Style[k][snapshot.BackgroundImage]; img != "" && img != "none" {
			return rgba{}, false
		}
		if c, ok := parseColor(s.Style[k][snapshot.BackgroundColor]); ok && c.a > 0 {
			if c.a >= 1 {
				return stack(layers, c), true
			}
			layers = append(layers, c)
		}
	}
	return stack(layers, white), true
}

// stack composites translucent layers, innermost first, over base.
func stack(layers []rgba, base rgba) rgba {
	for j := len(layers) - 1; j >= 0; j-- {
		base = layers[j].over(base)
	}
	return base
}

// struck reports line-through on the text's element or an ancestor that
// propagates its decoration.
func (a *analyzer) struck(i int32) bool {
	s := a.s
	for p := s.Parent[i]; p != snapshot.None; p = s.Parent[p] {
		if s.Kind[p] != snapshot.KindElement || !s.Laid[p] {
			continue
		}
		st := &s.Style[p]
		if strings.Contains(st[snapshot.TextDecorationLine], "line-through") {
			return true
		}
		if st[snapshot.Position] == "absolute" || st[snapshot.Position] == "fixed" ||
			(st[snapshot.Float] != "" && st[snapshot.Float] != "none") ||
			strings.HasPrefix(st[snapshot.Display], "inline-") {
			return false
		}
	}
	return false
}

// visibleText reports whether text node i carries readable content.
func (a *analyzer) visibleText(i int32) bool {
	n := &a.nodes[i]
	return a.s.Kind[i] == snapshot.KindText && !n.Hidden && !n.Unseen && strings.TrimSpace(a.s.Text[i]) != ""
}

// paintedThrough reports text drawn in the background of its element or an
// ancestor clipped to the text (CSS Backgrounds 4, background-clip: text):
// the glyphs show that background, as a gradient heading does, so the text's
// own colour, often transparent, is not what a person sees.
func (a *analyzer) paintedThrough(i int32) bool {
	s := a.s
	for k := s.Parent[i]; k != snapshot.None; k = s.Parent[k] {
		if s.Kind[k] != snapshot.KindElement || !strings.Contains(s.Style[k][snapshot.BackgroundClip], "text") {
			continue
		}
		if img := s.Style[k][snapshot.BackgroundImage]; img != "" && img != "none" {
			return true
		}
		if c, ok := parseColor(s.Style[k][snapshot.BackgroundColor]); ok && c.a > 0 {
			return true
		}
	}
	return false
}
