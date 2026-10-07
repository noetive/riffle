package view_test

import (
	"testing"

	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/snapshot"
)

func TestInlineMarkupJoinsTheSentenceWhateverItsDisplay(t *testing.T) {
	for _, tag := range []string{
		"b", "i", "em", "strong", "small", "code", "mark", "s", "del", "ins", "u", "abbr", "time",
		"sub", "sup", "label", "cite", "q", "kbd", "samp", "var", "font", "bdi", "span",
	} {
		b := pb.New(1280, 800)
		p := b.El(b.Body(), "p", R(0, 0, 600, 40))
		b.Text(p, "x ")
		b.Text(b.El(p, tag, R(20, 0, 20, 20)), "y")
		sameLines(t, outlineOf(b), `page shop.example/cart "Cart" 1280x800`, `text "x y"`)
	}
}

func TestBlockMarkupSplitsTheSentence(t *testing.T) {
	for _, tag := range []string{"div", "section", "p", "li", "td"} {
		b := pb.New(1280, 800)
		p := b.El(b.Body(), "p", R(0, 0, 600, 40))
		b.Text(p, "x ")
		b.Text(b.El(p, tag, R(20, 25, 20, 20)), "y")
		v := outlineOf(b)
		if indexOf(v, `text "x"`) < 0 || indexOf(v, `"y"`) < 0 || indexOf(v, `"x y"`) >= 0 {
			t.Errorf("%s must start a new line:\n%s", tag, v)
		}
	}
}

func TestInlineDisplayWithoutInlineTagAlsoJoins(t *testing.T) {
	b := pb.New(1280, 800)
	p := b.El(b.Body(), "p", R(0, 0, 600, 40))
	b.Text(p, "x ")
	b.Text(b.El(p, "div", R(20, 0, 20, 20), pb.Inline()), "y")
	sameLines(t, outlineOf(b), `page shop.example/cart "Cart" 1280x800`, `text "x y"`)

	c := pb.New(1280, 800)
	q := c.El(c.Body(), "p", R(0, 0, 600, 40))
	c.Text(q, "x ")
	c.Text(c.El(q, "div", R(20, 0, 20, 20), pb.Style(snapshot.Display, "inline-block")), "y")
	if v := outlineOf(c); indexOf(v, `"x y"`) >= 0 {
		t.Errorf("only display:inline joins a sentence:\n%s", v)
	}
}

func TestGeneratedContentJoinsTheSentence(t *testing.T) {
	b := pb.New(1280, 800)
	p := b.El(b.Body(), "p", R(0, 0, 600, 40))
	star := b.El(p, "span", R(0, 0, 20, 20), pb.Pseudo("before"))
	b.Snapshot().Text[star] = "★"
	b.Text(p, " after")
	sameLines(t, outlineOf(b), `page shop.example/cart "Cart" 1280x800`, `text "★ after"`)
}

func TestInlineElementsHoldingBlocksAreNotFlattened(t *testing.T) {
	b := pb.New(1280, 800)
	p := b.El(b.Body(), "p", R(0, 0, 600, 60))
	b.Text(p, "lead ")
	sp := b.El(p, "span", R(40, 0, 100, 40), pb.Inline())
	b.Text(b.El(sp, "div", R(40, 25, 100, 20)), "deep")
	v := outlineOf(b)
	if indexOf(v, "lead deep") >= 0 {
		t.Errorf("a block inside an inline element starts its own line:\n%s", v)
	}
}

func TestBlockChildrenOfAControlAreSeparatedByASpace(t *testing.T) {
	b := pb.New(1280, 800)
	cl := b.El(b.Body(), "div", R(0, 0, 300, 50), pb.Clickable())
	b.Text(b.El(cl, "div", R(0, 0, 300, 20)), "A")
	b.Text(b.El(cl, "div", R(0, 25, 300, 20)), "B")
	sameLines(t, outlineOf(b), `page shop.example/cart "Cart" 1280x800`, `clickable b1 "A B"`)

	c := pb.New(1280, 800)
	cl2 := c.El(c.Body(), "div", R(0, 0, 300, 50), pb.Clickable())
	c.Text(c.El(cl2, "span", R(0, 0, 30, 20), pb.Inline()), "A")
	c.Text(c.El(cl2, "span", R(30, 0, 30, 20), pb.Inline()), "B")
	sameLines(t, outlineOf(c), `page shop.example/cart "Cart" 1280x800`, `clickable b1 "AB"`)
}

