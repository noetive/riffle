package program

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/engine"
)

type probeKey struct{}

// probeDriver fails the named call and records, per call, whether the call
// ran under the caller's context.
type probeDriver struct {
	l      *log
	failOn string
	backOK bool
	ctxBad []string // calls made under a context that is not the caller's
}

func (d *probeDriver) enter(ctx context.Context, name string) error {
	d.l.add("%s", name)
	if ctx.Value(probeKey{}) != "caller" {
		d.ctxBad = append(d.ctxBad, name)
	}
	if d.failOn == name {
		return errors.New(name + " exploded")
	}
	return nil
}

func (d *probeDriver) Load(ctx context.Context, _ string) error { return d.enter(ctx, "load") }
func (d *probeDriver) Settle(ctx context.Context) error         { return d.enter(ctx, "settle") }
func (d *probeDriver) Advance(ctx context.Context, _ time.Duration) error {
	return d.enter(ctx, "advance")
}
func (d *probeDriver) Move(ctx context.Context, _, _ float64) error  { return d.enter(ctx, "move") }
func (d *probeDriver) Click(ctx context.Context, _, _ float64) error { return d.enter(ctx, "click") }
func (d *probeDriver) HitTest(ctx context.Context, _, _ float64) (int64, error) {
	return 0, d.enter(ctx, "hittest")
}
func (d *probeDriver) Wheel(ctx context.Context, _, _, _ float64) error {
	return d.enter(ctx, "wheel")
}
func (d *probeDriver) Key(ctx context.Context, _ string) error  { return d.enter(ctx, "key") }
func (d *probeDriver) Focus(ctx context.Context, _ int64) error { return d.enter(ctx, "focus") }
func (d *probeDriver) InsertText(ctx context.Context, _ string) error {
	return d.enter(ctx, "inserttext")
}
func (d *probeDriver) SetSecret(ctx context.Context, _ int64, _ string) error {
	return d.enter(ctx, "setsecret")
}
func (d *probeDriver) SelectOption(ctx context.Context, _ int64, _ []string) error {
	return d.enter(ctx, "selectoption")
}
func (d *probeDriver) AnswerDialog(engine.DialogAnswer) {}
func (d *probeDriver) SetFiles(ctx context.Context, _ int64, _ []string) error {
	return d.enter(ctx, "setfiles")
}
func (d *probeDriver) ScrollIntoView(ctx context.Context, _ int64) error {
	return d.enter(ctx, "scrollintoview")
}
func (d *probeDriver) Eval(ctx context.Context, _ string) (string, error) {
	return "v", d.enter(ctx, "eval")
}
func (d *probeDriver) Back(ctx context.Context) (bool, error) {
	err := d.enter(ctx, "back")
	return d.backOK, err
}
func (d *probeDriver) Drain() []engine.Event { return nil }

// probeScene fails the Nth refresh or Nth find and blocks a node on request.
type probeScene struct {
	l             *log
	url           string
	texts         map[string]bool
	appearAfter   int // refreshes after which "later" becomes visible
	refreshes     int
	failRefreshAt int
	finds         int
	failFindAt    int
	blockFindAt   int
	renders       []string
	ctxBad        int
}

func (s *probeScene) Refresh(ctx context.Context) error {
	s.refreshes++
	s.l.add("refresh")
	if ctx.Value(probeKey{}) != "caller" {
		s.ctxBad++
	}
	if s.failRefreshAt == s.refreshes {
		return fmt.Errorf("refresh %d exploded", s.refreshes)
	}
	if s.appearAfter > 0 && s.refreshes >= s.appearAfter {
		s.texts["later"] = true
	}
	return nil
}

func (s *probeScene) Find(t Target) (Node, error) {
	s.finds++
	if s.failFindAt == s.finds {
		return Node{}, fmt.Errorf("find %d exploded", s.finds)
	}
	n := Node{Backend: 7, X: 1, Y: 2}
	if s.blockFindAt == s.finds {
		n.Blocked = "b1 hidden"
	}
	return n, nil
}

