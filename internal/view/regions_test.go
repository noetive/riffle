package view_test

import (
	"fmt"
	"testing"

	"github.com/noetive/riffle/internal/facts"
	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/view"
)

func regionsPage() *pb.Builder {
	b := pb.New(1280, 800)
	body := b.Body()
	b.El(body, "main", R(0, 0, 100, 20))
	b.El(body, "table", R(0, 30, 100, 20))
	b.El(body, "section", R(0, 60, 100, 20), pb.Attr("aria-label", "Named"))
	nav := b.El(body, "nav", R(0, 100, 600, 60))
	ul := b.El(nav, "ul", R(0, 100, 600, 60))
	for k := 0; k < 3; k++ {
		li := b.El(ul, "li", R(float64(k*100), 100, 100, 20))
		b.Text(b.El(li, "a", R(float64(k*100), 100, 80, 20), pb.Attr("href", "/x")), fmt.Sprintf("L%d", k))
	}
	b.Text(nav, "extra")
	nav2 := b.El(body, "nav", R(0, 200, 600, 60))
	b.Text(nav2, "a")
	b.Text(b.El(nav2, "p", R(0, 220, 100, 20)), "b")
	return b
}

func TestEmptyUnnamedRegionsAreLeftOutButAddressableOnesStay(t *testing.T) {
	sameLines(t, outlineOf(regionsPage()),
		`page shop.example/cart "Cart" 1280x800`,
		`table r1`,
		`region "Named"`,
		`nav r2 collapsed 3 links`,
		`nav r3 collapsed 2 nodes`,
	)
}

func TestExpandShowsWhatACollapsedRegionHides(t *testing.T) {
	p := facts.Analyze(regionsPage().Snapshot())
	base := view.Compile(p, view.Options{})
	sameLines(t, view.Compile(p, view.Options{Projection: view.Expand, Target: "r2", Previous: base.Refs}),
		`nav r2`,
		`  list x3`,
		`    item link a1 "L0"`,
		`    item link a2 "L1"`,
		`    item link a3 "L2"`,
		`  text "extra"`,
	)
}

func TestRefsOfCollapsedContentComeAfterEverythingVisible(t *testing.T) {
	b := regionsPage()
	vis := b.El(b.Body(), "a", R(0, 300, 100, 20), pb.Attr("href", "/v"))
	b.Text(vis, "Visible")
	p := facts.Analyze(b.Snapshot())
	v := view.Compile(p, view.Options{})
	lineWith(t, v, `link a1 "Visible"`)
	ex := view.Compile(p, view.Options{Projection: view.Expand, Target: "r2", Previous: v.Refs})
	lineWith(t, ex, `link a2 "L0"`, "item")
	lineWith(t, ex, `link a4 "L2"`)
}

func TestOutlineOfModalLayersListsTopMostFirstAndKeepsEqualPaintInPageOrder(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	mk := func(label string, paint int32) {
		wrap := b.El(body, "div", R(0, 0, 1280, 800), pb.Position("fixed"), pb.Fill("rgba(0, 0, 0, 0.6)"), pb.Paint(paint))
		d := b.El(wrap, "div", R(300, 200, 400, 300), pb.Attr("role", "dialog"), pb.Attr("aria-label", label), pb.Position("fixed"), pb.Fill("rgb(255, 255, 255)"), pb.Paint(paint))
		b.Text(b.El(d, "button", R(320, 400, 80, 30)), "OK "+label)
	}
	mk("first", 50)
	mk("second", 50)
	mk("top", 70)
	v := outlineOf(b)
	iTop, iFirst, iSecond := indexOf(v, `"OK top"`), indexOf(v, `"OK first"`), indexOf(v, `"OK second"`)
	if iTop < 0 || iTop >= iFirst || iFirst >= iSecond {
		t.Errorf("order top=%d first=%d second=%d:\n%s", iTop, iFirst, iSecond, v)
	}
}
