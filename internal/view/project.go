package view

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/noetive/riffle/internal/snapshot"
)

// ---- read: main content as Markdown ----

func (c *compiler) read(main, modals []*item) []Line {
	var lines []Line
	for _, m := range modals {
		c.markdown(&lines, m, 0)
	}
	roots := main
	if m := findHead(main, "main"); m != nil {
		roots = []*item{m}
	}
	for _, it := range roots {
		c.markdown(&lines, it, 0)
	}
	if len(lines) == 0 {
		lines = append(lines, Line{Body: "no readable content", class: classModal, parent: -1})
	}
	return lines
}

func findHead(items []*item, head string) *item {
	for _, it := range items {
		if it.head == head {
			return it
		}
		if f := findHead(it.kids, head); f != nil {
			return f
		}
	}
	return nil
}

func (c *compiler) md(it *item) string {
	var sb strings.Builder
	var prev *seg
	for i := range it.segs {
		s := it.segs[i]
		if prev != nil && spaceBetween(prev, &s) {
			sb.WriteByte(' ')
		}
		prev = &it.segs[i]
		if s.link != nil {
			fmt.Fprintf(&sb, "[%s](%s)", unquoted(s.link.name), s.link.href)
			continue
		}
		t := s.text
		if s.strike {
			t = "~~" + t + "~~"
		}
		if s.italic {
			t = "*" + t + "*"
		}
		if s.bold {
			t = "**" + t + "**"
		}
		sb.WriteString(t)
	}
	return sb.String()
}

// spaceBetween reports whether two neighbouring stretches are written apart.
// Stretches are separate words unless punctuation closes the one or opens
// the other, as in "see [cities](/x), then".
func spaceBetween(prev, next *seg) bool {
	if prev.link == nil {
		if r, _ := utf8.DecodeLastRuneInString(prev.text); strings.ContainsRune("([{“‘«", r) {
			return false
		}
	}
	if next.link == nil {
		if r, _ := utf8.DecodeRuneInString(next.text); strings.ContainsRune(",.;:!?)]}%…”’»", r) {
			return false
		}
	}
	return true
}

func unquoted(q string) string {
	if len(q) >= 2 && q[0] == '"' {
		q = q[1 : len(q)-1]
	}
	return q
}

func (c *compiler) markdown(out *[]Line, it *item, depth int) {
	add := func(body string, indent int) {
		*out = append(*out, Line{Key: it.key, anchor: it.block, Indent: indent, Body: body, class: it.class, parent: -1})
	}
	switch {
	case it.head == "nav" || it.head == "footer" || it.head == "aside" || it.head == "header":
		return
	case it.heading > 0:
		add(strings.Repeat("#", it.heading)+" "+c.textOf(it.node), depth)
	case it.textRun:
		add(c.md(it), depth)
	case it.head == "frame":
		// Read says what it cannot read, in the words of the outline.
		*out = append(*out, Line{Key: it.key, Indent: depth, Label: "frame", Body: it.name, Tags: it.tags, class: it.class, parent: -1})
	case it.head == "img":
		add("![]("+unquoted(it.name)+")", depth)
	case it.head == "link":
		add(fmt.Sprintf("[%s](%s)", unquoted(it.name), it.href), depth)
	case it.head == "clickable":
		// A click handler does not make text unreadable.
		if t := c.textOf(it.node); t != "" {
			add(t, depth)
		}
	case it.kind == "b" || it.kind == "f":
		return
	case it.head == "table":
		c.markdownTable(out, it)
	case it.head == "list":
		for _, k := range it.kids {
			if k.head != "item" && k.runPos < 0 {
				// A list whose markup holds more than its entries, such as a
				// heading inside a <ul>: those are not entries.
				c.markdown(out, k, depth)
				continue
			}
			c.markdownItem(out, k, depth)
		}
	default:
		for _, k := range it.kids {
			c.markdown(out, k, depth)
		}
	}
}

