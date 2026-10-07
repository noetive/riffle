package facts_test

import (
	"testing"

	"github.com/noetive/riffle/internal/facts"
	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
)

func buttonWith(label, text string) (*facts.Page, int32) {
	b := pb.New(1280, 800)
	attrs := []pb.Opt{}
	if label != "" {
		attrs = append(attrs, pb.Attr("aria-label", label))
	}
	btn := b.El(b.Body(), "button", pb.Rect(10, 10, 120, 30), attrs...)
	b.Text(btn, text)
	return facts.Analyze(b.Snapshot()), btn
}

// A control whose hidden label contradicts the words on it is a second
// channel a person never reads; the page says one thing and the agent hears
// another.
func TestLabelThatContradictsVisibleTextIsFlagged(t *testing.T) {
	p, btn := buttonWith("Confirm payment", "Cancel")
	if got := p.Nodes[btn].Shown; got != "Cancel" {
		t.Fatalf("Shown = %q, want the words a person reads", got)
	}
}

func TestAgreeingOrIconOnlyLabelsAreNotFlagged(t *testing.T) {
	for _, c := range []struct{ label, text string }{
		{"Add to cart", "Add"},
		{"Close", "×"},
		{"Menu", "☰"},
		{"Search", "Search"},
		{"", "Plain"},
	} {
		p, btn := buttonWith(c.label, c.text)
		if p.Nodes[btn].Shown != "" {
			t.Errorf("label %q with text %q must not be flagged, Shown = %q", c.label, c.text, p.Nodes[btn].Shown)
		}
	}
}

func TestLabelDifferenceIsCaseAndSpaceInsensitiveAndContainmentTolerant(t *testing.T) {
	for _, c := range []struct {
		label, text string
		flagged     bool
	}{
		{"SEARCH", "search", false},
		{"Add  to   cart", "add to cart", false},
		{"Add to cart", "Add to cart now", false},
		{"Add to cart now", "to cart", false},
		{"Pay", "Cancel", true},
		{"Pay", "42", true},
		{"Pay", "×2", true},
		{"Pay", "★", false},
		{"Pay", "٣", true},
		// Punctuation and symbols on a control need not be in its name (WCAG 2.5.3,
		// Understanding Label in Name): the words are what is compared.
		{"Next page", "Next ›", false},
		{"Email", "E-mail", false},
		{"Sign in", "Sign-in", false},
		{"Pay now", "Pay now!", false},
		{"Go to cart", "Cart →", false},
		{"Pay", "Cancel!", true},
		{"Next page", "‹ Previous", true},
		// A vowel sign is part of the word: these differ only by one.
		{"कम", "काम", true},
	} {
		p, btn := buttonWith(c.label, c.text)
		if got := p.Nodes[btn].Shown != ""; got != c.flagged {
			t.Errorf("label %q text %q: flagged = %v, want %v", c.label, c.text, got, c.flagged)
		}
	}
}
