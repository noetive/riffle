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

// Lines is s as the lines a person sees when its white-space keeps line
// breaks; see Block. Text of only whitespace has none.
func Lines(s string, spaces bool) []string {
	b := Block([]string{s}, spaces)[0]
	if b == "" {
		return nil
	}
	return strings.Split(b, "\n")
}

// Block is the parts of one run of text whose white-space keeps line breaks
// (CSS Text 3, section 4.1), such as the styled stretches and links of a
// paragraph, cleaned as one: each part keeps what is left of it, and only
// the lines it holds a share of are judged, never the part alone. A line
// feed, or a carriage return and line feed, ends a line; every other
// whitespace character is a space, the carriage return, form feed and
// Unicode line separators included, as a browser draws them. Invisible
// characters are removed as Clean removes them and whitespace at the end of a
// line is dropped. With spaces set, as for white-space: pre, spaces and tabs
// stay as they are written; without, as for pre-line, a run of them is one
// space and none starts or ends a line. Blank lines before and after the text
// are dropped and a run of them inside it is one, so a page cannot pad a
// reply. A part holds "\n" between lines and no other line break.
func Block(parts []string, spaces bool) []string {
	kept := make([]string, len(parts))
	for i, p := range parts {
		kept[i] = keep(p, spaces)
	}
	var res []string
	var pending strings.Builder // part boundaries from dropped blank lines
	gap := false
	for l := range strings.SplitSeq(strings.Join(kept, string(boundary)), "\n") {
		l = trimLine(l, spaces)
		if strings.Trim(l, string(boundary)) == "" {
			pending.WriteString(l)
			gap = len(res) > 0
			continue
		}
		if gap {
			res = append(res, pending.String())
			pending.Reset()
			gap = false
		}
		res = append(res, pending.String()+l)
		pending.Reset()
	}
	if len(res) == 0 {
		res = []string{""}
	}
	res[len(res)-1] += pending.String()
	return strings.Split(strings.Join(res, "\n"), string(boundary))
}

// boundary marks where one part of a Block ends and the next begins. It is a
// control character, which keep removes from page text, so a page cannot
// write one.
const boundary = '\x00'

// keep is page text with line feeds as its only line breaks, every other
// whitespace character a space (tabs kept when spaces is set) and invisible
// characters removed.
func keep(s string, spaces bool) string {
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case r == '\n':
			b.WriteByte('\n')
		case r == '\r' && i+1 < len(rs) && rs[i+1] == '\n':
		case r == '\t' && spaces:
			b.WriteByte('\t')
		case blank(r):
			b.WriteByte(' ')
		case invisible(r) && !joins(rs, i):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// trimLine drops the whitespace at the end of a line of a Block and, without
// spaces, collapses each run of it to one space and drops it at the start
// too. Part boundaries stay where they are.
func trimLine(l string, spaces bool) string {
	var b strings.Builder
	// space: a run of whitespace waits to be one space; spaced: that space is
	// written, before a part boundary, and the run goes on after it.
	space, spaced, text := false, false, false
	for _, r := range l {
		switch r {
		case boundary:
			if space {
				// The space ends the part before: the part after may be a
				// link, whose own text is kept elsewhere.
				b.WriteByte(' ')
				space, spaced = false, true
			}
			b.WriteRune(r)
		case ' ', '\t':
			if spaces {
				b.WriteRune(r)
			} else {
				space = text && !spaced
			}
		default:
			if space {
				b.WriteByte(' ')
				space = false
			}
			b.WriteRune(r)
			text, spaced = true, false
		}
	}
	// Whitespace after the last character of the line goes; the boundaries
	// among it stay.
	s := b.String()
	head := strings.TrimRightFunc(s, func(r rune) bool { return r == ' ' || r == '\t' || r == boundary })
	return head + strings.Map(func(r rune) rune {
		if r == boundary {
			return r
		}
		return -1
	}, s[len(head):])
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
	unicode.So, emojiModifiers, // emoji and their skin tones
}

// emojiModifiers are the skin tones (Unicode 8.0, Fitzpatrick modifiers). The
// rest of their category, Sk, is marks such as the backtick and caret, which a
// joiner does not change.
var emojiModifiers = &unicode.RangeTable{R32: []unicode.Range32{{Lo: 0x1F3FB, Hi: 0x1F3FF, Stride: 1}}}

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