func (c *compiler) markdownItem(out *[]Line, it *item, depth int) {
	if len(it.kids) == 0 && !it.textRun {
		// An entry that is a single link or control reads as that, marked as
		// an entry; one that reads as nothing, as a button, is left out.
		var own []Line
		c.markdown(&own, it, depth)
		if len(own) > 0 {
			own[0].Body = "- " + own[0].Body
		}
		*out = append(*out, own...)
		return
	}
	var cells []piece
	var nested []*item
	var frames []*item
	var controls []string
	for _, k := range it.kids {
		switch {
		case k.head == "frame":
			// A frame is a line of its own, never words of the item.
			frames = append(frames, k)
		case k.textRun:
			cells = append(cells, piece{text: c.md(k), key: k.key})
		case k.head == "link":
			cells = append(cells, piece{text: fmt.Sprintf("[%s](%s)", unquoted(k.name), k.href), key: k.key})
		case k.leaf && k.kind == "":
			// A heading keeps its marks inside the item: "- ### Title".
			name := unquoted(k.name)
			head := k.heading > 0 && name != ""
			if head {
				name = strings.Repeat("#", k.heading) + " " + name
			}
			cells = append(cells, piece{text: name, head: head, key: k.key})
		case k.head == "clickable":
			// A clickable block is read for its text, as the block it is.
			cells = append(cells, piece{text: c.textOf(k.node), key: k.key})
		case k.kind == "b" && k.leaf:
			// A button is not content next to text, but an item made of nothing
			// else is its name.
			controls = append(controls, unquoted(k.name))
		case k.kind == "b" || k.kind == "f":
		default:
			nested = append(nested, k)
		}
	}
	cells = slices.DeleteFunc(cells, func(c piece) bool { return c.text == "" })
	if len(cells) == 0 && len(nested) == 0 {
		for _, name := range controls {
			if name != "" {
				cells = append(cells, piece{text: name})
			}
		}
	}
	if len(it.kids) == 0 && it.textRun {
		cells = append(cells, piece{text: c.md(it)})
	}
	if len(cells) == 0 {
		// An entry with no words of its own, as a row of cards or an item that
		// holds only frames, is its blocks: a bare "- " would say nothing.
		for _, k := range frames {
			c.markdown(out, k, depth)
		}
		for _, k := range nested {
			c.markdown(out, k, depth)
		}
		return
	}
	for i, l := range itemLines(cells) {
		if i == 0 {
			*out = append(*out, Line{Key: it.key, anchor: it.block, Indent: depth, Body: "- " + l.text, class: it.class, parent: -1})
			continue
		}
		// The item goes on under its bullet, a line of its own, keyed by the
		// node it starts with so a change to it reads as a change.
		*out = append(*out, Line{Key: l.key, anchor: it.block, Indent: depth + 1, Body: l.text, class: it.class, parent: -1})
	}
	for _, k := range frames {
		c.markdown(out, k, depth+1)
	}
	for _, k := range nested {
		c.markdown(out, k, depth+1)
	}
}

