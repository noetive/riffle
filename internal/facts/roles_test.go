package facts_test

import (
	"testing"

	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
)

// roleOf builds a single visible element under the body and returns its role.
func roleOf(tag string, opts ...pb.Opt) string {
	b := pb.New(1280, 800)
	i := b.El(b.Body(), tag, R(0, 0, 100, 20), opts...)
	return analyze(b).Nodes[i].Role
}

func TestTagRoles(t *testing.T) {
	for tag, want := range map[string]string{
		"button": "button", "textarea": "textbox", "nav": "navigation", "main": "main",
		"aside": "complementary", "form": "form", "dialog": "dialog", "article": "article",
		"ul": "list", "ol": "list", "li": "listitem", "table": "table", "tr": "row", "td": "cell",
		"thead": "rowgroup", "summary": "button", "details": "group", "fieldset": "group",
		"progress": "progressbar", "meter": "meter", "hr": "separator", "output": "status",
		"option": "option", "h1": "heading", "h6": "heading", "div": "", "span": "", "p": "",
	} {
		if got := roleOf(tag); got != want {
			t.Errorf("<%s> role = %q, want %q", tag, got, want)
		}
	}
}

func TestInputTypeRoles(t *testing.T) {
	for typ, want := range map[string]string{
		"": "textbox", "text": "textbox", "password": "textbox", "email": "textbox", "search": "searchbox",
		"number": "spinbutton", "range": "slider", "checkbox": "checkbox", "radio": "radio",
		"submit": "button", "button": "button", "reset": "button", "file": "button", "SEARCH": "searchbox",
		"hidden": "",
	} {
		if got := roleOf("input", pb.Attr("type", typ)); got != want {
			t.Errorf("input type=%q role = %q, want %q", typ, got, want)
		}
	}
	if got := roleOf("input"); got != "textbox" {
		t.Errorf("input without type = %q", got)
	}
}

func TestExplicitRoleWins(t *testing.T) {
	for _, c := range []struct {
		attr, want string
	}{
		{"tab", "tab"},
		{"  Menuitem extra", "menuitem"},
		{"presentation", "button"},
		{"none", "button"},
		{"", "button"},
	} {
		if got := roleOf("button", pb.Attr("role", c.attr)); got != c.want {
			t.Errorf("role=%q on button = %q, want %q", c.attr, got, c.want)
		}
	}
	if got := roleOf("div", pb.Attr("role", "presentation")); got != "" {
		t.Errorf("presentation div = %q", got)
	}
}

func TestLinkNeedsHref(t *testing.T) {
	if roleOf("a") != "" || roleOf("area") != "" {
		t.Error("anchor without href is not a link")
	}
	if roleOf("a", pb.Attr("href", "")) != "link" || roleOf("area", pb.Attr("href", "/x")) != "link" {
		t.Error("href makes a link, even when empty")
	}
}

