package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/engine"
	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/snapshot"
)

// buttonID is a button whose engine identity the test controls, so a test can
// model a framework remounting the node under a new identity.
func buttonID(b *pb.Builder, parent int32, y float64, name string, id int64, opts ...pb.Opt) int32 {
	return button(b, parent, y, name, append([]pb.Opt{pb.ID(id)}, opts...)...)
}

func TestNewFillsInTheDefaultBudgetOnlyWhenNoneIsSet(t *testing.T) {
	e := newFake(shopPage)
	if got := New(e, Config{}).cfg.Budget; got != DefaultBudget {
		t.Errorf("default budget = %d, want %d", got, DefaultBudget)
	}
	if got := New(e, Config{Budget: 7}).cfg.Budget; got != 7 {
		t.Errorf("an explicit budget must be kept, got %d", got)
	}
}

func TestSessionBudgetBoundsTheDefaultView(t *testing.T) {
	page := func(e *fakeEngine, b *pb.Builder) {
		for i := 0; i < 40; i++ {
			tiny(b, float64(10+i*18), fmt.Sprintf("Item %d", i), int64(600+i))
		}
	}
	small := run(t, newSession(newFake(page), Config{Budget: 30}), "goto https://shop.example/cart").Text
	large := run(t, newSession(newFake(page), Config{Budget: 5000}), "goto https://shop.example/cart").Text
	if len(small) >= len(large) || !strings.Contains(large, "Item 39") || strings.Contains(small, "Item 39") {
		t.Errorf("the session budget must cap views (small=%d bytes, large=%d bytes)", len(small), len(large))
	}
	// A view that names its own budget overrides the session's.
	own := run(t, newSession(newFake(page), Config{Budget: 30}), "goto https://shop.example/cart\nview interactive budget=5000").Text
	if !strings.Contains(own, "Item 39") {
		t.Errorf("budget=N must override the session budget:\n%s", own)
	}
}

func TestParseErrorsAreReportedAsFailedAndStopped(t *testing.T) {
	out := run(t, newSession(newFake(shopPage), Config{}), "frobnicate everything")
	if !out.Stopped || !strings.HasPrefix(out.Text, "failed ") || !strings.Contains(out.Text, "frobnicate") {
		t.Errorf("parse failure reply = %+v", out)
	}
}

func TestProgramRunsUnderTheCallersContext(t *testing.T) {
	e := newFake(shopPage)
	ctx := context.WithValue(context.Background(), ctxKey, "caller")
	newSession(e, Config{}).Do(ctx, "goto https://shop.example/cart")
	if e.snapCtxValue != "caller" {
		t.Errorf("the engine must be driven with the caller's context, saw %v", e.snapCtxValue)
	}
}

func TestRunAndViewReturnTheReplyText(t *testing.T) {
	e := newFake(shopPage)
	s := newSession(e, Config{})
	if got := s.Run(context.Background(), "goto https://shop.example/cart"); !strings.Contains(got, `button b1 "Buy"`) {
		t.Errorf("Run = %q", got)
	}
	if got := s.View(context.Background(), "interactive"); !strings.Contains(got, `button b2 "Wishlist"`) {
		t.Errorf("View(interactive) = %q", got)
	}
	if got := s.View(context.Background(), ""); !strings.Contains(got, `button b1 "Buy"`) {
		t.Errorf("View with no projection must render the outline, got %q", got)
	}
	if got := s.View(context.Background(), "  interactive budget=900 "); strings.Contains(got, "failed") {
		t.Errorf("surrounding space must not break the view: %q", got)
	}
}

func TestCloseReleasesTheEngineAndAliveFollowsIt(t *testing.T) {
	e := newFake(shopPage)
	s := newSession(e, Config{})
	if !s.Alive() {
		t.Error("a live engine means a live session")
	}
	e.dead = true
	if s.Alive() {
		t.Error("a dead engine means a dead session")
	}
	s.Close()
	if e.closed != 1 {
		t.Errorf("engine closed %d times", e.closed)
	}
}

// slowEngine detects overlapping use of the engine.
type slowEngine struct {
	*fakeEngine
	inflight, overlaps atomic.Int32
}

func (e *slowEngine) Load(ctx context.Context, url string) error {
	if e.inflight.Add(1) > 1 {
		e.overlaps.Add(1)
	}
	time.Sleep(5 * time.Millisecond)
	e.inflight.Add(-1)
	return e.fakeEngine.Load(ctx, url)
}

func TestConcurrentCallsAreServedOneAtATime(t *testing.T) {
	e := &slowEngine{fakeEngine: newFake(shopPage)}
	s := New(e, Config{})
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Do(context.Background(), "goto https://shop.example/cart")
		}()
	}
	wg.Wait()
	if e.overlaps.Load() != 0 {
		t.Errorf("%d programs overlapped on one engine", e.overlaps.Load())
	}
}

// ---- refs across refreshes ----

func reorderingPage(flip *bool) func(e *fakeEngine, b *pb.Builder) {
	return func(e *fakeEngine, b *pb.Builder) {
		if *flip {
			buttonID(b, b.Body(), 100, "Wishlist", 500)
			buttonID(b, b.Body(), 150, "Buy", 400)
			return
		}
		buttonID(b, b.Body(), 100, "Buy", 400)
		buttonID(b, b.Body(), 150, "Wishlist", 500)
	}
}

