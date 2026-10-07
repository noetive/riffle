// Package pagetext makes text taken from a web page safe to show an agent.
//
// A page controls every character of its text, its attributes and its
// addresses. Left as it is, that text can carry line breaks that start what
// looks like a new line of Riffle's own output, characters that render as
// nothing but still reach the model (zero-width and bidi controls, Unicode
// tag characters, variation selectors), and runs of blank lines that pad a
// reply. Everything a page wrote goes through Clean, or through Href for an
// address, before it is shown.
package pagetext

import (
	"strings"
	"unicode"
)

// Clean is s as one line of plain words: every run of whitespace, line
// breaks included, becomes a single space, leading and trailing whitespace
// is dropped, and characters that draw nothing, icon-font glyphs included, are removed.
func Clean(s string) string {
	if plain(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	space := false
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case blank(r):
			space = b.Len() > 0
		case invisible(r) && !joins(rs, i):
		default:
			if space {
				b.WriteByte(' ')
				space = false
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Href is an address from a page as one token: Clean, with the spaces that
// remain percent-encoded the way a browser sends them. An address with a
// space in it could otherwise end the token and start a fact tag of its own.
func Href(s string) string { return strings.ReplaceAll(Clean(s), " ", "%20") }

// Flatten keeps s on one line without changing its layout: line breaks
// become spaces, invisible characters are removed, tabs and runs of spaces
// stay. It is the last guard on a line of output, which may hold tabs
// between table cells.
func Flatten(s string) string {
	if flat(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case r == '\t':
			b.WriteRune(r)
		case lineBreak(r):
			b.WriteByte(' ')
		case invisible(r) && !joins(rs, i):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// blank reports whitespace, and the letters defined to draw as nothing.
func blank(r rune) bool {
	switch r {
	case '\u115f', '\u1160', '\u3164', '\uffa0': // Hangul fillers
		return true
	}
	return unicode.IsSpace(r)
}

// invisible reports characters that draw nothing as text and are not
// whitespace: controls, format characters (zero-width, bidi, tags), variation
// selectors, the other code points Unicode says to ignore, and private-use
// characters. Icon fonts draw their symbols from the private-use ranges, so
// those characters stand for a picture and carry no words; left in, they stick
// to the word beside them and break matching it. The marks that open an Arabic
// number or verse are format characters that do draw, and stay.
func invisible(r rune) bool {
	if unicode.Is(unicode.Prepended_Concatenation_Mark, r) {
		return false
	}
	return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Variation_Selector, unicode.Other_Default_Ignorable_Code_Point, unicode.Co)
}

// joins reports a zero-width joiner or non-joiner that changes how the
// letters or emoji on either side of it are written: Persian and Indic
// spelling depend on them, and so does an emoji family. Anywhere else, or
// in a run, it is invisible and dropped.
func joins(rs []rune, i int) bool {
	if rs[i] != '\u200c' && rs[i] != '\u200d' {
		return false
	}
	if i == 0 || i == len(rs)-1 {
		return false
	}
	return joinable(rs[i-1]) && joinable(rs[i+1])
}

// joining are the scripts whose spelling a joiner changes. In others, such
// as Latin, a joiner draws nothing and only splits a word for matching.
var joining = []*unicode.RangeTable{
	unicode.Arabic, unicode.Syriac, unicode.Nko, unicode.Mongolian,
	unicode.Devanagari, unicode.Bengali, unicode.Gurmukhi, unicode.Gujarati, unicode.Oriya,
	unicode.Tamil, unicode.Telugu, unicode.Kannada, unicode.Malayalam, unicode.Sinhala,
	unicode.So, unicode.Sk, // emoji and their modifiers
}

func joinable(r rune) bool { return unicode.In(r, joining...) }

func lineBreak(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', '\u0085', '\u2028', '\u2029':
		return true
	}
	return false
}

// plain reports printable ASCII with single spaces between words, which
// Clean returns as it is.
func plain(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c >= 0x7f {
			return false
		}
		if c == ' ' && (i == 0 || i == len(s)-1 || s[i-1] == ' ') {
			return false
		}
	}
	return true
}

// flat reports printable ASCII and tabs, which Flatten returns as it is.
func flat(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 0x20 && c != '\t') || c >= 0x7f {
			return false
		}
	}
	return true
}
