package view_test

import (
	"strings"
	"testing"

	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/snapshot"
)

func TestEmbeddedDocumentIsNamedByTitleThenHostThenGenerically(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	b.El(body, "iframe", R(0, 0, 100, 100), pb.Attr("title", "  Pay  now "), pb.Attr("src", "https://pay.example/x"))
	b.El(body, "iframe", R(0, 110, 100, 100), pb.Attr("title", "   "), pb.Attr("src", "https://host.example/y"))
	b.El(body, "iframe", R(0, 220, 100, 100), pb.Attr("title", "   "), pb.Attr("src", "nothost"))
	b.El(body, "iframe", R(0, 330, 100, 100))
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`frame "Pay now" content-not-shown`,
		`frame "host.example" content-not-shown`,
		`frame content-not-shown`,
		`frame content-not-shown`,
	)
}

func TestInlineStyleSplitsATextRunIntoStretches(t *testing.T) {
	b := pb.New(1280, 800)
	p := b.El(b.Body(), "p", R(0, 300, 600, 40))
	b.Text(p, "Plain ")
	b.Text(b.El(p, "b", R(50, 300, 30, 20), pb.Inline(), pb.Style(snapshot.FontWeight, "700")), "bold")
	b.El(p, "br", R(80, 300, 0, 0), pb.Inline())
	b.Text(b.El(p, "s", R(0, 320, 30, 20), pb.Inline(), pb.Style(snapshot.TextDecorationLine, "line-through")), "gone")
	b.Text(p, " end")
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`text "Plain" "bold" "gone" strike "end"`,
	)
}

func TestClickHandlerOnLongTextIsAControlUnlessItIsDelegation(t *testing.T) {
	long := strings.Repeat("w", 70)
	short := strings.Repeat("w", 60)

	b := pb.New(1280, 800)
	b.Text(b.El(b.Body(), "div", R(0, 0, 300, 20), pb.Clickable()), long)
	sameLines(t, outlineOf(b), `page shop.example/cart "Cart" 1280x800`, `clickable b1`, `  text "`+long+`"`)

	b = pb.New(1280, 800)
	b.Text(b.El(b.Body(), "div", R(0, 0, 300, 20), pb.Clickable()), short)
	sameLines(t, outlineOf(b), `page shop.example/cart "Cart" 1280x800`, `clickable b1 "`+short+`"`)

	// As big as half the page, a handler is delegation.
	b = pb.New(1280, 800)
	b.Text(b.El(b.Body(), "div", R(0, 0, 1280, 400), pb.Clickable()), long)
	if out := outlineOf(b).String(); strings.Contains(out, "clickable") {
		t.Errorf("half-page handler is not a control:\n%s", out)
	}

	// A hair smaller is a control.
	b = pb.New(1280, 800)
	b.Text(b.El(b.Body(), "div", R(0, 0, 1280, 399), pb.Clickable()), long)
	lineWith(t, outlineOf(b), "clickable b1")

	// Delegation also needs a viewport to compare with.
	b = pb.New(1280, 800)
	b.Snapshot().ViewportW, b.Snapshot().ViewportH = 0, 0
	b.Text(b.El(b.Body(), "div", R(0, 0, 1280, 400), pb.Clickable()), long)
	lineWith(t, outlineOf(b), "clickable b1")
}

func TestClickHandlerAroundAControlLeavesTheRefToTheControl(t *testing.T) {
	b := pb.New(1280, 800)
	wrap := b.El(b.Body(), "div", R(0, 0, 300, 40), pb.Clickable())
	b.Text(b.El(wrap, "a", R(0, 0, 100, 20), pb.Attr("href", "/x")), "inner")
	sameLines(t, outlineOf(b), `page shop.example/cart "Cart" 1280x800`, `link a1 "inner"`)
}

func TestImagesWithoutANameAreLeftOut(t *testing.T) {
	b := pb.New(1280, 800)
	b.El(b.Body(), "img", R(0, 0, 20, 20))
	b.El(b.Body(), "img", R(0, 30, 20, 20), pb.Attr("alt", "Chart"))
	sameLines(t, outlineOf(b), `page shop.example/cart "Cart" 1280x800`, `img "Chart"`)
}

