package view_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/noetive/riffle/internal/view"
)

func lines(ls ...view.Line) *view.View { return &view.View{Lines: ls} }

func TestDiffReportsRemovedBeforeAddedBeforeChanged(t *testing.T) {
	old := lines(
		view.Line{Key: 1, Ref: "b1", Label: "button b1", Body: `"Go"`},
		view.Line{Key: 2, Ref: "b2", Label: "button b2", Body: `"Old"`},
		view.Line{Key: 3, Ref: "b3", Label: "button b3", Body: `"Keep"`},
	)
	cur := lines(
		view.Line{Key: 4, Ref: "b4", Label: "button b4", Body: `"New"`},
		view.Line{Key: 1, Ref: "b1", Label: "button b1", Body: `"Went"`},
		view.Line{Key: 3, Ref: "b3", Label: "button b3", Body: `"Keep"`},
	)
	d := view.Diff(old, cur)
	want := "- b2\n+ button b4 \"New\"\n~ button b1 \"Went\""
	if d.String() != want {
		t.Errorf("delta:\n%s\nwant\n%s", d, want)
	}
	if d.Empty() {
		t.Error("a delta with lines is not empty")
	}
	if got := view.Diff(old, old); !got.Empty() || got.String() != "" {
		t.Errorf("same view must give an empty delta, got %q", got)
	}
}

func TestDiffNamesRemovedNodeWithoutRefByLabelAndText(t *testing.T) {
	old := lines(view.Line{Key: 7, Label: "h2", Body: `"Title"`}, view.Line{Key: 8, Label: "text"})
	d := view.Diff(old, lines())
	if want := "- h2 \"Title\"\n- text"; d.String() != want {
		t.Errorf("got %q want %q", d, want)
	}
}

func TestDiffTagChangesListAddedThenRemovedTags(t *testing.T) {
	old := lines(view.Line{Key: 1, Ref: "b1", Label: "button b1", Tags: []string{"primary", "red", "strike"}})
	cur := lines(view.Line{Key: 1, Ref: "b1", Label: "button b1", Tags: []string{"red", "disabled", "checked"}})
	if got, want := view.Diff(old, cur).String(), "~ b1 +disabled +checked -primary -strike"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	noRef := lines(view.Line{Key: 1, Label: "text", Body: `"x"`, Tags: []string{"red"}})
	noRef2 := lines(view.Line{Key: 1, Label: "text", Body: `"x"`})
	if got, want := view.Diff(noRef, noRef2).String(), `~ text "x" -red`; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestDiffMatchesNodesByKeyNotByPosition(t *testing.T) {
	a := view.Line{Key: 1, Ref: "b1", Label: "button b1", Body: `"A"`}
	b := view.Line{Key: 2, Ref: "b2", Label: "button b2", Body: `"B"`}
	if d := view.Diff(lines(a, b), lines(b, a)); !d.Empty() {
		t.Errorf("reordering alone is no change: %s", d)
	}
	// A renumbered ref with the same text is a change of that line.
	renum := view.Line{Key: 1, Ref: "b9", Label: "button b9", Body: `"A"`}
	if got, want := view.Diff(lines(a), lines(renum)).String(), `~ button b9 "A"`; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestDiffUsesFirstLineOfARepeatedKey(t *testing.T) {
	first := view.Line{Key: 1, Label: "text", Body: `"one"`}
	second := view.Line{Key: 1, Label: "text", Body: `"two"`}
	// Only the first line of a key counts, on both sides.
	if d := view.Diff(lines(first, second), lines(first)); !d.Empty() {
		t.Errorf("second line of a key must be ignored: %s", d)
	}
	if d := view.Diff(lines(second, first), lines(second)); !d.Empty() {
		t.Errorf("first duplicate wins: %s", d)
	}
}

func TestDiffCountsLinesWithoutNodeByText(t *testing.T) {
	more := view.Line{Label: "…", Body: "3 more lines"}
	page := view.Line{Label: "page", Body: "x 1x1"}
	// Two of the same text to one: one removal.
	if got, want := view.Diff(lines(page, more, more), lines(page, more)).String(), "- … 3 more lines"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	// One to two: one addition.
	if got, want := view.Diff(lines(page, more), lines(page, more, more)).String(), "+ … 3 more lines"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	// Two to two: nothing.
	if d := view.Diff(lines(more, more), lines(more, more)); !d.Empty() {
		t.Errorf("unexpected: %s", d)
	}
	// Three to zero: three removals, and zero to three: three additions.
	if got := view.Diff(lines(more, more, more), lines()); len(got.Lines) != 3 {
		t.Errorf("want 3 removals: %v", got.Lines)
	}
	if got := view.Diff(lines(), lines(more, more, more)); len(got.Lines) != 3 {
		t.Errorf("want 3 additions: %v", got.Lines)
	}
	// Differently placed same text: no change.
	if d := view.Diff(lines(page, more), lines(more, page)); !d.Empty() {
		t.Errorf("order of node-less lines is not a change: %s", d)
	}
}

func TestDiffBodyChangeWithoutTagChangeIsOneLine(t *testing.T) {
	old := lines(view.Line{Key: 1, Label: "item", Body: `"a"`, Tags: []string{"red"}})
	cur := lines(view.Line{Key: 1, Label: "item", Body: `"b"`, Tags: []string{"green"}})
	if got, want := view.Diff(old, cur).String(), `~ item "b" green`; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	lab := lines(view.Line{Key: 1, Label: "item x2", Body: `"a"`})
	lab2 := lines(view.Line{Key: 1, Label: "item x3", Body: `"a"`})
	if got, want := view.Diff(lab, lab2).String(), `~ item x3 "a"`; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestADeltaIsCutToTheBudgetAndSaysWhatItLeftOut(t *testing.T) {
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, fmt.Sprintf("+ item %d with a fairly long description of what it is", i))
	}
	d := view.Delta{Lines: lines}
	cut := d.Fit(100) // about 400 characters
	if len(cut.Lines) >= len(lines) || len(cut.Lines) < 3 {
		t.Fatalf("%d of %d lines kept", len(cut.Lines), len(lines))
	}
	last := cut.Lines[len(cut.Lines)-1]
	if !strings.Contains(last, "more changes") || !strings.Contains(last, "view outline") {
		t.Errorf("the cut must be announced with what to do next: %q", last)
	}
	kept := len(cut.Lines) - 1
	if want := fmt.Sprintf("%d more changes", len(lines)-kept); !strings.Contains(last, want) {
		t.Errorf("the note must count what was left out (%s): %q", want, last)
	}
	if len(strings.Join(cut.Lines, "\n")) > 100*4+200 {
		t.Error("the cut delta is still over budget")
	}
	if whole := d.Fit(0); len(whole.Lines) != len(lines) {
		t.Error("no budget keeps every change")
	}
	small := view.Delta{Lines: lines[:2]}
	if got := small.Fit(100); len(got.Lines) != 2 {
		t.Errorf("a delta within budget is untouched: %v", got.Lines)
	}
}