func TestRefsFollowTheirNodesWhenThePageReordersWithinTheSameDocument(t *testing.T) {
	for name, url := range map[string]string{
		"same url":            "https://shop.example/cart",
		"fragment navigation": "https://shop.example/cart#details",
	} {
		flip := false
		e := newFake(reorderingPage(&flip))
		s := newSession(e, Config{})
		first := run(t, s, "goto https://shop.example/cart").Text
		if !strings.Contains(first, `button b1 "Buy"`) || !strings.Contains(first, `button b2 "Wishlist"`) {
			t.Fatalf("%s setup:\n%s", name, first)
		}
		flip, e.url = true, url
		for _, v := range []string{"interactive", "find \"Buy\"", "find \"Wishlist\""} {
			out := run(t, s, "view "+v).Text
			if strings.Contains(out, `"Buy"`) && !strings.Contains(out, `button b1 "Buy"`) ||
				strings.Contains(out, `"Wishlist"`) && !strings.Contains(out, `button b2 "Wishlist"`) {
				t.Errorf("%s: view %s renumbered refs the agent already holds:\n%s", name, v, out)
			}
		}
	}
}

func TestClickByHeldRefHitsTheSameNodeAfterReorder(t *testing.T) {
	flip := false
	e := newFake(reorderingPage(&flip))
	s := newSession(e, Config{})
	run(t, s, "goto https://shop.example/cart")
	flip = true
	if out := run(t, s, "click b2"); out.Stopped {
		t.Fatalf("held ref must still resolve:\n%s", out.Text)
	}
	if len(e.clicks) != 1 || e.clicks[0][1] > 120 { // Wishlist is now the first button
		t.Errorf("clicked %v, want the Wishlist button at the top", e.clicks)
	}
}

func TestUnknownRefIsStaleWithGuidance(t *testing.T) {
	e := newFake(shopPage)
	s := newSession(e, Config{})
	out := run(t, s, "goto https://shop.example/cart\nclick b99")
	if !out.Stopped || !strings.Contains(out.Text, "stale: ref b99 is not known") || !strings.Contains(out.Text, "view interactive") {
		t.Errorf("unknown ref reply:\n%s", out.Text)
	}
	if len(e.clicks) != 0 {
		t.Error("must not click")
	}
}

func TestRefsRestartOnANewDocumentAtAnotherURL(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		if strings.HasSuffix(e.url, "/two") {
			buttonID(b, b.Body(), 100, "Wishlist", 500)
			return
		}
		buttonID(b, b.Body(), 100, "Buy", 400)
		buttonID(b, b.Body(), 150, "Wishlist", 500)
	})
	s := newSession(e, Config{})
	run(t, s, "goto https://shop.example/one")
	out := run(t, s, "goto https://shop.example/two").Text
	if !strings.Contains(out, `button b1 "Wishlist"`) {
		t.Errorf("a new document starts numbering at 1:\n%s", out)
	}
}

func TestRefFromANonOutlineViewCanBeUsedLater(t *testing.T) {
	page := func(e *fakeEngine, b *pb.Builder) {
		for i := 0; i < 40; i++ {
			tiny(b, float64(10+i*18), fmt.Sprintf("Item %d", i), int64(600+i))
		}
	}
	e := newFake(page)
	s := newSession(e, Config{Budget: 30})
	run(t, s, "goto https://shop.example/cart")
	listing := run(t, s, "view interactive budget=5000").Text
	if !strings.Contains(listing, `button b40 "Item 39"`) {
		t.Fatalf("setup, refs of the full listing:\n%s", listing)
	}
	out := run(t, s, "click b40")
	if out.Stopped || len(e.clicks) != 1 {
		t.Errorf("a ref handed out by a view must work in the next program:\n%s", out.Text)
	}
}

func TestAmbiguousCandidatesOutsideTheOutlineStillGetRefs(t *testing.T) {
	page := func(e *fakeEngine, b *pb.Builder) {
		for i := 0; i < 40; i++ {
			tiny(b, float64(10+i*18), fmt.Sprintf("Item %d", i), int64(600+i))
		}
		tiny(b, 740, "Buy", 700)
		tiny(b, 758, "Buy", 701)
	}
	s := newSession(newFake(page), Config{Budget: 30})
	out := run(t, s, "goto https://shop.example/cart\nclick \"Buy\"").Text
	i := strings.Index(out, "ambiguous")
	if i < 0 {
		t.Fatalf("want ambiguous:\n%s", out)
	}
	if strings.Contains(out[i:], "no ref") || strings.Count(out[i:], ` button "Buy"`) != 2 {
		t.Errorf("every candidate must carry a ref the agent can target:\n%s", out)
	}
}

func TestAmbiguityAmongCoveredDuplicatesNamesThemAll(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		button(b, b.Body(), 100, "Buy")
		button(b, b.Body(), 200, "Buy")
		overlay(b, "Cookie preferences")
	})
	out := run(t, newSession(e, Config{}), `click "Buy"`)
	if !out.Stopped || !strings.Contains(out.Text, `"Buy" matches 2 nodes`) || len(e.clicks) != 0 {
		t.Errorf("two covered duplicates are ambiguous, not a pick:\n%s", out.Text)
	}
}

func TestSingleCoveredMatchIsBlockedNotAmbiguous(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		button(b, b.Body(), 100, "Buy")
		overlay(b, "Cookie preferences")
	})
	out := run(t, newSession(e, Config{}), `click "Buy"`)
	if !out.Stopped || !strings.Contains(out.Text, "blocked:") || strings.Contains(out.Text, "ambiguous") {
		t.Errorf("one covered match:\n%s", out.Text)
	}
}

// ---- targets by text ----

