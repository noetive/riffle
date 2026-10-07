package view

import (
	"fmt"
	"slices"
	"sort"
	"unicode/utf8"

	"github.com/noetive/riffle/internal/snapshot"
)

func linesChars(lines []Line) int {
	n := 0
	for _, l := range lines {
		n += utf8.RuneCountInString(l.String()) + 1
	}
	return n
}

// fit trims lines to the token budget. Classes are dropped from the most
// expendable to the least; the first class that does not fit whole is kept
// as far as it goes. A run of dropped lines leaves one line with its count
// and the ref of the enclosing region. When even the modal layer does not
// fit, the tail is cut so the budget always holds.
func (c *compiler) fit(lines []Line) []Line {
	if c.opts.Budget <= 0 {
		return lines
	}
	limit := c.opts.Budget * 4
	if linesChars(lines) <= limit {
		return lines
	}
	var res []Line
	for t := classNone; t >= classFocus; t-- {
		res = c.elide(lines, func(_ int, l Line) bool { return l.class < t })
		if linesChars(res) <= limit {
			return c.refill(lines, t, limit)
		}
	}
	return cutTail(res, limit)
}

// refill starts from the lines of class below t, which fit, and adds back as
// many lines of class t as the budget allows: headings first, then the rest
// in reading order. Enclosing lines come along so the outline keeps its
// shape. Without this a whole class would go or stay, and a page whose
// content is all one class would be emptied to save a few tokens.
func (c *compiler) refill(lines []Line, t, limit int) []Line {
	var cand []int
	for i, l := range lines {
		if l.class == t {
			cand = append(cand, i)
		}
	}
	sort.SliceStable(cand, func(a, b int) bool { return lines[cand[a]].rank < lines[cand[b]].rank })
	build := func(n int, lines []Line) []Line {
		add := make([]bool, len(lines))
		for _, i := range cand[:n] {
			for j := i; j >= 0 && !add[j]; j = lines[j].parent {
				add[j] = true
			}
		}
		return c.elide(lines, func(i int, l Line) bool { return l.class < t || add[i] })
	}
	lo, hi := 0, len(cand)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if linesChars(build(mid, lines)) <= limit {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo < len(cand) {
		if cut, ok := c.cutToFit(lines, cand[lo], func(ls []Line) []Line { return build(lo+1, ls) }, limit); ok {
			return cut
		}
	}
	return build(lo, lines)
}

// cutToFit keeps the start of line i, cut to what is left of the budget and
// ending in the ref that expands it, when the line is longer than the whole
// budget and so can never be kept whole. Without it a page that is one long
// line would come back as a count of dropped lines and nothing to read. The
// cut falls between words and outside any link or emphasis the text opened;
// when that leaves too little to be worth keeping, no ref is handed out.
func (c *compiler) cutToFit(lines []Line, i int, build func([]Line) []Line, limit int) ([]Line, bool) {
	l := lines[i]
	if l.Body == "" || utf8.RuneCountInString(l.String())+1 <= limit {
		return nil, false
	}
	ref := l.Ref
	minted := false
	if ref == "" {
		key := l.expandKey()
		if !c.expandable(key) {
			return nil, false
		}
		_, had := c.rt.lookup(key)
		ref = c.rt.get(key, "r")
		minted = !had
	}
	body := []rune(l.Body)
	with := func(k int) []Line {
		cut := make([]Line, len(lines))
		copy(cut, lines)
		cut[i].Body = string(body[:k]) + "… " + ref
		cut[i].Label, cut[i].Tags = "", nil
		return build(cut)
	}
	lo, hi := 0, len(body)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if linesChars(with(mid)) <= limit {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	// Backing off only shortens the cut, so what fitted still fits.
	lo = cutPoint(body, lo)
	if lo < minCut || linesChars(with(lo)) > limit {
		if minted {
			c.rt.discard(l.expandKey())
		}
		return nil, false
	}
	return with(lo), true
}

// expandable: the backend id names an element, which is what expand resolves.
func (c *compiler) expandable(id int64) bool {
	if id == 0 {
		return false
	}
	n := c.s.ByBackend(id)
	return n != snapshot.None && c.s.Kind[n] == snapshot.KindElement
}

// cutPoint is how many runes of a line to keep when at most k fit: the cut is
// made at a space, where there is one in reach, and before any link or
// emphasis that the kept text opens and does not close.
func cutPoint(body []rune, k int) int {
	for {
		j := k
		if j < len(body) && body[j] != ' ' {
			s := j
			for s > 0 && body[s] != ' ' {
				s--
			}
			if s >= minCut { // an unbroken run longer than the room is cut where it is
				j = s
			}
		}
		for j > 0 && body[j-1] == ' ' {
			j--
		}
		open := openConstruct(body[:j])
		if open < 0 {
			return j
		}
		k = open
	}
}

// openConstruct is the index where the last link or emphasis in text begins
// and does not end, -1 when everything it opens is closed.
func openConstruct(text []rune) int {
	open := -1
	// A link is `[name](address)`; a bracket with no closing parenthesis after
	// its `](` is a link cut in two.
	for i := 0; i < len(text); i++ {
		if text[i] != '[' {
			continue
		}
		end := -1
		for j := i + 1; j+1 < len(text); j++ {
			if text[j] == ']' && text[j+1] == '(' {
				end = j + 2
				break
			}
			if text[j] == ']' {
				break
			}
		}
		if end < 0 {
			// No `](` yet: open only if no `]` closes it as plain brackets.
			if !slices.Contains(text[i:], ']') {
				open = i
			}
			continue
		}
		if !slices.Contains(text[end:], ')') {
			open = i
		}
	}
	// Emphasis opens and closes on the same mark: ** bold, * italic, ~~ strike.
	starts := map[string]int{}
	for i := 0; i < len(text); {
		mark := ""
		switch {
		case text[i] == '*' && i+1 < len(text) && text[i+1] == '*':
			mark = "**"
		case text[i] == '~' && i+1 < len(text) && text[i+1] == '~':
			mark = "~~"
		case text[i] == '*':
			mark = "*"
		default:
			i++
			continue
		}
		if _, on := starts[mark]; on {
			delete(starts, mark)
		} else {
			starts[mark] = i
		}
		i += len(mark)
	}
	for _, at := range starts {
		if open < 0 || at < open {
			open = at
		}
	}
	return open
}

// minCut is the fewest characters of a line worth keeping: a shorter stub
// says less than the count of lines it replaces.
const minCut = 24

// elide drops every line the predicate does not keep.
func (c *compiler) elide(lines []Line, kept func(i int, l Line) bool) []Line {
	keep := func(i int) bool { return kept(i, lines[i]) }
	newIdx := make([]int, len(lines))
	var out []Line
	for i := 0; i < len(lines); {
		if keep(i) {
			l := lines[i]
			if l.parent >= 0 {
				l.parent = newIdx[l.parent]
			}
			newIdx[i] = len(out)
			out = append(out, l)
			i++
			continue
		}
		j := i
		for j < len(lines) && !keep(j) {
			j++
		}
		first := lines[i]
		ref := ""
		switch {
		case first.parent >= 0 && keep(first.parent) && c.refers(lines[first.parent]):
			ref = c.refOf(lines[first.parent])
		case c.refers(first):
			ref = c.refOf(first)
		}
		noun := "lines"
		if j-i == 1 {
			noun = "line"
		}
		body := fmt.Sprintf("%d more %s", j-i, noun)
		if ref != "" {
			body += " " + ref
		}
		l := Line{Indent: first.Indent, Label: "…", Body: body, class: classModal, parent: -1}
		if first.parent >= 0 && keep(first.parent) {
			l.parent = newIdx[first.parent]
		}
		out = append(out, l)
		i = j
	}
	return out
}

// refers: the line names something expand can open, or already has a ref.
func (c *compiler) refers(l Line) bool { return l.Ref != "" || c.expandable(l.expandKey()) }

// refOf is the ref of a line, handed out on first use.
func (c *compiler) refOf(l Line) string {
	if l.Ref != "" {
		return l.Ref
	}
	return c.rt.get(l.expandKey(), "r")
}

// cutTail keeps whole lines from the front while they fit and cuts the one
// that straddles the limit.
func cutTail(lines []Line, limit int) []Line {
	var out []Line
	left := limit
	for _, l := range lines {
		s := l.String()
		n := utf8.RuneCountInString(s) + 1
		if n <= left {
			out = append(out, l)
			left -= n
			continue
		}
		if left > 1 {
			r := []rune(s)
			out = append(out, Line{Body: string(r[:left-1]), class: classModal, parent: -1})
		}
		break
	}
	return out
}
