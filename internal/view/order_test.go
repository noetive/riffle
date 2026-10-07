package view_test

import (
	"strings"
	"testing"

	"github.com/noetive/riffle/internal/view"
)

// TestBudgetKeepOrderIsAFixedRanking compiles the same page at every budget
// and checks the ranking the design promises: modal layer, controls and
// headings, text, first rows of a list, navigation, footer, later rows.
func TestBudgetKeepOrderIsAFixedRanking(t *testing.T) {
	c := newCart(cartOpts{extra: 12})
	p := c.page()
	full := view.Compile(p, view.Options{})
	type seen struct{ header, h1, checkout, shipping, text, row1, row3, row4, rowLast, nav, footer bool }
	read := func(v *view.View) seen {
		s := v.String()
		return seen{
			header:   strings.Contains(s, "page shop"),
			h1:       strings.Contains(s, `h1 "Your cart"`),
			checkout: strings.Contains(s, `"Checkout"`),
			shipping: strings.Contains(s, "Free shipping"),
			text:     strings.Contains(s, "Review the items"),
			row1:     strings.Contains(s, `item "Trail shoe 42"`),
			row3:     strings.Contains(s, `"Extra item 0`),
			row4:     strings.Contains(s, `"Extra item 1`),
			rowLast:  strings.Contains(s, `"Extra item 11`),
			nav:      strings.Contains(s, "nav r1"),
			footer:   strings.Contains(s, "footer r"),
		}
	}
	implies := func(budget int, what string, a, b bool) {
		t.Helper()
		if a && !b {
			t.Errorf("budget %d: %s", budget, what)
		}
	}
	for budget := 12; budget <= full.Tokens(); budget++ {
		s := read(view.Compile(p, view.Options{Budget: budget}))
		implies(budget, "text kept but the heading was dropped", s.text, s.h1)
		implies(budget, "a first row is kept but the controls outside the list were dropped", s.row1, s.checkout && s.h1)
		implies(budget, "footer kept while a first row was dropped", s.footer, s.row1)
		implies(budget, "a later row kept while the footer was dropped", s.row4, s.footer)
		implies(budget, "a later row kept while a first row was dropped", s.rowLast, s.row3 && s.row1)
		implies(budget, "the header must always be there", true, s.header)
	}
	whole := read(full)
	if !whole.nav || !whole.footer || !whole.rowLast || !whole.shipping {
		t.Fatalf("the full view lacks content: %+v", whole)
	}
}