func TestTextTargetsOnlyReachActionableNodes(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		para(b, b.Body(), 50, "Buy")
		button(b, b.Body(), 200, "Buy")
	})
	out := run(t, newSession(e, Config{}), `click "Buy"`)
	if out.Stopped || len(e.clicks) != 1 || e.clicks[0][1] < 150 {
		t.Errorf("plain text named Buy is not a target, clicks=%v:\n%s", e.clicks, out.Text)
	}

	e = newFake(func(e *fakeEngine, b *pb.Builder) { para(b, b.Body(), 50, "Buy") })
	out = run(t, newSession(e, Config{}), `click "Buy"`)
	if !out.Stopped || !strings.Contains(out.Text, "not found") || len(e.clicks) != 0 {
		t.Errorf("text alone must not be clickable:\n%s", out.Text)
	}
}

func TestTextTargetMatchIgnoresCaseAndSpacing(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) { button(b, b.Body(), 100, "Add  to   Cart") })
	out := run(t, newSession(e, Config{}), `click "add to cart"`)
	if out.Stopped || len(e.clicks) != 1 {
		t.Errorf("case and whitespace must not matter:\n%s", out.Text)
	}
}

func TestTextTargetFollowsWhatAPersonReadsNotTheHiddenLabel(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		n := b.El(b.Body(), "button", pb.Rect(20, 100, 120, 30), pb.Fill("rgb(230, 230, 230)"), pb.Attr("aria-label", "Confirm payment"))
		b.Text(n, "Cancel")
	})
	s := newSession(e, Config{})
	if out := run(t, s, `click "Cancel"`); out.Stopped || len(e.clicks) != 1 {
		t.Errorf("the visible text is a valid target:\n%s", out.Text)
	}
	out := run(t, s, `click "Confirm payment"`)
	if !out.Stopped || !strings.Contains(out.Text, "not found") {
		t.Errorf("a label no person reads is not a text target:\n%s", out.Text)
	}
}

func TestPlaceholderNamesAnInputWithoutALabel(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		b.El(b.Body(), "input", pb.Rect(20, 100, 200, 20), pb.Attr("type", "text"), pb.Attr("placeholder", "Search here"), pb.Value(e.typed))
	})
	s := newSession(e, Config{})
	out := run(t, s, `fill "Search here" "shoes"`)
	if out.Stopped || e.typed != "shoes" {
		t.Errorf("placeholder is the name a sighted user reads, typed=%q:\n%s", e.typed, out.Text)
	}
}

func TestClickCoordinatesAreRelativeToTheViewport(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) { button(b, b.Body(), 100, "Buy") })
	e.scrollX, e.scrollY = 10, 40
	run(t, newSession(e, Config{}), `click "Buy"`)
	if len(e.clicks) != 1 || e.clicks[0] != [2]float64{20 + 60 - 10, 100 + 15 - 40} {
		t.Errorf("click at %v, want the box centre minus the scroll offset", e.clicks)
	}
}

func TestBlockedFactNamesTheRefAndWhy(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		button(b, b.Body(), 100, "Checkout", pb.Attr("disabled", ""))
	})
	out := run(t, newSession(e, Config{}), `click "Checkout"`)
	if !strings.Contains(out.Text, "b1 disabled") {
		t.Errorf("blocked fact must name the ref and the reason:\n%s", out.Text)
	}
}

func TestBlockedCoveredFactNamesRefOfCoverAndItsName(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		button(b, b.Body(), 100, "Checkout")
		overlay(b, "Cookie preferences")
	})
	out := run(t, newSession(e, Config{}), `click "Checkout"`)
	if !strings.Contains(out.Text, `b1 covered-by`) || !strings.Contains(out.Text, `"Cookie preferences"`) {
		t.Errorf("covered fact:\n%s", out.Text)
	}
}

// ---- expectations about text ----

func TestExpectTextSeesOnlyWhatAPersonCanSee(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		para(b, b.Body(), 100, "Welcome shopper")
		para(b, b.Body(), 150, "Gone entirely", pb.Style(snapshot.Display, "none"))
		d := b.El(b.Body(), "div", pb.Rect(20, 200, 300, 20), pb.FontPx(0))
		b.Text(d, "Tiny secret")
	})
	s := newSession(e, Config{})
	run(t, s, "goto https://shop.example/cart")
	if out := run(t, s, `expect text "welcome SHOPPER"`); out.Stopped {
		t.Errorf("visible text, any case, must be found:\n%s", out.Text)
	}
	for _, hidden := range []string{"Gone entirely", "Tiny secret"} {
		if out := run(t, s, fmt.Sprintf("expect text %q", hidden)); !out.Stopped {
			t.Errorf("%q is not visible to a person and must not satisfy an expectation", hidden)
		}
	}
}

// ---- net and unseen views ----

func TestViewNetListsEveryRequestOnItsOwnLine(t *testing.T) {
	e := newFake(shopPage)
	e.reqs = []engine.Request{
		{Method: "GET", URL: "https://api.example/a", Status: 200, Mime: "application/json", Size: 12},
		{Method: "POST", URL: "https://api.example/b", Status: 500, Mime: "text/plain", Size: 3},
	}
	out := run(t, newSession(e, Config{}), "goto https://shop.example/cart\nview net").Text
	want := "GET https://api.example/a 200 application/json 12B\nPOST https://api.example/b 500 text/plain 3B"
	if !strings.HasSuffix(out, want) {
		t.Errorf("net lines:\n%s", out)
	}
}

func TestViewNetClipsLongURLsAtTwoHundredBytes(t *testing.T) {
	long := "https://api.example/" + strings.Repeat("x", 180) // exactly 200 bytes
	e := newFake(shopPage)
	e.reqs = []engine.Request{
		{Method: "GET", URL: long, Status: 200, Mime: "m", Size: 1},
		{Method: "GET", URL: long + "y", Status: 200, Mime: "m", Size: 1},
	}
	out := run(t, newSession(e, Config{}), "goto https://shop.example/cart\nview net").Text
	if !strings.Contains(out, "GET "+long+" 200") {
		t.Errorf("a 200 byte URL must stay whole:\n%s", out)
	}
	if !strings.Contains(out, "GET "+long+"… 200") {
		t.Errorf("a 201 byte URL must be clipped:\n%s", out)
	}
}

