package view_test

import (
	"testing"

	"github.com/noetive/riffle/internal/facts"
	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/view"
)

func findIn(b *pb.Builder, q string) *view.View {
	return view.Compile(facts.Analyze(b.Snapshot()), view.Options{Projection: view.Find, Query: q})
}

func TestFindListsMatchesInReadingOrderNotDocumentOrder(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	b.Text(b.El(body, "p", R(0, 200, 300, 20)), "needle low")
	b.Text(b.El(body, "p", R(0, 100, 300, 20)), "needle high")
	b.Text(b.El(body, "button", R(0, 150, 100, 20)), "needle mid")
	sameLines(t, findIn(b, "NEEDLE"),
		`find "NEEDLE" 3 matches`,
		`text "needle high"`,
		`button b1 "needle mid"`,
		`text "needle low"`,
	)
}

func TestFindMatchesControlsByNameAndShowsTheirRef(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	b.El(body, "input", R(0, 0, 100, 20), pb.Attr("type", "text"), pb.Attr("aria-label", "Search the shop"))
	b.Text(b.El(body, "a", R(0, 30, 100, 20), pb.Attr("href", "/s")), "Shop now")
	b.Text(b.El(body, "h1", R(0, 60, 100, 20)), "The shop")
	sameLines(t, findIn(b, "shop"),
		`find "shop" 3 matches`,
		`textbox f1 "Search the shop"`,
		`link a1 "Shop now"`,
		`text "The shop"`,
	)
}

func TestFindSkipsHiddenAndUnseenTextAndControls(t *testing.T) {
	b := pb.New(1280, 800)
	body := b.Body()
	b.Text(b.El(body, "p", R(0, 0, 300, 20), pb.NotLaid()), "needle hidden")
	b.Text(b.El(body, "p", R(-5000, 0, 300, 20)), "needle away")
	b.Text(b.El(body, "button", R(0, 30, 100, 20), pb.NotLaid()), "needle button")
	b.El(body, "button", R(0, 60, 100, 20), pb.NotLaid(), pb.Attr("aria-label", "needle named"))
	b.Text(b.El(body, "p", R(0, 90, 300, 20)), "needle shown")
	sameLines(t, findIn(b, "needle"), `find "needle" 1 matches`, `text "needle shown"`)
}

func TestFindReportsAControlOnceWhateverMatchesInsideIt(t *testing.T) {
	b := pb.New(1280, 800)
	btn := b.El(b.Body(), "button", R(0, 0, 200, 20))
	b.Text(b.El(btn, "span", R(0, 0, 60, 20), pb.Inline()), "hot ")
	b.Text(b.El(btn, "span", R(60, 0, 60, 20), pb.Inline()), "hotter")
	sameLines(t, findIn(b, "hot"), `find "hot" 1 matches`, `button b1 "hot hotter"`)
}

func TestFindFindsALinkInsideASentenceAsTheLink(t *testing.T) {
	b := pb.New(1280, 800)
	p := b.El(b.Body(), "p", R(0, 0, 600, 20))
	b.Text(p, "Read the ")
	b.Text(b.El(p, "a", R(80, 0, 80, 20), pb.Inline(), pb.Attr("href", "/spec")), "specification")
	sameLines(t, findIn(b, "specif"), `find "specif" 1 matches`, `link a1 "specification"`)
}

func TestFindRefsMatchTheOutline(t *testing.T) {
	c := newCart(cartOpts{extra: 2})
	p := c.page()
	base := view.Compile(p, view.Options{})
	f := view.Compile(p, view.Options{Projection: view.Find, Query: "remove", Previous: base.Refs})
	for ref, id := range f.Refs {
		if got, ok := base.Resolve(ref); !ok || got != id {
			t.Errorf("find handed out %s=%d but the outline has %d (%v)", ref, id, got, ok)
		}
	}
	if n := len(f.Lines) - 1; n != 4 {
		t.Errorf("four remove buttons expected, got %d:\n%s", n, f)
	}
}

func TestFindTrimsTheQueryButEchoesItCollapsed(t *testing.T) {
	b := pb.New(1280, 800)
	b.Text(b.El(b.Body(), "p", R(0, 0, 300, 20)), "needle in a haystack")
	sameLines(t, findIn(b, "  needle in "), `find "needle in" 1 matches`, `text "needle in a haystack"`)
}
