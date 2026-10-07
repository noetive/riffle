package view_test

import (
	"strings"
	"testing"

	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/view"
)

// Embedded documents are not read, and the agent is told so instead of
// finding nothing where a login or payment form would be.
func TestEmbeddedDocumentsAreReportedAsUnread(t *testing.T) {
	b := pb.New(1280, 800)
	b.El(b.Body(), "iframe", R(100, 100, 400, 300), pb.Attr("src", "https://pay.example/widget?x=1"))
	b.El(b.Body(), "iframe", R(100, 420, 400, 100), pb.Attr("title", "Support chat"), pb.Attr("src", "https://chat.example/"))
	out := outline(b, view.Options{}).String()
	for _, want := range []string{`frame "pay.example" content-not-shown`, `frame "Support chat" content-not-shown`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestHiddenEmbeddedDocumentsAreNotReported(t *testing.T) {
	b := pb.New(1280, 800)
	b.El(b.Body(), "iframe", R(0, 0, 0, 0), pb.Attr("src", "https://tracker.example/pixel"))
	if out := outline(b, view.Options{}).String(); strings.Contains(out, "frame") {
		t.Errorf("a zero-size frame is not something to act on:\n%s", out)
	}
}

// Frames are inline elements, and an inline element after a paragraph must
// not be folded into text and lost.
func TestInlineEmbeddedDocumentsAfterTextAreStillReported(t *testing.T) {
	b := pb.New(1280, 800)
	b.Text(b.El(b.Body(), "p", R(0, 0, 400, 20)), "before")
	b.El(b.Body(), "iframe", R(0, 40, 400, 200), pb.Inline(), pb.Attr("title", "Outer Frame"), pb.Attr("srcdoc", "<p>x</p>"))
	b.El(b.Body(), "iframe", R(0, 260, 400, 200), pb.Inline(), pb.Attr("title", "Src frame"), pb.Attr("src", "https://example.com"))
	for name, proj := range map[string]view.Projection{"outline": view.Outline, "interactive": view.Interactive} {
		out := outline(b, view.Options{Projection: proj}).String()
		for _, want := range []string{`frame "Outer Frame" content-not-shown`, `frame "Src frame" content-not-shown`} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: missing %q in:\n%s", name, want, out)
			}
		}
	}
}

// A frame with no title and no address has no name; the view does not put
// words of its own where the page's would be quoted.
func TestUnnamedEmbeddedDocumentsHaveNoQuotedName(t *testing.T) {
	b := pb.New(1280, 800)
	b.El(b.Body(), "iframe", R(0, 0, 400, 200))
	for name, proj := range map[string]view.Projection{"outline": view.Outline, "interactive": view.Interactive, "read": view.Read} {
		out := outline(b, view.Options{Projection: proj}).String()
		if !strings.Contains("\n"+out, "\nframe content-not-shown") || strings.Contains(out, `"`+"embedded") {
			t.Errorf("%s: want an unnamed frame line:\n%s", name, out)
		}
	}
}

// Read says what it cannot read, in the same words as the outline, also when
// a frame is all there is on the page.
func TestReadReportsEmbeddedDocuments(t *testing.T) {
	b := pb.New(1280, 800)
	b.El(b.Body(), "iframe", R(0, 0, 400, 200), pb.Attr("title", "Card form"))
	b.El(b.Body(), "iframe", R(0, 220, 400, 200), pb.Attr("src", "https://pay.example/x"))
	b.El(b.Body(), "iframe", R(0, 440, 400, 100))
	sameLines(t, readOf(b),
		`frame "Card form" content-not-shown`,
		`frame "pay.example" content-not-shown`,
		`frame content-not-shown`,
	)
}

func TestReadFrameInAListItemIsNotItemText(t *testing.T) {
	b := pb.New(1280, 800)
	ul := b.El(b.Body(), "ul", R(0, 0, 600, 300))
	li := b.El(ul, "li", R(0, 0, 600, 100))
	b.Text(b.El(li, "p", R(0, 0, 300, 20)), "Pay by card")
	b.El(li, "iframe", R(0, 30, 300, 60), pb.Attr("title", "Card form"))
	li2 := b.El(ul, "li", R(0, 120, 600, 100))
	b.El(li2, "iframe", R(0, 120, 300, 60))
	sameLines(t, readOf(b),
		"- Pay by card",
		`  frame "Card form" content-not-shown`,
		`frame content-not-shown`,
	)
}

// Frames are listed so the agent learns a form exists, but they have no ref
// and are not actionable nodes.
func TestInteractiveListsFramesWithoutRefsOrCount(t *testing.T) {
	b := pb.New(1280, 800)
	b.El(b.Body(), "iframe", R(0, 0, 400, 200), pb.Attr("title", "Sign in"))
	out := outline(b, view.Options{Projection: view.Interactive})
	sameLines(t, out, `page shop.example/cart "Cart" 1280x800`, `frame "Sign in" content-not-shown`, "interactive 0 actionable nodes")
	if len(out.Refs) != 0 {
		t.Errorf("a frame has no ref: %v", out.Refs)
	}
}

func TestReadFrameLineIdentifiesTheFrame(t *testing.T) {
	b := pb.New(1280, 800)
	f := b.El(b.Body(), "iframe", R(0, 0, 400, 200), pb.Attr("title", "Card form"), pb.ID(777))
	_ = f
	v := readOf(b)
	if len(v.Lines) != 1 || v.Lines[0].Key != 777 {
		t.Errorf("the frame line is keyed %+v, want backend 777", v.Lines)
	}
}
