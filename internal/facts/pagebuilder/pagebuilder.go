// Package pagebuilder assembles hand-made snapshots for tests of the fact
// and view layers, so they run without a browser. Nodes get a plausible
// default style (black 16px block text on white) and paint in creation order.
package pagebuilder

import (
	"strconv"
	"strings"

	"github.com/noetive/riffle/internal/snapshot"
)

// Builder grows a snapshot node by node in document order.
type Builder struct {
	s    *snapshot.Snapshot
	next int64
}

// Opt adjusts a node right after creation.
type Opt func(b *Builder, i int32)

// New starts a document with a viewport of the given size. Node 0 is the
// document and node 1 the body, filling the viewport.
func New(vw, vh float64) *Builder {
	b := &Builder{s: &snapshot.Snapshot{URL: "https://shop.example/cart", Title: "Cart", ViewportW: vw, ViewportH: vh, ContentW: vw, ContentH: vh}, next: 100}
	b.add(snapshot.None, snapshot.KindDocument, "", "")
	b.El(0, "body", snapshot.Rect{W: vw, H: vh})
	return b
}

// Body returns the index of the body element.
func (b *Builder) Body() int32 { return 1 }

// Snapshot returns the snapshot built so far.
func (b *Builder) Snapshot() *snapshot.Snapshot { return b.s }

func (b *Builder) add(parent int32, kind snapshot.NodeKind, tag, text string) int32 {
	s := b.s
	if last := int32(len(s.Parent)) - 1; parent != snapshot.None && last >= 0 {
		for p := last; p != parent; p = s.Parent[p] {
			if p == snapshot.None {
				panic("pagebuilder: nodes must be added in document order, parent is not on the open path")
			}
		}
	}
	i := int32(len(s.Parent))
	s.Parent = append(s.Parent, parent)
	s.Kind = append(s.Kind, kind)
	s.Tag = append(s.Tag, tag)
	s.Text = append(s.Text, text)
	s.Attrs = append(s.Attrs, nil)
	s.Backend = append(s.Backend, b.next)
	b.next++
	s.Clickable = append(s.Clickable, false)
	s.Ticked = append(s.Ticked, false)
	s.Pseudo = append(s.Pseudo, "")
	s.Value = append(s.Value, "")
	s.Laid = append(s.Laid, kind != snapshot.KindDocument && (parent == snapshot.None || s.Kind[parent] == snapshot.KindDocument || s.Laid[parent]))
	s.Box = append(s.Box, snapshot.Rect{})
	var st [snapshot.NumProps]string
	if parent != snapshot.None {
		st = s.Style[parent]
		st[snapshot.Position] = "static"
		st[snapshot.BackgroundColor] = "rgba(0, 0, 0, 0)"
		st[snapshot.OverflowX] = "visible"
		st[snapshot.OverflowY] = "visible"
		st[snapshot.TextDecorationLine] = "none"
		st[snapshot.TextOverflow] = "clip"
		st[snapshot.LineClamp] = "none"
	} else {
		st = [snapshot.NumProps]string{
			snapshot.Visibility: "visible", snapshot.Opacity: "1", snapshot.Color: "rgb(33, 33, 33)",
			snapshot.FontSize: "16px", snapshot.FontWeight: "400", snapshot.Cursor: "auto",
			snapshot.PointerEvents: "auto",
		}
	}
	st[snapshot.Display] = "block"
	st[snapshot.Opacity] = "1"
	s.Style = append(s.Style, st)
	s.Paint = append(s.Paint, i)
	s.Scroll = append(s.Scroll, snapshot.Rect{})
	bg := "rgb(255, 255, 255)"
	if parent != snapshot.None {
		bg = s.Background[parent]
		if f := s.Style[parent][snapshot.BackgroundColor]; f != "" && f != "rgba(0, 0, 0, 0)" {
			bg = f
		}
	}
	s.Background = append(s.Background, bg)
	s.Children = append(s.Children, nil)
	if parent != snapshot.None {
		s.Children[parent] = append(s.Children[parent], i)
	}
	return i
}

