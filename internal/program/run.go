package program

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/noetive/riffle/internal/engine"
	"github.com/noetive/riffle/internal/pagetext"
)

// Driver is the engine port as the interpreter needs it. Coordinates are
// viewport coordinates, node identities are the engine's backend ids.
type Driver interface {
	Load(ctx context.Context, url string) error
	Back(ctx context.Context) (bool, error)
	Forward(ctx context.Context) (bool, error)
	Settle(ctx context.Context) error
	Advance(ctx context.Context, d time.Duration) error
	Move(ctx context.Context, x, y float64) error
	// HitTest returns the backend id of the topmost node at viewport
	// coordinates: the node a click there lands on.
	HitTest(ctx context.Context, x, y float64) (int64, error)
	Click(ctx context.Context, x, y float64) error
	DoubleClick(ctx context.Context, x, y float64) error
	Wheel(ctx context.Context, x, y, dy float64) error
	Key(ctx context.Context, name string) error
	Focus(ctx context.Context, backend int64) error
	InsertText(ctx context.Context, text string) error
	// SetSecret puts a secret into the node without a keystroke. Keys go
	// wherever focus is, and a frame from another origin can take the focus
	// between two of them; a value set on the node goes nowhere else.
	SetSecret(ctx context.Context, backend int64, secret string) error
	// SelectOption makes the options named by want the selection: one for a
	// select, any number for a multi-select.
	SelectOption(ctx context.Context, backend int64, want []string) error
	SetFiles(ctx context.Context, backend int64, paths []string) error
	// AnswerDialog sets how the next confirm or prompt the page opens is
	// answered, once.
	AnswerDialog(a engine.DialogAnswer)
	ScrollIntoView(ctx context.Context, backend int64) error
	Eval(ctx context.Context, expr string) (string, error)
	Drain() []engine.Event
}

// Node is a resolved target: where it is and whether it can be acted on.
type Node struct {
	// Blocked is empty when the node can be acted on, otherwise the cause
	// as a fact, such as "b5 covered-by d1 \"Cookie preferences\"".
	Blocked string
	// NotTypable is the reason the node takes no typed text, such as
	// "a checkbox; use check", when Typable is false.
	NotTypable string
	// Value is what a text field holds, when the engine can say; secrets and
	// password fields never report one.
	Value   string
	Backend int64
	// X, Y is the center of the box in viewport coordinates.
	X, Y float64
	// Passing: the block can clear on its own, such as a field a script
	// enables, a control a page shows after a delay or an overlay that fades,
	// so an action waits briefly for it. A node covered by a dialog the agent
	// can answer stays blocked until the agent answers, so it fails at once.
	Passing bool
	// Checked: a checkbox, radio or switch that is on. check leaves it so.
	Checked bool
	// ReadOnly: a field that takes text, read-only for now. fill waits for it
	// briefly, as for a disabled one.
	ReadOnly bool
	// Typable: the node is a field that takes typed text. NotTypable is the
	// reason it is not, such as "a checkbox; use check", when it is not.
	Typable    bool
	ValueKnown bool
}

// Scene is the interpreter's knowledge of the current page. Refresh rebuilds
// it from the engine; every other call reads the last refresh.
type Scene interface {
	Refresh(ctx context.Context) error
	// Find resolves a ref or quoted text. Ambiguity and stale refs are
	// errors that carry candidates or the next action.
	Find(t Target) (Node, error)
	// Render produces the view a `view` step asks for.
	Render(s Step) (string, error)
	// Delta reports what changed since the last delivery and marks it delivered.
	Delta() string
	URL() string
	// Document names the loaded document: it changes on a navigation or
	// reload, not on a move within the page by fragment.
	Document() string
	// HasText reports whether the text is visible on the page, or, when in is
	// set, inside that element. An element that cannot be found is an error,
	// ErrGone when it is one the agent was shown and it has left the page.
	HasText(text string, in Target) (bool, error)
	// Reaches reports whether a click at x,y in the viewport, landing on hit,
	// acts on target: hit is the target, inside it, a label for it, or holds
	// it with the point inside the target's own box. That last needs the
	// target as it is now, so it is only judged on a look taken after the
	// pointer arrived: fresh. When it is not, the string says what is in the
	// way, as a fact, and passing says whether it may clear by itself, such as
	// a loading layer or a target still moving into place.
	Reaches(target, hit int64, x, y float64, fresh bool) (ok bool, what string, passing bool)
}

