// Package program parses the line-oriented text language an agent sends to
// the browser: one statement per line, run in order.
package program

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// maxWaitSeconds caps a timed wait. The wait is real time, and one call must
// end before the client calling it gives up on it.
const maxWaitSeconds = 60

// Verbs lists every statement verb in canonical order.
var Verbs = []string{
	"goto", "back", "forward", "click", "dblclick", "fill", "select", "check", "press", "hover",
	"scroll", "upload", "wait", "expect", "dialog", "try", "view", "eval", "replay",
}

// Program is an ordered list of steps.
type Program struct {
	Steps []Step
}

// Step is one parsed statement. Try marks it optional (wrapped in try);
// Verb is then the wrapped statement's verb.
type Step struct {
	Verb string
	Args Args
	Line int
	Try  bool
}

// Args holds the operands of a step. Which fields are set depends on Verb;
// Kind selects the variant for scroll, wait, expect and view.
//
//	select: Target, Values (one, or several for a multi-select)
//	scroll: Kind down|up|to|by, Target (optional subject), To (for "to"), N (for "by")
//	wait:   Kind text|gone (Text, Target the element it must be in, if any) or seconds (N)
//	expect: Kind url~ (Pattern), url= (Text), text|gone (Text, Target as for wait), visible (Target)
//	dialog: Kind accept|answer (Text)|dismiss
//	view:   Kind ""|outline|interactive|read|table|find|expand|net|unseen,
//	        Target.Ref for table/expand, Text for find, Budget (0 = unset)
type Args struct {
	Values  []Value
	URL     string
	Value   Value
	Key     string
	Kind    string
	Text    string
	Pattern string
	Path    string
	JS      string
	Target  Target
	To      Target
	N       int
	Budget  int
}

// Target addresses an element: a ref from a prior view, or accessible text.
type Target struct {
	Ref  string
	Text string
	// Words lets quoted text name any element a person reads it on when no
	// control has the name. Set by the runner for a hover, which presses
	// nothing, never by the parser.
	Words bool
}

// IsZero reports whether no target is set.
func (t Target) IsZero() bool { return t.Ref == "" && t.Text == "" }

// Value is a literal string or a named secret that is never expanded here.
type Value struct {
	Text   string
	Secret string
}

// IsSecret reports whether the value refers to a secret.
func (v Value) IsSecret() bool { return v.Secret != "" }

// Error is a parse failure at a 1-based source line.
type Error struct {
	Msg  string
	Line int
}

func (e *Error) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

var (
	refPattern    = regexp.MustCompile(`^[A-Za-z]+[0-9]+$`)
	secretPattern = regexp.MustCompile(`^\$secret:[A-Za-z0-9_.-]+$`)
	digits        = regexp.MustCompile(`^[0-9]+$`)
)

// Parse parses source text into a Program.
func Parse(src string) (Program, error) {
	var p Program
	for i, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		st, err := parseLine(line)
		if err != nil {
			return Program{}, &Error{Line: i + 1, Msg: err.Error()}
		}
		st.Line = i + 1
		p.Steps = append(p.Steps, st)
	}
	return p, nil
}

type scanner struct {
	s   string
	pos int
}

func (sc *scanner) skip() {
	for sc.pos < len(sc.s) && (sc.s[sc.pos] == ' ' || sc.s[sc.pos] == '\t') {
		sc.pos++
	}
}

func (sc *scanner) done() bool { sc.skip(); return sc.pos >= len(sc.s) }

func (sc *scanner) peekQuoted() bool { sc.skip(); return sc.pos < len(sc.s) && sc.s[sc.pos] == '"' }

// word returns the next whitespace-delimited bare token, or "".
func (sc *scanner) word() string {
	sc.skip()
	start := sc.pos
	for sc.pos < len(sc.s) && sc.s[sc.pos] != ' ' && sc.s[sc.pos] != '\t' {
		sc.pos++
	}
	return sc.s[start:sc.pos]
}

func (sc *scanner) peekWord() string {
	save := sc.pos
	w := sc.word()
	sc.pos = save
	return w
}

func (sc *scanner) rest() string {
	sc.skip()
	r := strings.TrimSpace(sc.s[sc.pos:])
	sc.pos = len(sc.s)
	return r
}