func TestViewUnseenClipsEachLineAtThreeHundredBytes(t *testing.T) {
	exact := strings.Repeat("a", 300)
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		for i, txt := range []string{exact, exact + "b"} {
			d := b.El(b.Body(), "div", pb.Rect(20, float64(200+i*20), 300, 20), pb.FontPx(0))
			b.Text(d, txt)
		}
	})
	out := run(t, newSession(e, Config{}), "goto https://shop.example/cart\nview unseen budget=5000").Text
	if !strings.Contains(out, fmt.Sprintf("  %q\n", exact)) {
		t.Errorf("a 300 byte line must stay whole:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("  %q\n", exact+"…")) && !strings.Contains(out, fmt.Sprintf("  %q\nunseen-text end", exact+"…")) {
		t.Errorf("a 301 byte line must be clipped:\n%s", out)
	}
}

func TestFitLinesKeepsWholeLinesWithinTheTokenBudget(t *testing.T) {
	lines := []string{"aaaaa", "bbbbb", "ccccc", "ddddd", "eeeee"} // 6 bytes each with its newline
	cases := []struct {
		budget int
		want   string
	}{
		{4, "aaaaa\nbbbbb\n… 3 more lines"},        // 16 bytes: two lines fit, the third passes 16
		{5, "aaaaa\nbbbbb\nccccc\n… 2 more lines"}, // 20 bytes: three lines fit exactly... fourth passes
		{1, "aaaaa\n… 4 more lines"},               // the first line is always kept
		{100, strings.Join(lines, "\n")},
	}
	for _, c := range cases {
		if got := fitLines(lines, c.budget); got != c.want {
			t.Errorf("budget %d:\n got %q\nwant %q", c.budget, got, c.want)
		}
	}
}

func TestFitLinesLineExactlyAtTheLimitIsKept(t *testing.T) {
	lines := []string{"123456789", "123456789", "123456789"} // 10 bytes each with its newline
	if got := fitLines(lines, 5); got != "123456789\n123456789\n… 1 more lines" {
		t.Errorf("a line that reaches the limit exactly fits: %q", got)
	}
}

func TestClipTextCutsOnRuneBoundaries(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"abcd", 4, "abcd"},
		{"abcd", 5, "abcd"},
		{"abcde", 4, "abcd…"},
		{"héllo", 2, "h…"}, // é occupies bytes 1 and 2
		{"héllo", 3, "hé…"},
		{"", 3, ""},
	}
	for _, c := range cases {
		if got := clipText(c.in, c.n); got != c.want {
			t.Errorf("clipText(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

// ---- views ----

func TestViewOfNothingMatchingIsAnAnswerNotSilence(t *testing.T) {
	e := newFake(shopPage)
	s := newSession(e, Config{})
	run(t, s, "goto https://shop.example/cart")
	out := run(t, s, `view find "zzz-not-there"`).Text
	if strings.TrimSpace(out) == "" {
		t.Error("an empty view must still say something")
	}
}

func TestUnknownProjectionIsRejected(t *testing.T) {
	out := run(t, newSession(newFake(shopPage), Config{}), "goto https://shop.example/cart\nview bogus")
	if !out.Stopped {
		t.Errorf("unknown projection must stop the program:\n%s", out.Text)
	}
}

func TestSecondOutlineOfAnUnchangedPageIsNotRepeatedByTheNextProgram(t *testing.T) {
	e := newFake(shopPage)
	s := newSession(e, Config{})
	run(t, s, "goto https://shop.example/cart\nview")
	if out := run(t, s, `expect text "Wishlist"`); out.Text != "ok, no changes" {
		t.Errorf("having read the outline, an unchanged page is no news:\n%s", out.Text)
	}
}

func TestOtherProjectionsDoNotCountAsHavingReadThePage(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		button(b, b.Body(), 100, "Buy")
		if e.added {
			para(b, b.Body(), 300, "Added to cart")
		}
	})
	s := newSession(e, Config{})
	run(t, s, "goto https://shop.example/cart")
	run(t, s, "view read")
	e.added = true
	out := run(t, s, `expect text "Added to cart"`)
	if !strings.Contains(out.Text, "Added to cart") {
		t.Errorf("the change must be reported:\n%s", out.Text)
	}
	if strings.Contains(out.Text, "Buy") {
		t.Errorf("a view other than the outline is not a delivery of the page:\n%s", out.Text)
	}
}

func TestRepeatedGotoOfTheSameURLDescribesTheReloadedPageInFull(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) { buttonID(b, b.Body(), 100, "Buy", 400) })
	s := newSession(e, Config{})
	run(t, s, "goto https://shop.example/cart")
	out := run(t, s, "goto https://shop.example/cart").Text
	if !strings.Contains(out, `button b1 "Buy"`) {
		t.Errorf("a reload is a new document and is described in full:\n%s", out)
	}
}

func TestAddressChangeWithinADocumentIsDescribedInFullAndKeepsRefs(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) { buttonID(b, b.Body(), 100, "Buy", 400) })
	s := newSession(e, Config{})
	run(t, s, "goto https://shop.example/cart")
	e.url = "https://shop.example/other"
	out := run(t, s, `expect text "Buy"`).Text
	if !strings.Contains(out, `button b1 "Buy"`) {
		t.Errorf("a new address is a new page for delivery:\n%s", out)
	}
	e.url = "https://shop.example/other#frag"
	if out := run(t, s, `expect text "Buy"`); !strings.Contains(out.Text, `button b1 "Buy"`) {
		t.Errorf("the address changed again, so the page is described again with the same refs:\n%s", out.Text)
	}
}

