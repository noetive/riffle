// Package facts derives what a page looks like to a person from a snapshot:
// roles and names, visibility, occlusion, emphasis, state and association.
// Facts are baseline-relative: only deviations from the page norm are set.
package facts

import (
	"strings"

	"github.com/noetive/riffle/internal/pagetext"
	"github.com/noetive/riffle/internal/snapshot"
)

// Tone is a semantic color class that deviates from the page baseline.
type Tone string

// Tone values. ToneNone is the page baseline and is never reported.
const (
	ToneNone  Tone = ""
	ToneRed   Tone = "red"
	ToneGreen Tone = "green"
	ToneMuted Tone = "muted"
)

// Node holds the facts of one snapshot node, at the same index.
type Node struct {
	// Role is the ARIA-like role, "" for generic nodes.
	Role string
	// Name is the accessible name, "" when none could be derived.
	Name string
	// Shown is the text a person reads on the control when it differs from its
	// accessible name, such as an aria-label that says something else.
	Shown string
	// Text is the whitespace-collapsed content of a text node.
	Text string
	// Tone is the text color class when it deviates from the page baseline.
	Tone Tone
	// Expansion is "expanded" or "collapsed" for a control that discloses
	// content, else "".
	Expansion string

	// Heading is the visual heading level 1..6, 0 for none.
	Heading int

	// CoveredBy is the node whose later-painted box covers all sample points
	// of this node, or snapshot.None. When the coverer is part of a modal
	// layer it is the modal node.
	CoveredBy int32
	// Label and Error are text nodes associated by proximity, or snapshot.None.
	Label, Error int32

	// Hidden: generates no visible box (display, visibility, opacity, size).
	Hidden bool
	// Offscreen: the box lies outside the scrollable document.
	Offscreen bool
	// Clipped: an overflow-clipping ancestor leaves none of the box visible.
	Clipped bool
	// Holds: the box itself is out of reach, but it holds something drawn on
	// screen, such as a fixed dialog in an empty wrapper. It is listed for
	// what it holds and covers nothing with its own box.
	Holds bool
	// Unseen marks text a person cannot read although it is in the DOM: zero
	// size font, transparent or background-colored text, clipped, offscreen.
	// It is set on text nodes only and never together with Hidden.
	Unseen bool
	// Painted: a visible element that draws something a person sees on its own
	// box: a background, or replaced content such as an image.
	Painted bool
	// Primary: a button whose fill deviates from its sibling buttons.
	Primary bool
	// Strike: struck-through text.
	Strike bool
	// Truncated: text cut by ellipsis or line clamp.
	Truncated bool
	// Generated: text produced by a pseudo element.
	Generated bool

	Disabled bool
	Selected bool
	Checked  bool
	Pressed  bool
	Required bool
	Invalid  bool
	// Scrollable: the node scrolls its own overflowing content, so a wheel
	// over it moves that content and not the page.
	Scrollable bool
	// NameUnseen: the name comes from text a person cannot see, such as a
	// label replaced by an image. It is the site's own name, but unread.
	NameUnseen bool
	// Unlabelled: the control has no accessible name; it is named by the words
	// a person sees on it, which the site hid from assistive technology.
	Unlabelled bool
	// Modal: a dialog layer above the page.
	Modal bool
	// Covers: the modal (or its backdrop) spans most of the viewport.
	Covers bool

	// Clickable: a non-semantic node that reacts to clicks (listener or
	// pointer cursor on its own box).
	Clickable bool
	// Interactive: the role is an actionable control.
	Interactive bool
}

// Visible reports whether the node can be seen at all by a person.
func (n *Node) Visible() bool { return !n.Hidden && !n.Offscreen && !n.Clipped }

// Page is the analyzed snapshot.
type Page struct {
	Snap  *snapshot.Snapshot
	Nodes []Node // indexed like Snap
	// MedianFontSize is the text-weighted median font size, the baseline
	// against which visual headings are measured.
	MedianFontSize float64
	// UnseenCount is the number of text nodes flagged Unseen.
	UnseenCount int
}