// quoted reads a double-quoted string with \", \\, \n and \t escapes. A
// program is one statement per line, so a newline can only be written as \n.
func (sc *scanner) quoted() (string, error) {
	sc.skip()
	if sc.pos >= len(sc.s) || sc.s[sc.pos] != '"' {
		return "", fmt.Errorf("expected a double-quoted string")
	}
	sc.pos++
	var b strings.Builder
	for sc.pos < len(sc.s) {
		c := sc.s[sc.pos]
		switch c {
		case '"':
			sc.pos++
			if sc.pos < len(sc.s) && sc.s[sc.pos] != ' ' && sc.s[sc.pos] != '\t' {
				return "", fmt.Errorf("expected whitespace or end of line after closing quote")
			}
			return b.String(), nil
		case '\\':
			if sc.pos+1 >= len(sc.s) {
				return "", fmt.Errorf("unterminated escape in quoted string")
			}
			n := sc.s[sc.pos+1]
			switch n {
			case '"', '\\':
				b.WriteByte(n)
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return "", fmt.Errorf("unknown escape \\%c in quoted string, expected \\\", \\\\, \\n or \\t", n)
			}
			sc.pos += 2
		default:
			b.WriteByte(c)
			sc.pos++
		}
	}
	return "", fmt.Errorf("unterminated quoted string")
}

func (sc *scanner) target() (Target, error) {
	if sc.done() {
		return Target{}, fmt.Errorf("expected a target (ref like b5 or quoted text), got end of line")
	}
	if sc.peekQuoted() {
		s, err := sc.quoted()
		if err != nil {
			return Target{}, err
		}
		if s == "" {
			return Target{}, fmt.Errorf("expected a target, got empty quoted text")
		}
		return Target{Text: s}, nil
	}
	w := sc.word()
	if !refPattern.MatchString(w) {
		return Target{}, fmt.Errorf("expected a target (ref like b5 or quoted text), got %q", w)
	}
	return Target{Ref: w}, nil
}

func (sc *scanner) ref() (string, error) {
	w := sc.word()
	if !refPattern.MatchString(w) {
		return "", fmt.Errorf("expected a ref like b5, got %q", w)
	}
	return w, nil
}

func (sc *scanner) value() (Value, error) {
	if sc.done() {
		return Value{}, fmt.Errorf("expected a value (quoted string or $secret:name), got end of line")
	}
	if sc.peekQuoted() {
		s, err := sc.quoted()
		return Value{Text: s}, err
	}
	w := sc.word()
	if !secretPattern.MatchString(w) {
		return Value{}, fmt.Errorf("expected a value (quoted string or $secret:name), got %q", w)
	}
	return Value{Secret: strings.TrimPrefix(w, "$secret:")}, nil
}

func (sc *scanner) text(what string) (string, error) {
	if !sc.peekQuoted() {
		return "", fmt.Errorf("expected %s as a double-quoted string, got %q", what, sc.peekWord())
	}
	return sc.quoted()
}

func (sc *scanner) integer(what string) (int, error) {
	w := sc.word()
	if !digits.MatchString(w) {
		return 0, fmt.Errorf("expected %s as a non-negative integer, got %q", what, w)
	}
	n, err := strconv.Atoi(w)
	if err != nil {
		return 0, fmt.Errorf("expected %s as a non-negative integer, got %q", what, w)
	}
	return n, nil
}

func (sc *scanner) end() error {
	if sc.done() {
		return nil
	}
	return fmt.Errorf("unexpected trailing input %q", sc.rest())
}

func parseLine(line string) (Step, error) {
	sc := &scanner{s: line}
	verb := sc.word()
	var st Step
	if verb == "try" {
		st.Try = true
		if sc.done() {
			return st, fmt.Errorf("expected a statement after try, got end of line")
		}
		verb = sc.word()
		if verb == "try" {
			return st, fmt.Errorf("try cannot be nested")
		}
	}
	st.Verb = verb
	var err error
	a := &st.Args
	switch verb {
	case "goto":
		// Agents quote URLs as they quote every other argument; accept both.
		if sc.peekQuoted() {
			if a.URL, err = sc.quoted(); err == nil && a.URL == "" {
				err = fmt.Errorf("expected a URL after goto, got \"\"")
			}
		} else if a.URL = sc.word(); a.URL == "" {
			return st, fmt.Errorf("expected a URL after goto, got end of line")
		}
	case "back", "forward":
	case "click", "dblclick", "check", "hover":
		a.Target, err = sc.target()
	case "fill":
		if a.Target, err = sc.target(); err == nil {
			a.Value, err = sc.value()
		}
	case "select":
		if a.Target, err = sc.target(); err != nil {
			break
		}
		// One value, or several for a multi-select: those become the selection.
		for err == nil && (len(a.Values) == 0 || !sc.done()) {
			var v Value
			if v, err = sc.value(); err == nil {
				a.Values = append(a.Values, v)
			}
		}
	case "press":
		a.Key = sc.word()
		if a.Key == "" {
			return st, fmt.Errorf("expected a key name after press, got end of line")
		}
	case "scroll":
		err = parseScroll(sc, a)
	case "upload":
		if a.Target, err = sc.target(); err == nil {
			a.Path, err = sc.text("a file path")
		}
	case "wait":
		err = parseWait(sc, a)
	case "expect":
		err = parseExpect(sc, a)
	case "view":
		err = parseView(sc, a)
	case "eval":
		a.JS = sc.rest()
		if a.JS == "" {
			return st, fmt.Errorf("expected JavaScript after eval, got end of line")
		}
	case "replay":
		a.N, err = sc.integer("a request number")
	case "dialog":
		err = parseDialog(sc, a)
	default:
		return st, fmt.Errorf("unknown verb %q, expected one of: %s", verb, strings.Join(Verbs, ", "))
	}
	if err != nil {
		return st, err
	}
	return st, sc.end()
}