// ---- remounted nodes ----

func remountPage(ids *[]int64, build func(b *pb.Builder, id func(k int) pb.Opt)) func(e *fakeEngine, b *pb.Builder) {
	return func(e *fakeEngine, b *pb.Builder) {
		build(b, func(k int) pb.Opt { return pb.ID((*ids)[k]) })
	}
}

func TestRefSurvivesRemountWhenExactlyOneNodeFits(t *testing.T) {
	ids := []int64{400, 500}
	e := newFake(remountPage(&ids, func(b *pb.Builder, id func(int) pb.Opt) {
		button(b, b.Body(), 100, "Buy", id(0))
		button(b, b.Body(), 150, "Wishlist", id(1))
	}))
	s := newSession(e, Config{})
	run(t, s, "goto https://shop.example/cart")
	ids = []int64{401, 501} // the framework replaced both nodes
	out := run(t, s, "click b2")
	if out.Stopped || len(e.clicks) != 1 || e.clicks[0][1] < 150 {
		t.Errorf("b2 must rebind to the new Wishlist, clicks=%v:\n%s", e.clicks, out.Text)
	}
}

func TestRemountRebindsByNameWhenPositionsSwap(t *testing.T) {
	ids := []int64{400, 500}
	swap := false
	e := newFake(remountPage(&ids, func(b *pb.Builder, id func(int) pb.Opt) {
		if swap {
			button(b, b.Body(), 100, "Wishlist", id(1))
			button(b, b.Body(), 150, "Buy", id(0))
			return
		}
		button(b, b.Body(), 100, "Buy", id(0))
		button(b, b.Body(), 150, "Wishlist", id(1))
	}))
	s := newSession(e, Config{})
	run(t, s, "goto https://shop.example/cart")
	ids, swap = []int64{401, 501}, true
	if out := run(t, s, "click b1"); out.Stopped {
		t.Fatalf("\n%s", out.Text)
	}
	if len(e.clicks) != 1 || e.clicks[0][1] < 150 {
		t.Errorf("b1 was Buy, now the lower button; clicked %v", e.clicks)
	}
}

func TestRemountRebindsByStableAttributesWhenPositionsSwap(t *testing.T) {
	ids := []int64{400, 500}
	swap := false
	e := newFake(remountPage(&ids, func(b *pb.Builder, id func(int) pb.Opt) {
		first, second := pb.Attr("id", "a"), pb.Attr("id", "b")
		if swap {
			first, second = second, first
		}
		idA, idB := id(0), id(1)
		if swap {
			idA, idB = idB, idA
		}
		button(b, b.Body(), 100, "Same", idA, first)
		button(b, b.Body(), 150, "Same", idB, second)
	}))
	s := newSession(e, Config{})
	run(t, s, "goto https://shop.example/cart")
	if out := run(t, s, "view interactive"); !strings.Contains(out.Text, "b2") {
		t.Fatalf("setup:\n%s", out.Text)
	}
	ids, swap = []int64{401, 501}, true
	out := run(t, s, "click b1") // b1 had id=a, which is now the lower button
	if out.Stopped || len(e.clicks) != 1 || e.clicks[0][1] < 150 {
		t.Errorf("b1 must follow id=a, clicks=%v:\n%s", e.clicks, out.Text)
	}
}

func TestRemountRebindsDuplicatesByPositionAmongSameTagSiblings(t *testing.T) {
	ids := []int64{400, 500}
	extra := false
	e := newFake(remountPage(&ids, func(b *pb.Builder, id func(int) pb.Opt) {
		if extra {
			// A banner of another tag appears before the buttons.
			para(b, b.Body(), 10, "Banner")
		}
		button(b, b.Body(), 100, "Same", id(0))
		button(b, b.Body(), 150, "Same", id(1))
	}))
	s := newSession(e, Config{})
	run(t, s, "goto https://shop.example/cart")
	ids, extra = []int64{401, 501}, true
	out := run(t, s, "click b2")
	if out.Stopped || len(e.clicks) != 1 || e.clicks[0][1] < 150 {
		t.Errorf("b2 is the second Same button even with a banner before it, clicks=%v:\n%s", e.clicks, out.Text)
	}
}

func TestRemountDuplicatesInSeparateWrappersAreToldApartByPlace(t *testing.T) {
	ids := []int64{400, 500}
	e := newFake(remountPage(&ids, func(b *pb.Builder, id func(int) pb.Opt) {
		for k, g := range []float64{100, 150} {
			wrapper := b.El(b.Body(), "div", pb.Rect(0, g, 300, 30))
			// Two buttons in different wrappers: position is the only difference.
			button(b, wrapper, g, "Same", id(k))
		}
	}))
	s := newSession(e, Config{})
	run(t, s, "goto https://shop.example/cart")
	ids = []int64{401, 501}
	// The wrappers keep their place, so position tells the buttons apart.
	if out := run(t, s, "click b1"); out.Stopped || len(e.clicks) != 1 || e.clicks[0][1] > 120 {
		t.Errorf("b1 is the first wrapper's button, clicks=%v:\n%s", e.clicks, out.Text)
	}
}

// deepPair builds two identical Same buttons, each nested under wrappers
// so the groups differ only at the given ancestor level.
func deepPair(wrappers int, ids *[]int64) func(e *fakeEngine, b *pb.Builder) {
	return func(e *fakeEngine, b *pb.Builder) {
		for k := 0; k < 2; k++ {
			y := float64(100 + 60*k)
			parent := b.El(b.Body(), "div", pb.Rect(0, y, 300, 50))
			for w := 0; w < wrappers; w++ {
				parent = b.El(parent, "div", pb.Rect(0, y, 300, 50))
			}
			button(b, parent, y, "Same", pb.ID((*ids)[k]))
		}
	}
}