// cellText renders the readable content of an item on one line.
func (c *compiler) cellText(it *item) string {
	switch {
	case it.textRun:
		return c.md(it)
	case it.head == "link":
		if unquoted(it.name) == "" {
			return ""
		}
		if h := it.href; h != "" {
			return fmt.Sprintf("[%s](%s)", unquoted(it.name), h)
		}
		return unquoted(it.name)
	case it.kind == "b" || it.kind == "f":
		return ""
	case it.leaf:
		return unquoted(it.name)
	}
	var parts []string
	for _, k := range it.kids {
		if t := c.cellText(k); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

// tableRows are the rows of a table. Row groups such as the tbody a browser
// adds are looked through, so their rows count as the table's own.
func tableRows(it *item) []*item {
	var rows []*item
	for _, k := range it.kids {
		if k.head == "list" && len(k.kids) > 0 && allRows(k.kids) {
			rows = append(rows, k.kids...)
			continue
		}
		rows = append(rows, k)
	}
	return rows
}

func allRows(items []*item) bool {
	for _, k := range items {
		if k.head != "row" {
			return false
		}
	}
	return true
}

// markdownTable renders rows as a Markdown table when they share a column
// count, and as one line per row when they do not, which is what tables used
// only for page layout look like.
func (c *compiler) markdownTable(out *[]Line, it *item) {
	for _, r := range it.kids {
		if r.head == "table" {
			// Tables nested in tables are page layout around the data.
			for _, k := range it.kids {
				c.markdown(out, k, 0)
			}
			return
		}
	}
	type row struct {
		it    *item
		cells []string
	}
	var rows []row
	uniform := true
	for _, r := range tableRows(it) {
		if len(r.kids) == 0 {
			continue
		}
		var cells []string
		for _, k := range r.kids {
			cells = append(cells, c.cellText(k))
		}
		if strings.TrimSpace(strings.Join(cells, "")) == "" {
			continue
		}
		if len(rows) > 0 && len(cells) != len(rows[0].cells) {
			uniform = false
		}
		rows = append(rows, row{r, cells})
	}
	if len(rows) > 0 && len(rows[0].cells) < 2 {
		uniform = false
	}
	for i, r := range rows {
		if !uniform {
			var parts []string
			for _, ce := range r.cells {
				if ce != "" {
					parts = append(parts, ce)
				}
			}
			*out = append(*out, Line{Key: r.it.key, Body: strings.Join(parts, " "), class: r.it.class, parent: -1})
			continue
		}
		*out = append(*out, Line{Key: r.it.key, Body: "| " + strings.Join(r.cells, " | ") + " |", class: r.it.class, parent: -1})
		if i == 0 {
			*out = append(*out, Line{Key: r.it.key, Body: "|" + strings.Repeat(" --- |", len(r.cells)), class: r.it.class, parent: -1})
		}
	}
}

// ---- table: a visual table as TSV ----

type cell struct {
	text string
	box  snapshot.Rect
}

var cellRoles = map[string]bool{"cell": true, "columnheader": true, "rowheader": true, "gridcell": true}

func (c *compiler) cells(n int32, out *[]cell) {
	for _, k := range c.s.Children[n] {
		nk := c.fact(k)
		switch c.s.Kind[k] {
		case snapshot.KindText:
			if !nk.Hidden && !nk.Unseen && words(nk.Text) != "" {
				*out = append(*out, cell{words(nk.Text), c.s.Box[k]})
			}
		case snapshot.KindElement:
			if !nk.Visible() {
				continue
			}
			if cellRoles[nk.Role] || c.onlyInline(k) {
				if t := c.textOf(k); t != "" {
					*out = append(*out, cell{t, c.s.Box[k]})
				}
				continue
			}
			c.cells(k, out)
		}
	}
}

func (c *compiler) table() []Line {
	ref := c.opts.Target
	id, ok := c.rt.resolve(ref)
	if !ok {
		return []Line{{Body: "unknown ref " + ref + "; refs come from the latest view", class: classModal, parent: -1}}
	}
	n := c.s.ByBackend(id)
	if n == snapshot.None || c.s.Kind[n] != snapshot.KindElement {
		return []Line{{Body: ref + " is no longer on the page; compile a fresh view", class: classModal, parent: -1}}
	}
	var cells []cell
	c.cells(n, &cells)
	if len(cells) == 0 {
		return []Line{{Body: ref + " has no rows", class: classModal, parent: -1}}
	}
	sort.SliceStable(cells, func(i, j int) bool { return cells[i].box.Y < cells[j].box.Y })
	var lines []Line
	for i := 0; i < len(cells); {
		j := i + 1
		r0 := cells[i].box
		for j < len(cells) {
			h := min(max(r0.H, 1), max(cells[j].box.H, 1))
			if cells[j].box.Y >= r0.Y+h*0.5 {
				break
			}
			j++
		}
		row := cells[i:j]
		sort.SliceStable(row, func(a, b int) bool { return row[a].box.X < row[b].box.X })
		texts := make([]string, len(row))
		for k, ce := range row {
			texts[k] = ce.text
		}
		lines = append(lines, Line{Body: strings.Join(texts, "\t"), class: classText, parent: -1})
		i = j
	}
	return lines
}

// ---- find: nodes matching a text ----

func (c *compiler) find() []Line {
	q := strings.ToLower(strings.TrimSpace(c.opts.Query))
	head := Line{Label: "find", Body: quoteText(c.opts.Query, 60), class: classModal, parent: -1}
	if q == "" {
		head.Body += " needs a non-empty query"
		return []Line{head}
	}
	seen := map[int64]bool{}
	var found []*item
	add := func(it *item) {
		if it == nil || seen[it.key] {
			return
		}
		seen[it.key] = true
		if it.kind != "" {
			it.ref = c.rt.get(it.refKey(), it.kind)
		}
		found = append(found, it)
	}
	for n := int32(0); n < int32(c.s.Len()); n++ {
		nd := c.fact(n)
		switch c.s.Kind[n] {
		case snapshot.KindText:
			if nd.Hidden || nd.Unseen || !strings.Contains(strings.ToLower(nd.Text), q) {
				continue
			}
			if c.s.Parent[n] == snapshot.None {
				continue // text that hangs off nothing is not on the page
			}
			if a := c.interactiveAncestor(n); a != snapshot.None {
				add(c.leaf(a))
				continue
			}
			rb := &runBuilder{c: c, owner: c.s.Parent[n]}
			rb.text(n)
			add(rb.finish())
		case snapshot.KindElement:
			if nd.Interactive && nd.Visible() && strings.Contains(strings.ToLower(nd.Name), q) {
				add(c.leaf(n))
			}
		}
	}
	// A phrase a framework renders in pieces matches no single text node, but
	// it reads as one in its paragraph.
	if strings.Contains(strings.ToLower(c.textOf(0)), q) {
		for n := int32(0); n < int32(c.s.Len()); n++ {
			if !c.inlineParagraph(n) || !strings.Contains(strings.ToLower(c.textOf(n)), q) || c.textNodeHolds(n, q) {
				continue
			}
			rb := &runBuilder{c: c, owner: n}
			rb.fold(n)
			add(rb.finish())
		}
	}
	c.sortVisual(found)
	head.Body += fmt.Sprintf(" %d matches", len(found))
	lines := []Line{head}
	for _, it := range found {
		it.class = classActive
		lines = append(lines, c.linesFor(it, 0, -1, snapshot.None))
	}
	return lines
}

// inlineParagraph: a visible block-level element whose content is inline, so
// its text reads as one run.
func (c *compiler) inlineParagraph(n int32) bool {
	if c.s.Kind[n] != snapshot.KindElement || c.s.Pseudo[n] != "" || len(c.s.Children[n]) == 0 {
		return false
	}
	nd := c.fact(n)
	if !nd.Visible() || nd.Interactive || strings.HasPrefix(c.s.Style[n][snapshot.Display], "inline") {
		return false
	}
	return c.onlyInline(n)
}

// textNodeHolds: some visible text node under n contains q by itself, which
// the main pass has already reported.
func (c *compiler) textNodeHolds(n int32, q string) bool {
	for _, k := range c.s.Children[n] {
		nk := c.fact(k)
		if nk.Hidden || nk.Unseen {
			continue
		}
		if c.s.Kind[k] == snapshot.KindText && strings.Contains(strings.ToLower(nk.Text), q) {
			return true
		}
		if c.s.Kind[k] == snapshot.KindElement && c.textNodeHolds(k, q) {
			return true
		}
	}
	return false
}

func (c *compiler) interactiveAncestor(n int32) int32 {
	for p := c.s.Parent[n]; p != snapshot.None; p = c.s.Parent[p] {
		if c.fact(p).Interactive && c.fact(p).Visible() {
			return p
		}
	}
	return snapshot.None
}

// piece is one part of a list item's line: its text, whether it is a heading,
// and the node it reads.
type piece struct {
	text string
	key  int64
	head bool
}

// itemLines are the lines a list item's cells read as, each keyed by the node
// it starts with. A heading is a line of its own: in "- ### Question? Answer"
// the answer would be part of the heading, and a heading mark after other
// words is no heading at all.
func itemLines(cells []piece) []piece {
	var lines []piece
	var run []string
	var key int64
	flush := func() {
		if len(run) > 0 {
			lines = append(lines, piece{text: strings.Join(run, " "), key: key})
			run = nil
		}
	}
	for _, c := range cells {
		if c.head {
			flush()
			lines = append(lines, c)
			continue
		}
		if len(run) == 0 {
			key = c.key
		}
		run = append(run, c.text)
	}
	flush()
	return lines
}
