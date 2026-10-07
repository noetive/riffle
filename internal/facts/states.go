package facts

import (
	"strings"

	"github.com/noetive/riffle/internal/snapshot"
)

// states sets Disabled, Selected, Checked, Expansion, Pressed, Required,
// Invalid and Clickable from attributes and
// listener/cursor evidence.
func (a *analyzer) states() {
	s := a.s
	// The first legend child of each fieldset: what is inside it stays usable
	// when the fieldset is disabled. Nodes come in document order.
	firstLegend := map[int32]int32{}
	for i := int32(0); i < int32(a.n); i++ {
		if p := s.Parent[i]; s.Tag[i] == "legend" && p != snapshot.None && s.Tag[p] == "fieldset" {
			if _, ok := firstLegend[p]; !ok {
				firstLegend[p] = i
			}
		}
	}
	for i := int32(0); i < int32(a.n); i++ {
		if s.Kind[i] != snapshot.KindElement {
			continue
		}
		nd := &a.nodes[i]
		nd.Disabled = (disablesByAttribute[s.Tag[i]] && a.hasAttr(i, "disabled")) || a.attr(i, "aria-disabled") == "true"
		if p := s.Parent[i]; !nd.Disabled && s.Tag[i] == "option" && p != snapshot.None && s.Tag[p] == "optgroup" && a.hasAttr(p, "disabled") {
			// HTML: an option in a disabled optgroup is disabled.
			nd.Disabled = true
		}
		if !nd.Disabled && formControl[s.Tag[i]] {
			// HTML: a form control in a disabled fieldset is disabled, unless
			// it is in that fieldset's first legend.
			via := i
			for p := s.Parent[i]; p != snapshot.None; via, p = p, s.Parent[p] {
				if s.Tag[p] == "fieldset" && a.hasAttr(p, "disabled") {
					if l, ok := firstLegend[p]; !ok || l != via {
						nd.Disabled = true
						break
					}
				}
			}
		}
		// WAI-ARIA: an empty aria-current is its default, false; any other
		// value but false, known or not, marks the element current.
		cur := strings.ToLower(strings.TrimSpace(a.attr(i, "aria-current")))
		nd.Selected = a.attr(i, "aria-selected") == "true" || (cur != "" && cur != "false") ||
			(s.Tag[i] == "option" && s.Ticked[i])
		ac := a.attr(i, "aria-checked")
		nd.Checked = ac == "true" || ac == "mixed" ||
			(s.Tag[i] == "input" && (nd.Role == "checkbox" || nd.Role == "radio") && s.Ticked[i])
		switch ae := a.attr(i, "aria-expanded"); {
		case ae == "true":
			nd.Expansion = "expanded"
		case ae == "false":
			nd.Expansion = "collapsed"
		case s.Tag[i] == "summary" && s.Parent[i] != snapshot.None && s.Tag[s.Parent[i]] == "details":
			nd.Expansion = "collapsed"
			if a.hasAttr(s.Parent[i], "open") {
				nd.Expansion = "expanded"
			}
		}
		nd.Pressed = a.attr(i, "aria-pressed") == "true"
		nd.Required = a.hasAttr(i, "required") || a.attr(i, "aria-required") == "true"
		nd.Invalid = a.attr(i, "aria-invalid") == "true"
		nd.Clickable = a.clickable(i)
		if nd.Clickable && nd.Name == "" {
			// A block with a click handler is named by what it shows, an image's
			// alt included.
			if t := a.content(i, false); t != "" {
				nd.Name = t
			}
		}
	}
}

// handsClickToControl: the label points at a control with for, or wraps one,
// so clicking it is clicking that control.
func (a *analyzer) handsClickToControl(i int32) bool {
	return a.hasAttr(i, "for") || a.holdsControl(i)
}

func (a *analyzer) holdsControl(i int32) bool {
	for _, c := range a.s.Children[i] {
		switch a.s.Tag[c] {
		case "input", "select", "textarea", "button":
			return true
		}
		if a.s.Kind[c] == snapshot.KindElement && a.holdsControl(c) {
			return true
		}
	}
	return false
}

// clickable: a non-semantic node that reacts to clicks. Cursor evidence only
// counts where the pointer cursor starts, because cursor is inherited.
func (a *analyzer) clickable(i int32) bool {
	s := a.s
	nd := &a.nodes[i]
	if nd.Hidden || nd.Role != "" || s.Tag[i] == "html" || s.Tag[i] == "body" {
		return false
	}
	if s.Tag[i] == "label" && a.handsClickToControl(i) {
		return false
	}
	if s.Clickable[i] {
		return true
	}
	if s.Laid[i] && s.Style[i][snapshot.Cursor] == "pointer" {
		p := s.Parent[i]
		if p == snapshot.None || !s.Laid[p] || s.Style[p][snapshot.Cursor] != "pointer" {
			return true
		}
	}
	if v, ok := s.Attr(i, "tabindex"); ok && strings.TrimSpace(v) != "" && !strings.HasPrefix(strings.TrimSpace(v), "-") {
		return true
	}
	return false
}

// disablesByAttribute are the elements HTML gives a disabled attribute: on
// any other element, such as a link, the attribute does nothing.
var disablesByAttribute = map[string]bool{
	"button": true, "input": true, "select": true, "textarea": true,
	"optgroup": true, "option": true, "fieldset": true,
}

// formControl are the elements a disabled fieldset disables (HTML, listed
// form-associated elements that can be disabled).
var formControl = map[string]bool{
	"button": true, "input": true, "select": true, "textarea": true, "fieldset": true,
}
