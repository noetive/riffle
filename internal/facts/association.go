package facts

import (
	"math"
	"strings"
	"unicode/utf8"

	"github.com/noetive/riffle/internal/snapshot"
)

var fieldRoles = map[string]bool{
	"textbox": true, "searchbox": true, "spinbutton": true, "combobox": true, "listbox": true,
	"checkbox": true, "radio": true, "switch": true, "slider": true,
}

// pair is a candidate text for a control with a proximity distance.
type pair struct {
	ctrl, text int32
	dist       float64
}

// association links form controls to the text that labels them and the
// error text that explains them. It is conservative: a link is set only when
// the control and the text pick each other as nearest and no rival is close.
func (a *analyzer) association() {
	s := a.s
	inControl := make([]bool, a.n)
	inExplicitLabel := make([]bool, a.n)
	inRealHeading := make([]bool, a.n)
	// layer is the covering modal a node sits in, or None for the page itself.
	layer := make([]int32, a.n)
	for i := int32(0); i < int32(a.n); i++ {
		layer[i] = snapshot.None
		if p := s.Parent[i]; p != snapshot.None {
			layer[i] = layer[p]
			inControl[i] = inControl[p] || a.nodes[p].Interactive
			inExplicitLabel[i] = inExplicitLabel[p] || (s.Tag[p] == "label" && a.hasAttr(p, "for"))
			inRealHeading[i] = inRealHeading[p]
		}
		inRealHeading[i] = inRealHeading[i] || a.nodes[i].Role == "heading" // h1-h6 or role=heading, and the text in them
		if a.nodes[i].Modal && a.nodes[i].Covers {
			layer[i] = i
		}
	}
	var ctrls, texts []int32
	for i := int32(0); i < int32(a.n); i++ {
		nd := &a.nodes[i]
		switch {
		case s.Kind[i] == snapshot.KindElement && fieldRoles[nd.Role] && nd.Visible() && s.Laid[i] && !s.Box[i].Empty():
			ctrls = append(ctrls, i)
		case a.visibleText(i) && !inControl[i] && !inExplicitLabel[i] && !a.hiddenBehind(i) && utf8.RuneCountInString(nd.Text) <= 90:
			texts = append(texts, i)
		}
	}
	a.explicitErrors(ctrls)

	var labels, errs []pair
	for _, c := range ctrls {
		for _, t := range texts {
			if layer[c] != layer[t] {
				continue // the page behind a modal is not what labels the modal's controls
			}
			tone := a.nodes[t].Tone
			// A real heading titles a section. It labels a field only when the
			// field has no other name; a survey question set as a heading is then
			// the best name it has.
			titles := inRealHeading[t]
			// A sentence near a field that has a name is a message about the
			// form, such as a sign-in status, that changes while the field does
			// not. A field with no name still takes it: it is the best it has.
			tells := a.nodes[c].Name != "" && sentence(a.nodes[t].Text)
			if d, ok := a.labelDistance(c, t); ok && tone != ToneRed && !a.explicit[c] && (!titles || a.nodes[c].Name == "") && !tells {
				labels = append(labels, pair{c, t, d})
			}
			if d, ok := a.errorDistance(c, t); ok && tone == ToneRed && !titles {
				errs = append(errs, pair{c, t, d})
			}
		}
	}
	for _, p := range mutualNearest(labels) {
		if a.nodes[p.ctrl].Label == snapshot.None {
			a.nodes[p.ctrl].Label = p.text
			if a.nodes[p.ctrl].Name == "" {
				a.nodes[p.ctrl].Name = words(a.nodes[p.text].Text)
			}
		}
	}
	for _, p := range mutualNearest(errs) {
		if a.nodes[p.ctrl].Error == snapshot.None {
			a.nodes[p.ctrl].Error = p.text
		}
	}
}