func TestHeadingsOfInlineContentAreOneLine(t *testing.T) {
	b := pb.New(1280, 800)
	h3 := b.El(b.Body(), "h3", R(0, 0, 300, 20))
	b.Text(b.El(h3, "span", R(0, 0, 30, 20), pb.Inline()), "In")
	b.Text(b.El(h3, "em", R(30, 0, 30, 20), pb.Inline()), "line")
	h2 := b.El(b.Body(), "h2", R(0, 50, 300, 50))
	b.Text(b.El(h2, "div", R(0, 50, 300, 20)), "H1")
	b.Text(b.El(h2, "div", R(0, 75, 300, 20)), "H2")
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`h3 "Inline"`,
		`h2`,
		`  text "H1"`,
		`  text "H2"`,
	)
}

func TestStrikeAndToneOfAControlComeFromItsContentOnly(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	btn := b.El(body, "button", R(0, 0, 200, 20))
	b.Text(b.El(btn, "span", R(0, 0, 50, 20), pb.Inline(), pb.Style(snapshot.TextDecorationLine, "line-through")), "old")
	b.Text(b.El(btn, "span", R(60, 0, 50, 20), pb.Inline(), pb.Style(snapshot.Color, green)), "new")
	b.Text(b.El(body, "button", R(0, 30, 200, 20)), "plain")
	h := b.El(body, "h2", R(0, 60, 200, 20))
	b.Text(b.El(h, "span", R(0, 60, 50, 20), pb.Inline(), pb.Style(snapshot.Color, red)), "Hot")
	b.Text(b.El(body, "h2", R(0, 90, 200, 20)), "Cold")
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`button b1 "oldnew" strike green`,
		`button b2 "plain"`,
		`h2 "Hot" red`,
		`h2 "Cold"`,
	)
}

func TestUnseenContentDoesNotColourAControl(t *testing.T) {
	b := pb.New(1280, 800)
	btn := b.El(b.Body(), "button", R(0, 0, 200, 20))
	b.Text(btn, "Buy")
	b.Text(b.El(btn, "span", R(-5000, 0, 50, 20), pb.Inline(), pb.Style(snapshot.Color, red)), "secret")
	b.Text(b.El(btn, "span", R(0, 0, 50, 20), pb.Inline(), pb.Style(snapshot.TextDecorationLine, "line-through")), "was")
	sameLines(t, outlineOf(b), `page shop.example/cart "Cart" 1280x800`, `button b1 "Buywas" strike`, `unseen 1 node`)
}

func TestRunOfTextKeepsItsPlaceBetweenControlsByItsExtent(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	b.Text(b.El(body, "button", R(100, 0, 50, 20)), "Mid")
	p := b.El(body, "p", R(0, 0, 600, 20))
	t1 := b.Text(p, "far")
	t2 := b.Text(p, "near")
	b.Snapshot().Box[t1] = R(300, 0, 24, 16)
	b.Snapshot().Box[t2] = R(0, 0, 32, 16)
	v := outlineOf(b)
	if indexOf(v, `text "farnear"`) < 0 {
		t.Fatalf("run lost:\n%s", v)
	}
	if indexOf(v, `text "farnear"`) > indexOf(v, `"Mid"`) {
		t.Errorf("the run starts at its leftmost text, ahead of the button:\n%s", v)
	}
}

// Words a person sees apart are apart: two badges set side by side with a
// margin and no space in the markup read as two words, while a word split
// across touching elements still reads as one.
func TestInlineElementsApartOnTheLineReadAsSeparateWords(t *testing.T) {
	b := pb.New(1280, 800)
	p := b.El(b.Body(), "p", R(0, 0, 600, 20))
	b.Text(b.El(p, "span", R(0, 0, 44, 20), pb.Inline()), "Active")
	b.Text(b.El(p, "span", R(52, 0, 56, 20), pb.Inline()), "Inactive")
	q := b.El(b.Body(), "p", R(0, 40, 600, 20))
	b.Text(b.El(q, "span", R(0, 40, 22, 20), pb.Inline()), "Act")
	b.Text(b.El(q, "span", R(22, 40, 22, 20), pb.Inline()), "ive")
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`text "Active Inactive"`,
		`text "Active"`,
	)
}

// Text between two elements is what they are told apart from; markup that
// runs a word through elements and text stays one word.
func TestAWordRunThroughElementsAndTextStaysOneWord(t *testing.T) {
	b := pb.New(1280, 800)
	p := b.El(b.Body(), "p", R(0, 0, 600, 20))
	b.Text(b.El(p, "b", R(0, 0, 20, 20), pb.Inline()), "foo")
	b.Text(p, "bar", pb.Box(R(20, 0, 24, 20)))
	b.Text(b.El(p, "i", R(44, 0, 20, 20), pb.Inline()), "baz")
	sameLines(t, outlineOf(b),
		`page shop.example/cart "Cart" 1280x800`,
		`text "foobarbaz"`,
	)
}
