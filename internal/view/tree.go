package view

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/noetive/riffle/internal/facts"
	"github.com/noetive/riffle/internal/pagetext"
	"github.com/noetive/riffle/internal/snapshot"
)

// Drop classes: higher numbers are dropped first when the budget is tight.
// The keep order is the design's: modal layer, focused region, interactive
// nodes in main content, main text, first items of repeated lists,
// navigation, footer. Later items of repeated lists go before everything.
const (
	classModal    = 0
	classFocus    = 1
	classActive   = 2
	classText     = 3
	classFirst    = 4
	classNav      = 5
	classFooter   = 6
	classLater    = 7
	classNone     = 8
	firstItemKept = 3 // items of a repeated run that count as "first"
)

// seg is a stretch of text with uniform style, or an inline link.
type seg struct {
	link   *item
	text   string
	tags   []string
	bold   bool
	italic bool
	strike bool
	// br marks the line break an element made, not text.
	br bool
}

// item is a node of the compiled tree before it is flattened to lines.
type item struct {
	parent *item
	head   string // output word; "" for a transparent group
	kind   string // ref prefix, "" for none
	ref    string
	name   string // quoted name for leaves and regions
	href   string
	segs   []seg // text of a text run
	tags   []string
	kids   []*item
	box    snapshot.Rect
	key    int64
	// block is the backend id of the element that holds a text run, which expand
	// can open; 0 for any other item.
	block   int64
	heading int
	repeat  int // length of the repeated run among kids, 0 for none
	runPos  int // position in the parent's repeated run, -1 for none
	class   int
	node    int32
	cover   int32

	leaf      bool
	textRun   bool
	collapsed bool
	modal     bool
}

type compiler struct {
	p      *facts.Page
	s      *snapshot.Snapshot
	rt     *refTable
	labels map[int32]bool // text nodes that label a control and are shown as its name
	modals []*item
	opts   Options
	// rootNode is the node being expanded; it is exempt from modal hoisting.
	rootNode int32
}

func (c *compiler) fact(n int32) *facts.Node { return &c.p.Nodes[n] }

var inlineTags = map[string]bool{
	"b": true, "i": true, "em": true, "strong": true, "small": true, "code": true, "mark": true,
	"s": true, "del": true, "ins": true, "u": true, "abbr": true, "time": true, "sub": true,
	"sup": true, "label": true, "br": true, "wbr": true, "cite": true, "q": true, "kbd": true,
	"samp": true, "var": true, "font": true, "bdi": true, "span": true,
}

// structure maps roles to the output word and ref kind of their lines.
var structure = map[string][2]string{
	"main": {"main", ""}, "banner": {"header", ""}, "contentinfo": {"footer", "r"},
	"navigation": {"nav", "r"}, "complementary": {"aside", ""}, "form": {"form", ""},
	"region": {"region", ""}, "article": {"article", ""}, "list": {"list", ""},
	"listitem": {"item", ""}, "table": {"table", "r"}, "grid": {"table", "r"}, "row": {"row", ""},
	"dialog": {"dialog", "d"}, "alertdialog": {"dialog", "d"},
}

// leafKinds maps actionable roles to the output word and ref kind.
var leafKinds = map[string][2]string{
	"link": {"link", "a"}, "button": {"button", "b"}, "tab": {"tab", "b"},
	"menuitem": {"menuitem", "b"}, "menuitemcheckbox": {"menuitem", "b"}, "menuitemradio": {"menuitem", "b"},
	"option": {"option", "b"}, "textbox": {"textbox", "f"}, "searchbox": {"searchbox", "f"},
	"spinbutton": {"spinbutton", "f"}, "checkbox": {"checkbox", "f"}, "radio": {"radio", "f"},
	"switch": {"switch", "f"}, "combobox": {"select", "f"}, "listbox": {"listbox", "f"},
	"slider": {"slider", "f"},
}