func TestSelectRole(t *testing.T) {
	for _, c := range []struct {
		name string
		opts []pb.Opt
		want string
	}{
		{"plain", nil, "combobox"},
		{"multiple", []pb.Opt{pb.Attr("multiple", "")}, "listbox"},
		{"size 1", []pb.Opt{pb.Attr("size", "1")}, "combobox"},
		{"size 2", []pb.Opt{pb.Attr("size", "2")}, "listbox"},
		{"size 0", []pb.Opt{pb.Attr("size", "0")}, "combobox"},
		{"size junk", []pb.Opt{pb.Attr("size", "x")}, "combobox"},
	} {
		if got := roleOf("select", c.opts...); got != c.want {
			t.Errorf("select %s = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestImageRole(t *testing.T) {
	if roleOf("img") != "img" || roleOf("img", pb.Attr("alt", "x")) != "img" {
		t.Error("img with no or non-empty alt is an image")
	}
	if roleOf("img", pb.Attr("alt", "")) != "" {
		t.Error("empty alt marks a decorative image")
	}
}

func TestTableHeaderRoles(t *testing.T) {
	if roleOf("th") != "columnheader" || roleOf("th", pb.Attr("scope", "col")) != "columnheader" {
		t.Error("th defaults to column header")
	}
	if roleOf("th", pb.Attr("scope", "row")) != "rowheader" {
		t.Error("th scope=row is a row header")
	}
}

func TestSectionIsRegionOnlyWhenNamed(t *testing.T) {
	if roleOf("section") != "" {
		t.Error("unnamed section is generic")
	}
	if roleOf("section", pb.Attr("aria-label", "Deals")) != "region" {
		t.Error("labelled section is a region")
	}
	b := pb.New(1280, 800)
	h := b.El(b.Body(), "h2", R(0, 0, 100, 20), pb.Attr("id", "hd"))
	b.Text(h, "Deals")
	sec := b.El(b.Body(), "section", R(0, 30, 100, 20), pb.Attr("aria-labelledby", "hd"))
	if got := analyze(b).Nodes[sec]; got.Role != "region" || got.Name != "Deals" {
		t.Errorf("labelledby section = %q %q", got.Role, got.Name)
	}
	// An aria-label that is only whitespace names nothing, so no region.
	if roleOf("section", pb.Attr("aria-label", "   ")) != "" {
		t.Error("blank label must not make a region")
	}
}

func TestHeaderAndFooterLandmarksOnlyAtPageLevel(t *testing.T) {
	if roleOf("header") != "banner" || roleOf("footer") != "contentinfo" {
		t.Error("page level header/footer are landmarks")
	}
	for _, anc := range []string{"article", "section", "main", "aside", "nav"} {
		b := pb.New(1280, 800)
		w := b.El(b.Body(), anc, R(0, 0, 500, 100))
		h := b.El(w, "header", R(0, 0, 100, 20))
		f := b.El(w, "footer", R(0, 30, 100, 20))
		p := analyze(b)
		if p.Nodes[h].Role != "" || p.Nodes[f].Role != "" {
			t.Errorf("header/footer inside <%s> must be generic, got %q %q", anc, p.Nodes[h].Role, p.Nodes[f].Role)
		}
	}
	b := pb.New(1280, 800)
	w := b.El(b.Body(), "div", R(0, 0, 500, 100))
	h := b.El(w, "header", R(0, 0, 100, 20))
	if analyze(b).Nodes[h].Role != "banner" {
		t.Error("a div ancestor does not demote the landmark")
	}
}

func TestInteractiveRoles(t *testing.T) {
	for _, c := range []struct {
		tag  string
		opts []pb.Opt
		want bool
	}{
		{"button", nil, true},
		{"a", []pb.Opt{pb.Attr("href", "/")}, true},
		{"input", nil, true},
		{"select", nil, true},
		{"textarea", nil, true},
		{"div", []pb.Opt{pb.Attr("role", "tab")}, true},
		{"div", []pb.Opt{pb.Attr("role", "option")}, true},
		{"div", []pb.Opt{pb.Attr("role", "switch")}, true},
		{"nav", nil, false},
		{"h1", nil, false},
		{"li", nil, false},
		{"img", nil, false},
		{"div", nil, false},
	} {
		b := pb.New(1280, 800)
		i := b.El(b.Body(), c.tag, R(0, 0, 100, 20), c.opts...)
		if got := analyze(b).Nodes[i].Interactive; got != c.want {
			t.Errorf("<%s> interactive = %v, want %v", c.tag, got, c.want)
		}
	}
}

func TestHeadingLevels(t *testing.T) {
	for _, c := range []struct {
		name string
		tag  string
		opts []pb.Opt
		want int
	}{
		{"h1", "h1", nil, 1},
		{"h3", "h3", nil, 3},
		{"h6", "h6", nil, 6},
		{"role heading defaults to 2", "div", []pb.Opt{pb.Attr("role", "heading")}, 2},
		{"aria-level on div", "div", []pb.Opt{pb.Attr("role", "heading"), pb.Attr("aria-level", "5")}, 5},
		{"aria-level overrides tag", "h1", []pb.Opt{pb.Attr("aria-level", "4")}, 4},
		{"aria-level 1 accepted", "div", []pb.Opt{pb.Attr("role", "heading"), pb.Attr("aria-level", "1")}, 1},
		{"aria-level 6 accepted", "div", []pb.Opt{pb.Attr("role", "heading"), pb.Attr("aria-level", "6")}, 6},
		{"aria-level 0 ignored", "h2", []pb.Opt{pb.Attr("aria-level", "0")}, 2},
		{"aria-level 7 ignored", "h2", []pb.Opt{pb.Attr("aria-level", "7")}, 2},
		{"aria-level junk ignored", "h3", []pb.Opt{pb.Attr("aria-level", "x")}, 3},
	} {
		b := pb.New(1280, 800)
		i := b.El(b.Body(), c.tag, R(0, 0, 100, 20), c.opts...)
		if got := analyze(b).Nodes[i].Heading; got != c.want {
			t.Errorf("%s: level %d, want %d", c.name, got, c.want)
		}
	}
	// A non-heading never carries a level, even with aria-level.
	b := pb.New(1280, 800)
	i := b.El(b.Body(), "div", R(0, 0, 100, 20), pb.Attr("aria-level", "3"))
	if analyze(b).Nodes[i].Heading != 0 {
		t.Error("aria-level alone does not make a heading")
	}
}

func TestHiddenElementIsNotInteractive(t *testing.T) {
	b := pb.New(1280, 800)
	i := b.El(b.Body(), "button", R(0, 0, 100, 20), pb.NotLaid())
	if analyze(b).Nodes[i].Interactive {
		t.Error("hidden button interactive")
	}
}

func TestContentEditableHostIsATextbox(t *testing.T) {
	for _, v := range []string{"true", "", "plaintext-only", "TRUE"} {
		if got := roleOf("div", pb.Attr("contenteditable", v)); got != "textbox" {
			t.Errorf("contenteditable=%q = %q, want textbox", v, got)
		}
	}
	if got := roleOf("div", pb.Attr("contenteditable", "false")); got != "" {
		t.Errorf("contenteditable=false = %q", got)
	}
	if got := roleOf("div", pb.Attr("contenteditable", "true"), pb.Attr("role", "combobox")); got != "combobox" {
		t.Errorf("explicit role = %q", got)
	}
}

func TestContentEditableChildIsNotASecondTextbox(t *testing.T) {
	b := pb.New(1280, 800)
	host := b.El(b.Body(), "div", R(0, 0, 100, 40), pb.Attr("contenteditable", "true"))
	child := b.El(host, "div", R(0, 0, 100, 20), pb.Attr("contenteditable", "true"))
	plain := b.El(host, "p", R(0, 20, 100, 20))
	p := analyze(b)
	if p.Nodes[host].Role != "textbox" || p.Nodes[child].Role != "" || p.Nodes[plain].Role != "" {
		t.Errorf("roles %q %q %q", p.Nodes[host].Role, p.Nodes[child].Role, p.Nodes[plain].Role)
	}
}

func TestContentEditableKeepsTheStructureOfItsHost(t *testing.T) {
	b := pb.New(1280, 800)
	art := b.El(b.Body(), "article", R(0, 0, 400, 200), pb.Attr("contenteditable", "true"))
	h := b.El(art, "h1", R(0, 0, 400, 30))
	b.Text(h, "Title")
	b.Text(b.El(art, "p", R(0, 40, 400, 20)), "Body")
	link := b.El(art, "a", R(0, 70, 100, 20), pb.Attr("href", "/x"))
	b.Text(link, "more")
	p := analyze(b)
	if got := p.Nodes[art].Role; got != "article" {
		t.Errorf("article host role = %q, want article", got)
	}
	if p.Nodes[h].Role != "heading" || p.Nodes[link].Role != "link" {
		t.Errorf("heading %q link %q", p.Nodes[h].Role, p.Nodes[link].Role)
	}
}

func TestContentEditableOnAnotherRoleKeepsThatRole(t *testing.T) {
	for tag, want := range map[string]string{"h1": "heading", "li": "listitem", "td": "cell", "button": "button"} {
		if got := roleOf(tag, pb.Attr("contenteditable", "true")); got != want {
			t.Errorf("<%s contenteditable> = %q, want %q", tag, got, want)
		}
	}
	if got := roleOf("a", pb.Attr("href", "/x"), pb.Attr("contenteditable", "true")); got != "link" {
		t.Errorf("<a href contenteditable> = %q, want link", got)
	}
}

func TestContentEditableHostWithOnlyLinesOfTextIsATextbox(t *testing.T) {
	b := pb.New(1280, 800)
	host := b.El(b.Body(), "div", R(0, 0, 400, 100), pb.Attr("contenteditable", "true"))
	b.Text(b.El(host, "p", R(0, 0, 400, 20)), "line one")
	b.Text(b.El(host, "div", R(0, 20, 400, 20)), "line two")
	if got := analyze(b).Nodes[host].Role; got != "textbox" {
		t.Errorf("role = %q, want textbox", got)
	}
}

func TestContentEditableHostWithAriaStructureIsNotATextbox(t *testing.T) {
	b := pb.New(1280, 800)
	host := b.El(b.Body(), "div", R(0, 0, 400, 100), pb.Attr("contenteditable", "true"))
	b.Text(b.El(host, "div", R(0, 0, 400, 20), pb.Attr("role", "heading")), "Title")
	if got := analyze(b).Nodes[host].Role; got == "textbox" {
		t.Error("a host holding a heading is not one textbox")
	}
}

// editableDivWith builds an editable div holding one child element and
// returns the host's role.
func editableDivWith(tag string, opts ...pb.Opt) string {
	b := pb.New(1280, 800)
	host := b.El(b.Body(), "div", R(0, 0, 400, 100), pb.Attr("contenteditable", "true"))
	k := b.El(host, tag, R(0, 0, 100, 20), opts...)
	b.Text(k, "inside")
	return analyze(b).Nodes[host].Role
}

func TestContentEditableGenericHostSwallowsNoStructure(t *testing.T) {
	for _, tag := range []string{"h1", "h3", "ul", "ol", "li", "table", "nav", "article", "form"} {
		if got := editableDivWith(tag); got == "textbox" {
			t.Errorf("host holding <%s> is a textbox", tag)
		}
	}
	if got := editableDivWith("a", pb.Attr("href", "/x")); got == "textbox" {
		t.Error("host holding a link is a textbox")
	}
	for _, r := range []string{"heading", "link", "list", "table", "navigation"} {
		if got := editableDivWith("div", pb.Attr("role", r)); got == "textbox" {
			t.Errorf("host holding role=%s is a textbox", r)
		}
	}
	for tag, opts := range map[string][]pb.Opt{"a": nil, "span": nil, "b": nil, "br": nil} {
		if got := editableDivWith(tag, opts...); got != "textbox" {
			t.Errorf("host holding plain <%s> = %q, want textbox", tag, got)
		}
	}
}

func TestContentEditableStructureOutsideTheHostDoesNotCount(t *testing.T) {
	b := pb.New(1280, 800)
	host := b.El(b.Body(), "div", R(0, 0, 400, 20), pb.Attr("contenteditable", "true"))
	b.Text(host, "just text")
	b.Text(b.El(b.Body(), "h1", R(0, 30, 400, 20)), "A heading after the editor")
	if got := analyze(b).Nodes[host].Role; got != "textbox" {
		t.Errorf("role = %q, want textbox", got)
	}
}
