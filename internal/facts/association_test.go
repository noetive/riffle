package facts_test

import (
	"testing"

	"github.com/noetive/riffle/internal/facts"
	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/snapshot"
)

var redText = pb.Style(snapshot.Color, "rgb(200, 30, 30)")

// fieldAndText places a 100x24 text field at (200,100) and a text node with
// the given box, returning whether it was taken as label and as error.
func fieldAndText(role string, text snapshot.Rect, opts ...pb.Opt) (label, errText bool, p *facts.Page, field int32) {
	b := pb.New(1280, 800)
	attrs := []pb.Opt{}
	switch role {
	case "checkbox":
		attrs = append(attrs, pb.Attr("type", "checkbox"))
	}
	for k := 0; k < 3; k++ {
		base := b.El(b.Body(), "p", R(0, float64(500+k*30), 500, 20))
		b.Text(base, "Ordinary text that forms the baseline of the page.")
	}
	field = b.El(b.Body(), "input", R(200, 100, 100, 24), attrs...)
	holder := b.El(b.Body(), "span", text, append([]pb.Opt{pb.Inline()}, opts...)...)
	tx := b.Text(holder, "Some text", pb.Box(text))
	p = analyze(b)
	return p.Nodes[field].Label == tx, p.Nodes[field].Error == tx, p, field
}

func TestLabelToTheLeftOfFieldBoundaries(t *testing.T) {
	for _, c := range []struct {
		name string
		box  snapshot.Rect
		want bool
	}{
		{"adjacent", R(100, 100, 90, 24), true},
		{"gap 160", R(-60, 100, 100, 24), true},
		{"gap 161", R(-61, 100, 100, 24), false},
		{"overlapping by 2", R(110, 100, 92, 24), true},
		{"overlapping by 3", R(110, 100, 93, 24), false},
		{"right of the field", R(320, 100, 50, 24), false},
		{"half overlap rows", R(100, 92, 90, 16), true},
		{"just under half overlap rows", R(100, 91.9, 90, 16), false},
		{"different row", R(100, 200, 90, 24), false},
	} {
		label, _, _, _ := fieldAndText("textbox", c.box)
		if label != c.want {
			t.Errorf("%s: label = %v, want %v", c.name, label, c.want)
		}
	}
}

func TestLabelAboveFieldBoundaries(t *testing.T) {
	for _, c := range []struct {
		name string
		box  snapshot.Rect
		want bool
	}{
		{"directly above", R(200, 76, 60, 16), true},
		{"gap 24", R(200, 60, 60, 16), true},
		{"gap 25", R(200, 59, 60, 16), false},
		{"overlapping by 2", R(200, 86, 60, 16), true},
		{"overlapping by 3 is beside it, not above", R(200, 87, 60, 16), false},
		{"offset 24", R(224, 76, 60, 16), true},
		{"offset 25", R(225, 76, 60, 16), false},
		{"offset left 24", R(176, 76, 60, 16), true},
		{"offset left 25", R(175, 76, 60, 16), false},
	} {
		label, _, _, _ := fieldAndText("textbox", c.box)
		if label != c.want {
			t.Errorf("%s: label = %v, want %v", c.name, label, c.want)
		}
	}
}

func TestToggleLabelsMayFollowTheControl(t *testing.T) {
	for _, c := range []struct {
		name string
		role string
		box  snapshot.Rect
		want bool
	}{
		{"checkbox label right", "checkbox", R(310, 100, 80, 24), true},
		{"checkbox gap 24", "checkbox", R(324, 100, 80, 24), true},
		{"checkbox gap 25", "checkbox", R(325, 100, 80, 24), false},
		{"checkbox overlapping 2", "checkbox", R(298, 100, 80, 24), true},
		{"checkbox overlapping 3", "checkbox", R(297, 100, 80, 24), false},
		{"checkbox label right other row", "checkbox", R(310, 200, 80, 24), false},
		{"textbox label right", "textbox", R(310, 100, 80, 24), false},
	} {
		label, _, _, _ := fieldAndText(c.role, c.box)
		if label != c.want {
			t.Errorf("%s: label = %v, want %v", c.name, label, c.want)
		}
	}
}

