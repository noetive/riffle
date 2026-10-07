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
