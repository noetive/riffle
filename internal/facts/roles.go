package facts

import (
	"strconv"
	"strings"

	"github.com/noetive/riffle/internal/snapshot"
)

var interactiveRoles = map[string]bool{
	"link": true, "button": true, "textbox": true, "searchbox": true, "spinbutton": true,
	"checkbox": true, "radio": true, "switch": true, "combobox": true, "listbox": true,
	"slider": true, "menuitem": true, "menuitemcheckbox": true, "menuitemradio": true,
	"tab": true, "option": true,
}

var inputRoles = map[string]string{
	"": "textbox", "text": "textbox", "password": "textbox", "email": "textbox", "tel": "textbox",
	"url": "textbox", "search": "searchbox", "number": "spinbutton", "range": "slider",
	"checkbox": "checkbox", "radio": "radio", "submit": "button", "button": "button",
	"reset": "button", "image": "button", "date": "textbox", "time": "textbox",
	"datetime-local": "textbox", "month": "textbox", "week": "textbox", "color": "button",
	"file": "button",
}

var tagRoles = map[string]string{
	"button": "button", "textarea": "textbox", "nav": "navigation", "main": "main",
	"aside": "complementary", "form": "form", "dialog": "dialog", "article": "article",
	"ul": "list", "ol": "list", "menu": "list", "li": "listitem", "table": "table",
	"tr": "row", "td": "cell", "thead": "rowgroup", "tbody": "rowgroup", "tfoot": "rowgroup",
	"summary": "button", "details": "group", "fieldset": "group", "progress": "progressbar",
	"meter": "meter", "hr": "separator", "output": "status", "option": "option",
	"h1": "heading", "h2": "heading", "h3": "heading", "h4": "heading", "h5": "heading", "h6": "heading",
}

// roles sets Role, Interactive and the explicit heading level.
func (a *analyzer) roles() {
	s := a.s
	for i := int32(0); i < int32(a.n); i++ {
		if s.Kind[i] != snapshot.KindElement {
			continue
		}
		r := a.roleOf(i)
		a.nodes[i].Role = r
		a.nodes[i].Interactive = interactiveRoles[r] && !a.nodes[i].Hidden
		if r == "heading" {
			lvl := 2
			if t := s.Tag[i]; len(t) == 2 && t[0] == 'h' && t[1] >= '1' && t[1] <= '6' {
				lvl = int(t[1] - '0')
			}
			if v, err := strconv.Atoi(a.attr(i, "aria-level")); err == nil && v >= 1 && v <= 6 {
				lvl = v
			}
			a.nodes[i].Heading = lvl
		}
	}
}

func (a *analyzer) roleOf(i int32) string {
	if r := strings.Fields(strings.ToLower(a.attr(i, "role"))); len(r) > 0 && r[0] != "presentation" && r[0] != "none" {
		return r[0]
	}
	base := a.tagRole(i)
	if base == "" && a.editingHost(i) && !a.hasStructure(i) {
		return "textbox"
	}
	return base
}

// tagRole is the role an element's tag gives it, "" for generic containers.
func (a *analyzer) tagRole(i int32) string {
	s := a.s
	tag := s.Tag[i]
	switch tag {
	case "a", "area":
		if a.hasAttr(i, "href") {
			return "link"
		}
		return ""
	case "input":
		return inputRoles[strings.ToLower(a.attr(i, "type"))]
	case "select":
		if a.hasAttr(i, "multiple") {
			return "listbox"
		}
		if n, err := strconv.Atoi(a.attr(i, "size")); err == nil && n > 1 {
			return "listbox"
		}
		return "combobox"
	case "img":
		if alt, ok := s.Attr(i, "alt"); ok && alt == "" {
			return ""
		}
		return "img"
	case "th":
		if a.attr(i, "scope") == "row" {
			return "rowheader"
		}
		return "columnheader"
	case "header", "footer":
		for p := s.Parent[i]; p != snapshot.None; p = s.Parent[p] {
			switch s.Tag[p] {
			case "article", "section", "main", "aside", "nav":
				return ""
			}
		}
		if tag == "header" {
			return "banner"
		}
		return "contentinfo"
	case "section":
		if a.hasAttr(i, "aria-label") || a.hasAttr(i, "aria-labelledby") {
			return "region"
		}
		return ""
	}
	return tagRoles[tag]
}

// editable reports contenteditable set to anything but "false".
func (a *analyzer) editable(i int32) bool {
	v, ok := a.s.Attr(i, "contenteditable")
	return ok && strings.ToLower(strings.TrimSpace(v)) != "false"
}

// structural tags give a region structure of its own: headings, lists,
// tables and landmarks. A link counts only with an href.
var structural = map[string]bool{
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"ul": true, "ol": true, "menu": true, "li": true, "table": true,
	"nav": true, "main": true, "aside": true, "article": true, "form": true,
}

// hasStructure: the subtree holds a heading, link, list, table or landmark,
// which a lone textbox would swallow. Plain paragraphs and line breaks do not
// count, so a rich text editor's lines stay one textbox.
func (a *analyzer) hasStructure(i int32) bool {
	for j := i + 1; j < a.end[i]; j++ {
		if a.s.Kind[j] != snapshot.KindElement {
			continue
		}
		if structural[a.s.Tag[j]] || (a.s.Tag[j] == "a" && a.hasAttr(j, "href")) {
			return true
		}
		switch strings.ToLower(a.attr(j, "role")) {
		case "heading", "link", "list", "table", "navigation":
			return true
		}
	}
	return false
}

// editingHost: an editable element whose parent is not itself editable, so
// one editing region is one textbox.
func (a *analyzer) editingHost(i int32) bool {
	if !a.editable(i) {
		return false
	}
	p := a.s.Parent[i]
	return p == snapshot.None || !a.editable(p)
}

// Acts reports whether the role is one a person acts on, such as a button or
// a field, whether or not the node is shown now.
func Acts(role string) bool { return interactiveRoles[role] }
