package facts

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/noetive/riffle/internal/snapshot"
)

var nameFromContent = map[string]bool{
	"button": true, "link": true, "heading": true, "cell": true, "columnheader": true,
	"rowheader": true, "option": true, "tab": true, "menuitem": true, "menuitemcheckbox": true,
	"menuitemradio": true, "checkbox": true, "radio": true, "switch": true, "listitem": false,
}

// names sets Name on elements and records which controls are named by an
// explicit label, which excludes them from proximity labelling.
func (a *analyzer) names() {
	s := a.s
	a.explicit = make([]bool, a.n)
	for i := int32(0); i < int32(a.n); i++ {
		if s.Kind[i] != snapshot.KindElement || a.nodes[i].Hidden {
			continue
		}
		a.labelled = false
		name, explicit := a.nameOf(i)
		a.nodes[i].Name = name
		a.explicit[i] = explicit
		// A label inside the content is a second channel too: the words a
		// person reads may say something else.
		if (explicit || a.labelled) && nameFromContent[a.nodes[i].Role] {
			if shown := a.content(i, false); differs(name, shown) {
				a.nodes[i].Shown = shown
			}
		}
	}
	for i := int32(0); i < int32(a.n); i++ {
		nd := &a.nodes[i]
		if nd.Role == "region" && nd.Name == "" {
			nd.Role = ""
		}
	}
}