func TestRemountPositionLooksFourAncestorsUpAndNoFurther(t *testing.T) {
	// button(0) > wrapper(1) > ... : the group div is level wrappers+1.
	// Four ancestors are compared, so groups at level 4 tell the buttons apart.
	for _, c := range []struct {
		wrappers int
		resolves bool
	}{
		{3, true},  // group div at level 4: part of the position
		{4, false}, // group div at level 5: beyond the position, both fit
	} {
		ids := []int64{400, 500}
		e := newFake(deepPair(c.wrappers, &ids))
		s := newSession(e, Config{})
		run(t, s, "goto https://shop.example/cart")
		ids = []int64{401, 501}
		out := run(t, s, "click b1")
		if c.resolves && (out.Stopped || len(e.clicks) != 1 || e.clicks[0][1] > 150) {
			t.Errorf("wrappers=%d: b1 must rebind to the first button, clicks=%v:\n%s", c.wrappers, e.clicks, out.Text)
		}
		if !c.resolves && (!out.Stopped || !strings.Contains(out.Text, "stale") || !strings.Contains(out.Text, "2 nodes could be it") || len(e.clicks) != 0) {
			t.Errorf("wrappers=%d: indistinguishable nodes must be stale, not a guess, clicks=%v:\n%s", c.wrappers, e.clicks, out.Text)
		}
	}
}

func TestRemountWithNothingLeftIsStale(t *testing.T) {
	ids := []int64{400, 500}
	gone := false
	e := newFake(remountPage(&ids, func(b *pb.Builder, id func(int) pb.Opt) {
		if gone {
			return
		}
		button(b, b.Body(), 100, "Buy", id(0))
	}))
	s := newSession(e, Config{})
	run(t, s, "goto https://shop.example/cart")
	gone = true
	out := run(t, s, "click b1")
	if !out.Stopped || !strings.Contains(out.Text, "stale") || !strings.Contains(out.Text, "0 nodes could be it") {
		t.Errorf("a vanished node is stale:\n%s", out.Text)
	}
}

// ---- redaction ----

func TestRedactionLeavesNoSecretOrPasswordInTheSnapshotTheViewsReadFrom(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		b.El(b.Body(), "input", pb.Rect(20, 50, 200, 20), pb.Attr("type", "text"), pb.Attr("aria-label", "Note"), pb.Value(e.typed))
		b.El(b.Body(), "input", pb.Rect(20, 80, 200, 20), pb.Attr("type", "PassWord"), pb.Attr("aria-label", "Password"), pb.Value("human-typed"))
		b.El(b.Body(), "input", pb.Rect(20, 110, 200, 20), pb.Attr("type", "password"), pb.Attr("aria-label", "Empty"))
		b.El(b.Body(), "input", pb.Rect(20, 140, 200, 20), pb.Attr("type", "text"), pb.Attr("aria-label", "Name"), pb.Value("plain value"))
		para(b, b.Body(), 200, "you typed: "+e.typed)
		a := b.El(b.Body(), "a", pb.Rect(20, 300, 200, 20), pb.Attr("href", "/x"), pb.Attr("title", "t-"+e.typed))
		b.Text(a, "Profile")
	})
	s := newSession(e, Config{Policy: policyWith(fakeSecrets{"pw": secretValue})})
	run(t, s, "goto https://shop.example/cart\nfill \"Note\" $secret:pw")
	snap := s.page.Snap
	for i := range snap.Value {
		for _, field := range []string{snap.Value[i], snap.Text[i]} {
			if strings.Contains(field, secretValue) {
				t.Errorf("node %d keeps the secret: %q", i, field)
			}
		}
		for _, a := range snap.Attrs[i] {
			if strings.Contains(a.Value, secretValue) {
				t.Errorf("node %d attribute %s keeps the secret", i, a.Name)
			}
		}
	}
	value := func(label string) string {
		for i := range snap.Value {
			if v, _ := snap.Attr(int32(i), "aria-label"); v == label {
				return snap.Value[i]
			}
		}
		t.Fatalf("no input labelled %q", label)
		return ""
	}
	if value("Note") != mask {
		t.Errorf("the echoed secret is masked, got %q", value("Note"))
	}
	if value("Password") != mask {
		t.Errorf("a password value is masked whatever the type's case, got %q", value("Password"))
	}
	if value("Empty") != "" {
		t.Errorf("an empty password has nothing to mask, got %q", value("Empty"))
	}
	if value("Name") != "plain value" {
		t.Errorf("ordinary values are left alone, got %q", value("Name"))
	}
}

func TestSecretsAreScrubbedFromTheReplyAndTheLog(t *testing.T) {
	e := newFake(echoPage)
	log := &recorder{}
	s := newSession(e, Config{Log: log, Policy: policyWith(fakeSecrets{"pw": secretValue})})
	out := run(t, s, "goto https://shop.example/cart\nfill \"Note\" $secret:pw\nview read")
	if strings.Contains(out.Text, secretValue) || !strings.Contains(out.Text, mask) {
		t.Errorf("reply:\n%s", out.Text)
	}
	if len(log.replies) != 1 || strings.Contains(log.replies[0], secretValue) {
		t.Errorf("log: %v", log.replies)
	}
}

// tiny is a button short enough to stack forty of them inside one viewport.
func tiny(b *pb.Builder, y float64, name string, id int64) int32 {
	n := b.El(b.Body(), "button", pb.Rect(20, y, 120, 14), pb.Fill("rgb(230, 230, 230)"), pb.ID(id))
	b.Text(n, name)
	return n
}