// SecretResolver substitutes $secret:name at dispatch. The model never sees
// the value.
type SecretResolver interface {
	Resolve(name, origin string) (string, error)
}

// Policy limits what a program may do.
type Policy struct {
	// AllowURL returns nil when navigation to the URL is allowed.
	AllowURL func(url string) error
	// AllowUpload returns nil when the file may be attached; nil refuses all.
	AllowUpload func(path string) error
	Secrets     SecretResolver
	// Pacing is how long waits spend in real time; nil means DefaultPacing.
	// A host that drives a page with no network, such as a test, passes the
	// zero Pacing so a wait that cannot succeed ends after its rounds.
	Pacing *Pacing
	// AllowEval enables the eval statement.
	AllowEval bool
}

// Pacing is the real time a wait may spend. A zero bound runs its wait for
// waitRounds rounds instead, for a page with no network, such as a test's.
type Pacing struct {
	// Action bounds an action's wait for a target that is blocked for now.
	Action time.Duration
	// Text bounds `wait text` and `wait gone`.
	Text time.Duration
	// Pause is the rest between rounds.
	Pause time.Duration
}

// DefaultPacing is the pacing of a live session. An action gives up sooner
// than a text wait: a target still blocked after a few seconds needs the
// agent, while a page may legitimately take longer to show its text.
func DefaultPacing() Pacing {
	return Pacing{Action: 5 * time.Second, Text: 15 * time.Second, Pause: 250 * time.Millisecond}
}

// Event kinds the interpreter acts on. A driver reports them like any other
// page event.
const (
	// BotCheck: the page asks whether a person is there. The program stops.
	BotCheck engine.EventKind = "bot-check"
	// Refused: the page's own document was answered with a refusal status.
	Refused engine.EventKind = "refused"
)

// Failure names the step that stopped a program and why.
type Failure struct {
	Cause string
	Step  Step
}

// Result is what one program run returns to the agent.
type Result struct {
	Failed *Failure
	// Output is the accumulated delta, view output and events, as text.
	Output string
}

// String renders the result as the reply text.
func (r Result) String() string {
	if r.Failed == nil {
		return r.Output
	}
	// A cause can quote the page, as in a script error; it stays on its line.
	f := fmt.Sprintf("failed line %d: %s: %s", r.Failed.Step.Line, r.Failed.Step.String(), pagetext.Flatten(r.Failed.Cause))
	if r.Output == "" {
		return f
	}
	return r.Output + "\n" + f
}

// waitRounds bounds how many settle rounds a wait statement runs when it has
// no real time to spend, as on a page with no network.
const waitRounds = 20

// waits reports whether a wait begun at start may run round i: for its limit
// in real time, since every round is a real settle and one on a busy page
// takes seconds, or for waitRounds rounds when it has no limit.
func waits(i int, start time.Time, limit time.Duration) bool {
	if limit > 0 {
		return i == 0 || time.Since(start) < limit
	}
	return i < waitRounds
}