func TestErrorBelowFieldBoundaries(t *testing.T) {
	for _, c := range []struct {
		name string
		box  snapshot.Rect
		want bool
	}{
		{"directly below", R(200, 130, 100, 16), true},
		{"gap 32", R(200, 156, 100, 16), true},
		{"gap 33", R(200, 157, 100, 16), false},
		{"overlapping by 2", R(200, 122, 100, 16), true},
		{"overlapping by 3", R(200, 121, 100, 16), false},
		{"offset left 24", R(176, 130, 100, 16), true},
		{"offset left 25", R(175, 130, 100, 16), false},
		{"starts inside the field", R(260, 130, 100, 16), true},
		{"starts at the right edge", R(300, 130, 100, 16), false},
		{"starts at the left edge", R(200, 130, 10, 16), true},
		{"above the field", R(200, 60, 100, 16), false},
	} {
		_, e, _, _ := fieldAndText("textbox", c.box, redText)
		if e != c.want {
			t.Errorf("%s: error = %v, want %v", c.name, e, c.want)
		}
	}
}

func TestToneDecidesBetweenLabelAndError(t *testing.T) {
	label, errText, _, _ := fieldAndText("textbox", R(100, 100, 90, 24), redText)
	if label || errText {
		t.Errorf("red text left of a field: label=%v error=%v", label, errText)
	}
	label, errText, _, _ = fieldAndText("textbox", R(200, 130, 100, 16))
	if label || errText {
		t.Errorf("plain text below a field: label=%v error=%v", label, errText)
	}
	label, errText, _, _ = fieldAndText("textbox", R(200, 76, 60, 16), redText)
	if label || errText {
		t.Errorf("red text above a field is neither: label=%v error=%v", label, errText)
	}
	label, errText, _, _ = fieldAndText("textbox", R(200, 76, 60, 16))
	if !label || errText {
		t.Errorf("plain text above: label=%v error=%v", label, errText)
	}
}

func TestProximityLabelNamesOnlyUnnamedFields(t *testing.T) {
	_, _, p, f := fieldAndText("textbox", R(100, 100, 90, 24))
	if p.Nodes[f].Name != "Some text" {
		t.Errorf("name = %q", p.Nodes[f].Name)
	}
	b := pb.New(1280, 800)
	in := b.El(b.Body(), "input", R(200, 100, 100, 24), pb.Attr("placeholder", "Type here"))
	l := b.El(b.Body(), "span", R(100, 100, 90, 24), pb.Inline())
	lt := b.Text(l, "Qty")
	q := analyze(b)
	if q.Nodes[in].Name != "Type here" || q.Nodes[in].Label != lt {
		t.Errorf("name = %q label = %d", q.Nodes[in].Name, q.Nodes[in].Label)
	}
}

func TestLabelAndErrorCandidateFilters(t *testing.T) {
	long := func(n int) string { return repeat('x', n) }
	for _, c := range []struct {
		name string
		text string
		want bool
	}{
		{"90 characters", long(90), true},
		{"91 characters", long(91), false},
	} {
		b := pb.New(1280, 800)
		in := b.El(b.Body(), "input", R(200, 100, 100, 24))
		l := b.El(b.Body(), "span", R(100, 100, 90, 24), pb.Inline())
		lt := b.Text(l, c.text, pb.Box(R(100, 100, 90, 24)))
		if got := analyze(b).Nodes[in].Label == lt; got != c.want {
			t.Errorf("%s: label = %v", c.name, got)
		}
	}

	b := pb.New(1280, 800)
	in := b.El(b.Body(), "input", R(200, 100, 100, 24))
	hidden := b.El(b.Body(), "span", R(100, 100, 90, 24), pb.NotLaid())
	b.Text(hidden, "Ghost", pb.NotLaid())
	unseen := b.El(b.Body(), "span", R(100, 100, 90, 24), pb.Inline(), pb.Style(snapshot.FontSize, "0px"))
	b.Text(unseen, "Tiny")
	if p := analyze(b); p.Nodes[in].Label != snapshot.None {
		t.Error("hidden and unseen text never label")
	}
}

func TestTextInsideControlsOrExplicitLabelsIsNotACandidate(t *testing.T) {
	b := pb.New(1280, 800)
	in := b.El(b.Body(), "input", R(200, 100, 100, 24))
	btn := b.El(b.Body(), "button", R(100, 100, 90, 24))
	b.Text(btn, "Go")
	lbl := b.El(b.Body(), "label", R(100, 100, 90, 24), pb.Attr("for", "other"))
	b.Text(lbl, "Other")
	if got := analyze(b).Nodes[in].Label; got != snapshot.None {
		t.Errorf("label = %d", got)
	}
}