func TestRunAndViewDriveTheEngineUnderTheCallersContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxKey, "caller")
	e := newFake(shopPage)
	s := newSession(e, Config{})
	s.Run(ctx, "goto https://shop.example/cart")
	if e.snapCtxValue != "caller" {
		t.Errorf("Run: engine saw %v", e.snapCtxValue)
	}
	e.snapCtxValue = nil
	s.View(ctx, "interactive")
	if e.snapCtxValue != "caller" {
		t.Errorf("View: engine saw %v", e.snapCtxValue)
	}
}

func TestAnEngineThatCannotSnapshotStopsTheProgramWithItsCause(t *testing.T) {
	e := newFake(shopPage)
	e.snapErr = errors.New("page crashed")
	out := run(t, newSession(e, Config{}), "goto https://shop.example/cart")
	if !out.Stopped || !strings.Contains(out.Text, "page crashed") || len(e.clicks) != 0 {
		t.Errorf("%+v", out)
	}
}

func TestSecretsNeedAResolverConfiguredOnTheHost(t *testing.T) {
	e := newFake(echoPage)
	out := run(t, newSession(e, Config{}), "goto https://shop.example/cart\nfill \"Note\" $secret:pw")
	if !out.Stopped || !strings.Contains(out.Text, "no secret") || e.typed != "" {
		t.Errorf("without a resolver a secret cannot be used, typed=%q:\n%s", e.typed, out.Text)
	}
}

func TestActionsAddressTheEngineNodeOfTheTarget(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		buttonID(b, b.Body(), 100, "Buy", 400)
		b.El(b.Body(), "input", pb.Rect(20, 200, 200, 20), pb.Attr("type", "text"), pb.Attr("aria-label", "Note"), pb.ID(777), pb.Value(e.typed))
	})
	run(t, newSession(e, Config{}), `fill "Note" "x"`)
	if len(e.focused) != 1 || e.focused[0] != 777 {
		t.Errorf("focus went to %v, want node 777", e.focused)
	}
	if len(e.scrolledTo) == 0 || e.scrolledTo[0] != 777 {
		t.Errorf("scrolled to %v, want node 777", e.scrolledTo)
	}
}

func TestOnlyControlsThatReactAreTextTargets(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		d := b.El(b.Body(), "div", pb.Rect(20, 100, 100, 20), pb.Clickable())
		b.Text(d, "Plain clickable")
		d = b.El(b.Body(), "div", pb.Rect(20, 130, 100, 20), pb.Attr("role", "button"))
		b.Text(d, "Role button")
		d = b.El(b.Body(), "div", pb.Rect(20, 160, 100, 20), pb.Attr("role", "region"), pb.Attr("aria-label", "Named region"))
		b.Text(d, "Inside")
	})
	s := newSession(e, Config{})
	if out := run(t, s, `click "Role button"`); out.Stopped || len(e.clicks) != 1 {
		t.Errorf("a control with an interactive role is a target:\n%s", out.Text)
	}
	// The view lists a clickable block by the text it shows, so that text
	// must reach it.
	if out := run(t, s, `click "Plain clickable"`); out.Stopped || len(e.clicks) != 2 {
		t.Errorf("a block with a click handler is a target by its text:\n%s", out.Text)
	}
	out := run(t, s, `click "Named region"`)
	if !out.Stopped || !strings.Contains(out.Text, "not found") {
		t.Errorf("a labelled region does not react, so it is no target:\n%s", out.Text)
	}
	if len(e.clicks) != 2 {
		t.Errorf("clicks = %v", e.clicks)
	}
}

func TestOutlineViewedMidProgramIsNotRepeatedAsTheClosingDelta(t *testing.T) {
	e := newFake(shopPage)
	out := run(t, newSession(e, Config{}), "goto https://shop.example/cart\nview\nclick \"Buy\"").Text
	if n := strings.Count(out, `button b1 "Buy"`); n != 1 {
		t.Errorf("the outline was already shown and must not be repeated:\n%s", out)
	}
	if !strings.Contains(out, "Added to cart") {
		t.Errorf("the change after the view must be reported:\n%s", out)
	}
}

func TestViewNetHasNoBlankLinesAndOneLinePerRequest(t *testing.T) {
	e := newFake(shopPage)
	e.reqs = []engine.Request{
		{Method: "GET", URL: "https://api.example/a", Status: 200, Mime: "m", Size: 1},
		{Method: "GET", URL: "https://api.example/b", Status: 200, Mime: "m", Size: 1},
	}
	s := newSession(e, Config{})
	run(t, s, "goto https://shop.example/cart")
	out := run(t, s, "view net").Text
	if out != "GET https://api.example/a 200 m 1B\nGET https://api.example/b 200 m 1B" {
		t.Errorf("net = %q", out)
	}
}

func TestClipTextNeverSplitsARuneEvenAtTheFirstByte(t *testing.T) {
	if got := clipText("éa", 1); got != "…" {
		t.Errorf("clipText = %q", got)
	}
}

func TestFillNotesWhatATextareaHoldsInsteadOfWhatWasTyped(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		b.El(b.Body(), "textarea", pb.Rect(20, 100, 200, 60), pb.Attr("aria-label", "Message"), pb.Value("kept by the page"))
	})
	out := run(t, newSession(e, Config{}), `fill "Message" "hello"`)
	if !strings.Contains(out.Text, `holds "kept by the page" after typing "hello"`) {
		t.Errorf("a textarea that holds other text than was typed is said to:\n%s", out.Text)
	}
}