func (s *probeScene) Render(Step) (string, error) {
	if len(s.renders) == 0 {
		return "", nil
	}
	r := s.renders[0]
	s.renders = s.renders[1:]
	return r, nil
}
func (s *probeScene) Delta() string                            { return "" }
func (s *probeScene) URL() string                              { return s.url }
func (s *probeScene) Document() string                         { return "" }
func (s *probeScene) HasText(t string, _ Target) (bool, error) { return s.texts[t], nil }
func (s *probeScene) Reaches(_, _ int64, _, _ float64, _ bool) (bool, string, bool) {
	return true, "", false
}

func probe() (*log, *probeDriver, *probeScene) {
	l := &log{}
	return l, &probeDriver{l: l, backOK: true}, &probeScene{l: l, url: "https://shop.example/a", texts: map[string]bool{}}
}

var allowAll = Policy{
	AllowUpload: func(string) error { return nil },
	AllowEval:   true,
	Pacing:      &Pacing{},
	Secrets:     fakeSecrets{l: &log{}, vals: map[string]string{"pw": "s3cret"}},
}

// everyVerb exercises each statement that touches the driver or the scene.
var everyVerb = []string{
	`goto https://shop.example/a`,
	`back`,
	`click b1`,
	`hover b1`,
	`fill b1 "x"`,
	`fill b1 $secret:pw`,
	`select b1 "opt"`,
	`select b1 $secret:pw`,
	`upload b1 "/tmp/f"`,
	`press Enter`,
	`scroll down`,
	`scroll b1 50`,
	`scroll to b1`,
	`wait seconds 1`,
	`wait text "later"`,
	`wait text "later" in b1`,
	`eval 1+1`,
}