func TestOnlyFieldRolesAreLabelled(t *testing.T) {
	b := pb.New(1280, 800)
	btn := b.El(b.Body(), "button", R(200, 100, 100, 24))
	b.Text(btn, "Go")
	l := b.El(b.Body(), "span", R(100, 100, 90, 24), pb.Inline())
	b.Text(l, "Label")
	sel := b.El(b.Body(), "select", R(200, 200, 100, 24))
	l2 := b.El(b.Body(), "span", R(100, 200, 90, 24), pb.Inline())
	l2t := b.Text(l2, "Size")
	hid := b.El(b.Body(), "input", R(200, 300, 100, 24), pb.NotLaid())
	l3 := b.El(b.Body(), "span", R(100, 300, 90, 24), pb.Inline())
	b.Text(l3, "Ghost field")
	empty := b.El(b.Body(), "input", R(200, 400, 0, 0))
	l4 := b.El(b.Body(), "span", R(100, 400, 90, 24), pb.Inline())
	b.Text(l4, "Empty field")
	p := analyze(b)
	if p.Nodes[btn].Label != snapshot.None || p.Nodes[hid].Label != snapshot.None || p.Nodes[empty].Label != snapshot.None {
		t.Error("buttons, hidden and empty fields get no label")
	}
	if p.Nodes[sel].Label != l2t {
		t.Error("a select is a field")
	}
}

func TestRivalCandidatesNeedAClearMargin(t *testing.T) {
	for _, c := range []struct {
		name string
		gap2 float64
		want bool
	}{
		{"rival 6px farther", 16, true},
		{"rival 5px farther", 15, false},
		{"rival far away", 80, true},
	} {
		b := pb.New(1280, 800)
		in := b.El(b.Body(), "input", R(200, 100, 100, 24))
		near := b.El(b.Body(), "span", R(100, 100, 90, 24), pb.Inline())
		nt := b.Text(near, "Near", pb.Box(R(100, 100, 90, 24)))
		far := b.El(b.Body(), "span", R(0, 100, 200-c.gap2, 24), pb.Inline())
		b.Text(far, "Far", pb.Box(R(0, 100, 200-c.gap2, 24)))
		got := analyze(b).Nodes[in].Label
		if (got == nt) != c.want {
			t.Errorf("%s: label = %d, want near=%v", c.name, got, c.want)
		}
	}
}

func TestTextEquallyCloseToTwoFieldsLabelsNeither(t *testing.T) {
	b := pb.New(1280, 800)
	l := b.El(b.Body(), "span", R(100, 100, 90, 24), pb.Inline())
	b.Text(l, "Name")
	a := b.El(b.Body(), "input", R(200, 100, 100, 24))
	c := b.El(b.Body(), "input", R(205, 100, 100, 24))
	p := analyze(b)
	if p.Nodes[a].Label != snapshot.None || p.Nodes[c].Label != snapshot.None {
		t.Error("ambiguous text must label neither field")
	}
}

func TestTwoFieldsWithTheirOwnLabels(t *testing.T) {
	b := pb.New(1280, 800)
	l1 := b.El(b.Body(), "span", R(100, 100, 90, 24), pb.Inline())
	t1 := b.Text(l1, "First")
	f1 := b.El(b.Body(), "input", R(200, 100, 100, 24))
	l2 := b.El(b.Body(), "span", R(100, 300, 90, 24), pb.Inline())
	t2 := b.Text(l2, "Second")
	f2 := b.El(b.Body(), "input", R(200, 300, 100, 24))
	p := analyze(b)
	if p.Nodes[f1].Label != t1 || p.Nodes[f2].Label != t2 {
		t.Errorf("labels %d %d, want %d %d", p.Nodes[f1].Label, p.Nodes[f2].Label, t1, t2)
	}
}