// build compiles the whole document into the main flow and the modal layers.
func (c *compiler) build() (main []*item, modals []*item) {
	top := &item{key: -1, cover: snapshot.None}
	for i := int32(0); i < int32(c.s.Len()); i++ {
		if c.s.Parent[i] == snapshot.None {
			top.kids = append(top.kids, c.elem(i)...)
		}
	}
	main = c.simplify(top).kids
	for _, m := range c.modals {
		sub := &item{key: -1, kids: []*item{m}}
		modals = append(modals, c.simplify(sub).kids...)
	}
	for _, m := range modals {
		m.kids = withoutTitle(m)
	}
	sort.SliceStable(modals, func(i, j int) bool {
		return c.s.Paint[modals[i].node] > c.s.Paint[modals[j].node]
	})
	setParents(main, nil)
	setParents(modals, nil)
	return main, modals
}

func setParents(items []*item, p *item) {
	for _, it := range items {
		it.parent = p
		setParents(it.kids, it)
	}
}

// elem compiles one node into zero or more items.
func (c *compiler) elem(n int32) []*item {
	s := c.s
	switch s.Kind[n] {
	case snapshot.KindDocument:
		return []*item{c.container(n, "")}
	case snapshot.KindElement:
	default:
		return nil
	}
	nd := c.fact(n)
	if !nd.Visible() {
		return nil
	}
	if nd.Modal && n != c.rootNode {
		m := c.container(n, "modal")
		m.kind = "d"
		m.modal = true
		m.name = quoteName(nd.Name)
		if nd.Covers {
			m.tags = append(m.tags, "covers=page")
		}
		c.modals = append(c.modals, m)
		return nil
	}
	if s.Tag[n] == "option" {
		return nil
	}
	switch {
	case s.Tag[n] == "iframe":
		// Embedded documents are not read; say so rather than stay silent.
		title, _ := s.Attr(n, "title")
		src, _ := s.Attr(n, "src")
		it := c.base(n, "frame", "")
		it.leaf = true
		it.name = quoteName(frameLabel(title, src))
		it.tags = append(it.tags, "content-not-shown")
		return []*item{it}
	case nd.Interactive && nd.Role == "listbox" && c.s.Tag[n] != "select" && c.hasInteractive(n):
		// An opened custom listbox is its options: they are what gets clicked.
		it := c.container(n, "listbox")
		it.kind = "f"
		if nd.Name != "" {
			it.name = quoteName(nd.Name)
		}
		return []*item{it}
	case nd.Interactive:
		return []*item{c.leaf(n)}
	case nd.Heading > 0:
		h := c.heading(n)
		if h.leaf && h.name == "" {
			return nil // a heading with no readable text says nothing
		}
		return []*item{h}
	case (nd.Role == "progressbar" || nd.Role == "meter") && !c.hasInteractive(n):
		// A ref, so a wait can watch it: wait text "40%" in rN. One that
		// holds controls, as a stepper does, is read as a container.
		it := c.base(n, "progress", "r")
		it.leaf = true
		name, value := nd.Name, c.progressValue(n)
		if name == "" && value == "" {
			name = c.textOf(n) // it says how far along it is in words
		}
		it.name = strings.TrimSpace(quoteName(name) + " " + value)
		return []*item{it}
	case nd.Role == "img":
		if nd.Name == "" {
			return nil
		}
		it := c.base(n, "img", "")
		it.leaf = true
		it.name = quoteName(nd.Name)
		return []*item{it}
	case nd.Clickable:
		return []*item{c.clickable(n)}
	}
	if w, ok := structure[nd.Role]; ok {
		it := c.container(n, w[0])
		it.kind = w[1]
		if w[0] == "nav" || w[0] == "footer" {
			it.collapsed = true
		}
		if nd.Name != "" && w[0] != "item" && w[0] != "row" && w[0] != "list" {
			it.name = quoteName(nd.Name)
		}
		return []*item{c.scrolling(it, n)}
	}
	return []*item{c.scrolling(c.container(n, ""), n)}
}

// scrolling marks a region that scrolls its own content and gives it a ref, so
// that a scroll can be aimed at it. A box the size of the page scrolls the
// page, which needs no aim.
func (c *compiler) scrolling(it *item, n int32) *item {
	if !c.fact(n).Scrollable || c.pageSized(n) {
		return it
	}
	if it.head == "" {
		it.head = "scroll"
	}
	if it.kind == "" {
		it.kind = "r"
	}
	if nm := c.fact(n).Name; nm != "" && it.name == "" {
		it.name = quoteName(nm)
	}
	it.tags = append(it.tags, "scrollable")
	return it
}