// Run executes the program in order, settling after each step, and stops at
// the first failed step. Optional steps (try) never stop the program. A page
// that asks whether a person is there (a BotCheck event) stops it for good,
// try or not.
func Run(ctx context.Context, p Program, d Driver, sc Scene, pol Policy) Result {
	r := &runner{d: d, sc: sc, pol: pol, pace: DefaultPacing()}
	if pol.Pacing != nil {
		r.pace = *pol.Pacing
	}
	if err := sc.Refresh(ctx); err != nil {
		return Result{Failed: &Failure{Step: Step{Verb: "refresh"}, Cause: err.Error()}}
	}
	// An answer set for a dialog that never came is not left for one the
	// next program does not expect.
	defer d.AnswerDialog(engine.DialogAnswer{})
	for _, st := range p.Steps {
		err := r.step(ctx, st)
		if origin, ok := r.collect(); ok {
			r.flush()
			cause := fmt.Sprintf("stopped: bot-check at %q; the site is asking whether a person is there. Tell the user; do not retry or try to solve it.", pagetext.Clean(origin))
			return Result{Output: strings.TrimRight(r.out.String(), "\n"), Failed: &Failure{Step: st, Cause: cause}}
		}
		if err != nil {
			if st.Try {
				r.emit(fmt.Sprintf("skipped line %d: %s: %s", st.Line, st.String(), pagetext.Flatten(err.Error())))
				continue
			}
			r.flush()
			return Result{Output: strings.TrimRight(r.out.String(), "\n"), Failed: &Failure{Step: st, Cause: err.Error()}}
		}
	}
	r.flush()
	return Result{Output: strings.TrimRight(r.out.String(), "\n")}
}

type runner struct {
	d      Driver
	sc     Scene
	pol    Policy
	events []engine.Event // drained after each step, reported at the end
	out    strings.Builder
	pace   Pacing
	viewed bool // the last step was a view, so the agent has seen the page as it now is
}

// collect takes the page events reported so far. It returns the origin of a
// bot-check among them.
func (r *runner) collect() (origin string, botCheck bool) {
	for _, e := range r.d.Drain() {
		r.events = append(r.events, e)
		if e.Kind == BotCheck {
			origin, botCheck = e.Text, true
		}
	}
	return origin, botCheck
}

func (r *runner) emit(s string) {
	if s == "" {
		return
	}
	r.out.WriteString(s)
	if !strings.HasSuffix(s, "\n") {
		r.out.WriteByte('\n')
	}
}

// flush appends the delta and the page events collected after each step.
func (r *runner) flush() {
	// A view the agent asked for after the last change already shows the page;
	// repeating it as a delta would only cost tokens.
	if delta := r.sc.Delta(); !r.viewed {
		r.emit(delta)
	}
	for _, e := range r.events {
		// Page-controlled text is quoted: it is data, never a line the agent obeys.
		r.emit(fmt.Sprintf("event %s %q", e.Kind, pagetext.Clean(e.Text)))
	}
	r.events = nil
}

func (r *runner) settle(ctx context.Context) error {
	if err := r.d.Settle(ctx); err != nil {
		return err
	}
	return r.sc.Refresh(ctx)
}

// place is where a target was first found: the document and its origin.
type place struct{ doc, origin string }

func (r *runner) here() place { return place{r.sc.Document(), OriginOf(r.sc.URL())} }

// moved fails when the page is no longer the one the target was found on.
// A redirect or script navigation while an action waits would otherwise
// carry the click or the text onto another page that happens to have a
// target of the same name.
func (r *runner) moved(from place) error {
	if now := r.here(); now != from {
		return fmt.Errorf("the page changed while waiting for the target: it is now at %s; take a fresh `view interactive` before acting", now.origin)
	}
	return nil
}

// target resolves, pre-flights and scrolls the target into view. It returns
// the node with fresh coordinates.
func (r *runner) target(ctx context.Context, t Target) (Node, error) {
	n, err := r.sc.Find(t)
	if err != nil {
		return n, err
	}
	at := r.here()
	if n.Blocked != "" {
		if n, err = r.unblock(ctx, t, n, at); err != nil {
			return n, err
		}
	}
	if err := r.d.ScrollIntoView(ctx, n.Backend); err != nil {
		return n, fmt.Errorf("scroll into view: %w", err)
	}
	if err := r.sc.Refresh(ctx); err != nil {
		return n, err
	}
	if err := r.moved(at); err != nil {
		return n, err
	}
	n, err = r.sc.Find(t)
	if err != nil {
		return n, err
	}
	if n.Blocked != "" {
		return r.unblock(ctx, t, n, at)
	}
	return n, nil
}