func TestExplicitErrorMessage(t *testing.T) {
	b := pb.New(1280, 800)
	in := b.El(b.Body(), "input", R(200, 100, 100, 24), pb.Attr("aria-errormessage", "err"))
	box := b.El(b.Body(), "div", R(0, 600, 300, 40), pb.Attr("id", "err"))
	hidden := b.El(box, "span", R(0, 600, 10, 10), pb.NotLaid())
	b.Text(hidden, "Ghost", pb.NotLaid())
	blank := b.El(box, "span", R(0, 610, 10, 10))
	b.Text(blank, "   ")
	first := b.Text(box, "Bad value")
	b.Text(box, "Second")
	invalidFalse := b.El(b.Body(), "input", R(200, 200, 100, 24), pb.Attr("aria-errormessage", "err"), pb.Attr("aria-invalid", "false"))
	unknown := b.El(b.Body(), "input", R(200, 300, 100, 24), pb.Attr("aria-errormessage", "nope"))
	p := analyze(b)
	if p.Nodes[in].Error != first {
		t.Errorf("error = %d, want %d", p.Nodes[in].Error, first)
	}
	if p.Nodes[invalidFalse].Error != snapshot.None || p.Nodes[unknown].Error != snapshot.None {
		t.Error("aria-invalid=false and unknown ids give no error")
	}
}

func TestExplicitErrorBeatsProximity(t *testing.T) {
	b := pb.New(1280, 800)
	in := b.El(b.Body(), "input", R(200, 100, 100, 24), pb.Attr("aria-errormessage", "err"))
	near := b.El(b.Body(), "div", R(200, 130, 100, 16), redText)
	b.Text(near, "Near error")
	far := b.El(b.Body(), "div", R(0, 600, 300, 40), pb.Attr("id", "err"))
	ft := b.Text(far, "Explicit error")
	if got := analyze(b).Nodes[in].Error; got != ft {
		t.Errorf("error = %d, want the explicit %d", got, ft)
	}
}

func TestTextHiddenBehindAnOverlayIsNotTheLabelOfAControlOnTheOverlay(t *testing.T) {
	b := pb.New(1280, 800)
	for k := 0; k < 3; k++ {
		base := b.El(b.Body(), "p", R(0, float64(500+k*30), 500, 20))
		b.Text(base, "Ordinary text that forms the baseline of the page.")
	}
	behind := b.El(b.Body(), "p", R(200, 70, 150, 20))
	behindText := b.Text(behind, "A brief history of React")
	b.El(b.Body(), "div", R(100, 40, 600, 200), pb.Fill("rgb(255, 255, 255)"), pb.Position("fixed"))
	field := b.El(b.Body(), "input", R(200, 100, 100, 24))
	p := analyze(b)
	if p.Nodes[behind].CoveredBy == snapshot.None {
		t.Fatal("fixture: the paragraph must be covered by the overlay")
	}
	if p.Nodes[field].Label == behindText {
		t.Error("text the overlay hides cannot be what labels a control on the overlay")
	}
	// Uncovered text beside a control is still its label.
	label, _, _, _ := fieldAndText("textbox", R(100, 100, 60, 24))
	if !label {
		t.Error("ordinary neighbouring text still labels a control")
	}
}

func TestControlsAreLabelledOnlyByTextInTheSameLayer(t *testing.T) {
	b := pb.New(1280, 800)
	for k := 0; k < 3; k++ {
		base := b.El(b.Body(), "p", R(0, float64(600+k*30), 500, 20))
		b.Text(base, "Ordinary text that forms the baseline of the page.")
	}
	outside := b.El(b.Body(), "p", R(200, 70, 150, 20))
	outsideText := b.Text(outside, "A brief history of React")
	dlg := b.El(b.Body(), "div", R(100, 40, 1000, 600), pb.Attr("role", "dialog"), pb.Position("fixed"))
	field := b.El(dlg, "input", R(200, 100, 100, 24))
	plain := b.El(b.Body(), "input", R(700, 500, 100, 24))
	plainText := b.Text(b.El(b.Body(), "span", R(700, 476, 60, 16), pb.Inline()), "Nickname")
	p := analyze(b)
	if !p.Nodes[dlg].Modal {
		t.Fatal("fixture: the dialog must be modal")
	}
	if got := p.Nodes[field].Label; got == outsideText {
		t.Error("text on the page behind a modal cannot label a control in it")
	}
	if got := p.Nodes[field].Label; got != snapshot.None {
		t.Errorf("a control in the modal with no text of its own stays unlabelled, got %d", got)
	}
	if got := p.Nodes[plain].Label; got != plainText {
		t.Errorf("ordinary controls keep their labels, got %d want %d", got, plainText)
	}
}

func TestHeadingTextIsNotAProximityLabel(t *testing.T) {
	for _, tag := range []string{"h1", "h4"} {
		headingLabelCase(t, tag)
	}
}