func TestNamesAreClippedForDisplay(t *testing.T) {
	b := pb.New(1280, 800)
	b.Text(b.El(b.Body(), "button", R(0, 0, 100, 20)), strings.Repeat("n", 80))
	b.Text(b.El(b.Body(), "button", R(0, 30, 100, 20)), strings.Repeat("m", 81))
	b.El(b.Body(), "input", R(0, 60, 100, 20), pb.Attr("type", "text"), pb.Attr("aria-label", "Q"), pb.Value(strings.Repeat("v", 61)))
	b.El(b.Body(), "input", R(0, 90, 100, 20), pb.Attr("type", "text"), pb.Attr("aria-label", "R"), pb.Value(strings.Repeat("v", 60)))
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`button b1 "`+strings.Repeat("n", 80)+`"`,
		`button b2 "`+strings.Repeat("m", 80)+`…"`,
		`textbox f1 "Q" ="`+strings.Repeat("v", 60)+`…"`,
		`textbox f2 "R" ="`+strings.Repeat("v", 60)+`"`,
	)
}

// A clipped run ends in the ref that expand opens to read the rest.
func TestLongTextRunsAreClippedAtTwoHundred(t *testing.T) {
	b := pb.New(1280, 800)
	b.Text(b.El(b.Body(), "p", R(0, 0, 600, 20)), strings.Repeat("t", 201))
	sameLines(t, outlineOf(b), `page shop.example/cart "Cart" 1280x800`, `text "`+strings.Repeat("t", 200)+`…" r1`)
}

func TestCoveredNodesNameTheirCoverOnlyWhenItHasARef(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	b.Text(b.El(body, "button", R(100, 100, 100, 40)), "Under")
	over := b.El(body, "div", R(90, 90, 200, 100), pb.Position("absolute"), pb.Fill("rgb(255, 255, 255)"))
	b.Text(over, "Overlay text")
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`text "Overlay text"`,
		`button b1 "Under" covered`,
	)

	c := pb.New(1280, 800)
	b2 := c.El(c.Body(), "button", R(100, 100, 100, 40))
	c.Text(b2, "Under")
	top := c.El(c.Body(), "button", R(90, 90, 200, 100), pb.Position("absolute"), pb.Fill("rgb(255, 255, 255)"))
	c.Text(top, "Top")
	out := outlineOf(c).String()
	if !strings.Contains(out, `button b2 "Under" covered-by=b1`) {
		t.Errorf("a covering control is named by its ref:\n%s", out)
	}
}

func TestTextThatNamesAControlIsNotRepeatedAsText(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	b.Text(b.El(body, "span", R(0, 0, 40, 20), pb.Inline()), "Qty")
	b.El(body, "input", R(50, 0, 100, 20), pb.Attr("type", "number"))
	b.Text(b.El(body, "span", R(0, 100, 40, 20), pb.Inline()), "Gone")
	b.El(body, "input", R(50, 100, 100, 20), pb.Attr("type", "number"), pb.NotLaid())
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`spinbutton f1 "Qty"`,
		`text "Gone"`,
	)
}

func TestSelectOptionsAreNotListedSeparately(t *testing.T) {
	b := pb.New(1280, 800)
	sel := b.El(b.Body(), "select", R(0, 0, 100, 20), pb.Attr("aria-label", "Size"), pb.Value("M"))
	for k, s := range []string{"S", "M", "L"} {
		b.Text(b.El(sel, "option", R(0, float64(20+k*20), 100, 20)), s)
	}
	sameLines(t, outlineOf(b), `page shop.example/cart "Cart" 1280x800`, `select f1 "Size" ="M"`)
}

// Text exactly as long as a line shows is whole: no cut, and no ref to open it.
func TestTextThatFitsTheLineExactlyIsWhole(t *testing.T) {
	b := pb.New(1280, 800)
	b.Text(b.El(b.Body(), "p", R(0, 0, 600, 20)), strings.Repeat("t", 200))
	sameLines(t, outlineOf(b), `page shop.example/cart "Cart" 1280x800`, `text "`+strings.Repeat("t", 200)+`"`)
}