func (c *compiler) base(n int32, head, kind string) *item {
	nd := c.fact(n)
	it := &item{node: n, key: c.s.Backend[n], head: head, kind: kind, cover: nd.CoveredBy, box: c.s.Box[n], runPos: -1, class: classNone}
	if nd.Shown != "" {
		it.tags = append(it.tags, "name-differs")
	}
	if nd.NameUnseen {
		it.tags = append(it.tags, "unseen-name")
	}
	if nd.Unlabelled {
		it.tags = append(it.tags, "unlabelled")
	}
	if nd.Primary {
		it.tags = append(it.tags, "primary")
	}
	if nd.Selected {
		it.tags = append(it.tags, "selected")
	}
	if nd.Checked {
		it.tags = append(it.tags, "checked")
	}
	if nd.Expansion != "" {
		it.tags = append(it.tags, nd.Expansion)
	}
	if nd.Pressed {
		it.tags = append(it.tags, "pressed")
	}
	if nd.Required {
		it.tags = append(it.tags, "required")
	}
	if nd.Invalid {
		it.tags = append(it.tags, "invalid")
	}
	if nd.Disabled {
		it.tags = append(it.tags, "disabled")
	}
	return it
}

// leaf compiles an actionable node with its facts. Its content is not
// descended into: the accessible name already summarizes it.
func (c *compiler) leaf(n int32) *item {
	nd := c.fact(n)
	k := leafKinds[nd.Role]
	if nd.Role == "combobox" && c.s.Tag[n] != "select" {
		k[0] = "combobox" // typed into or opened by script, not chosen with `select`
	}
	it := c.base(n, k[0], k[1])
	it.leaf = true
	var body []string
	if nd.Name != "" {
		body = append(body, quoteName(nd.Name))
	}
	v := c.s.Value[n]
	if v == "" && c.editable(n) {
		v = c.rawText(n)
	}
	if v != "" && k[1] == "f" && nd.Role != "checkbox" && nd.Role != "radio" && nd.Role != "switch" {
		if strings.EqualFold(c.attr(n, "type"), "password") {
			// A password is never shown, only that the field holds one.
			body = append(body, "=filled")
		} else {
			body = append(body, "="+quoteText(v, 60))
		}
	}
	if c.s.Tag[n] == "select" {
		if v := c.selectedOptions(n); v != "" {
			body = append(body, "="+quoteText(v, 60))
		}
	}
	it.name = strings.Join(body, " ")
	it.href = pagetext.Href(c.attr(n, "href"))
	it.tags = append(it.tags, c.styleTags(n)...)
	if c.s.Tag[n] == "input" && strings.EqualFold(c.attr(n, "type"), "password") {
		it.tags = append(it.tags, "password")
	}
	if nd.Role == "link" {
		it.tags = append(it.tags, c.destinationTags(n, it.href)...)
	}
	if nd.Label != snapshot.None && words(c.fact(nd.Label).Text) != nd.Name {
		it.tags = append(it.tags, "label="+quoteText(c.fact(nd.Label).Text, 40))
	}
	if nd.Error != snapshot.None {
		it.tags = append(it.tags, "error="+quoteText(c.fact(nd.Error).Text, 80))
	}
	return it
}

// styleTags are the strike and tone facts of an actionable node or heading.
func (c *compiler) styleTags(n int32) []string {
	var tags []string
	strike, tone := c.fact(n).Strike, c.fact(n).Tone
	for j := n + 1; (!strike || tone == facts.ToneNone) && j < int32(c.s.Len()) && c.s.Parent[j] != snapshot.None && c.isInside(j, n); j++ {
		if c.p.Nodes[j].Unseen || c.p.Nodes[j].Hidden {
			continue
		}
		strike = strike || c.fact(j).Strike
		if tone == facts.ToneNone {
			tone = c.fact(j).Tone
		}
	}
	if strike {
		tags = append(tags, "strike")
	}
	if tone != facts.ToneNone {
		tags = append(tags, string(tone))
	}
	if c.fact(n).Truncated {
		tags = append(tags, "truncated")
	}
	return tags
}