// unblock gives a node that is blocked for now, such as a disabled field, a
// control a page shows after a delay or a fading overlay, a few seconds to
// clear. A node blocked for a reason that needs the agent, covered by a
// dialog it can answer, fails at once. When the time is spent the error is the block as it stands and how
// long was waited, so the agent learns what is in the way. The wait ends at
// once if the page is no longer the one the target was found on.
func (r *runner) unblock(ctx context.Context, t Target, n Node, at place) (Node, error) {
	start := time.Now()
	for i := 0; n.Passing && waits(i, start, r.pace.Action); i++ {
		if err := r.rest(ctx); err != nil {
			return n, err
		}
		if err := r.moved(at); err != nil {
			return n, err
		}
		var err error
		if n, err = r.sc.Find(t); err != nil {
			return n, err
		}
		if n.Blocked == "" {
			return n, nil
		}
	}
	if n.Passing {
		return n, fmt.Errorf("blocked: %s (waited %ds)", n.Blocked, int(time.Since(start).Round(time.Second)/time.Second))
	}
	return n, fmt.Errorf("blocked: %s", n.Blocked)
}

// untilTypable gives a field that is read-only for now the few seconds a
// disabled one gets to take text again. One still read-only is refused, saying
// how long was waited.
func (r *runner) untilTypable(ctx context.Context, t Target, n Node) (Node, error) {
	at := r.here()
	start := time.Now()
	for i := 0; n.ReadOnly && waits(i, start, r.pace.Action); i++ {
		if err := r.rest(ctx); err != nil {
			return n, err
		}
		if err := r.moved(at); err != nil {
			return n, err
		}
		var err error
		if n, err = r.sc.Find(t); err != nil {
			return n, err
		}
	}
	if n.ReadOnly {
		return n, fmt.Errorf("%s is %s (waited %ds)", t.String(), n.NotTypable, int(time.Since(start).Round(time.Second)/time.Second))
	}
	return n, nil
}

// rest lets the page run one more round and pauses for the network.
func (r *runner) rest(ctx context.Context) error {
	if err := r.settle(ctx); err != nil {
		return err
	}
	return r.pause(ctx)
}

