package view

import (
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/noetive/riffle/internal/snapshot"
)

func clip(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}

func quoteText(s string, max int) string { return strconv.Quote(clip(words(s), max)) }

// quoteName quotes a name for display, "" when there is none. Icon-font
// glyphs are removed from page text by words, so a name of only glyphs is none.
func quoteName(s string) string {
	if words(s) == "" {
		return ""
	}
	return quoteText(s, 80)
}

func (c *compiler) header() Line {
	s := c.s
	where := s.URL
	if u, err := url.Parse(s.URL); err == nil && u.Host != "" {
		where = u.Host + u.EscapedPath()
		if u.RawQuery != "" {
			where += "?" + u.RawQuery
		}
	}
	body := where
	if s.Title != "" {
		body += " " + quoteText(s.Title, 80)
	}
	body += fmt.Sprintf(" %dx%d", int(s.ViewportW), int(s.ViewportH))
	l := Line{Label: "page", Body: body, class: classModal, parent: -1}
	if room := s.ContentH - s.ViewportH; room > 1 {
		l.Tags = append(l.Tags, fmt.Sprintf("scroll %d/%d", int(s.ScrollY), int(room)))
	}
	return l
}

func (c *compiler) unseenLine() []Line {
	n := c.p.UnseenCount
	if n == 0 {
		return nil
	}
	noun := "nodes"
	if n == 1 {
		noun = "node"
	}
	return []Line{{Label: "unseen", Body: fmt.Sprintf("%d %s", n, noun), class: classModal, parent: -1}}
}

// tagsOf lists the fact tags of an item. The cover tag is left out when the
// enclosing line is covered by the same node.
func (c *compiler) tagsOf(it *item, parentCover int32) []string {
	tags := append([]string(nil), it.tags...)
	if it.cover != snapshot.None && it.cover != parentCover {
		if ref, ok := c.rt.lookup(c.s.Backend[it.cover]); ok {
			tags = append(tags, "covered-by="+ref)
		} else {
			tags = append(tags, "covered")
		}
	}
	if c.opts.Focus != 0 && it.key == c.opts.Focus {
		tags = append(tags, "focused")
	}
	return tags
}

func (c *compiler) label(it *item) string {
	l := it.head
	if it.ref != "" {
		l += " " + it.ref
	}
	if it.repeat > 0 && !it.collapsed {
		l += " x" + strconv.Itoa(it.repeat)
	}
	return l
}

// textLimit is how many characters of a text run an outline line shows. Expand
// is how the rest of a clipped or cut run is read, so it shows all of it, within
// the budget.
func (c *compiler) textLimit() int {
	if c.opts.Projection == Expand {
		return math.MaxInt
	}
	return 200
}

// segParts renders the stretches of a text run.
func (c *compiler) segParts(it *item, parentCover int32) []string {
	var parts []string
	for _, s := range it.segs {
		if s.link != nil {
			parts = append(parts, c.inline(s.link, parentCover))
			continue
		}
		parts = append(parts, quoteText(s.text, c.textLimit()))
		// Text cut to the line limit is followed by the ref that expand opens,
		// as text cut to a budget is.
		if utf8.RuneCountInString(words(s.text)) > c.textLimit() {
			if key := it.block; key != 0 && c.expandable(key) {
				parts = append(parts, c.rt.get(key, "r"))
			}
		}
		parts = append(parts, s.tags...)
	}
	return parts
}

// inline renders a leaf on one line without its indentation, as used for
// cells of a row and parts of a sentence.
func (c *compiler) inline(it *item, parentCover int32) string {
	tags := c.tagsOf(it, parentCover)
	if it.textRun {
		return strings.Join(append(c.segParts(it, parentCover), tags...), " ")
	}
	parts := []string{c.label(it)}
	if it.name != "" {
		parts = append(parts, it.name)
	}
	return strings.Join(append(parts, tags...), " ")
}

func (c *compiler) linesFor(it *item, indent, parent int, parentCover int32) Line {
	l := Line{Key: it.key, anchor: it.block, Ref: it.ref, Indent: indent, Label: c.label(it), Body: it.name, Tags: c.tagsOf(it, parentCover), class: it.class, parent: parent}
	l.rank = 1
	if it.heading > 0 || (it.collapsed && it.head == "nav") {
		l.rank = 0
	}
	if it.textRun {
		l.Label = "text"
		l.Body = strings.Join(c.segParts(it, parentCover), " ")
	}
	return l
}

func flat(it *item) bool {
	if (it.head != "item" && it.head != "row") || len(it.kids) == 0 || it.collapsed {
		return false
	}
	for _, k := range it.kids {
		if !k.leaf {
			return false
		}
	}
	return true
}