func (a *analyzer) nameOf(i int32) (name string, explicit bool) {
	a.naming = true
	defer func() { a.naming = false }()
	s := a.s
	nd := &a.nodes[i]
	if ids := strings.Fields(a.attr(i, "aria-labelledby")); len(ids) > 0 {
		var parts []string
		for _, id := range ids {
			if j, ok := a.ids[id]; ok {
				// A referenced node gives its own aria-label first. A node
				// referenced while hidden is read whole, hidden as it is
				// (step 2A); hidden parts of a shown one are still left out.
				t := words(a.attr(j, "aria-label"))
				if t == "" {
					a.referenced = a.nodes[j].Hidden || ariaHidden(a.attr(j, "aria-hidden"))
					t = a.content(j, false)
					a.referenced = false
				}
				if t != "" {
					parts = append(parts, t)
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, " "), true
		}
	}
	if l := words(a.attr(i, "aria-label")); l != "" {
		return l, true
	}
	if s.Tag[i] == "area" {
		// An image-map region has no content; its alt is what it says.
		if alt := words(a.attr(i, "alt")); alt != "" {
			return alt, false
		}
	}
	tag := s.Tag[i]
	typ := strings.ToLower(a.attr(i, "type"))
	switch {
	case tag == "img":
		if alt := words(a.attr(i, "alt")); alt != "" {
			return alt, true
		}
		return words(a.attr(i, "title")), false
	case tag == "input" && (typ == "submit" || typ == "button" || typ == "reset"):
		if v := words(a.attr(i, "value")); v != "" {
			return v, true
		}
		if v := words(s.Value[i]); v != "" {
			return v, true
		}
		if typ == "submit" {
			return "Submit", false
		}
	case tag == "input" && typ == "image":
		if alt := words(a.attr(i, "alt")); alt != "" {
			return alt, true
		}
	case tag == "input" || tag == "textarea" || tag == "select":
		if l := a.controlLabel(i); l != "" {
			return l, true
		}
		if t := words(a.attr(i, "title")); t != "" {
			return t, false
		}
		return words(a.attr(i, "placeholder")), false
	}
	if nd.Role == "dialog" || nd.Role == "alertdialog" {
		if j := a.firstHeading(i); j != snapshot.None {
			return a.content(j, false), false
		}
	}
	if nameFromContent[nd.Role] {
		if t := a.content(i, false); t != "" {
			return t, false
		}
		// The control's own title is the last name the rules give (step 2I).
		if t := words(a.attr(i, "title")); t != "" {
			return t, false
		}
		// With no name by the rules, the words a person reads on the control
		// name it, as for a close button whose cross the site hid from
		// assistive technology.
		a.naming = false
		t := a.content(i, false)
		a.naming = true
		if t != "" {
			nd.Unlabelled = true
			return t, false
		}
		// An icon-only control is named by the title of what it wraps.
		if nd.Role == "link" || nd.Role == "button" {
			for j := i + 1; j < a.end[i]; j++ {
				if a.s.Kind[j] == snapshot.KindElement && !a.nodes[j].Hidden {
					if t := words(a.attr(j, "title")); t != "" {
						return t, false
					}
				}
			}
			// Image replacement hides the words of a control and draws them as
			// a picture. With no other name, the hidden words are the name.
			a.showUnseen = true
			t := a.content(i, false)
			a.showUnseen = false
			if t != "" {
				nd.NameUnseen = true
				return t, false
			}
		}
	}
	return words(a.attr(i, "title")), false
}

// controlLabel resolves label[for] and wrapping label elements.
func (a *analyzer) controlLabel(i int32) string {
	var parts []string
	if id := a.attr(i, "id"); id != "" {
		for _, l := range a.labels[id] {
			if !a.nodes[l].Hidden {
				if t := a.content(l, true); t != "" {
					parts = append(parts, t)
				}
			}
		}
	}
	if len(parts) == 0 {
		for p := a.s.Parent[i]; p != snapshot.None; p = a.s.Parent[p] {
			if a.s.Tag[p] == "label" {
				if t := a.content(p, true); t != "" {
					parts = append(parts, t)
				}
				break
			}
		}
	}
	return strings.Join(parts, " ")
}

func (a *analyzer) firstHeading(i int32) int32 {
	for j := i + 1; j < a.end[i]; j++ {
		if a.s.Kind[j] == snapshot.KindElement && a.nodes[j].Role == "heading" && !a.nodes[j].Hidden {
			return j
		}
	}
	return snapshot.None
}

// content is the readable text of a subtree: unseen and hidden text is left
// out, images contribute their alt text, block boundaries become spaces.
// skipControls leaves form controls out, as a wrapping label requires.
func (a *analyzer) content(i int32, skipControls bool) string {
	defer func(root int32) { a.root = root }(a.root)
	a.root = i
	var sb strings.Builder
	a.appendContent(&sb, i, skipControls)
	return words(sb.String())
}

func (a *analyzer) appendContent(sb *strings.Builder, i int32, skipControls bool) {
	s := a.s
	nd := &a.nodes[i]
	if nd.Hidden && !a.referenced {
		return
	}
	switch s.Kind[i] {
	case snapshot.KindText:
		if !nd.Unseen || a.showUnseen {
			sb.WriteString(s.Text[i])
		}
		return
	case snapshot.KindElement:
		tag := s.Tag[i]
		if skipControls && (tag == "input" || tag == "select" || tag == "textarea" || tag == "button") {
			return
		}
		if nonRendered[tag] {
			return // script, style and the like say nothing, even when referenced
		}
		if a.naming && i != a.root {
			// Accessible Name and Description Computation 1.2. Step 2A:
			// hidden content is no part of a name unless the reference that
			// reached it is hidden itself. Step 2C: a field inside the content
			// gives its value. Step 2D: an element's aria-label is its text
			// alternative, as for an icon labelled on its svg.
			if !a.referenced && ariaHidden(a.attr(i, "aria-hidden")) {
				return
			}
			if v, ok := a.embeddedValue(i); ok {
				sb.WriteString(" " + v + " ")
				return
			}
			if l := words(a.attr(i, "aria-label")); l != "" {
				a.labelled = true
				sb.WriteString(" " + l + " ")
				return
			}
		}
		if tag == "img" {
			sb.WriteString(" " + a.attr(i, "alt") + " ")
			return
		}
		if tag == "svg" {
			// An icon's own title is its label; <title> is not rendered, so it
			// is read here and not through the walk below.
			if t := a.svgTitle(i); t != "" {
				sb.WriteByte(' ')
				sb.WriteString(t)
				sb.WriteByte(' ')
				return
			}
		}
		// A bullet or disclosure triangle is decoration; a number or a symbol
		// that says something is read, and so is ::before and ::after content.
		if DecorativeMarker(s, i) {
			return
		}
		if s.Pseudo[i] != "" && s.Text[i] != "" {
			sb.WriteString(s.Text[i])
		}
		block := !strings.HasPrefix(a.style(i)[snapshot.Display], "inline") || tag == "br" || tag == "wbr"
		if block {
			sb.WriteByte(' ')
		}
		for _, c := range s.Children[i] {
			a.appendContent(sb, c, skipControls)
		}
		if block {
			sb.WriteByte(' ')
		}
		return
	}
	for _, c := range s.Children[i] {
		a.appendContent(sb, c, skipControls)
	}
}

// svgTitle is the text of an svg's own <title> child.
func (a *analyzer) svgTitle(i int32) string {
	s := a.s
	for _, c := range s.Children[i] {
		if s.Kind[c] != snapshot.KindElement || s.Tag[c] != "title" {
			continue
		}
		var sb strings.Builder
		for _, t := range s.Children[c] {
			if s.Kind[t] == snapshot.KindText {
				sb.WriteString(s.Text[t])
			}
		}
		return words(sb.String())
	}
	return ""
}

// differs reports whether an attribute-given name says something other than
// the text on the control. That is a second channel a person does not read.
// Only letters and digits are compared, in order: punctuation, symbols and
// spacing on a control need not be in its name (WCAG 2.5.3, Label in Name),
// and icon glyphs such as × or ☰ carry no words to contradict.
func differs(name, shown string) bool {
	n, s := letters(name), letters(shown)
	return n != "" && s != "" && !strings.Contains(n, s) && !strings.Contains(s, n)
}

// letters is s lower-cased, with only its letters, marks and digits.
func letters(s string) string {
	return strings.Map(func(r rune) rune {
		// Marks are kept: in many scripts a vowel sign is what tells words apart.
		if unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}

// decorativeMarkers are the glyphs a list or disclosure widget draws as its
// bullet: they repeat the structure the page already has and say nothing.
const decorativeMarkers = "•◦▪▫‣⁃·●○■□–-▸▹►▶▾▿▼◂◃◄◀"

// DecorativeMarker reports whether node is a ::marker whose text is only a
// bullet or a disclosure triangle, which is not read. The number of an ordered list
// ("1.", "iv)"), a letter and a symbol that carries meaning (a tick, a cross, a
// star) are page text and are read, in a name and in a view alike.
func DecorativeMarker(s *snapshot.Snapshot, node int32) bool {
	if s.Pseudo[node] != "marker" {
		return false
	}
	var sb strings.Builder
	markerText(&sb, s, node)
	t := strings.TrimFunc(sb.String(), unicode.IsSpace)
	if t == "" {
		return true
	}
	r, n := utf8.DecodeRuneInString(t)
	return n == len(t) && strings.ContainsRune(decorativeMarkers, r)
}

// markerText is the text a marker draws: its own generated content, which a
// capture holds on the element, and the text nodes beneath it.
func markerText(sb *strings.Builder, s *snapshot.Snapshot, n int32) {
	sb.WriteString(s.Text[n])
	if s.Kind[n] == snapshot.KindText {
		return
	}
	for _, c := range s.Children[n] {
		markerText(sb, s, c)
	}
}

func ariaHidden(v string) bool { return strings.EqualFold(strings.TrimSpace(v), "true") }

// embeddedValue is what a field inside a name's content contributes: its
// value, not its label (Accessible Name Computation 1.2, step 2C).
func (a *analyzer) embeddedValue(i int32) (string, bool) {
	s := a.s
	if t := s.Tag[i]; t != "input" && t != "textarea" && t != "select" && a.attr(i, "aria-valuetext") == "" && a.attr(i, "aria-valuenow") == "" {
		return "", false // an editable box or a widget without a value: its words are read
	}
	if s.Tag[i] == "input" && strings.EqualFold(strings.TrimSpace(a.attr(i, "type")), "password") {
		return "", true // a password is never part of a name
	}
	switch a.nodes[i].Role {
	case "textbox", "searchbox":
		if v := words(s.Value[i]); v != "" {
			return v, true
		}
		return words(a.attr(i, "value")), true
	case "combobox", "listbox":
		if s.Tag[i] != "select" {
			return words(s.Value[i]), true
		}
		var picked []string
		for j := i + 1; j < a.end[i]; j++ {
			if s.Tag[j] == "option" && s.Ticked[j] {
				picked = append(picked, a.content(j, false))
			}
		}
		return strings.Join(picked, " "), true
	case "slider", "spinbutton":
		if v := words(a.attr(i, "aria-valuetext")); v != "" {
			return v, true
		}
		if v := words(a.attr(i, "aria-valuenow")); v != "" {
			return v, true
		}
		if v := words(s.Value[i]); v != "" {
			return v, true
		}
		return words(a.attr(i, "value")), true
	}
	return "", false
}
