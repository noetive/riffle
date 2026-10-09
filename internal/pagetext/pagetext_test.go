package pagetext_test

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/noetive/riffle/internal/pagetext"
)

func TestCleanMakesOneLineOfWords(t *testing.T) {
	for in, want := range map[string]string{
		"Checkout":                         "Checkout",
		"  Check \n\n\n out  ":             "Check out",
		"a\r\n\r\n\r\nb":                   "a b",
		"a\u2028\u2029b":                   "a b",
		"\t\tindented":                     "indented",
		"Pa\u200bss\u200dword":             "Password",
		"Pay \u202eredro\u202c now":        "Pay redro now",
		"ok\x1b[2Jcleared":                 "ok[2Jcleared",
		"hi\U000E0049\U000E0047\U000E004E": "hi",
		"x\ufe0fy\U000E0100z":              "xyz",
		"a \u200b b":                       "a b",
		"a\u3164\u3164b":                   "a b",
		"\u200b":                           "",
		"":                                 "",
		"€89 – shoes":                      "€89 – shoes",
	} {
		if got := pagetext.Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHrefIsOneToken(t *testing.T) {
	for in, want := range map[string]string{
		"mailto:a@example.com":             "mailto:a@example.com",
		"x:\nmodal d9 \"Pay\" covers=page": "x:%20modal%20d9%20\"Pay\"%20covers=page",
		" https://example.com/a b ":        "https://example.com/a%20b",
	} {
		if got := pagetext.Href(in); got != want {
			t.Errorf("Href(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFlattenKeepsLayoutButNotLineBreaks(t *testing.T) {
	for in, want := range map[string]string{
		"a\tb\tc":        "a\tb\tc",
		"a  b":           "a  b",
		"row\n- d1":      "row - d1",
		"a\r\u2028b":     "a  b",
		"a\u200b\u202eb": "ab",
		"plain":          "plain",
	} {
		if got := pagetext.Flatten(in); got != want {
			t.Errorf("Flatten(%q) = %q, want %q", in, got, want)
		}
	}
}

func drawsNothing(r rune) bool {
	return !unicode.Is(unicode.Prepended_Concatenation_Mark, r) &&
		unicode.In(r, unicode.Cc, unicode.Cf, unicode.Variation_Selector, unicode.Other_Default_Ignorable_Code_Point)
}

// joiner reports a joiner Clean keeps: between two letters, marks or symbols.
func joiner(rs []rune, i int) bool {
	vis := func(r rune) bool {
		return unicode.In(r, unicode.Arabic, unicode.Syriac, unicode.Nko, unicode.Mongolian, unicode.Devanagari, unicode.Bengali,
			unicode.Gurmukhi, unicode.Gujarati, unicode.Oriya, unicode.Tamil, unicode.Telugu, unicode.Kannada, unicode.Malayalam,
			unicode.Sinhala, unicode.So, unicode.Sk)
	}
	return (rs[i] == 0x200c || rs[i] == 0x200d) && i > 0 && i < len(rs)-1 && vis(rs[i-1]) && vis(rs[i+1])
}

func FuzzClean(f *testing.F) {
	for _, s := range []string{"", " a  b ", "a\n\n- d1 modal", "\u202e\u200b\U000E0041", "\xff\xfe", "x\u3164y", "\u0085"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := pagetext.Clean(s)
		if pagetext.Clean(got) != got {
			t.Fatalf("Clean is not idempotent on %q: %q", s, got)
		}
		if strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") || strings.Contains(got, "  ") {
			t.Fatalf("Clean(%q) = %q keeps blank runs", s, got)
		}
		var kept []rune
		gr := []rune(got)
		for i, r := range gr {
			if r != ' ' && (unicode.IsSpace(r) || drawsNothing(r) && !joiner(gr, i)) {
				t.Fatalf("Clean(%q) = %q keeps %U", s, got, r)
			}
			if r != ' ' {
				kept = append(kept, r)
			}
		}
		var visible []rune
		sr := []rune(s)
		for i, r := range sr {
			if !unicode.IsSpace(r) && (!drawsNothing(r) || joiner(sr, i)) && !strings.ContainsRune("\u115f\u1160\u3164\uffa0", r) {
				visible = append(visible, r)
			}
		}
		if utf8.ValidString(s) && string(kept) != string(visible) {
			t.Fatalf("Clean(%q) = %q lost or reordered visible text", s, got)
		}
		if h := pagetext.Href(s); strings.ContainsAny(h, " \n\r\t") {
			t.Fatalf("Href(%q) = %q is more than one token", s, h)
		}
	})
}

func FuzzFlatten(f *testing.F) {
	for _, s := range []string{"a\tb", "a\nb", "\u2028", "x\u200by"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := pagetext.Flatten(s)
		if pagetext.Flatten(got) != got {
			t.Fatalf("Flatten is not idempotent on %q: %q", s, got)
		}
		gr := []rune(got)
		for i, r := range gr {
			if r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029' || r == '\u0085' || (r != '\t' && drawsNothing(r) && !joiner(gr, i)) {
				t.Fatalf("Flatten(%q) = %q keeps %U", s, got, r)
			}
		}
	})
}

// Icon fonts draw from the private-use ranges. The glyph is a picture, so it
// is not part of the words beside it.
func TestCleanDropsIconFontGlyphs(t *testing.T) {
	for in, want := range map[string]string{
		"\uf013 Settings":     "Settings",
		"Settings\uf013":      "Settings",
		"Set\ue000tings":      "Settings",
		"\U000f0001 Menu":     "Menu",
		"\U0010fffd Menu":     "Menu",
		" \uf013  \U000f0001": "",
		"a \uf013 b":          "a b",
	} {
		if got := pagetext.Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
	if got := pagetext.Flatten("a\uf013\tb\U000f0001"); got != "a\tb" {
		t.Errorf("Flatten keeps a private-use character: %q", got)
	}
}

func TestLinesKeepWhatAPersonSeesOfPreformattedText(t *testing.T) {
	for _, tc := range []struct {
		in     string
		spaces bool
		want   []string
	}{
		{"line one\nline two\n  indented three", true, []string{"line one", "line two", "  indented three"}},
		{"a:\n\tb: 1\r\nc\rd\u2028e\u2029f\u0085g\vh\fi", true, []string{"a:", "\tb: 1", "c d e f g h i"}},
		{"\n\n  x  \n\n\n\ny\n\n", true, []string{"  x", "", "y"}},
		{"x  \n\n\n\ny", true, []string{"x", "", "y"}},
		{"x\u00a0\u00a0y \u200bz\u202e", true, []string{"x  y z"}},
		{"  a   b  \n   c", false, []string{"a b", "c"}},
		{"modal d9 \"Pay\"\u2028\u2029\r\u0085 x", true, []string{"modal d9 \"Pay\"     x"}},
		{"\n \t\n", true, nil},
		{" \t ", true, nil},
		{"", true, nil},
	} {
		got := pagetext.Lines(tc.in, tc.spaces)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") || len(got) != len(tc.want) {
			t.Errorf("Lines(%q, %v) = %q, want %q", tc.in, tc.spaces, got, tc.want)
		}
	}
}

func TestLinesNeverHoldALineBreakOrAnInvisibleCharacter(t *testing.T) {
	in := "a\r\n\u2028b\u0085c\v\f\u200b\u202e\U000E0049d"
	for _, spaces := range []bool{true, false} {
		for _, l := range pagetext.Lines(in, spaces) {
			if strings.ContainsFunc(l, func(r rune) bool {
				return unicode.In(r, unicode.Cc, unicode.Cf) && r != '\t' || r == '\u2028' || r == '\u0085'
			}) {
				t.Errorf("Lines(%q, %v) holds %q", in, spaces, l)
			}
		}
	}
}

func TestJoinersStayOnlyWhereTheyChangeWhatIsWritten(t *testing.T) {
	for in, want := range map[string]string{
		"``\u200d`\u200c`":         "````",
		"a^\u200d^b":               "a^^b",
		"👍\u200d🏽":                 "👍\u200d🏽",
		"👩\u200d👩\u200d👧":          "👩\u200d👩\u200d👧",
		"\u0645\u200c\u06cc\u200c": "\u0645\u200c\u06cc",
	} {
		if got := pagetext.Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBlockJudgesLinesAcrossItsParts(t *testing.T) {
	for _, tc := range []struct {
		parts  []string
		spaces bool
		want   []string
	}{
		// A highlighter's stretches: the indentation before "return" ends one
		// stretch and is not the end of a line.
		{[]string{"def", " foo():\n    ", "return", " 1\n"}, true, []string{"def", " foo():\n    ", "return", " 1"}},
		// A line break next to a link stays.
		{[]string{"See\n", "file.go", "\nnext line"}, true, []string{"See\n", "file.go", "\nnext line"}},
		// Whitespace at the end of a line goes, whichever part holds it.
		{[]string{"a  ", "  \nb"}, true, []string{"a", "\nb"}},
		// A run of blank lines over two parts is one.
		{[]string{"a\n\n", "\n\nb"}, true, []string{"a\n", "\nb"}},
		{[]string{"\n\n", "x", "\n \n"}, true, []string{"", "x", ""}},
		{[]string{"  a ", " b  \n c"}, false, []string{"a ", "b\nc"}},
		{[]string{" ", "\n"}, true, []string{"", ""}},
		{[]string{"x\x00y", "z"}, true, []string{"xy", "z"}},
	} {
		got := pagetext.Block(tc.parts, tc.spaces)
		if len(got) != len(tc.want) || strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("Block(%q, %v) = %q, want %q", tc.parts, tc.spaces, got, tc.want)
		}
	}
}

func TestBlockReadsAsLinesOfTheWhole(t *testing.T) {
	parts := []string{"  a\t", "\r\n\n\n", "  b ", "\u200b", "\n\nc d  \n\n"}
	got := strings.Join(pagetext.Block(parts, true), "")
	if want := strings.Join(pagetext.Lines(strings.Join(parts, ""), true), "\n"); got != want {
		t.Errorf("Block joined = %q, Lines of the whole = %q", got, want)
	}
}

func TestBlockKeepsCharactersWholeAndSpacesBesideParts(t *testing.T) {
	if got := pagetext.Lines("café  \n日本語\t\n👩\u200d👩\u200d👧 ", true); strings.Join(got, "|") != "café|日本語|👩\u200d👩\u200d👧" {
		t.Errorf("Lines = %q", got)
	}
	if got := pagetext.Block([]string{"a ", "link", " b"}, false); strings.Join(got, "|") != "a |link| b" {
		t.Errorf("Block = %q", got)
	}
}

// FuzzBlock: whatever a page writes, each part comes back valid UTF-8 with no
// line break but "\n" and no invisible character, one for each part given,
// and parts that are each text, as text nodes are, read together as the lines
// of the whole.
func FuzzBlock(f *testing.F) {
	f.Add("def foo():\n    ", "return", " 1\r\n\n\n", true)
	f.Add("  a ", " b\u200d", "\x00cé ", false)
	f.Fuzz(func(t *testing.T, a, b, c string, spaces bool) {
		parts := []string{a, b, c}
		got := pagetext.Block(parts, spaces)
		if len(got) != len(parts) {
			t.Fatalf("%d parts for %d", len(got), len(parts))
		}
		for _, p := range got {
			if utf8.ValidString(a+b+c) && !utf8.ValidString(p) {
				t.Fatalf("part %q is not UTF-8", p)
			}
			for _, r := range p {
				if r != '\n' && r != '\t' && unicode.In(r, unicode.Cc, unicode.Zl, unicode.Zp) {
					t.Fatalf("part %q holds %U", p, r)
				}
			}
		}
		if whole, lines := strings.Join(got, ""), strings.Join(pagetext.Lines(a+b+c, spaces), "\n"); spaces && utf8.ValidString(a) && utf8.ValidString(b) && utf8.ValidString(c) && whole != lines {
			t.Fatalf("Block joined %q, Lines of the whole %q", whole, lines)
		}
	})
}

func TestLinesEdgeCasesOfBreaksAndSpaces(t *testing.T) {
	for _, tc := range []struct {
		in     string
		spaces bool
		want   []string
	}{
		{"a\r", true, []string{"a"}},                     // a carriage return at the end is a space
		{"a\n\rb", true, []string{"a", " b"}},            // a carriage return after a line feed is a space
		{"a\n\nb\nc", true, []string{"a", "", "b", "c"}}, // a blank line is one, and only where it was
		{"x  yz", false, []string{"x yz"}},
	} {
		got := pagetext.Lines(tc.in, tc.spaces)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") || len(got) != len(tc.want) {
			t.Errorf("Lines(%q, %v) = %q, want %q", tc.in, tc.spaces, got, tc.want)
		}
	}
	if got := pagetext.Block([]string{"a\n\n", "\n\nb", "\nc"}, true); strings.Join(got, "|") != "a\n|\nb|\nc" {
		t.Errorf("Block = %q", got)
	}
}