// pause waits the pacing's rest between rounds, for the network.
func (r *runner) pause(ctx context.Context) error {
	if r.pace.Pause > 0 {
		select {
		case <-time.After(r.pace.Pause):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// scopedTick is the time a wait on one element lets pass between looks, and
// ticksPerRound how many looks make one round.
const (
	scopedTick    = 100 * time.Millisecond
	ticksPerRound = 6
)

// tick lets one scoped tick pass and looks again.
func (r *runner) tick(ctx context.Context) error {
	if err := r.d.Advance(ctx, scopedTick); err != nil {
		return err
	}
	return r.sc.Refresh(ctx)
}

func (r *runner) value(v Value) (string, error) {
	if !v.IsSecret() {
		return v.Text, nil
	}
	if r.pol.Secrets == nil {
		return "", fmt.Errorf("no secret %q is registered; secrets are registered on the host", v.Secret)
	}
	return r.pol.Secrets.Resolve(v.Secret, OriginOf(r.sc.URL()))
}

// OriginOf is the origin of an address as it is printed and compared: the
// scheme://host[:port], without credentials, path, query or fragment. An
// address with no host of its own (data:, about:, blob:) is its scheme and a
// colon. An address that does not parse has no origin, "". A secret is bound
// to this origin, so credentials in the address never make one.
func OriginOf(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Scheme == "" {
		return ""
	}
	if !strings.HasPrefix(u[len(p.Scheme):], "://") {
		return p.Scheme + ":"
	}
	return p.Scheme + "://" + p.Host
}

func (r *runner) step(ctx context.Context, st Step) error {
	if st.Verb != "view" {
		r.viewed = false
	}
	a := st.Args
	switch st.Verb {
	case "goto":
		return r.goTo(ctx, a.URL)
	case "back":
		ok, err := r.d.Back(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no earlier page in history")
		}
		return r.sc.Refresh(ctx)
	case "forward":
		ok, err := r.d.Forward(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no later page in history")
		}
		return r.sc.Refresh(ctx)
	case "click", "check":
		n, err := r.target(ctx, a.Target)
		if err != nil {
			return err
		}
		if st.Verb == "check" && n.Checked {
			return r.settle(ctx) // already checked: a click would uncheck it
		}
		if n, err = r.aim(ctx, a.Target, n); err != nil {
			return err
		}
		if st.Verb == "check" && n.Checked {
			return r.settle(ctx) // the page checked it while the step waited
		}
		if err := r.d.Click(ctx, n.X, n.Y); err != nil {
			return err
		}
		return r.settle(ctx)
	case "dblclick":
		n, err := r.target(ctx, a.Target)
		if err != nil {
			return err
		}
		if n, err = r.aim(ctx, a.Target, n); err != nil {
			return err
		}
		if err := r.d.DoubleClick(ctx, n.X, n.Y); err != nil {
			return err
		}
		return r.settle(ctx)
	case "hover":
		// A hover reveals what a page shows under the pointer, so it may aim
		// at any text a person sees, such as a row whose actions appear on it.
		t := a.Target
		t.Words = true
		n, err := r.target(ctx, t)
		if err != nil {
			return err
		}
		if err := r.d.Move(ctx, n.X, n.Y); err != nil {
			return err
		}
		return r.settle(ctx)
	case "fill":
		if err := r.vet(a.Value); err != nil {
			return err
		}
		n, err := r.target(ctx, a.Target)
		if err != nil {
			return err
		}
		if n.ReadOnly {
			if n, err = r.untilTypable(ctx, a.Target, n); err != nil {
				return err
			}
		}
		if n.NotTypable != "" {
			return fmt.Errorf("%s is %s", a.Target.String(), n.NotTypable)
		}
		// Typing goes where a person's click into the field would: a field
		// under a layer would take the keys while the page throws them away.
		if n, err = r.aim(ctx, a.Target, n); err != nil {
			return err
		}
		if n.NotTypable != "" {
			return fmt.Errorf("%s is %s", a.Target.String(), n.NotTypable)
		}
		if err := r.d.Focus(ctx, n.Backend); err != nil {
			return err
		}
		val, err := r.secretValue(ctx, a.Value)
		if err != nil {
			return err
		}
		// A secret is never typed: keys follow the focus, and a frame from
		// another origin can take it midway. It is set on the field itself.
		if a.Value.IsSecret() {
			err = r.d.SetSecret(ctx, n.Backend, val)
		} else {
			err = r.d.InsertText(ctx, val)
		}
		if err != nil {
			return err
		}
		if err := r.settle(ctx); err != nil {
			return err
		}
		// A field may hold something else than was typed: letters in a number
		// field, text past a length limit, a mask. A person sees it; say so.
		// A secret is never compared or echoed.
		if !a.Value.IsSecret() && val != "" {
			if now, err := r.sc.Find(a.Target); err == nil && now.ValueKnown && now.Value != val {
				r.emit(fmt.Sprintf("note: %s holds %q after typing %q", a.Target.String(), clip(pagetext.Clean(now.Value), 60), clip(val, 60)))
			}
		}
		return nil
	case "select":
		for _, v := range a.Values {
			if err := r.vet(v); err != nil {
				return err
			}
		}
		n, err := r.target(ctx, a.Target)
		if err != nil {
			return err
		}
		vals := make([]string, 0, len(a.Values))
		for _, v := range a.Values {
			val, err := r.secretValue(ctx, v)
			if err != nil {
				return err
			}
			vals = append(vals, val)
		}
		if err := r.d.SelectOption(ctx, n.Backend, vals); err != nil {
			return err
		}
		return r.settle(ctx)
	case "upload":
		if r.pol.AllowUpload == nil {
			return fmt.Errorf("uploads are off for this session; the operator enables them")
		}
		if err := r.pol.AllowUpload(a.Path); err != nil {
			return err
		}
		n, err := r.target(ctx, a.Target)
		if err != nil {
			return err
		}
		if err := r.d.SetFiles(ctx, n.Backend, []string{a.Path}); err != nil {
			return err
		}
		return r.settle(ctx)
	case "press":
		if err := r.d.Key(ctx, a.Key); err != nil {
			return err
		}
		return r.settle(ctx)
	case "scroll":
		return r.scroll(ctx, a)
	case "wait":
		return r.wait(ctx, a)
	case "expect":
		return r.expect(a)
	case "view":
		defer func() { r.viewed = true }()
		s, err := r.sc.Render(st)
		if err != nil {
			return err
		}
		r.emit(s)
		return nil
	case "dialog":
		// A dialog blocks the page, so its answer is set before the step that
		// opens it.
		switch a.Kind {
		case "dismiss":
			r.d.AnswerDialog(engine.DialogAnswer{Dismiss: true})
		case "answer":
			r.d.AnswerDialog(engine.DialogAnswer{Text: a.Text, Typed: true})
		default:
			r.d.AnswerDialog(engine.DialogAnswer{})
		}
		return nil
	case "eval":
		if !r.pol.AllowEval {
			return fmt.Errorf("eval is off for this session; ask the operator to enable it")
		}
		v, err := r.d.Eval(ctx, a.JS)
		if err != nil {
			return err
		}
		// The result is page data: one line, like every other.
		r.emit("eval " + pagetext.Clean(v))
		return r.settle(ctx)
	case "replay":
		return fmt.Errorf("replay is not available in this build; read the page's own calls with `view net` instead")
	}
	return fmt.Errorf("unsupported statement %q", st.Verb)
}

// goTo opens an address. One typed without a scheme is https, and falls back
// to http as a browser's address bar does when nothing answers over https; an
// address that names https is never downgraded.
func (r *runner) goTo(ctx context.Context, typed string) error {
	url := withScheme(typed)
	err := r.open(ctx, url)
	if err != nil && url != typed && strings.HasPrefix(url, "https://") && noHTTPS(err) {
		if r.open(ctx, "http://"+strings.TrimPrefix(url, "https://")) == nil {
			return r.sc.Refresh(ctx)
		}
	}
	if err != nil {
		return err
	}
	return r.sc.Refresh(ctx)
}

func (r *runner) open(ctx context.Context, url string) error {
	if r.pol.AllowURL != nil {
		if err := r.pol.AllowURL(url); err != nil {
			return err
		}
	}
	return r.d.Load(ctx, url)
}

// noHTTPS: the failure says the server does not speak https, not that the
// page is missing or the policy refused it.
func noHTTPS(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "ERR_CONNECTION_REFUSED") || strings.Contains(msg, "ERR_SSL_PROTOCOL_ERROR") || strings.Contains(msg, "ERR_CONNECTION_CLOSED")
}

func (r *runner) scroll(ctx context.Context, a Args) error {
	x, y := 640.0, 400.0
	if !a.Target.IsZero() {
		n, err := r.target(ctx, a.Target)
		if err != nil {
			return err
		}
		x, y = n.X, n.Y
	}
	const page = 600
	var dy float64
	switch a.Kind {
	case "down":
		dy = page
	case "up":
		dy = -page
	case "by":
		dy = float64(a.N)
	case "to":
		n, err := r.sc.Find(a.To)
		if err != nil {
			return err
		}
		if err := r.d.ScrollIntoView(ctx, n.Backend); err != nil {
			return err
		}
		return r.settle(ctx)
	}
	if err := r.d.Wheel(ctx, x, y, dy); err != nil {
		return err
	}
	return r.settle(ctx)
}

func (r *runner) wait(ctx context.Context, a Args) error {
	switch a.Kind {
	case "seconds":
		if err := r.d.Advance(ctx, time.Duration(a.N)*time.Second); err != nil {
			return err
		}
		return r.sc.Refresh(ctx)
	case "text", "gone":
		want := a.Kind == "text"
		scoped := !a.Target.IsZero()
		start := time.Now()
		var lost error
		// look reports whether the wait is over.
		look := func() bool {
			has, err := r.sc.HasText(a.Text, a.Target)
			lost = err
			return err == nil && has == want || !want && errors.Is(err, ErrGone)
		}
		for i := 0; waits(i, start, r.pace.Text); i++ {
			if look() {
				return nil
			}
			if !want && lost != nil && !errors.Is(lost, ErrGone) {
				return lost // an element never found is not one that went
			}
			if !scoped {
				if err := r.rest(ctx); err != nil {
					return err
				}
				continue
			}
			// One element is often a value that moves, such as a progress
			// bar: it is looked at every tick, so a value it passes through
			// is seen unless it is shown for less than a tick.
			for k := 1; k < ticksPerRound && (r.pace.Text == 0 || time.Since(start) < r.pace.Text); k++ {
				if err := r.tick(ctx); err != nil {
					return err
				}
				if look() {
					return nil
				}
			}
			if err := r.tick(ctx); err != nil {
				return err
			}
			if err := r.pause(ctx); err != nil {
				return err
			}
		}
		// The last round was spent on the page; what it brought is looked at.
		if look() {
			return nil
		}
		where := ""
		if scoped {
			where = " in " + a.Target.String()
		}
		if lost != nil {
			return fmt.Errorf("text %q did not appear%s: %w", a.Text, where, lost)
		}
		if want {
			return fmt.Errorf("text %q did not appear%s", a.Text, where)
		}
		return fmt.Errorf("text %q did not go away%s", a.Text, where)
	}
	return fmt.Errorf("unsupported wait %q", a.Kind)
}

func (r *runner) expect(a Args) error {
	switch a.Kind {
	case "url~":
		re, err := regexp.Compile(a.Pattern)
		if err != nil {
			return err
		}
		if !re.MatchString(r.sc.URL()) {
			return fmt.Errorf("url is %s", r.sc.URL())
		}
	case "url=":
		if r.sc.URL() != a.Text {
			return fmt.Errorf("url is %s", r.sc.URL())
		}
	case "text", "gone":
		where := "on the page"
		if !a.Target.IsZero() {
			where = "in " + a.Target.String()
		}
		has, err := r.sc.HasText(a.Text, a.Target)
		switch {
		case a.Kind == "gone" && errors.Is(err, ErrGone):
			return nil // the element is gone, and the text with it
		case err != nil:
			return err
		case a.Kind == "text" && !has:
			return fmt.Errorf("text %q is not %s", a.Text, where)
		case a.Kind == "gone" && has:
			return fmt.Errorf("text %q is still %s", a.Text, where)
		}
	case "visible":
		n, err := r.sc.Find(a.Target)
		if err != nil {
			return err
		}
		if n.Blocked != "" {
			return fmt.Errorf("not visible: %s", n.Blocked)
		}
	default:
		return fmt.Errorf("unsupported expect %q", a.Kind)
	}
	return nil
}

// secretValue substitutes a secret as late as possible. Where the page is
// now, not where it was when the program was written, decides whether the
// secret may go here: a redirect or script navigation while the target was
// being reached must not carry the secret to another origin.
func (r *runner) secretValue(ctx context.Context, v Value) (string, error) {
	if v.IsSecret() {
		if err := r.sc.Refresh(ctx); err != nil {
			return "", err
		}
	}
	return r.value(v)
}

// vet refuses a secret that cannot be used here before the page is touched.
// The value is resolved again at dispatch, see secretValue.
func (r *runner) vet(v Value) error {
	if !v.IsSecret() {
		return nil
	}
	_, err := r.value(v)
	return err
}

// clip shortens text for a message.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// aim puts the pointer on the target and checks what is under it before a
// click or typing. The page has seen the pointer arrive by then, so an
// overlay a mousemove handler slides in, a transparent layer, or an embedded
// frame over the target is found here and not by the action. A block that
// may clear by itself, such as a loading layer or a target still moving into
// place, is given the same few seconds as a disabled control; the target is
// found again after each wait, since it may have moved. It returns the target
// as it was last found.
func (r *runner) aim(ctx context.Context, t Target, n Node) (Node, error) {
	at := r.here()
	start := time.Now()
	for i := 0; ; i++ {
		ok, what, passing, err := r.look(ctx, n)
		if errors.Is(err, engine.ErrOffViewport) {
			// The page moved the target off the visible page after it was
			// measured, as a list that grows while it is scrolled to: bring it
			// back into view and look again, as for any target still moving.
			ok, what, passing, err = false, t.String()+" is outside the visible page: the page moved it", true, nil
			if err := r.d.ScrollIntoView(ctx, n.Backend); err != nil {
				return n, fmt.Errorf("scroll into view: %w", err)
			}
		}
		if err != nil {
			return n, err
		}
		if ok {
			if i > 0 {
				// The page ran on while the step waited: the caller acts on
				// the target as it is now, checked or read-only as it may be,
				// at the point just found to reach it.
				now, err := r.sc.Find(t)
				now.X, now.Y = n.X, n.Y
				return now, err
			}
			return n, nil
		}
		if !passing {
			return n, fmt.Errorf("blocked: %s", what)
		}
		if !waits(i, start, r.pace.Action) {
			return n, fmt.Errorf("blocked: %s (waited %ds)", what, int(time.Since(start).Round(time.Second)/time.Second))
		}
		if err := r.rest(ctx); err != nil {
			return n, err
		}
		if err := r.moved(at); err != nil {
			return n, err
		}
		if n, err = r.sc.Find(t); err != nil {
			return n, err
		}
		if n.Blocked != "" {
			if n, err = r.unblock(ctx, t, n, at); err != nil {
				return n, err
			}
		}
	}
}

// look moves the pointer onto the node and reports whether an action there
// reaches it. A first verdict against it is checked once more on a fresh
// look: the node under the pointer may be new since the last one, such as a
// control that re-rendered on hover.
func (r *runner) look(ctx context.Context, n Node) (bool, string, bool, error) {
	if err := r.d.Move(ctx, n.X, n.Y); err != nil {
		return false, "", false, err
	}
	hit, err := r.d.HitTest(ctx, n.X, n.Y)
	if err != nil {
		return false, "", false, fmt.Errorf("cannot tell what is under the pointer: %w", err)
	}
	if ok, _, _ := r.sc.Reaches(n.Backend, hit, n.X, n.Y, false); ok {
		return true, "", false, nil
	}
	if err := r.sc.Refresh(ctx); err != nil {
		return false, "", false, err
	}
	ok, what, passing := r.sc.Reaches(n.Backend, hit, n.X, n.Y, true)
	return ok, what, passing, nil
}

// ErrGone is the error a Scene gives for an element the agent was shown that
// has since left the page: text in it is gone with it.
var ErrGone = errors.New("the element is no longer on the page")
