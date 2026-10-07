package facts_test

import (
	"fmt"
	"testing"

	"github.com/noetive/riffle/internal/facts"
	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/snapshot"
)

func TestDisabledSources(t *testing.T) {
	for _, c := range []struct {
		name string
		opts []pb.Opt
		want bool
	}{
		{"disabled attribute", []pb.Opt{pb.Attr("disabled", "")}, true},
		{"aria-disabled true", []pb.Opt{pb.Attr("aria-disabled", "true")}, true},
		{"aria-disabled false", []pb.Opt{pb.Attr("aria-disabled", "false")}, false},
		{"aria-disabled junk", []pb.Opt{pb.Attr("aria-disabled", "yes")}, false},
		{"nothing", nil, false},
	} {
		b := pb.New(1280, 800)
		i := b.El(b.Body(), "button", R(0, 0, 50, 20), c.opts...)
		if got := analyze(b).Nodes[i].Disabled; got != c.want {
			t.Errorf("%s: Disabled = %v", c.name, got)
		}
	}
}

func TestDisabledFieldsetReachesNestedControlsOnly(t *testing.T) {
	b := pb.New(1280, 800)
	fs := b.El(b.Body(), "fieldset", R(0, 0, 200, 100), pb.Attr("disabled", ""))
	wrap := b.El(fs, "div", R(0, 0, 200, 50))
	deep := b.El(wrap, "input", R(0, 0, 50, 20))
	open := b.El(b.Body(), "fieldset", R(0, 200, 200, 100))
	inOpen := b.El(open, "input", R(0, 200, 50, 20))
	div := b.El(b.Body(), "div", R(0, 400, 200, 100), pb.Attr("disabled", ""))
	inDiv := b.El(div, "input", R(0, 400, 50, 20))
	p := analyze(b)
	if !p.Nodes[deep].Disabled {
		t.Error("control nested in a disabled fieldset is disabled")
	}
	if p.Nodes[inOpen].Disabled || p.Nodes[inDiv].Disabled {
		t.Error("only a disabled fieldset disables its descendants")
	}
}

func TestSelectedSources(t *testing.T) {
	for _, c := range []struct {
		name string
		tag  string
		opts []pb.Opt
		want bool
	}{
		{"aria-selected true", "div", []pb.Opt{pb.Attr("aria-selected", "true")}, true},
		{"aria-selected false", "div", []pb.Opt{pb.Attr("aria-selected", "false")}, false},
		{"aria-current page", "a", []pb.Opt{pb.Attr("aria-current", "page")}, true},
		{"aria-current true", "a", []pb.Opt{pb.Attr("aria-current", "true")}, true},
		{"aria-current empty is its default, false", "a", []pb.Opt{pb.Attr("aria-current", "")}, false},
		{"aria-current blank is its default, false", "a", []pb.Opt{pb.Attr("aria-current", "  ")}, false},
		{"aria-current unknown token counts as true", "a", []pb.Opt{pb.Attr("aria-current", "yes")}, true},
		{"aria-current false", "a", []pb.Opt{pb.Attr("aria-current", "false")}, false},
		{"selected option", "option", []pb.Opt{pb.Ticked()}, true},
		{"selected attribute on other tag", "div", []pb.Opt{pb.Attr("selected", "")}, false},
		{"unselected option", "option", nil, false},
	} {
		b := pb.New(1280, 800)
		i := b.El(b.Body(), c.tag, R(0, 0, 50, 20), c.opts...)
		if got := analyze(b).Nodes[i].Selected; got != c.want {
			t.Errorf("%s: Selected = %v", c.name, got)
		}
	}
}

func TestCheckedSources(t *testing.T) {
	for _, c := range []struct {
		name string
		tag  string
		opts []pb.Opt
		want bool
	}{
		{"aria-checked true", "div", []pb.Opt{pb.Attr("aria-checked", "true")}, true},
		{"aria-checked mixed", "div", []pb.Opt{pb.Attr("aria-checked", "mixed")}, true},
		{"aria-checked false", "div", []pb.Opt{pb.Attr("aria-checked", "false")}, false},
		{"checked checkbox", "input", []pb.Opt{pb.Attr("type", "checkbox"), pb.Ticked()}, true},
		{"checked radio", "input", []pb.Opt{pb.Attr("type", "radio"), pb.Ticked()}, true},
		{"checked text input", "input", []pb.Opt{pb.Attr("type", "text"), pb.Attr("checked", "")}, false},
		{"checked attribute on div", "div", []pb.Opt{pb.Attr("checked", "")}, false},
		{"checked attribute on role checkbox div", "div", []pb.Opt{pb.Attr("role", "checkbox"), pb.Attr("checked", "")}, false},
		{"unchecked checkbox", "input", []pb.Opt{pb.Attr("type", "checkbox")}, false},
	} {
		b := pb.New(1280, 800)
		i := b.El(b.Body(), c.tag, R(0, 0, 50, 20), c.opts...)
		if got := analyze(b).Nodes[i].Checked; got != c.want {
			t.Errorf("%s: Checked = %v", c.name, got)
		}
	}
}

