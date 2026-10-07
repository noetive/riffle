// Package view compiles analyzed pages into the line-oriented text an agent
// reads: an outline in visual order with short refs on actionable nodes and
// style facts as trailing tags, plus projections of the same tree and deltas
// between consecutive views.
package view

import (
	"strings"
	"unicode/utf8"

	"github.com/noetive/riffle/internal/facts"
	"github.com/noetive/riffle/internal/pagetext"
	"github.com/noetive/riffle/internal/snapshot"
)

// Projection selects what a compiled view contains.
type Projection int

// Projections of the one compiler.
const (
	// Outline is the full page: regions, headings, text and actionable nodes.
	Outline Projection = iota
	// Interactive lists only actionable nodes, flat, with their facts.
	Interactive
	// Read is the main content as Markdown, with emphasis from style.
	Read
	// Table is the region named by Options.Target as TSV, whatever its markup.
	Table
	// Find lists nodes whose text or name contains Options.Query.
	Find
	// Expand renders the region named by Options.Target at full detail.
	Expand
)

// Options controls one compilation.
type Options struct {
	// Previous is the Refs table of the view this agent saw last. Nodes keep
	// the refs they had there, and numbers are never reused for other nodes.
	Previous map[string]int64
	// Query is the text searched by Find, matched case-insensitively.
	Query string
	// Target is the ref consumed by Table and Expand.
	Target     string
	Projection Projection
	// Budget is the token limit (about 4 characters per token); 0 means none.
	Budget int
	// Focus is the backend id of the focused node, 0 for none. Its region is
	// kept ahead of other content when the budget is tight.
	Focus int64
}

// Line is one output line. Lines carry the identity of the node they
// describe so views can be compared.
type Line struct {
	// Ref is the ref of the node when it has one.
	Ref string
	// Label is the leading words: kind and ref, such as "button b1".
	Label string
	// Body is the quoted name or text.
	Body string
	// Tags are the trailing fact tags.
	Tags []string
	// Key is the backend id of the node the line describes, 0 when the line
	// is a summary such as the page header.
	Key int64
	// Indent is the nesting depth in two-space steps.
	Indent int

	// anchor is the backend id of the element that expand opens to show the
	// whole of the line, when that is not Key: a run of text is keyed by its
	// first text node, which is no element, and anchors on the block that holds
	// it.
	anchor int64
	class  int // drop order, higher is dropped first
	rank   int // within a class, lower is kept first: 0 for headings
	parent int // index of the enclosing line, -1 for none
}

// expandKey is the backend id a ref for this line is made for.
func (l Line) expandKey() int64 {
	if l.anchor != 0 {
		return l.anchor
	}
	return l.Key
}

// String renders the line with its indentation.
func (l Line) String() string {
	return strings.Repeat("  ", l.Indent) + l.Text()
}

// Text renders the line without indentation. It is always one line: page
// text is cleaned where it is read, and anything that slipped past is
// flattened here, so a page can never write a line of its own.
func (l Line) Text() string {
	var parts []string
	if l.Label != "" {
		parts = append(parts, l.Label)
	}
	if l.Body != "" {
		parts = append(parts, l.Body)
	}
	parts = append(parts, l.Tags...)
	return pagetext.Flatten(strings.Join(parts, " "))
}

// View is a compiled projection.
type View struct {
	// Refs maps every ref handed out while compiling to its backend id. Pass
	// it as Options.Previous when compiling the next view.
	Refs  map[string]int64
	Lines []Line
	// Full is every line of the view before the budget trimmed it. A change is
	// judged on the whole page, so a line the budget dropped is not a line the
	// page lost.
	Full []Line
}

// Resolve returns the backend id a ref names in this view.
func (v *View) Resolve(ref string) (backend int64, ok bool) {
	backend, ok = v.Refs[ref]
	return
}

// String renders all lines separated by newlines.
func (v *View) String() string {
	var sb strings.Builder
	for i, l := range v.Lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(l.String())
	}
	return sb.String()
}

// Tokens estimates the size of the rendered view at 4 characters per token.
func (v *View) Tokens() int { return tokensOf(v.chars()) }

func (v *View) chars() int {
	n := 0
	for _, l := range v.Lines {
		n += utf8.RuneCountInString(l.String()) + 1
	}
	return n
}

func tokensOf(chars int) int { return (chars + 3) / 4 }

// Compile turns analyzed facts into a view.
func Compile(p *facts.Page, opts Options) *View {
	c := &compiler{p: p, s: p.Snap, opts: opts, rt: newRefTable(opts.Previous), rootNode: snapshot.None}
	c.labels = map[int32]bool{}
	for i := range p.Nodes {
		if l := p.Nodes[i].Label; l != snapshot.None && p.Nodes[i].Visible() {
			c.labels[l] = true
		}
	}
	main, modals := c.build()
	c.classify(main, modals)
	c.assign(main, modals)

	var lines []Line
	switch opts.Projection {
	case Interactive:
		lines = c.interactive(main, modals)
	case Read:
		lines = c.read(main, modals)
	case Table:
		lines = c.table()
	case Find:
		lines = c.find()
	case Expand:
		lines = c.expand()
	default:
		lines = c.outline(main, modals)
	}
	full := lines
	lines = c.fit(lines)
	return &View{Lines: lines, Full: full, Refs: c.rt.table()}
}