func parseScroll(sc *scanner, a *Args) error {
	if sc.done() {
		return fmt.Errorf("expected [TARGET] then down, up, to TARGET or a number, got end of line")
	}
	if w := sc.peekWord(); sc.peekQuoted() || refPattern.MatchString(w) {
		t, err := sc.target()
		if err != nil {
			return err
		}
		a.Target = t
	}
	w := sc.peekWord()
	switch {
	case w == "down" || w == "up":
		a.Kind = sc.word()
	case w == "to":
		sc.word()
		a.Kind = "to"
		t, err := sc.target()
		if err != nil {
			return err
		}
		a.To = t
	case digits.MatchString(w):
		a.Kind = "by"
		n, err := sc.integer("a scroll amount")
		if err != nil {
			return err
		}
		a.N = n
	default:
		return fmt.Errorf("expected down, up, to TARGET or a number, got %q", w)
	}
	return nil
}

func parseWait(sc *scanner, a *Args) error {
	w := sc.word()
	var err error
	switch w {
	case "text", "gone":
		a.Kind = w
		a.Text, err = sc.text("the " + w + " text")
		if err == nil {
			a.Target, err = sc.within()
		}
	case "seconds":
		a.Kind = w
		a.N, err = sc.integer("seconds")
		if err == nil && a.N > maxWaitSeconds {
			return fmt.Errorf("wait seconds %d is more than the %d allowed; to wait longer, wait again in your next call", a.N, maxWaitSeconds)
		}
	default:
		return fmt.Errorf("expected text, gone or seconds after wait, got %q", w)
	}
	return err
}

func parseExpect(sc *scanner, a *Args) error {
	w := sc.word()
	var err error
	switch w {
	case "url":
		op := sc.word()
		switch op {
		case "~":
			a.Kind = "url~"
			a.Pattern = sc.rest()
			if a.Pattern == "" {
				return fmt.Errorf("expected a regular expression after url ~, got end of line")
			}
			if _, err := regexp.Compile(a.Pattern); err != nil {
				return fmt.Errorf("invalid regular expression %q: %w", a.Pattern, err)
			}
		case "=":
			a.Kind = "url="
			a.Text, err = sc.text("the URL")
		default:
			return fmt.Errorf("expected ~ or = after url, got %q", op)
		}
	case "text", "gone":
		a.Kind = w
		a.Text, err = sc.text("the " + w + " text")
		if err == nil {
			a.Target, err = sc.within()
		}
	case "visible":
		a.Kind = w
		a.Target, err = sc.target()
	default:
		return fmt.Errorf("expected url, text, gone or visible after expect, got %q", w)
	}
	return err
}