// hiddenBehind: the text sits under something painted over it, such as the
// page behind a modal. A person cannot read it, so it labels nothing.
func (a *analyzer) hiddenBehind(i int32) bool {
	p := a.s.Parent[i]
	return p != snapshot.None && a.nodes[p].CoveredBy != snapshot.None
}

// explicitErrors follows aria-errormessage to the first readable text.
func (a *analyzer) explicitErrors(ctrls []int32) {
	for _, c := range ctrls {
		id := a.attr(c, "aria-errormessage")
		if id == "" || a.attr(c, "aria-invalid") == "false" {
			continue
		}
		j, ok := a.ids[id]
		if !ok {
			continue
		}
		for t := j + 1; t < a.end[j]; t++ {
			if a.visibleText(t) {
				a.nodes[c].Error = t
				break
			}
		}
	}
}

// mutualNearest keeps pairs where each side's nearest partner is the other
// and the runner-up on both sides is clearly farther.
func mutualNearest(ps []pair) []pair {
	const margin = 6.0
	type best struct {
		other        int32
		dist, second float64
	}
	byCtrl := map[int32]*best{}
	byText := map[int32]*best{}
	upd := func(m map[int32]*best, key, other int32, d float64) {
		b, ok := m[key]
		if !ok {
			m[key] = &best{other, d, math.Inf(1)}
			return
		}
		if d < b.dist {
			b.second, b.dist, b.other = b.dist, d, other
		} else if d < b.second {
			b.second = d
		}
	}
	for _, p := range ps {
		upd(byCtrl, p.ctrl, p.text, p.dist)
		upd(byText, p.text, p.ctrl, p.dist)
	}
	var out []pair
	for c, b := range byCtrl {
		t := byText[b.other]
		if t.other == c && b.second-b.dist >= margin && t.second-t.dist >= margin {
			out = append(out, pair{c, b.other, b.dist})
		}
	}
	// Deterministic order.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ctrl < out[j-1].ctrl; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func vOverlap(a, b snapshot.Rect) float64 {
	top, bot := math.Max(a.Y, b.Y), math.Min(a.Y+a.H, b.Y+b.H)
	h := math.Min(a.H, b.H)
	if h <= 0 {
		return 0
	}
	return math.Max(0, bot-top) / h
}

// labelDistance: text left of the control on its row, directly above it, or
// (for toggles) right of it.
func (a *analyzer) labelDistance(c, t int32) (float64, bool) {
	cb, tb := a.s.Box[c], a.s.Box[t]
	role := a.nodes[c].Role
	if vOverlap(cb, tb) >= 0.5 {
		if gap := cb.X - (tb.X + tb.W); gap >= -2 && gap <= 160 {
			return gap, true
		}
		if role == "checkbox" || role == "radio" || role == "switch" {
			if gap := tb.X - (cb.X + cb.W); gap >= -2 && gap <= 24 {
				return gap, true
			}
		}
	}
	if gap := cb.Y - (tb.Y + tb.H); gap >= -2 && gap <= 24 {
		if dx := math.Abs(tb.X - cb.X); dx <= 24 {
			return gap + 0.25*dx, true
		}
	}
	return 0, false
}

// errorDistance: text directly below the control, left aligned with it.
func (a *analyzer) errorDistance(c, t int32) (float64, bool) {
	cb, tb := a.s.Box[c], a.s.Box[t]
	if gap := tb.Y - (cb.Y + cb.H); gap >= -2 && gap <= 32 {
		if dx := math.Abs(tb.X - cb.X); dx <= 24 || (tb.X >= cb.X && tb.X < cb.X+cb.W) {
			return gap + 0.25*math.Min(dx, 24), true
		}
	}
	return 0, false
}

// sentence reports whether the text ends as a sentence does, with a full
// stop, an exclamation or a question mark. A label names a thing; it rarely
// ends like a sentence.
func sentence(text string) bool {
	t := strings.TrimSpace(text)
	return strings.HasSuffix(t, ".") && !strings.HasSuffix(t, "..") || strings.HasSuffix(t, "!") || strings.HasSuffix(t, "?")
}
