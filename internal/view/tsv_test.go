package view_test

import (
	"testing"

	"github.com/noetive/riffle/internal/facts"
	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/snapshot"
	"github.com/noetive/riffle/internal/view"
)

func tsvOf(b *pb.Builder, target int32) *view.View {
	p := facts.Analyze(b.Snapshot())
	return view.Compile(p, view.Options{Projection: view.Table, Target: "r1", Previous: map[string]int64{"r1": p.Snap.Backend[target]}})
}

func TestTableTakesCellsFromRolesNestedWrappersAndBareText(t *testing.T) {
	b := pb.New(1280, 800)
	g := b.El(b.Body(), "div", R(0, 0, 600, 100))
	// A cell by role holding several parts is one cell.
	c1 := b.El(g, "div", R(0, 0, 100, 20), pb.Attr("role", "cell"))
	b.Text(b.El(c1, "div", R(0, 0, 50, 20)), "two")
	b.Text(b.El(c1, "div", R(50, 0, 50, 20)), "parts")
	// A cell with only inline content is one cell.
	c2 := b.El(g, "div", R(200, 0, 100, 20))
	b.Text(b.El(c2, "span", R(200, 0, 50, 20), pb.Inline()), "in")
	b.Text(b.El(c2, "span", R(250, 0, 50, 20), pb.Inline()), "line")
	// Text straight in the container is a cell.
	b.Text(g, "bare")
	b.Snapshot().Box[len(b.Snapshot().Box)-1] = R(400, 0, 32, 16)
	// Wrappers are looked through.
	wrap := b.El(g, "div", R(0, 40, 600, 20))
	b.Text(b.El(wrap, "div", R(0, 40, 100, 20), pb.Attr("role", "gridcell")), "g1")
	b.Text(b.El(wrap, "div", R(100, 40, 100, 20), pb.Attr("role", "columnheader")), "g2")
	b.Text(b.El(wrap, "div", R(200, 40, 100, 20), pb.Attr("role", "rowheader")), "g3")
	sameLines(t, tsvOf(b, g), "two parts\tinline\tbare", "g1\tg2\tg3")
}

func TestTableIgnoresHiddenUnseenAndEmptyCells(t *testing.T) {
	b := pb.New(1280, 800)
	g := b.El(b.Body(), "div", R(0, 0, 600, 100))
	b.Text(b.El(g, "div", R(0, 0, 100, 20)), "keep")
	b.Text(b.El(g, "div", R(100, 0, 100, 20), pb.NotLaid()), "hidden")
	b.Text(b.El(g, "div", R(-5000, 0, 100, 20)), "offscreen")
	b.Text(b.El(g, "div", R(200, 0, 100, 20), pb.Style(snapshot.FontSize, "0px")), "tiny")
	b.Text(b.El(g, "div", R(300, 0, 100, 20)), "   ")
	b.Text(b.El(g, "div", R(400, 0, 100, 20)), "also")
	b.El(g, "div", R(500, 0, 100, 20))
	sameLines(t, tsvOf(b, g), "keep\talso")
}

func TestTableRowsGroupByHalfTheSmallerCellHeight(t *testing.T) {
	b := pb.New(1280, 800)
	g := b.El(b.Body(), "div", R(0, 0, 600, 200))
	add := func(x, y, h float64, s string) { b.Text(b.El(g, "div", R(x, y, 50, h)), s) }
	add(0, 0, 40, "r1a")
	add(100, 19, 40, "r1b") // starts inside the first half of a 40px cell
	add(200, 20, 40, "r2a") // starts at half: a new row
	add(0, 20, 40, "r2b")
	sameLines(t, tsvOf(b, g), "r1a\tr1b", "r2b\tr2a")
}