func parseView(sc *scanner, a *Args) error {
	seenBudget := false
	first := true
	for !sc.done() {
		w := sc.peekWord()
		if strings.HasPrefix(w, "budget=") {
			if seenBudget {
				return fmt.Errorf("budget given twice")
			}
			sc.word()
			n, err := strconv.Atoi(strings.TrimPrefix(w, "budget="))
			if err != nil || n < 0 || !digits.MatchString(strings.TrimPrefix(w, "budget=")) {
				return fmt.Errorf("expected budget=N with a non-negative integer, got %q", w)
			}
			a.Budget = n
			seenBudget = true
			continue
		}
		if !first {
			return fmt.Errorf("unexpected %q, expected budget=N", w)
		}
		first = false
		sc.word()
		var err error
		switch w {
		case "outline", "interactive", "read", "net", "unseen", "help":
			a.Kind = w
		case "table", "expand":
			a.Kind = w
			a.Target.Ref, err = sc.ref()
		case "find":
			a.Kind = w
			a.Text, err = sc.text("the search text")
		default:
			return fmt.Errorf("expected projection outline, interactive, read, table REF, find \"text\", expand REF, net, unseen or help, or budget=N, got %q", w)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(s) + `"`
}

func (t Target) String() string {
	if t.Ref != "" {
		return t.Ref
	}
	return quote(t.Text)
}

func (v Value) String() string {
	if v.Secret != "" {
		return "$secret:" + v.Secret
	}
	return quote(v.Text)
}

// String renders the step as canonical source; Parse(String()) reproduces it.
func (s Step) String() string {
	a := s.Args
	var parts []string
	if s.Try {
		parts = append(parts, "try")
	}
	parts = append(parts, s.Verb)
	switch s.Verb {
	case "goto":
		u := a.URL
		if strings.ContainsAny(u, " \t\"\\\n") {
			u = quote(u)
		}
		parts = append(parts, u)
	case "click", "dblclick", "check", "hover":
		parts = append(parts, a.Target.String())
	case "fill":
		parts = append(parts, a.Target.String(), a.Value.String())
	case "select":
		parts = append(parts, a.Target.String())
		for _, v := range a.Values {
			parts = append(parts, v.String())
		}
	case "press":
		parts = append(parts, a.Key)
	case "scroll":
		if !a.Target.IsZero() {
			parts = append(parts, a.Target.String())
		}
		switch a.Kind {
		case "to":
			parts = append(parts, "to", a.To.String())
		case "by":
			parts = append(parts, strconv.Itoa(a.N))
		default:
			parts = append(parts, a.Kind)
		}
	case "upload":
		parts = append(parts, a.Target.String(), quote(a.Path))
	case "wait":
		if a.Kind == "seconds" {
			parts = append(parts, "seconds", strconv.Itoa(a.N))
		} else {
			parts = append(parts, a.Kind, quote(a.Text))
			parts = within(parts, a.Target)
		}
	case "expect":
		switch a.Kind {
		case "url~":
			parts = append(parts, "url", "~", a.Pattern)
		case "url=":
			parts = append(parts, "url", "=", quote(a.Text))
		case "visible":
			parts = append(parts, "visible", a.Target.String())
		default:
			parts = append(parts, a.Kind, quote(a.Text))
			parts = within(parts, a.Target)
		}
	case "view":
		switch a.Kind {
		case "":
		case "table", "expand":
			parts = append(parts, a.Kind, a.Target.Ref)
		case "find":
			parts = append(parts, a.Kind, quote(a.Text))
		default:
			parts = append(parts, a.Kind)
		}
		if a.Budget > 0 {
			parts = append(parts, "budget="+strconv.Itoa(a.Budget))
		}
	case "eval":
		parts = append(parts, a.JS)
	case "replay":
		parts = append(parts, strconv.Itoa(a.N))
	case "dialog":
		if a.Kind == "answer" {
			parts = append(parts, "accept", quote(a.Text))
		} else {
			parts = append(parts, a.Kind)
		}
	}
	return strings.Join(parts, " ")
}

// within adds the `in TARGET` of a text wait or expect, when it has one.
func within(parts []string, t Target) []string {
	if t.IsZero() {
		return parts
	}
	return append(parts, "in", t.String())
}

// String renders the program as canonical source, one step per line.
func (p Program) String() string {
	lines := make([]string, len(p.Steps))
	for i, s := range p.Steps {
		lines[i] = s.String()
	}
	return strings.Join(lines, "\n")
}

var (
	// hasScheme matches text that opens with a scheme, such as https: or about:.
	hasScheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)
	// hostWithPort matches host:port, which looks like a scheme but is not.
	hostWithPort = regexp.MustCompile(`^[^/?#:]+:[0-9]+([/?#]|$)`)
)

// withScheme reads a goto target the way a person types an address: a host
// with no scheme is an https address. Anything that carries a scheme, and
// anything that is not a host, is left alone for the policy to judge.
func withScheme(u string) string {
	switch {
	case u == "" || strings.HasPrefix(u, "/"):
		return u
	case strings.HasPrefix(u, "["): // [::1]:9000
		return "https://" + u
	case hasScheme.MatchString(u) && !hostWithPort.MatchString(u):
		return u
	case strings.Contains(u, ".") || strings.HasPrefix(u, "localhost"):
		return "https://" + u
	}
	return u
}

// within reads an optional `in TARGET`: the element a text must be in.
func (sc *scanner) within() (Target, error) {
	if sc.done() {
		return Target{}, nil
	}
	if w := sc.word(); w != "in" {
		return Target{}, fmt.Errorf("expected in TARGET or end of line after the text, got %q", w)
	}
	return sc.target()
}

func parseDialog(sc *scanner, a *Args) error {
	if sc.done() {
		return fmt.Errorf("expected accept, accept \"text\" or dismiss after dialog, got end of line")
	}
	switch w := sc.word(); w {
	case "dismiss":
		a.Kind = w
	case "accept":
		a.Kind = w
		if sc.peekQuoted() {
			a.Kind = "answer"
			var err error
			a.Text, err = sc.quoted()
			return err
		}
	default:
		return fmt.Errorf("expected accept, accept \"text\" or dismiss after dialog, got %q", w)
	}
	return nil
}