func TestFillIntoAnEditableBlockMakesNoClaimAboutItsValue(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		b.El(b.Body(), "div", pb.Rect(20, 100, 200, 60), pb.Attr("role", "textbox"), pb.Attr("contenteditable", "true"), pb.Attr("aria-label", "Message"))
	})
	out := run(t, newSession(e, Config{}), `fill "Message" "hello"`)
	if out.Stopped || e.typed != "hello" {
		t.Fatalf("an editable block takes typing, typed=%q:\n%s", e.typed, out.Text)
	}
	if strings.Contains(out.Text, "note:") {
		t.Errorf("a block has no value to compare, so nothing is noted:\n%s", out.Text)
	}
}

func TestAControlHiddenForGoodIsBlockedUnderTheWordsTheAgentUsed(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		button(b, b.Body(), 100, "Later", pb.Style(snapshot.Display, "none"))
	})
	out := run(t, newSession(e, Config{}), `click "Later"`)
	if !strings.Contains(out.Text, `"Later" hidden`) || len(e.clicks) != 0 {
		t.Errorf("a control with no ref is named by the agent's words:\n%s", out.Text)
	}
}

func TestAVisibleControlInsideALabelIsClickedWhereItIs(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		l := b.El(b.Body(), "label", pb.Rect(20, 100, 300, 20))
		b.El(l, "input", pb.Rect(20, 100, 16, 16), pb.Attr("type", "checkbox"), pb.Attr("aria-label", "Agree"))
		b.Text(l, "I agree to the terms")
	})
	run(t, newSession(e, Config{}), `click "Agree"`)
	if len(e.clicks) != 1 || e.clicks[0] != [2]float64{20 + 8, 100 + 8} {
		t.Errorf("a visible control is clicked at its own centre, not its label's: %v", e.clicks)
	}
}

func TestTwoClickableBlocksWithNoRoleAreListedAsClickable(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		for _, y := range []float64{100, 140} {
			d := b.El(b.Body(), "div", pb.Rect(20, y, 100, 20), pb.Clickable())
			b.Text(d, "Pay")
		}
	})
	out := run(t, newSession(e, Config{}), `click "Pay"`)
	if !strings.Contains(out.Text, `"Pay" matches 2 nodes`) || strings.Count(out.Text, " clickable ") != 2 {
		t.Errorf("a block with no role is listed as clickable:\n%s", out.Text)
	}
}

func TestAClickLandingOnWhatHoldsTheTargetIsJudgedInDocumentCoordinates(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		holder := b.El(b.Body(), "div", pb.Rect(0, 0, 400, 400), pb.ID(2000))
		button(b, holder, 100, "Buy", pb.ID(2001))
	})
	e.scrollX, e.scrollY = 10, 40
	s := newSession(e, Config{})
	c := (*scene)(s)
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The button covers x 20..140 and y 100..130 on the page.
	for name, at := range map[string][2]float64{
		"near its left edge": {25 - 10, 115 - 40},
		"near its top edge":  {80 - 10, 105 - 40},
	} {
		if ok, why, _ := c.Reaches(2001, 2000, at[0], at[1], true); !ok {
			t.Errorf("a point %s, in viewport coordinates, is on the button: %s", name, why)
		}
	}
	if ok, _, _ := c.Reaches(2001, 2000, 15-10, 115-40, true); ok {
		t.Errorf("a point left of the button is not on it")
	}
}

func TestAControlWithAFullerLabelIsNotNamedByItsShortWords(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		n := b.El(b.Body(), "button", pb.Rect(20, 100, 120, 30), pb.Fill("rgb(230, 230, 230)"), pb.Attr("aria-label", "Add to cart"))
		b.Text(n, "Add")
	})
	out := run(t, newSession(e, Config{}), `click "Add"`)
	if !out.Stopped || len(e.clicks) != 0 || !strings.Contains(out.Text, "nothing is named exactly") {
		t.Errorf("only a block with no name of its own answers to its words:\n%s", out.Text)
	}
}

func TestAPageIsArchivedAsTheBrowserHoldsIt(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		b.El(b.Body(), "input", pb.Rect(20, 50, 200, 20), pb.Attr("type", "text"), pb.Attr("aria-label", "Name"), pb.Value("plain"))
	})
	s := newSession(e, Config{})
	run(t, s, "goto https://shop.example/cart")
	page, err := s.Archive(context.Background())
	if err != nil || page != "MHTML of "+e.url {
		t.Errorf("archive = %q, %v", page, err)
	}
}

// An archive keeps what the page holds, where a view masks it: a session that
// used a secret, or a page holding a typed password, is never archived.
func TestNoArchiveCarriesASecretOrATypedPassword(t *testing.T) {
	withSecret := newFake(func(e *fakeEngine, b *pb.Builder) {
		b.El(b.Body(), "input", pb.Rect(20, 50, 200, 20), pb.Attr("type", "text"), pb.Attr("aria-label", "Note"), pb.Value(e.typed))
	})
	s := newSession(withSecret, Config{Policy: policyWith(fakeSecrets{"pw": secretValue})})
	run(t, s, "goto https://shop.example/cart\nfill \"Note\" $secret:pw")
	if page, err := s.Archive(context.Background()); err == nil || !strings.Contains(err.Error(), "used a secret") || page != "" {
		t.Errorf("a session that used a secret is not archived: %q, %v", page, err)
	}
	withPassword := newFake(func(e *fakeEngine, b *pb.Builder) {
		b.El(b.Body(), "input", pb.Rect(20, 80, 200, 20), pb.Attr("type", "Password"), pb.Attr("aria-label", "Password"), pb.Value("typed-by-hand"))
	})
	s = newSession(withPassword, Config{})
	run(t, s, "goto https://shop.example/login")
	if page, err := s.Archive(context.Background()); err == nil || !strings.Contains(err.Error(), "typed password") || page != "" {
		t.Errorf("a page holding a typed password is not archived: %q, %v", page, err)
	}
}