func (c *compiler) isInside(j, n int32) bool {
	for p := c.s.Parent[j]; p != snapshot.None; p = c.s.Parent[p] {
		if p == n {
			return true
		}
	}
	return false
}

func (c *compiler) attr(n int32, name string) string {
	v, _ := c.s.Attr(n, name)
	return v
}

func (c *compiler) clickable(n int32) *item {
	txt := c.textOf(n)
	if txt == "" {
		txt = c.fact(n).Name // an image-only block is named by its alt
	}
	if !c.hasInteractive(n) && utf8.RuneCountInString(txt) <= 60 {
		it := c.base(n, "clickable", "b")
		it.leaf = true
		it.name = quoteName(txt)
		it.tags = append(it.tags, c.styleTags(n)...)
		return it
	}
	// A click handler on a wrapper around actionable nodes, or on a region as
	// big as the page, is delegation and not a control: the nodes inside
	// carry the refs.
	if c.hasInteractive(n) || c.pageSized(n) {
		return c.container(n, "")
	}
	it := c.container(n, "clickable")
	it.kind = "b"
	return it
}

func (c *compiler) pageSized(n int32) bool {
	b := c.s.Box[n]
	return c.s.ViewportW > 0 && c.s.ViewportH > 0 && b.W*b.H >= c.s.ViewportW*c.s.ViewportH/2
}

func (c *compiler) hasInteractive(n int32) bool {
	for _, k := range c.s.Children[n] {
		if c.s.Kind[k] != snapshot.KindElement || !c.fact(k).Visible() {
			continue
		}
		if c.fact(k).Interactive || c.hasInteractive(k) {
			return true
		}
	}
	return false
}

// heading compiles a heading. One that contains actionable nodes stays a
// container so they keep their own lines.
func (c *compiler) heading(n int32) *item {
	nd := c.fact(n)
	head := "h" + string(rune('0'+nd.Heading))
	if c.hasInteractive(n) || !c.onlyInline(n) {
		it := c.container(n, head)
		it.heading = nd.Heading
		return it
	}
	it := c.base(n, head, "")
	it.leaf = true
	it.heading = nd.Heading
	it.name = quoteName(c.textOf(n))
	it.tags = append(it.tags, c.styleTags(n)...)
	return it
}

// container compiles an element with children. Text and inline content turn
// into text runs, everything else recurses.
func (c *compiler) container(n int32, head string) *item {
	it := c.base(n, head, "")
	if c.s.Kind[n] == snapshot.KindDocument {
		it.cover = snapshot.None
	}
	run := &runBuilder{c: c, owner: n}
	flush := func() {
		if r := run.finish(); r != nil {
			it.kids = append(it.kids, r)
		}
	}
	for _, k := range c.s.Children[n] {
		switch c.s.Kind[k] {
		case snapshot.KindText:
			run.text(k)
		case snapshot.KindElement:
			nk := c.fact(k)
			switch {
			case !nk.Visible(), c.decorativeMarker(k):
				// A bullet is drawn by the list; reading it would repeat it.
			case c.foldable(k):
				// A second line break in a row is a blank line: a paragraph ends.
				if c.s.Tag[k] == "br" && run.endsWithBreak() {
					flush()
					continue
				}
				run.fold(k)
			case nk.Role == "link" && !nk.Modal && run.nonEmpty():
				run.link(c.leaf(k))
			default:
				flush()
				it.kids = append(it.kids, c.elem(k)...)
			}
		default:
			flush()
			it.kids = append(it.kids, c.elem(k)...)
		}
	}
	flush()
	return it
}

// decorativeMarker: the node is a list or disclosure marker that only draws a
// bullet. A number or a symbol with a meaning is text of the page.
func (c *compiler) decorativeMarker(n int32) bool {
	return facts.DecorativeMarker(c.s, n)
}

// foldable: an inline element whose text joins the surrounding run.
func (c *compiler) foldable(n int32) bool {
	nd := c.fact(n)
	if nd.Role != "" || nd.Interactive || nd.Clickable || nd.Modal || nd.Heading > 0 {
		return false
	}
	return c.inlineLevel(n) && c.inlineWithLinks(n)
}

