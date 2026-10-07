package view

import (
	"sort"
	"strings"
)

// simplify puts the kids of it in visual order, detects repeated siblings,
// and dissolves transparent groups. It returns it with its kids rewritten.
func (c *compiler) simplify(it *item) *item {
	for i, k := range it.kids {
		it.kids[i] = c.simplify(k)
	}
	if it.leaf {
		return it
	}
	c.sortVisual(it.kids)
	start, n := longestRun(it.kids, it.head)
	switch {
	case n == 0:
	case it.head == "" && n < len(it.kids):
		// The run is part of a section that also holds headings or prose:
		// the run is the list, not the section. It has no element of its own:
		// its key is its own, so a diff tells it from the section, and its ref
		// and expand open the section that holds it.
		run := &item{head: "list", key: -it.key, block: it.key, node: it.node, cover: it.cover, runPos: -1, class: classNone}
		run.kids = append([]*item(nil), it.kids[start:start+n]...)
		markRun(run, 0, n)
		run.kids = entries(run.kids)
		listKind(run)
		run.box = run.kids[0].box // as any container without a box of its own
		it.kids = append(append(append([]*item(nil), it.kids[:start]...), run), it.kids[start+n:]...)
	default:
		markRun(it, start, n)
		if it.head == "" {
			it.head = "list"
		}
	}
	it.kids = entries(it.kids)
	listKind(it)
	if it.box.Empty() && len(it.kids) > 0 {
		it.box = it.kids[0].box
	}
	return it
}

// markRun records the run of n kids from start as the repeated run of it.
func markRun(it *item, start, n int) {
	it.repeat = n
	for i := start; i < start+n; i++ {
		it.kids[i].runPos = i - start
	}
}

// entries dissolves layout wrappers and transparent groups among kids, and
// makes each member of a repeated run an entry.
func entries(kids []*item) []*item {
	var out []*item
	for _, k := range kids {
		switch {
		case layoutWrapper(k):
			out = append(out, k.kids...)
		case k.head != "":
			out = append(out, k)
		case k.runPos >= 0 && len(k.kids) == 1:
			only := k.kids[0]
			only.runPos = k.runPos
			out = append(out, only)
		case k.runPos >= 0:
			k.head = "item"
			out = append(out, k)
		default:
			out = append(out, k.kids...)
		}
	}
	return out
}

// listKind gives a repeated list whose entries are rows a ref, so it can be
// read as a table.
func listKind(it *item) {
	if it.repeat > 0 && it.kind == "" && it.head == "list" && tableLike(it) {
		it.kind = "r"
	}
}

// signature describes the shape of an item for repeated-sibling detection.
func signature(it *item) string {
	var sb strings.Builder
	sb.WriteString(it.head)
	if it.head == "" && len(it.kids) == 1 {
		return signature(it.kids[0])
	}
	sb.WriteByte('[')
	for _, k := range it.kids {
		sb.WriteString(signature(k))
		sb.WriteByte(',')
	}
	sb.WriteByte(']')
	return sb.String()
}

func repeatable(it *item) bool {
	if it.textRun || it.heading > 0 || it.head == "img" {
		return false
	}
	return listLike(it)
}

// listLike: the item reads as one entry of a list. Plain prose blocks do not
// qualify however many of them follow each other: an entry is a list item or
// row, carries an actionable node, or is made of two or more parts.
func listLike(it *item) bool {
	switch it.head {
	case "item", "row":
		return true
	}
	if it.kind == "a" || it.kind == "b" || it.kind == "f" {
		return true
	}
	if len(it.kids) >= 2 {
		return true
	}
	for _, k := range it.kids {
		if listLike(k) && !k.textRun && k.heading == 0 {
			return true
		}
	}
	return false
}

// longestRun finds the longest stretch of consecutive kids with one shape.
// Three alike make a run; two do under a list or table.
func longestRun(kids []*item, head string) (start, length int) {
	need := 3
	if head == "list" || head == "table" {
		need = 2
	}
	best, bestStart := 0, 0
	for i := 0; i < len(kids); {
		j := i + 1
		if repeatable(kids[i]) {
			sig := signature(kids[i])
			for j < len(kids) && repeatable(kids[j]) && signature(kids[j]) == sig {
				j++
			}
		}
		if j-i > best {
			best, bestStart = j-i, i
		}
		i = j
	}
	if best < need {
		return 0, 0
	}
	return bestStart, best
}