func headingLabelCase(t *testing.T, tag string) {
	b := pb.New(1280, 800)
	h := b.El(b.Body(), tag, R(200, 70, 100, 24))
	b.Text(h, "Playground")
	in := b.El(b.Body(), "input", R(200, 100, 100, 24), pb.Attr("placeholder", "Edit Field"))
	n := analyze(b).Nodes[in]
	if n.Label != snapshot.None {
		t.Errorf("%s: label = %d, want none", tag, n.Label)
	}
	if n.Name != "Edit Field" {
		t.Errorf("%s: name = %q", tag, n.Name)
	}
}

func TestTextInsideAHeadingIsNotAProximityLabel(t *testing.T) {
	b := pb.New(1280, 800)
	h := b.El(b.Body(), "h4", R(200, 70, 100, 24))
	sp := b.El(h, "span", R(200, 70, 100, 24), pb.Inline())
	b.Text(sp, "Playground")
	in := b.El(b.Body(), "input", R(200, 100, 100, 24), pb.Attr("placeholder", "Edit Field"))
	if got := analyze(b).Nodes[in].Label; got != snapshot.None {
		t.Errorf("label = %d, want none", got)
	}
}

func TestVisualHeadingLabelsAnUnnamedField(t *testing.T) {
	b := pb.New(1280, 800)
	lbl := b.El(b.Body(), "div", R(200, 70, 100, 24), pb.FontPx(19), pb.Style(snapshot.FontWeight, "700"))
	text := b.Text(lbl, "Email address")
	b.Text(b.El(b.Body(), "p", R(200, 300, 400, 20)), "Body copy in the ordinary size of the page.")
	in := b.El(b.Body(), "input", R(200, 100, 100, 24))
	p := analyze(b)
	if p.Nodes[lbl].Heading == 0 {
		t.Fatal("fixture: the bold larger label must read as a visual heading")
	}
	if p.Nodes[lbl].Role == "heading" {
		t.Fatal("fixture: a visual heading has no heading role")
	}
	if got := p.Nodes[in].Label; got != text {
		t.Errorf("label = %d, want %d", got, text)
	}
	if got := p.Nodes[in].Name; got != "Email address" {
		t.Errorf("name = %q", got)
	}
}

func TestHeadingQuestionLabelsAFieldWithNoOtherName(t *testing.T) {
	b := pb.New(1280, 800)
	h := b.El(b.Body(), "h3", R(200, 70, 200, 24))
	text := b.Text(h, "What is your name?")
	in := b.El(b.Body(), "input", R(200, 100, 100, 24))
	n := analyze(b).Nodes[in]
	if n.Label != text {
		t.Errorf("label = %d, want %d", n.Label, text)
	}
	if n.Name != "What is your name?" {
		t.Errorf("name = %q", n.Name)
	}
}

func TestRoleHeadingAboveANamedFieldIsNotItsLabel(t *testing.T) {
	b := pb.New(1280, 800)
	h := b.El(b.Body(), "div", R(200, 70, 100, 24), pb.Attr("role", "heading"))
	b.Text(h, "Playground")
	in := b.El(b.Body(), "input", R(200, 100, 100, 24), pb.Attr("title", "Edit Field"))
	n := analyze(b).Nodes[in]
	if n.Label != snapshot.None || n.Name != "Edit Field" {
		t.Errorf("label = %d name = %q", n.Label, n.Name)
	}
}

// A sentence above a field that already has a name is a message about the
// form, such as a login status, not the field's label: it changes with what
// the page says, and the field keeps meaning the same thing.
func TestASentenceAboveANamedFieldIsNotItsLabel(t *testing.T) {
	for _, c := range []struct {
		text string
		want bool
	}{
		{"Session saved.", false},
		{"Welcome back, Ada!", false},
		{"Forgot your password?", false},
		{"Username", true},
		{"Email:", true},
	} {
		b := pb.New(1280, 800)
		lbl := b.El(b.Body(), "label", R(200, 70, 200, 20), pb.Inline())
		lt := b.Text(lbl, c.text)
		in := b.El(b.Body(), "input", R(200, 100, 200, 24), pb.Attr("placeholder", "Account"))
		p := analyze(b)
		if got := p.Nodes[in].Label == lt; got != c.want {
			t.Errorf("%q above a named field: label = %v, want %v", c.text, got, c.want)
		}
		if p.Nodes[in].Name != "Account" {
			t.Errorf("%q: the field's own name changed to %q", c.text, p.Nodes[in].Name)
		}
	}
}