func countLinks(it *item) (links, nodes int) {
	for _, k := range it.kids {
		if k.kind == "a" {
			links++
		}
		if k.leaf {
			nodes++
		}
		l, n := countLinks(k)
		links, nodes = links+l, nodes+n
	}
	return
}

// emit renders an item and its kids, one line each; rows of leaves share
// one line, collapsed regions shrink to a count unless expand is set.
func (c *compiler) emit(out *[]Line, it *item, indent, parent int, parentCover int32, expand bool) {
	switch {
	case it.head == "":
		for _, k := range it.kids {
			c.emit(out, k, indent, parent, parentCover, expand)
		}
	case it.collapsed && !expand:
		l := c.linesFor(it, indent, parent, parentCover)
		links, nodes := countLinks(it)
		switch {
		case links > 0:
			l.Body = strings.TrimSpace(l.Body + fmt.Sprintf(" collapsed %d links", links))
		case nodes > 0:
			l.Body = strings.TrimSpace(l.Body + fmt.Sprintf(" collapsed %d nodes", nodes))
		default:
			l.Body = strings.TrimSpace(l.Body + " collapsed")
		}
		*out = append(*out, l)
	case it.leaf:
		*out = append(*out, c.linesFor(it, indent, parent, parentCover))
	case flat(it):
		l := c.linesFor(it, indent, parent, parentCover)
		var cells []string
		for _, k := range it.kids {
			cells = append(cells, c.inline(k, it.cover))
		}
		l.Body = strings.TrimSpace(l.Body + " " + strings.Join(cells, " | "))
		*out = append(*out, l)
	default:
		if len(it.kids) == 0 && it.ref == "" && it.name == "" {
			return
		}
		*out = append(*out, c.linesFor(it, indent, parent, parentCover))
		idx := len(*out) - 1
		for _, k := range it.kids {
			c.emit(out, k, indent+1, idx, it.cover, expand)
		}
	}
}

func (c *compiler) outline(main, modals []*item) []Line {
	lines := []Line{c.header()}
	for _, m := range modals {
		c.emit(&lines, m, 0, -1, snapshot.None, false)
	}
	for _, it := range main {
		c.emit(&lines, it, 0, -1, snapshot.None, false)
	}
	return append(lines, c.unseenLine()...)
}

// walkAll visits every item, kids of collapsed regions included.
func walkAll(items []*item, fn func(*item)) {
	for _, it := range items {
		fn(it)
		for _, s := range it.segs {
			if s.link != nil {
				fn(s.link)
			}
		}
		walkAll(it.kids, fn)
	}
}

func (c *compiler) interactive(main, modals []*item) []Line {
	lines := []Line{c.header()}
	n := 0
	walkAll(append(append([]*item{}, modals...), main...), func(it *item) {
		// Frames are listed too: their content is not shown, and a login or
		// payment form may sit inside one. They have no ref and
		// are not counted as actionable.
		if it.kind != "a" && it.kind != "b" && it.kind != "f" && it.head != "frame" {
			return
		}
		if it.head != "frame" {
			n++
		}
		lines = append(lines, Line{Key: it.key, Ref: it.ref, Label: c.label(it), Body: it.name, Tags: c.tagsOf(it, snapshot.None), class: it.class, parent: -1})
	})
	if n == 0 {
		lines = append(lines, Line{Label: "interactive", Body: "0 actionable nodes", class: classModal, parent: -1})
	}
	return append(lines, c.unseenLine()...)
}

func (c *compiler) expand() []Line {
	ref := c.opts.Target
	id, ok := c.rt.resolve(ref)
	if !ok {
		return []Line{{Body: "unknown ref " + ref + "; refs come from the latest view", class: classModal, parent: -1}}
	}
	n := c.s.ByBackend(id)
	if n == snapshot.None || c.s.Kind[n] != snapshot.KindElement {
		return []Line{{Body: ref + " is no longer on the page; compile a fresh view", class: classModal, parent: -1}}
	}
	c.modals = nil
	c.rootNode = n
	top := &item{key: -1, cover: snapshot.None, kids: c.elem(n)}
	items := c.simplify(top).kids
	setParents(items, nil)
	for _, it := range items {
		c.classifyItem(it, classText, false)
	}
	walkAll(items, func(it *item) {
		if it.kind != "" {
			it.ref = c.rt.get(it.refKey(), it.kind)
		}
	})
	var lines []Line
	for _, it := range items {
		c.emit(&lines, it, 0, -1, snapshot.None, true)
	}
	if len(lines) == 0 {
		return []Line{{Body: ref + " has nothing visible", class: classModal, parent: -1}}
	}
	return lines
}