func TestEveryDriverAndSceneCallRunsUnderTheCallersContext(t *testing.T) {
	for _, src := range everyVerb {
		l, d, sc := probe()
		sc.appearAfter = 2
		p, err := Parse(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		ctx := context.WithValue(context.Background(), probeKey{}, "caller")
		r := Run(ctx, p, d, sc, allowAll)
		if r.Failed != nil {
			t.Fatalf("%s failed: %s", src, r.Failed.Cause)
		}
		if len(d.ctxBad) != 0 || sc.ctxBad != 0 {
			t.Errorf("%s: calls detached from the caller's context: driver %v, scene refreshes %d (calls %v)", src, d.ctxBad, sc.ctxBad, l.calls)
		}
	}
}

func TestADriverFailureStopsTheProgramAtThatStepWithItsCause(t *testing.T) {
	cases := []struct{ src, failOn string }{
		{`goto https://shop.example/a`, "load"},
		{`back`, "back"},
		{`click b1`, "click"},
		{`click b1`, "scrollintoview"},
		{`click b1`, "settle"},
		{`hover b1`, "move"},
		{`hover b1`, "settle"},
		{`click b1`, "move"},
		{`click b1`, "hittest"},
		{`fill b1 "x"`, "move"},
		{`fill b1 "x"`, "focus"},
		{`fill b1 "x"`, "inserttext"},
		{`fill b1 "x"`, "settle"},
		{`select b1 "o"`, "selectoption"},
		{`select b1 "o"`, "settle"},
		{`upload b1 "/tmp/f"`, "setfiles"},
		{`upload b1 "/tmp/f"`, "settle"},
		{`press Enter`, "key"},
		{`press Enter`, "settle"},
		{`scroll down`, "wheel"},
		{`scroll down`, "settle"},
		{`scroll b1 down`, "scrollintoview"},
		{`scroll to b1`, "scrollintoview"},
		{`scroll to b1`, "settle"},
		{`wait seconds 1`, "advance"},
		{`wait text "never"`, "settle"},
		{`wait text "never" in b1`, "advance"},
		{`wait gone "never"`, "settle"},
		{`eval 1`, "eval"},
		{`eval 1`, "settle"},
	}
	for _, c := range cases {
		_, d, sc := probe()
		d.failOn = c.failOn
		sc.texts["never"] = strings.HasPrefix(c.src, "wait gone") // gone: the text stays; text: it never appears
		sc.renders = []string{"AFTER"}
		r := run(t, c.src+"\nview", d, sc, allowAll)
		if r.Failed == nil || r.Failed.Step.Line != 1 || !strings.Contains(r.Failed.Cause, c.failOn+" exploded") {
			t.Errorf("%q with %s failing: %+v", c.src, c.failOn, r.Failed)
			continue
		}
		if strings.Contains(r.Output, "AFTER") {
			t.Errorf("%q with %s failing: the next step still ran", c.src, c.failOn)
		}
	}
}

func TestASceneRefreshFailureStopsTheProgramWhereverItHappens(t *testing.T) {
	for _, src := range everyVerb {
		// A clean run tells how many refreshes the statement makes.
		_, d, sc := probe()
		sc.appearAfter = 2
		if r := run(t, src, d, sc, allowAll); r.Failed != nil {
			t.Fatalf("%s: %s", src, r.Failed.Cause)
		}
		total := sc.refreshes
		for n := 1; n <= total; n++ {
			_, d, sc := probe()
			sc.appearAfter = 2
			sc.failRefreshAt = n
			r := run(t, src, d, sc, allowAll)
			if r.Failed == nil || !strings.Contains(r.Failed.Cause, fmt.Sprintf("refresh %d exploded", n)) {
				t.Errorf("%q: refresh %d of %d failed silently: %+v", src, n, total, r.Failed)
			}
		}
	}
}

func TestATargetThatCannotBeFoundAgainAfterScrollingFailsTheStep(t *testing.T) {
	for _, src := range []string{`click b1`, `hover b1`, `fill b1 "x"`, `select b1 "o"`, `upload b1 "/tmp/f"`, `scroll b1 down`} {
		_, d, sc := probe()
		sc.failFindAt = 2
		r := run(t, src, d, sc, allowAll)
		if r.Failed == nil || !strings.Contains(r.Failed.Cause, "find 2 exploded") {
			t.Errorf("%q: %+v", src, r.Failed)
		}
		if d.l.count("click") != 0 && src == `click b1` {
			t.Error("must not click a target that vanished")
		}
	}
}

func TestATargetThatBecomesBlockedWhileScrollingIsNotActedOn(t *testing.T) {
	_, d, sc := probe()
	sc.blockFindAt = 2
	r := run(t, `click b1`, d, sc, allowAll)
	if r.Failed == nil || !strings.Contains(r.Failed.Cause, "blocked: b1 hidden") || d.l.count("click") != 0 {
		t.Errorf("%+v calls=%v", r.Failed, d.l.calls)
	}
}

func TestABlockedTargetIsNeverScrolledTo(t *testing.T) {
	l, d, sc := probe()
	sc.blockFindAt = 1
	r := run(t, `click b1`, d, sc, allowAll)
	if r.Failed == nil || !strings.Contains(r.Failed.Cause, "blocked: b1 hidden") {
		t.Fatalf("%+v", r.Failed)
	}
	if l.count("scrollintoview") != 0 {
		t.Errorf("a target found blocked must be refused before the page is touched: %v", l.calls)
	}
}

func TestInitialRefreshFailureIsReportedAsARefreshFailure(t *testing.T) {
	_, d, sc := probe()
	sc.failRefreshAt = 1
	r := run(t, "click b1", d, sc, allowAll)
	if r.Failed == nil || r.Failed.Step.Verb != "refresh" || !strings.Contains(r.Failed.Cause, "refresh 1 exploded") {
		t.Errorf("%+v", r.Failed)
	}
	if d.l.count("scrollintoview") != 0 || d.l.count("click") != 0 {
		t.Error("nothing may run when the page cannot be read")
	}
}

func TestPlainValuesDoNotForceAPageReadButSecretsDo(t *testing.T) {
	refreshes := func(src string) int {
		_, d, sc := probe()
		if r := run(t, src, d, sc, allowAll); r.Failed != nil {
			t.Fatal(r.Failed.Cause)
		}
		return sc.refreshes
	}
	plain, secret := refreshes(`fill b1 "x"`), refreshes(`fill b1 $secret:pw`)
	if secret != plain+1 {
		t.Errorf("a secret is checked against where the page is now: plain %d refreshes, secret %d", plain, secret)
	}
	plain, secret = refreshes(`select b1 "x"`), refreshes(`select b1 $secret:pw`)
	if secret != plain+1 {
		t.Errorf("select: plain %d refreshes, secret %d", plain, secret)
	}
}

func TestResultStringKeepsOutputAndFailureOnSeparateLines(t *testing.T) {
	f := &Failure{Step: Step{Verb: "click", Line: 3, Args: Args{Target: Target{Ref: "b1"}}}, Cause: "boom"}
	if got := (Result{Failed: f}).String(); got != "failed line 3: click b1: boom" {
		t.Errorf("failure alone: %q", got)
	}
	if got := (Result{Failed: f, Output: "out"}).String(); got != "out\nfailed line 3: click b1: boom" {
		t.Errorf("failure after output: %q", got)
	}
	if got := (Result{Output: "out"}).String(); got != "out" {
		t.Errorf("success: %q", got)
	}
}

func TestSkippedTryStepsAreReportedWithLineStatementAndCause(t *testing.T) {
	_, d, sc := probe()
	sc.failFindAt = 1
	r := run(t, "view\ntry click b1\nview", d, sc, allowAll)
	if r.Failed != nil {
		t.Fatalf("try must not stop the program: %+v", r.Failed)
	}
	if !strings.Contains(r.Output, "skipped line 2: try click b1: find 1 exploded") {
		t.Errorf("output:\n%s", r.Output)
	}
}

func TestOutputLinesAreSeparatedOnceAndEmptyViewsAddNothing(t *testing.T) {
	_, d, sc := probe()
	sc.renders = []string{"first\n", "", "second"}
	r := run(t, "view\nview\nview", d, sc, allowAll)
	if r.Output != "first\nsecond" {
		t.Errorf("output = %q", r.Output)
	}
}

func TestOnlyAViewDirectlyBeforeTheEndSuppressesTheDelta(t *testing.T) {
	for _, c := range []struct {
		src      string
		wantDiff bool
	}{
		{"view", false},
		{"press Enter\nview", false},
		{"view\npress Enter", true},
		{"view\nview\npress Enter", true},
		{"press Enter", true},
	} {
		_, d, sc := probe()
		fs := &deltaScene{probeScene: sc, delta: "DELTA"}
		r := run(t, c.src, d, fs, allowAll)
		if got := strings.Contains(r.Output, "DELTA"); got != c.wantDiff {
			t.Errorf("%q: delta shown = %v, want %v:\n%s", c.src, got, c.wantDiff, r.Output)
		}
	}
}

type deltaScene struct {
	*probeScene
	delta string
}

func (s *deltaScene) Delta() string { return s.delta }

func TestReplyEndingInNewlineIsNotDoubleSpaced(t *testing.T) {
	_, d, sc := probe()
	fs := &deltaScene{probeScene: sc, delta: "changed\n"}
	r := run(t, "press Enter", d, fs, allowAll)
	if r.Output != "changed" {
		t.Errorf("output = %q", r.Output)
	}
}

func TestReplayIsRefusedWithGuidance(t *testing.T) {
	_, d, sc := probe()
	r := run(t, "replay 3", d, sc, allowAll)
	if r.Failed == nil || !strings.Contains(r.Failed.Cause, "not available") || !strings.Contains(r.Failed.Cause, "view net") {
		t.Errorf("%+v", r.Failed)
	}
}

func TestStepsOutsideTheLanguageAreRefused(t *testing.T) {
	_, d, sc := probe()
	r := Run(context.Background(), Program{Steps: []Step{{Verb: "format-disk", Line: 1}}}, d, sc, allowAll)
	if r.Failed == nil || !strings.Contains(r.Failed.Cause, "unsupported statement") {
		t.Errorf("%+v", r.Failed)
	}
	r = Run(context.Background(), Program{Steps: []Step{{Verb: "wait", Args: Args{Kind: "forever"}, Line: 1}}}, d, sc, allowAll)
	if r.Failed == nil || !strings.Contains(r.Failed.Cause, "unsupported wait") {
		t.Errorf("%+v", r.Failed)
	}
	r = Run(context.Background(), Program{Steps: []Step{{Verb: "expect", Args: Args{Kind: "mood"}, Line: 1}}}, d, sc, allowAll)
	if r.Failed == nil || !strings.Contains(r.Failed.Cause, "unsupported expect") {
		t.Errorf("%+v", r.Failed)
	}
}

func TestAnInvalidUrlPatternFailsTheExpectation(t *testing.T) {
	_, d, sc := probe()
	r := Run(context.Background(), Program{Steps: []Step{{Verb: "expect", Args: Args{Kind: "url~", Pattern: "("}, Line: 1}}}, d, sc, allowAll)
	if r.Failed == nil || !strings.Contains(r.Failed.Cause, "missing closing") {
		t.Errorf("%+v", r.Failed)
	}
}

func TestExpectVisibleReportsAMissingOrBlockedTarget(t *testing.T) {
	_, d, sc := probe()
	sc.failFindAt = 1
	if r := run(t, "expect visible b1", d, sc, allowAll); r.Failed == nil || !strings.Contains(r.Failed.Cause, "find 1 exploded") {
		t.Errorf("missing target: %+v", r.Failed)
	}
	_, d, sc = probe()
	sc.blockFindAt = 1
	if r := run(t, "expect visible b1", d, sc, allowAll); r.Failed == nil || !strings.Contains(r.Failed.Cause, "not visible: b1 hidden") {
		t.Errorf("blocked target: %+v", r.Failed)
	}
	_, d, sc = probe()
	if r := run(t, "expect visible b1", d, sc, allowAll); r.Failed != nil {
		t.Errorf("a findable target is visible: %+v", r.Failed)
	}
}

func TestScrollToAMissingTargetFails(t *testing.T) {
	_, d, sc := probe()
	sc.failFindAt = 1
	r := run(t, "scroll to b1", d, sc, allowAll)
	if r.Failed == nil || !strings.Contains(r.Failed.Cause, "find 1 exploded") || d.l.count("scrollintoview") != 0 {
		t.Errorf("%+v %v", r.Failed, d.l.calls)
	}
}

func TestOriginOfStripsPathQueryAndFragment(t *testing.T) {
	for in, want := range map[string]string{
		"https://shop.example/a/b?x=1#f": "https://shop.example",
		"https://shop.example?x=1":       "https://shop.example",
		"https://shop.example#f":         "https://shop.example",
		"https://shop.example":           "https://shop.example",
		"http://localhost:8080/x":        "http://localhost:8080",
		"https://?x":                     "https://",
		"about:blank":                    "about:",
		"data:text/html,x":               "data:",
		"https://user:pw@shop.example/a": "https://shop.example",
		"blob:https://shop.example/id":   "blob:",
		"not an address":                 "",
	} {
		if got := OriginOf(in); got != want {
			t.Errorf("OriginOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func (d *probeDriver) DoubleClick(ctx context.Context, _, _ float64) error {
	return d.enter(ctx, "dblclick")
}

func (d *probeDriver) Forward(ctx context.Context) (bool, error) {
	err := d.enter(ctx, "forward")
	return d.backOK, err
}

// refusedTLS is a driver whose https loads are refused, as when nothing
// answers over TLS, so a bare address falls back to http.
type refusedTLS struct{ *probeDriver }

func (d refusedTLS) Load(ctx context.Context, u string) error {
	if err := d.enter(ctx, "load "+u); err != nil {
		return err
	}
	if strings.HasPrefix(u, "https://") {
		return errors.New("net::ERR_CONNECTION_REFUSED")
	}
	return nil
}

func TestAnAddressThatFallsBackToHTTPRunsUnderTheCallersContext(t *testing.T) {
	l, d, sc := probe()
	p, err := Parse("goto example.com")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), probeKey{}, "caller")
	r := Run(ctx, p, refusedTLS{d}, sc, allowAll)
	if r.Failed != nil {
		t.Fatalf("the fallback to http must load: %s", r.Failed.Cause)
	}
	if l.index("load http://example.com") < 0 {
		t.Fatalf("http was never tried: %v", l.calls)
	}
	if len(d.ctxBad) != 0 || sc.ctxBad != 0 {
		t.Errorf("the fallback left the caller's context: driver %v, scene refreshes %d", d.ctxBad, sc.ctxBad)
	}
}

// laterLook is a scene whose first look at what is under the pointer is
// refused and whose fresh look is not, so a click is looked at twice.
type laterLook struct{ *probeScene }

func (s laterLook) Reaches(_, _ int64, _, _ float64, fresh bool) (bool, string, bool) {
	return fresh, "b1 covered-by a layer", false
}

func TestASceneRefreshFailureWhileLookingAgainUnderThePointerStopsTheStep(t *testing.T) {
	_, d, sc := probe()
	if r := run(t, `click b1`, d, laterLook{sc}, allowAll); r.Failed != nil {
		t.Fatalf("a fresh look that reaches lets the click through: %s", r.Failed.Cause)
	}
	total := sc.refreshes
	for n := 1; n <= total; n++ {
		_, d, sc := probe()
		sc.failRefreshAt = n
		r := run(t, `click b1`, d, laterLook{sc}, allowAll)
		if r.Failed == nil || !strings.Contains(r.Failed.Cause, fmt.Sprintf("refresh %d exploded", n)) {
			t.Errorf("refresh %d of %d failed silently: %+v", n, total, r.Failed)
		}
	}
	_, d, sc = probe()
	p, _ := Parse(`click b1`)
	ctx := context.WithValue(context.Background(), probeKey{}, "caller")
	Run(ctx, p, d, laterLook{sc}, allowAll)
	if len(d.ctxBad) != 0 || sc.ctxBad != 0 {
		t.Errorf("the second look left the caller's context: driver %v, scene refreshes %d", d.ctxBad, sc.ctxBad)
	}
}

// drifting is a scene whose page is replaced from its n-th refresh on.
type drifting struct {
	*fakeScene
	at int
}

func (s *drifting) Refresh(ctx context.Context) error {
	err := s.fakeScene.Refresh(ctx)
	if s.refreshes >= s.at {
		s.doc = "doc-2"
	}
	return err
}

func TestAClickWaitingOnACoverStopsWhenThePageIsReplacedOrTheTargetIsLost(t *testing.T) {
	covered := func() (*log, *fakeDriver, *fakeScene) {
		l, d, sc := setup()
		sc.doc = "doc-1"
		sc.covers, sc.coversPassing = "b1 covered-by a loading layer", true
		return l, d, sc
	}
	l, d, sc := covered()
	r := run(t, `click "Email"`, d, &drifting{fakeScene: sc, at: 3}, Policy{})
	if r.Failed == nil || !strings.Contains(r.Failed.Cause, "page changed") || l.count("click") != 0 {
		t.Errorf("a page replaced while the click waited is never clicked: %+v %v", r.Failed, l.calls)
	}

	l, d, sc = covered()
	finds := 0
	sc.onFind = func(s *fakeScene) {
		if finds++; finds == 3 { // after the first wait
			delete(s.nodes, "Email")
		}
	}
	r = run(t, `click "Email"`, d, sc, Policy{})
	if r.Failed == nil || !strings.Contains(r.Failed.Cause, "no such target") || l.count("click") != 0 {
		t.Errorf("a target lost while the click waited is reported and never clicked: %+v %v", r.Failed, l.calls)
	}
}

func TestACancelledRunStopsAtTheNextRestOfAScopedWaitOrAClickWaitingOnACover(t *testing.T) {
	for _, src := range []string{`wait text "Never" in "Email"`, `click "Email"`} {
		_, d, sc := setup()
		sc.covers, sc.coversPassing = "b1 covered-by a loading layer", true
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		p, _ := Parse(src)
		start := time.Now()
		r := Run(ctx, p, d, sc, Policy{Pacing: &Pacing{Pause: 300 * time.Millisecond}})
		cancel()
		if r.Failed == nil || !strings.Contains(r.Failed.Cause, "deadline") {
			t.Errorf("%s: a cancelled run reports why it stopped: %+v", src, r.Failed)
		}
		if elapsed := time.Since(start); elapsed >= 250*time.Millisecond {
			t.Errorf("%s: kept resting for %s after it was cancelled", src, elapsed)
		}
	}
}

// advanceFailsAt is a driver whose n-th advance of page time fails.
type advanceFailsAt struct {
	*probeDriver
	n, seen int
}

func (d *advanceFailsAt) Advance(ctx context.Context, _ time.Duration) error {
	d.seen++
	if d.seen == d.n {
		return fmt.Errorf("advance %d exploded", d.n)
	}
	return d.enter(ctx, "advance")
}

func TestAScopedWaitFailsAtWhicheverTickOfARoundFails(t *testing.T) {
	const src = `wait text "later" in b1`
	// The text shows up two rounds in, so every tick of the first round runs.
	rounds := 2*ticksPerRound + 2
	for n := 1; n <= ticksPerRound+1; n++ {
		_, pd, sc := probe()
		sc.appearAfter = rounds
		r := run(t, src, &advanceFailsAt{probeDriver: pd, n: n}, sc, allowAll)
		if r.Failed == nil || !strings.Contains(r.Failed.Cause, fmt.Sprintf("advance %d exploded", n)) {
			t.Errorf("tick %d failed silently: %+v", n, r.Failed)
		}
	}
	for n := 1; n <= ticksPerRound+1; n++ {
		_, d, sc := probe()
		sc.appearAfter, sc.failRefreshAt = rounds, n
		r := run(t, src, d, sc, allowAll)
		if r.Failed == nil || !strings.Contains(r.Failed.Cause, fmt.Sprintf("refresh %d exploded", n)) {
			t.Errorf("refresh %d failed silently: %+v", n, r.Failed)
		}
	}
	l, d, sc := probe()
	sc.appearAfter = rounds
	p, _ := Parse(src)
	ctx := context.WithValue(context.Background(), probeKey{}, "caller")
	if r := Run(ctx, p, d, sc, allowAll); r.Failed != nil {
		t.Fatalf("text that appears later is waited for: %s", r.Failed.Cause)
	}
	if len(d.ctxBad) != 0 || sc.ctxBad != 0 {
		t.Errorf("a whole round left the caller's context: driver %v, scene %d (%v)", d.ctxBad, sc.ctxBad, l.calls)
	}
}

// settlesAfterWait is a scene whose cover clears on the second look and
// whose target is found checked from then on, as a page that ticks a box
// while a click waits.
type settlesAfterWait struct {
	*probeScene
	fresh int
}

func (s *settlesAfterWait) Reaches(_, _ int64, _, _ float64, fresh bool) (bool, string, bool) {
	if fresh {
		s.fresh++
	}
	return s.fresh > 1, "b1 covered-by a loading layer", true
}

func (s *settlesAfterWait) Find(t Target) (Node, error) {
	n, err := s.probeScene.Find(t)
	n.Checked = s.fresh > 1
	return n, err
}

func TestACheckThatFindsTheBoxTickedAfterAWaitSettlesUnderTheCallersContext(t *testing.T) {
	l, d, sc := probe()
	p, _ := Parse(`check b1`)
	ctx := context.WithValue(context.Background(), probeKey{}, "caller")
	r := Run(ctx, p, d, &settlesAfterWait{probeScene: sc}, allowAll)
	if r.Failed != nil || l.count("click") != 0 {
		t.Fatalf("a box ticked while waiting is not clicked off: %+v %v", r.Failed, l.calls)
	}
	if len(d.ctxBad) != 0 || sc.ctxBad != 0 {
		t.Errorf("settling left the caller's context: driver %v, scene %d", d.ctxBad, sc.ctxBad)
	}
}