// Analyze derives facts for every node of s. Node indexes must be in
// document order (a parent precedes its children and a subtree is contiguous).
func Analyze(s *snapshot.Snapshot) *Page {
	placeImageMapRegions(s)
	a := &analyzer{s: s, n: s.Len()}
	a.nodes = make([]Node, a.n)
	for i := range a.nodes {
		a.nodes[i].CoveredBy = snapshot.None
		a.nodes[i].Label = snapshot.None
		a.nodes[i].Error = snapshot.None
	}
	a.subtrees()
	a.visibility()
	a.textFacts()
	a.roles()
	a.names()
	a.states()
	a.emphasis()
	a.layers()
	a.association()
	p := &Page{Snap: s, Nodes: a.nodes, MedianFontSize: a.median}
	for i := range a.nodes {
		if a.nodes[i].Unseen {
			p.UnseenCount++
		}
	}
	return p
}

type analyzer struct {
	s      *snapshot.Snapshot
	ids    map[string]int32
	labels map[string][]int32 // label[for] -> label elements
	// backdrops maps a node that dims the page behind a modal to that modal.
	backdrops map[int32]int32
	// nativeModals are dialogs opened with showModal(): their ::backdrop has
	// no node, yet it makes everything outside them inert.
	nativeModals []int32
	nodes        []Node
	end          []int32 // exclusive end of each subtree
	gone         []bool  // removed from rendering, inherited by descendants
	// explicit marks controls named by label/aria rather than by proximity.
	explicit []bool
	dominant baselineText
	n        int
	median   float64
	// showUnseen lets content() read text a person cannot see.
	showUnseen bool
	// naming: content() computes a name, so the Accessible Name Computation's
	// rules for descendants apply: an aria-label stands for its element's
	// content, and aria-hidden content is left out unless an aria-labelledby
	// reference to a hidden node reached it (referenced). root is the node
	// content() began at.
	naming, referenced bool
	// labelled: an aria-label inside the content went into the name.
	labelled bool
	root     int32
}

func (a *analyzer) subtrees() {
	a.end = make([]int32, a.n)
	for i := range a.end {
		a.end[i] = int32(i) + 1
	}
	for i := a.n - 1; i >= 0; i-- {
		if p := a.s.Parent[i]; p != snapshot.None && a.end[p] < a.end[i] {
			a.end[p] = a.end[i]
		}
	}
	a.ids = map[string]int32{}
	a.labels = map[string][]int32{}
	for i := int32(0); i < int32(a.n); i++ {
		if a.s.Kind[i] != snapshot.KindElement {
			continue
		}
		if id, ok := a.s.Attr(i, "id"); ok && id != "" {
			if _, dup := a.ids[id]; !dup {
				a.ids[id] = i
			}
		}
		if a.s.Tag[i] == "label" {
			if f, ok := a.s.Attr(i, "for"); ok && f != "" {
				a.labels[f] = append(a.labels[f], i)
			}
		}
	}
}

// isAncestor reports whether x is a proper ancestor of y.
func (a *analyzer) isAncestor(x, y int32) bool { return x < y && y < a.end[x] }

func (a *analyzer) attr(i int32, name string) string {
	v, _ := a.s.Attr(i, name)
	return v
}

func (a *analyzer) hasAttr(i int32, name string) bool {
	_, ok := a.s.Attr(i, name)
	return ok
}

// style returns the computed style of i; text nodes fall back to their parent.
func (a *analyzer) style(i int32) *[snapshot.NumProps]string {
	if a.s.Kind[i] == snapshot.KindText && (!a.s.Laid[i] || a.s.Style[i][snapshot.FontSize] == "") {
		if p := a.s.Parent[i]; p != snapshot.None {
			return &a.s.Style[p]
		}
	}
	return &a.s.Style[i]
}

func collapse(s string) string {
	if s == "" {
		return ""
	}
	out := pagetext.Clean(s)
	if out == "" {
		if strings.TrimSpace(s) == "" {
			return " "
		}
		return ""
	}
	if r := s[0]; r == ' ' || r == '\n' || r == '\t' {
		out = " " + out
	}
	if r := s[len(s)-1]; r == ' ' || r == '\n' || r == '\t' {
		out += " "
	}
	return out
}

// words is page text as one line of plain words; see pagetext.Clean.
func words(s string) string { return pagetext.Clean(s) }
