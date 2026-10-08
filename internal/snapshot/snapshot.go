// Package snapshot is the data model between the engine and the view
// compiler: a flat struct-of-arrays describing one document after cascade,
// layout and paint ordering. It carries no judgement about what the page
// means; facts are derived from it elsewhere.
package snapshot

// NodeKind distinguishes the node types the compiler cares about.
type NodeKind uint8

// Node kinds a snapshot distinguishes.
const (
	KindOther NodeKind = iota
	KindElement
	KindText
	KindDocument
)

// Rect is a box in CSS pixels, document coordinates.
type Rect struct {
	X, Y, W, H float64
}

// Empty reports whether the rectangle has no area.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Center returns the middle point of the rectangle.
func (r Rect) Center() (x, y float64) { return r.X + r.W/2, r.Y + r.H/2 }

// Intersects reports whether two rectangles overlap with non-zero area.
func (r Rect) Intersects(o Rect) bool {
	return r.X < o.X+o.W && o.X < r.X+r.W && r.Y < o.Y+o.H && o.Y < r.Y+r.H
}

// Attr is one element attribute.
type Attr struct {
	Name, Value string
}

// Prop indexes the fixed subset of computed style properties a snapshot keeps.
type Prop uint8

// Computed style properties a snapshot keeps.
const (
	Display Prop = iota
	Visibility
	Opacity
	Position
	ZIndex
	OverflowX
	OverflowY
	Color
	BackgroundColor
	FontSize
	FontWeight
	FontStyle
	TextDecorationLine
	TextOverflow
	WhiteSpace
	LineClamp
	Cursor
	PointerEvents
	ClipPath
	Float
	Transform
	TextIndent
	FlexDirection
	Order
	BackgroundImage
	BackgroundClip
	NumProps
)

// PropNames are the CSS names requested from the engine, indexed by Prop.
var PropNames = [NumProps]string{
	Display:            "display",
	Visibility:         "visibility",
	Opacity:            "opacity",
	Position:           "position",
	ZIndex:             "z-index",
	OverflowX:          "overflow-x",
	OverflowY:          "overflow-y",
	Color:              "color",
	BackgroundColor:    "background-color",
	FontSize:           "font-size",
	FontWeight:         "font-weight",
	FontStyle:          "font-style",
	TextDecorationLine: "text-decoration-line",
	TextOverflow:       "text-overflow",
	WhiteSpace:         "white-space",
	LineClamp:          "-webkit-line-clamp",
	Cursor:             "cursor",
	PointerEvents:      "pointer-events",
	ClipPath:           "clip-path",
	Float:              "float",
	Transform:          "transform",
	TextIndent:         "text-indent",
	FlexDirection:      "flex-direction",
	Order:              "order",
	BackgroundImage:    "background-image",
	BackgroundClip:     "background-clip",
}

// None marks an absent node index.
const None int32 = -1

// Snapshot is one document. Every slice indexed by node has length Len().
// Node indexes are positions in document order and are valid only for this
// snapshot; Backend ids are the engine's identity and survive between
// snapshots while the node lives.
type Snapshot struct {
	URL, Title string

	Parent    []int32
	Kind      []NodeKind
	Tag       []string // lower-case element name, "" for non-elements
	Text      []string // text node content, whitespace as laid out
	Attrs     [][]Attr
	Backend   []int64
	Clickable []bool   // the engine reports a click listener
	Ticked    []bool   // a checkbox or radio is checked, or an option selected, now, whatever its markup says
	Pseudo    []string // "before", "after", "marker" for generated boxes, else ""
	Value     []string // current value of form controls

	// Layout. Laid[i] is false for nodes that generate no box.
	Laid   []bool
	Box    []Rect
	Style  [][NumProps]string
	Paint  []int32 // paint order, higher paints later
	Scroll []Rect  // scrollable extent, empty when not a scroll container
	// Background is the blended background color at the node, its own fill
	// included. Chrome leaves it empty for most elements.
	Background []string

	// Children lists child indexes in DOM order.
	Children [][]int32

	// Viewport and scroll state of the document.
	ViewportW, ViewportH float64
	ScrollX, ScrollY     float64
	// Focus is the backend id of the element holding keyboard focus, 0 for none.
	Focus              int64
	ContentW, ContentH float64
}

// Len is the number of nodes.
func (s *Snapshot) Len() int { return len(s.Parent) }

// Attr returns the value of the named attribute and whether it is present.
func (s *Snapshot) Attr(i int32, name string) (string, bool) {
	for _, a := range s.Attrs[i] {
		if a.Name == name {
			return a.Value, true
		}
	}
	return "", false
}

// Prop returns a computed style property of node i, "" when the node has no box.
func (s *Snapshot) Prop(i int32, p Prop) string {
	if !s.Laid[i] {
		return ""
	}
	return s.Style[i][p]
}

// Ancestors calls fn for each ancestor of i, nearest first, until fn returns false.
func (s *Snapshot) Ancestors(i int32, fn func(int32) bool) {
	for p := s.Parent[i]; p != None; p = s.Parent[p] {
		if !fn(p) {
			return
		}
	}
}

// ByBackend returns the node index holding the engine identity, or None.
func (s *Snapshot) ByBackend(id int64) int32 {
	for i, b := range s.Backend {
		if b == id {
			return int32(i)
		}
	}
	return None
}