// El adds an element with the given box.
func (b *Builder) El(parent int32, tag string, box snapshot.Rect, opts ...Opt) int32 {
	i := b.add(parent, snapshot.KindElement, tag, "")
	b.s.Box[i] = box
	behind := b.s.Background[i]
	for _, o := range opts {
		o(b, i)
	}
	// Chrome paints an opaque fill into the node's own blended background.
	if f := b.s.Style[i][snapshot.BackgroundColor]; b.s.Background[i] == behind && strings.HasPrefix(f, "rgb(") {
		b.s.Background[i] = f
	}
	return i
}

// Text adds a text node. Unless Box is given it is placed at the top-left of
// its parent and sized from the text length.
func (b *Builder) Text(parent int32, text string, opts ...Opt) int32 {
	i := b.add(parent, snapshot.KindText, "", text)
	pb := b.s.Box[parent]
	b.s.Box[i] = snapshot.Rect{X: pb.X, Y: pb.Y, W: float64(8 * len(text)), H: 16}
	for _, o := range opts {
		o(b, i)
	}
	return i
}

// Box sets the node's rectangle.
func Box(r snapshot.Rect) Opt { return func(b *Builder, i int32) { b.s.Box[i] = r } }

// Attr sets an attribute.
func Attr(name, value string) Opt {
	return func(b *Builder, i int32) {
		b.s.Attrs[i] = append(b.s.Attrs[i], snapshot.Attr{Name: name, Value: value})
	}
}

// Style sets a computed style property.
func Style(p snapshot.Prop, v string) Opt { return func(b *Builder, i int32) { b.s.Style[i][p] = v } }

// Inline marks the node as inline-level.
func Inline() Opt { return Style(snapshot.Display, "inline") }

// Fill sets the node's own background color.
func Fill(v string) Opt { return Style(snapshot.BackgroundColor, v) }

// Behind sets the blended background color at the node, its own fill included as Chrome reports it.
func Behind(v string) Opt { return func(b *Builder, i int32) { b.s.Background[i] = v } }

// Position sets the CSS position.
func Position(v string) Opt { return Style(snapshot.Position, v) }

// Paint overrides the paint order.
func Paint(n int32) Opt { return func(b *Builder, i int32) { b.s.Paint[i] = n } }

// Clickable reports a click listener on the node.
func Clickable() Opt { return func(b *Builder, i int32) { b.s.Clickable[i] = true } }

// Pseudo marks the node as generated content.
func Pseudo(kind string) Opt { return func(b *Builder, i int32) { b.s.Pseudo[i] = kind } }

// Ticked marks a checkbox or radio as checked.
func Ticked() Opt { return func(b *Builder, i int32) { b.s.Ticked[i] = true } }

// Value sets the current value of a form control.
func Value(v string) Opt { return func(b *Builder, i int32) { b.s.Value[i] = v } }

// Scroll sets the scrollable extent.
func Scroll(r snapshot.Rect) Opt { return func(b *Builder, i int32) { b.s.Scroll[i] = r } }

// Laid gives a node a box although its parent has none, as the content of a
// display:contents wrapper has.
func Laid() Opt { return func(b *Builder, i int32) { b.s.Laid[i] = true } }

// NotLaid removes the node's box, as display:none does.
func NotLaid() Opt { return func(b *Builder, i int32) { b.s.Laid[i] = false } }

// ID sets the backend identity, for tests that track a node across snapshots.
func ID(id int64) Opt { return func(b *Builder, i int32) { b.s.Backend[i] = id } }

// FontPx sets the font size in pixels.
func FontPx(px float64) Opt {
	return Style(snapshot.FontSize, strconv.FormatFloat(px, 'f', -1, 64)+"px")
}

// Rect builds a rectangle from position and size.
func Rect(x, y, w, h float64) snapshot.Rect { return snapshot.Rect{X: x, Y: y, W: w, H: h} }