// inlineWithLinks: like onlyInline, but plain links may appear inside, as in
// a superscript that holds a citation link.
func (c *compiler) inlineWithLinks(n int32) bool {
	for _, k := range c.s.Children[n] {
		if c.s.Kind[k] != snapshot.KindElement || !c.fact(k).Visible() {
			continue
		}
		nk := c.fact(k)
		if nk.Role == "link" && !nk.Modal && !nk.Clickable && !c.hasInteractive(k) && nk.Heading == 0 {
			continue
		}
		if nk.Role != "" || nk.Interactive || nk.Clickable || nk.Modal || nk.Heading > 0 || !c.inlineLevel(k) || !c.inlineWithLinks(k) {
			return false
		}
	}
	return true
}

// embeddedTags are elements that hold another document. Their content is not
// text in this page, so they never fold into a run of text, whatever their
// display says. Other replaced elements (images, video, canvas, svg) are left
// out: they read the same folded or not.
var embeddedTags = map[string]bool{
	"iframe": true, "embed": true, "object": true,
}

// inlineLevel: the element sits in a line of text. An embedded document does not,
// whatever its display says.
func (c *compiler) inlineLevel(n int32) bool {
	if embeddedTags[c.s.Tag[n]] {
		return false
	}
	if c.s.Pseudo[n] != "" || inlineTags[c.s.Tag[n]] {
		return true
	}
	d := c.s.Style[n][snapshot.Display]
	return d == "inline"
}

// onlyInline: every visible descendant element is plain inline content.
func (c *compiler) onlyInline(n int32) bool {
	for _, k := range c.s.Children[n] {
		if c.s.Kind[k] != snapshot.KindElement || !c.fact(k).Visible() {
			continue
		}
		nk := c.fact(k)
		if nk.Role != "" || nk.Interactive || nk.Clickable || nk.Modal || nk.Heading > 0 || !c.inlineLevel(k) || !c.onlyInline(k) {
			return false
		}
	}
	return true
}

// editable: the node is a contenteditable region, whose content is its text
// and not a form value.
func (c *compiler) editable(n int32) bool {
	v, ok := c.s.Attr(n, "contenteditable")
	return ok && !strings.EqualFold(strings.TrimSpace(v), "false")
}

// selectedOptions lists the labels of the options a select has chosen. An
// option's text is read even though a closed select lays its options out of
// view.
func (c *compiler) selectedOptions(n int32) string {
	var picked []string
	var walk func(int32)
	walk = func(i int32) {
		for _, k := range c.s.Children[i] {
			switch {
			case c.s.Tag[k] == "option" && c.s.Ticked[k]:
				picked = append(picked, c.rawText(k))
			case c.s.Kind[k] == snapshot.KindElement:
				walk(k)
			}
		}
	}
	walk(n)
	return strings.Join(picked, ", ")
}

// rawText is the text of a subtree whether or not it is laid out.
func (c *compiler) rawText(n int32) string {
	var sb strings.Builder
	var walk func(int32)
	walk = func(i int32) {
		if c.s.Kind[i] == snapshot.KindText {
			sb.WriteString(c.s.Text[i])
			return
		}
		for _, k := range c.s.Children[i] {
			walk(k)
		}
	}
	walk(n)
	return words(sb.String())
}

// destinationTags says where a link goes when that is not a web page: a mail
// or phone handler, a script, or a file to download. Links to pages stay
// short, since the agent reads their text and not their address.
func (c *compiler) destinationTags(n int32, href string) []string {
	var tags []string
	if _, ok := c.s.Attr(n, "download"); ok {
		tags = append(tags, "download")
	}
	h := href
	if i := strings.Index(h, ":"); i > 0 && !strings.HasPrefix(h, "/") {
		switch scheme := strings.ToLower(h[:i]); scheme {
		case "http", "https":
		default:
			tags = append(tags, "href="+clip(h, 50))
		}
	}
	return tags
}

// textOf is the readable text of a subtree: hidden and unseen text is left out.
func (c *compiler) textOf(n int32) string {
	var sb strings.Builder
	c.collect(&sb, n)
	return words(sb.String())
}