// sortVisual orders items by reading order: rows top to bottom, within a row
// left to right.
func (c *compiler) sortVisual(items []*item) {
	if len(items) < 2 {
		return
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].box.Y < items[j].box.Y })
	var out []*item
	for i := 0; i < len(items); {
		j := i + 1
		r0 := items[i].box
		for j < len(items) {
			b := items[j].box
			h := min(max(r0.H, 1), max(b.H, 1))
			if b.Y < r0.Y+h*0.5 {
				j++
				continue
			}
			break
		}
		row := items[i:j]
		sort.SliceStable(row, func(a, b int) bool { return row[a].box.X < row[b].box.X })
		out = append(out, row...)
		i = j
	}
	copy(items, out)
}

// classify assigns drop classes. Containers are kept while anything below
// them is kept.
func (c *compiler) classify(main, modals []*item) {
	for _, m := range modals {
		c.classifyItem(m, classModal, true)
	}
	for _, it := range main {
		c.classifyItem(it, classText, false)
	}
	if c.opts.Focus != 0 {
		for _, it := range append(append([]*item{}, modals...), main...) {
			c.boost(it)
		}
	}
}

func (c *compiler) classifyItem(it *item, floor int, force bool) int {
	switch it.head {
	case "nav":
		floor = max(floor, classNav)
	case "footer":
		floor = max(floor, classFooter)
	}
	best := classNone
	for _, k := range it.kids {
		kf := floor
		if it.repeat > 0 && k.runPos >= 0 {
			if k.runPos < firstItemKept {
				kf = max(kf, classFirst)
			} else {
				kf = classLater
			}
		}
		best = min(best, c.classifyItem(k, kf, force))
	}
	own := classText
	if it.kind == "a" || it.kind == "b" || it.kind == "f" || it.heading > 0 {
		own = classActive
	}
	switch {
	case force:
		it.class = classModal
	case it.collapsed:
		// A collapsed navigation is one line that orients the reader: it costs
		// little and outlives the contents it stands in for. The footer keeps
		// its place at the end of the keep order.
		it.class = floor
		if it.head == "nav" {
			it.class = min(floor, classActive)
		}
	case len(it.kids) == 0:
		it.class = max(floor, own)
	default:
		it.class = best
	}
	return it.class
}

// boost keeps the region around the focused node ahead of other content: the
// node, its siblings with their contents, and the chain of ancestors.
func (c *compiler) boost(it *item) bool {
	direct, deeper := false, false
	for _, k := range it.kids {
		if k.key == c.opts.Focus {
			direct = true
		}
		if c.boost(k) {
			deeper = true
		}
	}
	if direct {
		for _, k := range it.kids {
			capClass(k, classFocus)
		}
	}
	if direct || deeper || it.key == c.opts.Focus {
		it.class = min(it.class, classFocus)
		return true
	}
	return false
}

func capClass(it *item, limit int) {
	it.class = min(it.class, limit)
	for _, k := range it.kids {
		capClass(k, limit)
	}
}

// assign hands out refs in reading order, modal layers first, so numbering
// does not depend on the projection. Collapsed regions get their own ref
// first and the refs of their contents after everything else.
func (c *compiler) assign(main, modals []*item) {
	var collapsed []*item
	var walk func(it *item, deep bool)
	walk = func(it *item, deep bool) {
		if it.kind != "" {
			it.ref = c.rt.get(it.refKey(), it.kind)
		}
		// Links inside a sentence are actionable too and need refs.
		for _, s := range it.segs {
			if s.link != nil {
				s.link.ref = c.rt.get(s.link.key, s.link.kind)
			}
		}
		if it.collapsed && !deep {
			collapsed = append(collapsed, it)
			return
		}
		for _, k := range it.kids {
			walk(k, deep)
		}
	}
	for _, it := range modals {
		walk(it, false)
	}
	for _, it := range main {
		walk(it, false)
	}
	for _, r := range collapsed {
		for _, k := range r.kids {
			walk(k, true)
		}
	}
}

// tableLike: a repeated list whose members are rows of two or more cells,
// which makes it addressable as a table.
func tableLike(it *item) bool {
	for _, k := range it.kids {
		if k.runPos >= 0 {
			return flat(k) && len(k.kids) >= 2
		}
	}
	return false
}

// layoutWrapper: a table or row that holds nothing but tables, which is how
// pages without CSS lay out their columns. It carries no data of its own.
func layoutWrapper(it *item) bool {
	if (it.head != "table" && it.head != "row") || it.repeat > 0 || len(it.kids) == 0 {
		return false
	}
	for _, k := range it.kids {
		if k.head != "table" {
			return false
		}
	}
	return true
}

// refKey is the backend id a ref to the item resolves to: its own, or, for a
// list Riffle drew around a run, the element that holds the run.
func (it *item) refKey() int64 {
	if it.key < 0 {
		return it.block
	}
	return it.key
}
