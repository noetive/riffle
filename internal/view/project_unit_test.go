package view

import (
	"strings"
	"testing"
)

func TestSpaceBetweenKeepsPunctuationAttached(t *testing.T) {
	text := func(s string) *seg { return &seg{text: s} }
	link := &seg{link: &item{}}
	for _, c := range []struct {
		prev, next *seg
		want       bool
	}{
		{text("See"), link, true},
		{link, text(", then"), false},
		{link, text("and"), true},
		{text("("), link, false},
		{link, text(")"), false},
		{text("done"), text("!"), false},
		{text("a"), text("b"), true},
		{link, link, true},
	} {
		if got := spaceBetween(c.prev, c.next); got != c.want {
			t.Errorf("%+v then %+v: %v, want %v", c.prev, c.next, got, c.want)
		}
	}
}

func TestTableRowsLookThroughRowGroupsOnly(t *testing.T) {
	row := func() *item { return &item{head: "row"} }
	r1, r2, r3 := row(), row(), row()
	group := &item{head: "list", kids: []*item{r1, r2}}
	mixed := &item{head: "list", kids: []*item{row(), {head: "text"}}}
	empty := &item{head: "list"}
	got := tableRows(&item{head: "table", kids: []*item{group, r3, mixed, empty}})
	want := []*item{r1, r2, r3, mixed, empty}
	if len(got) != len(want) {
		t.Fatalf("%d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d is not the expected item", i)
		}
	}
	if !allRows([]*item{r1, r2}) || allRows([]*item{r1, {head: "text"}}) || !allRows(nil) {
		t.Error("allRows: only a list made wholly of rows is a row group")
	}
}

// An icon-font glyph is not a name to show, and not part of one.
func TestQuoteNameDropsNamesOfOnlyPrivateUseGlyphs(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"  ", ""},
		{"\uf013", ""},
		{" \uf013\ue001 ", ""},
		{"\U000f0001", ""},
		{"Go", `"Go"`},
		{"\uf013 Go", `"Go"`},
	} {
		if got := quoteName(c.in); got != c.want {
			t.Errorf("quoteName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCutPointBacksOffBeforeUnclosedMarkdown(t *testing.T) {
	text := "alpha beta gamma **bold words here** and [a link](/x) tail"
	for _, c := range []struct {
		at   int
		want string
	}{
		{len("alpha beta gamma **bold wo"), "alpha beta gamma"},
		{len("alpha beta gamma **bold words here** and [a li"), "alpha beta gamma **bold words here** and"},
		{len("alpha beta gamma **bold words here** and [a link]("), "alpha beta gamma **bold words here** and"},
		{len("alpha beta gamma **bold words here** and [a link](/x"), "alpha beta gamma **bold words here** and"},
		{len("alpha beta gamma **bold words here** and [a link](/x)"), "alpha beta gamma **bold words here** and [a link](/x)"},
		{len("alpha beta gamma **bold words here** an"), "alpha beta gamma **bold words here**"},
	} {
		body := []rune(text)
		if got := string(body[:cutPoint(body, c.at)]); got != c.want {
			t.Errorf("cut at %d: %q, want %q", c.at, got, c.want)
		}
	}
}

func TestOpenConstructFindsWhereTheLastUnclosedLinkOrEmphasisBegins(t *testing.T) {
	for in, want := range map[string]int{
		"plain text":         -1,
		"a [b](/c) d":        -1,
		"a [b](/c":           2,
		"a [b](":             2,
		"a [b]":              -1,
		"[1] and [b](/x":     8,
		"a [b":               2,
		"a ](x":              -1,
		"[a](/x) [b":         8,
		"[a](/x) [b](/y)":    -1,
		"[a] b](c":           -1,
		"[a (b)":             0,
		"[a)](/x":            0,
		"x **y":              2,
		"x **y**":            -1,
		"x *y":               2,
		"x *y*":              -1,
		"~~a":                0,
		"~~a~~ b":            -1,
		"***t***":            -1,
		"a **b ~~c":          2,
		"a *b **c":           2,
		"**[a](/x)** and *b": 16,
		"":                   -1,
		"*":                  0,
		"[":                  0,
		"]":                  -1,
	} {
		if got := openConstruct([]rune(in)); got != want {
			t.Errorf("openConstruct(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestCutPointPrefersASpaceAndKeepsALongWordWhole(t *testing.T) {
	long := strings.Repeat("a", 28) + " " + strings.Repeat("b", 20)
	for _, c := range []struct {
		name string
		text string
		at   int
		want int
	}{
		{"at a space", long, 28, 28},
		{"inside a word", long, 40, 28},
		{"next to a space", long, 29, 28},
		{"whole text", long, len(long), len(long)},
		{"space too early to keep", "ab " + strings.Repeat("c", 40), 30, 30},
		{"unbroken", strings.Repeat("d", 60), 40, 40},
		{"trailing spaces go", strings.Repeat("e", 30) + "   " + strings.Repeat("f", 5), 32, 30},
		{"space at the minimum", strings.Repeat("g", minCut) + " " + strings.Repeat("h", 10), minCut + 5, minCut},
		{"space just below the minimum", strings.Repeat("g", minCut-1) + " " + strings.Repeat("h", 10), minCut + 5, minCut + 5},
	} {
		body := []rune(c.text)
		if got := cutPoint(body, c.at); got != c.want {
			t.Errorf("%s: cutPoint = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestDiscardedRefIsGoneAndItsNumberIsNotReused(t *testing.T) {
	rt := newRefTable(nil)
	first := rt.get(10, "r")
	rt.discard(10)
	if _, ok := rt.lookup(10); ok {
		t.Error("a discarded ref is still assigned")
	}
	if _, ok := rt.resolve(first); ok {
		t.Error("a discarded ref still resolves")
	}
	if second := rt.get(11, "r"); second == first {
		t.Errorf("number %s was handed out twice", first)
	}
	rt.discard(99) // nothing was handed out for it
}