func (c *compiler) collect(sb *strings.Builder, n int32) {
	nd := c.fact(n)
	if nd.Hidden || nd.Unseen {
		return
	}
	switch c.s.Kind[n] {
	case snapshot.KindText:
		sb.WriteString(nd.Text)
		return
	case snapshot.KindElement:
		if !nd.Visible() {
			return
		}
		// A bullet is drawn by the list; reading it would repeat it.
		if c.decorativeMarker(n) {
			return
		}
		if c.s.Pseudo[n] != "" && nd.Text != "" {
			sb.WriteString(nd.Text)
		}
		block := (!strings.HasPrefix(c.s.Style[n][snapshot.Display], "inline") || c.s.Tag[n] == "br" || c.s.Tag[n] == "wbr") && c.s.Pseudo[n] == ""
		if block {
			sb.WriteByte(' ')
		}
		for _, k := range c.s.Children[n] {
			c.collect(sb, k)
		}
		if block {
			sb.WriteByte(' ')
		}
		return
	}
	for _, k := range c.s.Children[n] {
		c.collect(sb, k)
	}
}

// runBuilder gathers the text and inline content of one container.
type runBuilder struct {
	c     *compiler
	segs  []seg
	box   snapshot.Rect
	owner int32
	first int32
	has   bool
	// last is the box of the element read last, to tell words a person sees
	// apart from words that touch.
	last snapshot.Rect
}

// endsWithBreak: the run's last stretch is a line break.
func (r *runBuilder) endsWithBreak() bool {
	return len(r.segs) > 0 && r.segs[len(r.segs)-1].br
}

func (r *runBuilder) nonEmpty() bool {
	for _, s := range r.segs {
		if s.link != nil || strings.TrimSpace(s.text) != "" {
			return true
		}
	}
	return false
}