func TestStatesAreNotSetOnTextOrDocument(t *testing.T) {
	b := pb.New(1280, 800)
	d := b.El(b.Body(), "div", R(0, 0, 100, 20), pb.Attr("tabindex", "0"))
	tx := b.Text(d, "hello")
	p := analyze(b)
	if p.Nodes[tx].Clickable || p.Nodes[0].Clickable {
		t.Error("text and document are never clickable")
	}
}

func TestClickableEvidence(t *testing.T) {
	for _, c := range []struct {
		name string
		tag  string
		opts []pb.Opt
		want bool
	}{
		{"listener", "div", []pb.Opt{pb.Clickable()}, true},
		{"pointer cursor", "div", []pb.Opt{pb.Style(snapshot.Cursor, "pointer")}, true},
		{"text cursor", "div", []pb.Opt{pb.Style(snapshot.Cursor, "text")}, false},
		{"tabindex 0", "div", []pb.Opt{pb.Attr("tabindex", "0")}, true},
		{"tabindex padded", "div", []pb.Opt{pb.Attr("tabindex", " 3 ")}, true},
		{"tabindex negative", "div", []pb.Opt{pb.Attr("tabindex", "-1")}, false},
		{"tabindex padded negative", "div", []pb.Opt{pb.Attr("tabindex", " -1")}, false},
		{"tabindex empty", "div", []pb.Opt{pb.Attr("tabindex", "")}, false},
		{"tabindex blank", "div", []pb.Opt{pb.Attr("tabindex", "  ")}, false},
		{"listener on a role", "button", []pb.Opt{pb.Clickable()}, false},
		{"listener on hidden", "div", []pb.Opt{pb.Clickable(), pb.NotLaid()}, false},
		{"pointer on unlaid", "div", []pb.Opt{pb.Style(snapshot.Cursor, "pointer"), pb.NotLaid()}, false},
		{"listener on body", "body", []pb.Opt{pb.Clickable()}, false},
	} {
		b := pb.New(1280, 800)
		parent := b.Body()
		i := b.El(parent, c.tag, R(0, 0, 50, 20), c.opts...)
		if c.tag == "body" {
			i = b.Body()
			b.Snapshot().Clickable[i] = true
		}
		if got := analyze(b).Nodes[i].Clickable; got != c.want {
			t.Errorf("%s: Clickable = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPointerCursorCountsWhereItStarts(t *testing.T) {
	b := pb.New(1280, 800)
	outer := b.El(b.Body(), "div", R(0, 0, 100, 20), pb.Style(snapshot.Cursor, "pointer"))
	inner := b.El(outer, "span", R(0, 0, 50, 20), pb.Style(snapshot.Cursor, "pointer"))
	// A child that changes the cursor back and then forth again starts anew.
	plain := b.El(b.Body(), "div", R(0, 60, 100, 20))
	restart := b.El(plain, "span", R(0, 60, 50, 20), pb.Style(snapshot.Cursor, "pointer"))
	p := analyze(b)
	if !p.Nodes[outer].Clickable || p.Nodes[inner].Clickable {
		t.Errorf("outer=%v inner=%v", p.Nodes[outer].Clickable, p.Nodes[inner].Clickable)
	}
	if !p.Nodes[restart].Clickable {
		t.Error("pointer under a default-cursor parent is a new evidence")
	}
}

func TestClickableOnBodyAndHTMLNeverFires(t *testing.T) {
	b := pb.New(1280, 800)
	b.Snapshot().Style[b.Body()][snapshot.Cursor] = "pointer"
	if analyze(b).Nodes[b.Body()].Clickable {
		t.Error("body is never a click target")
	}
}

func TestWidgetStatesFromAttributes(t *testing.T) {
	for _, c := range []struct {
		name string
		tag  string
		opts []pb.Opt
		get  func(n facts.Node) string
		want string
	}{
		{"aria-expanded true", "button", []pb.Opt{pb.Attr("aria-expanded", "true")}, func(n facts.Node) string { return n.Expansion }, "expanded"},
		{"aria-expanded false", "button", []pb.Opt{pb.Attr("aria-expanded", "false")}, func(n facts.Node) string { return n.Expansion }, "collapsed"},
		{"no aria-expanded", "button", nil, func(n facts.Node) string { return n.Expansion }, ""},
		{"aria-pressed true", "button", []pb.Opt{pb.Attr("aria-pressed", "true")}, func(n facts.Node) string { return fmt.Sprint(n.Pressed) }, "true"},
		{"aria-pressed false", "button", []pb.Opt{pb.Attr("aria-pressed", "false")}, func(n facts.Node) string { return fmt.Sprint(n.Pressed) }, "false"},
		{"required input", "input", []pb.Opt{pb.Attr("required", "")}, func(n facts.Node) string { return fmt.Sprint(n.Required) }, "true"},
		{"aria-required", "div", []pb.Opt{pb.Attr("role", "textbox"), pb.Attr("aria-required", "true")}, func(n facts.Node) string { return fmt.Sprint(n.Required) }, "true"},
		{"optional input", "input", nil, func(n facts.Node) string { return fmt.Sprint(n.Required) }, "false"},
		{"aria-invalid", "input", []pb.Opt{pb.Attr("aria-invalid", "true")}, func(n facts.Node) string { return fmt.Sprint(n.Invalid) }, "true"},
	} {
		b := pb.New(1280, 800)
		i := b.El(b.Body(), c.tag, R(0, 0, 50, 20), c.opts...)
		if got := c.get(analyze(b).Nodes[i]); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDetailsSummaryReportsWhetherItIsOpen(t *testing.T) {
	for _, open := range []bool{false, true} {
		b := pb.New(1280, 800)
		var opts []pb.Opt
		if open {
			opts = append(opts, pb.Attr("open", ""))
		}
		d := b.El(b.Body(), "details", R(0, 0, 100, 40), opts...)
		sum := b.El(d, "summary", R(0, 0, 100, 20))
		want := "collapsed"
		if open {
			want = "expanded"
		}
		if got := analyze(b).Nodes[sum].Expansion; got != want {
			t.Errorf("open=%v: summary is %q, want %q", open, got, want)
		}
	}
}

func TestLabelOfAControlIsNotAControlItself(t *testing.T) {
	b := pb.New(1280, 800)
	forLabel := b.El(b.Body(), "label", R(0, 0, 50, 20), pb.Attr("for", "e"), pb.Clickable())
	wrap := b.El(b.Body(), "label", R(0, 30, 80, 20), pb.Clickable())
	b.El(wrap, "input", R(0, 30, 20, 20), pb.Attr("type", "checkbox"))
	bare := b.El(b.Body(), "label", R(0, 60, 50, 20), pb.Clickable())
	p := analyze(b)
	if p.Nodes[forLabel].Clickable || p.Nodes[wrap].Clickable {
		t.Error("a label that hands the click to a control is the control's name, not a second control")
	}
	if !p.Nodes[bare].Clickable {
		t.Error("a label with no control behind it and a listener is something to click")
	}
}

func TestMarkupCheckedAttributeAloneDoesNotMeanChecked(t *testing.T) {
	b := pb.New(1280, 800)
	i := b.El(b.Body(), "input", R(0, 0, 20, 20), pb.Attr("type", "checkbox"), pb.Attr("checked", ""))
	if analyze(b).Nodes[i].Checked {
		t.Error("a person may have unticked the box; only the live state counts")
	}
}

func TestOptionSelectedFollowsTheLiveChoiceNotTheMarkup(t *testing.T) {
	b := pb.New(1280, 800)
	sel := b.El(b.Body(), "select", R(0, 0, 50, 20))
	small := b.El(sel, "option", R(0, 0, 50, 20), pb.Attr("selected", ""))
	large := b.El(sel, "option", R(0, 20, 50, 20), pb.Ticked())
	p := analyze(b)
	if p.Nodes[small].Selected || !p.Nodes[large].Selected {
		t.Errorf("selected: small=%v large=%v, want the live choice (large) only", p.Nodes[small].Selected, p.Nodes[large].Selected)
	}
}

func TestClickableBlockIsNamedByTextAndImageAlt(t *testing.T) {
	b := pb.New(1280, 800)
	img := b.El(b.Body(), "div", R(0, 0, 40, 40), pb.Clickable())
	b.El(img, "img", R(0, 0, 24, 24), pb.Attr("alt", "Example News"))
	text := b.El(b.Body(), "div", R(0, 50, 100, 20), pb.Clickable())
	b.Text(text, "Read more")
	mixed := b.El(b.Body(), "div", R(0, 80, 200, 40), pb.Clickable())
	b.El(mixed, "img", R(0, 80, 24, 24), pb.Attr("alt", "Logo"))
	b.Text(mixed, "Brand")
	bare := b.El(b.Body(), "div", R(0, 130, 40, 40), pb.Clickable())
	b.El(bare, "img", R(0, 130, 24, 24), pb.Attr("alt", ""))
	p := analyze(b)
	for id, want := range map[int32]string{img: "Example News", text: "Read more", mixed: "Logo Brand", bare: ""} {
		if got := p.Nodes[id].Name; got != want {
			t.Errorf("name = %q, want %q", got, want)
		}
	}
}

// The disabled attribute disables only the elements HTML gives it to (form
// controls, optgroup, option, fieldset); on anything else, such as a link, it
// does nothing and a person can still use the element.
func TestTheDisabledAttributeDisablesOnlyFormControls(t *testing.T) {
	for _, c := range []struct {
		tag  string
		want bool
	}{
		{"button", true}, {"input", true}, {"select", true}, {"textarea", true},
		{"a", false}, {"div", false}, {"span", false},
	} {
		b := pb.New(1280, 800)
		opts := []pb.Opt{pb.Attr("disabled", "")}
		if c.tag == "a" {
			opts = append(opts, pb.Attr("href", "/next"))
		}
		i := b.El(b.Body(), c.tag, R(0, 0, 50, 20), opts...)
		if got := analyze(b).Nodes[i].Disabled; got != c.want {
			t.Errorf("<%s disabled>: Disabled = %v, want %v", c.tag, got, c.want)
		}
	}
}

// A disabled fieldset disables the form controls inside it, except those in
// its first legend, and nothing that is not a form control (HTML, "disabled"
// form controls).
func TestADisabledFieldsetDisablesItsFormControlsButNotItsLegendOrLinks(t *testing.T) {
	b := pb.New(1280, 800)
	fs := b.El(b.Body(), "fieldset", R(0, 0, 400, 200), pb.Attr("disabled", ""))
	legend := b.El(fs, "legend", R(0, 0, 400, 20))
	inLegend := b.El(legend, "input", R(0, 0, 20, 20), pb.Attr("type", "checkbox"))
	field := b.El(fs, "input", R(0, 40, 100, 20))
	link := b.El(fs, "a", R(0, 80, 100, 20), pb.Attr("href", "/help"))
	second := b.El(fs, "legend", R(0, 120, 400, 20))
	inSecond := b.El(second, "button", R(0, 120, 50, 20))
	nodes := analyze(b).Nodes
	if !nodes[field].Disabled || !nodes[inSecond].Disabled {
		t.Error("form controls in a disabled fieldset are disabled, a second legend gives no exemption")
	}
	if nodes[inLegend].Disabled {
		t.Error("a control in the fieldset's first legend stays usable")
	}
	if nodes[link].Disabled {
		t.Error("a link is not a form control; a disabled fieldset leaves it usable")
	}
}

// HTML: an option in a disabled optgroup is disabled, though it has no
// disabled attribute of its own.
func TestAnOptionInADisabledGroupIsDisabled(t *testing.T) {
	b := pb.New(1280, 800)
	sel := b.El(b.Body(), "select", R(0, 0, 200, 20))
	off := b.El(sel, "optgroup", R(0, 0, 200, 20), pb.Attr("disabled", ""))
	inOff := b.El(off, "option", R(0, 0, 200, 20))
	on := b.El(sel, "optgroup", R(0, 20, 200, 20))
	inOn := b.El(on, "option", R(0, 20, 200, 20))
	nodes := analyze(b).Nodes
	if !nodes[inOff].Disabled || nodes[inOn].Disabled {
		t.Errorf("disabled: in a disabled group %v, in an enabled one %v", nodes[inOff].Disabled, nodes[inOn].Disabled)
	}
}