func (r *runBuilder) note(n int32) {
	b := r.c.s.Box[n]
	if !r.has {
		r.first, r.box, r.has = n, b, true
		return
	}
	x0, y0 := min(r.box.X, b.X), min(r.box.Y, b.Y)
	x1, y1 := max(r.box.X+r.box.W, b.X+b.W), max(r.box.Y+r.box.H, b.Y+b.H)
	r.box = snapshot.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

func (r *runBuilder) text(n int32) {
	// Text sits between elements: the next one is told apart from the text,
	// or, when the text has no box of its own, not at all.
	r.last = r.c.s.Box[n]
	nd := r.c.fact(n)
	if nd.Hidden || nd.Unseen || nd.Text == "" || r.c.labels[n] {
		return
	}
	var tags []string
	if nd.Strike {
		tags = append(tags, "strike")
	}
	if nd.Tone != facts.ToneNone {
		tags = append(tags, string(nd.Tone))
	}
	st := r.c.s.Style
	p := r.c.s.Parent[n]
	var bold, italic bool
	if p != snapshot.None {
		bold = fontWeight(st[p][snapshot.FontWeight]) >= 600
		italic = strings.Contains(st[p][snapshot.FontStyle], "italic")
	}
	r.note(n)
	r.segs = append(r.segs, seg{text: nd.Text, tags: tags, bold: bold, italic: italic, strike: nd.Strike})
}

func (r *runBuilder) fold(n int32) {
	r.apart(n)
	defer r.seen(n)
	if r.c.s.Pseudo[n] != "" && r.c.s.Kind[n] != snapshot.KindText && r.c.fact(n).Text != "" {
		r.note(n)
		r.segs = append(r.segs, seg{text: r.c.fact(n).Text})
	}
	if r.c.s.Tag[n] == "br" {
		r.segs = append(r.segs, seg{text: " ", br: true})
	}
	for _, k := range r.c.s.Children[n] {
		switch r.c.s.Kind[k] {
		case snapshot.KindText:
			r.text(k)
		case snapshot.KindElement:
			if nk := r.c.fact(k); nk.Visible() && !r.c.decorativeMarker(k) {
				if nk.Role == "link" && !nk.Modal {
					r.link(r.c.leaf(k))
				} else {
					r.fold(k)
				}
			}
		}
	}
}

// seen records where element n sits, for the next one to be told apart from.
func (r *runBuilder) seen(n int32) {
	if b := r.c.s.Box[n]; !b.Empty() {
		r.last = b
	}
}

// wordGap is how far apart, in CSS pixels, two elements on one line must be
// for a person to read their words as separate.
const wordGap = 2

// apart puts a space before an element that starts clear of the one read last
// on the same line, as badges set apart by a margin are: the markup has no
// space between them, but a person sees one.
func (r *runBuilder) apart(n int32) {
	l, b := r.last, r.c.s.Box[n]
	if l.Empty() || b.Empty() || len(r.segs) == 0 {
		return
	}
	sameLine := b.Y < l.Y+l.H && l.Y < b.Y+b.H
	if sameLine && b.X > l.X+l.W+wordGap {
		r.segs = append(r.segs, seg{text: " "})
	}
}

func (r *runBuilder) link(l *item) {
	r.apart(l.node)
	defer r.seen(l.node)
	r.note(l.node)
	r.segs = append(r.segs, seg{link: l})
}

// finish merges neighbouring stretches of equal style and returns the run as
// a text item, or nil when there is nothing readable.
func (r *runBuilder) finish() *item {
	var merged []seg
	for _, s := range r.segs {
		if s.link == nil && len(merged) > 0 {
			l := &merged[len(merged)-1]
			if l.link == nil && strings.Join(l.tags, ",") == strings.Join(s.tags, ",") && l.bold == s.bold && l.italic == s.italic {
				l.text += s.text
				continue
			}
		}
		merged = append(merged, s)
	}
	var out []seg
	for _, s := range merged {
		if s.link == nil {
			s.text = words(s.text)
			if s.text == "" {
				continue
			}
		}
		out = append(out, s)
	}
	r.segs = nil
	if len(out) == 0 {
		return nil
	}
	it := &item{node: r.first, key: r.c.s.Backend[r.first], head: "text", leaf: true, textRun: true, block: r.c.s.Backend[r.owner], segs: out, box: r.box,
		cover: r.c.fact(r.owner).CoveredBy, runPos: -1, class: classNone}
	if r.c.fact(r.owner).Truncated {
		it.tags = append(it.tags, "truncated")
	}
	r.has = false
	return it
}

func fontWeight(w string) float64 {
	switch w {
	case "bold":
		return 700
	case "normal", "":
		return 400
	}
	var v float64
	for _, ch := range w {
		if ch < '0' || ch > '9' {
			return 400
		}
		v = v*10 + float64(ch-'0')
	}
	return v
}

// words is page text as one line of plain words; see pagetext.Clean.
func words(s string) string { return pagetext.Clean(s) }

// withoutTitle drops the heading that only repeats the modal's own name.
func withoutTitle(m *item) []*item {
	var out []*item
	for _, k := range m.kids {
		if k.heading > 0 && k.leaf && k.name == m.name && m.name != "" {
			continue
		}
		out = append(out, k)
	}
	return out
}

// progressValue is how far along a progress bar or meter is: the text the
// page gives for it, else its value as a share of its range, "" when the page
// says neither, as a progress bar that only shows that work goes on. Native
// elements read as the browser draws them: a range of 0 to 1 unless one is
// set, and never past either end.
func (c *compiler) progressValue(n int32) string {
	if t := c.attr(n, "aria-valuetext"); strings.TrimSpace(t) != "" {
		return "=" + quoteText(t, 60)
	}
	native := c.s.Tag[n] == "progress" || c.s.Tag[n] == "meter"
	num := func(aria, attr string) (float64, bool) {
		v := c.attr(n, aria)
		if v == "" && native {
			v = c.attr(n, attr)
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	}
	now, ok := num("aria-valuenow", "value")
	if !ok {
		return ""
	}
	lo, hi := 0.0, 100.0
	if native {
		hi = 1
	}
	if v, ok := num("aria-valuemin", "min"); ok && c.s.Tag[n] != "progress" {
		lo = v
	}
	if v, ok := num("aria-valuemax", "max"); ok && (!native || v > lo) {
		hi = v
	}
	if hi <= lo {
		return "=" + strconv.FormatFloat(now, 'f', -1, 64)
	}
	share := math.Min(math.Max((now-lo)/(hi-lo), 0), 1)
	return "=" + strconv.FormatFloat(math.Round(share*100), 'f', -1, 64) + "%"
}
