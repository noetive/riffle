package program

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

var verbSamples = map[string][]string{
	"goto":     {`goto https://example.com/a?b=1`},
	"back":     {`back`},
	"forward":  {`forward`},
	"click":    {`click b5`, `click "Sign in"`},
	"dblclick": {`dblclick "Todo item"`},
	"fill":     {`fill "Email" "a@b.c"`, `fill f2 $secret:shop`},
	"select":   {`select d1 "Sweden"`, `select f4 "Red" "Blue"`},
	"check":    {`check a1`, `check "Remember me"`},
	"press":    {`press Enter`, `press Control+A`},
	"hover":    {`hover b5`},
	"scroll":   {`scroll down`, `scroll up`, `scroll 300`, `scroll to b5`, `scroll "Feed" down`, `scroll r3 to "Footer"`, `scroll r3 40`},
	"upload":   {`upload f2 "/tmp/my file.pdf"`},
	"wait":     {`wait text "Done"`, `wait gone "Loading"`, `wait seconds 3`, `wait text "40%" in r4`, `wait gone "Saving" in "Status"`},
	"expect":   {`expect url ~ /account(/\d+)?`, `expect url = "https://x.example/"`, `expect text "Welcome"`, `expect gone "Spinner"`, `expect visible b5`, `expect text "Done" in r2`},
	"try":      {`try click "Reject"`},
	"view":     {`view`, `view outline`, `view interactive budget=800`, `view read`, `view table r3`, `view find "price"`, `view expand b5 budget=10`, `view net`, `view unseen`, `view budget=5`},
	"eval":     {`eval document.title + "x" // hi`},
	"replay":   {`replay 2`},
	"dialog":   {`dialog dismiss`, `dialog accept`, `dialog accept "blue"`, `dialog accept ""`},
}

func TestRoundTripEveryVerb(t *testing.T) {
	for _, v := range Verbs {
		samples := verbSamples[v]
		if len(samples) == 0 {
			t.Fatalf("no samples for verb %s", v)
		}
		for _, src := range samples {
			p, err := Parse(src)
			if err != nil {
				t.Fatalf("%q: %v", src, err)
			}
			if len(p.Steps) != 1 {
				t.Fatalf("%q: %d steps", src, len(p.Steps))
			}
			out := p.Steps[0].String()
			if out != src {
				t.Errorf("canonical form of %q = %q", src, out)
			}
			p2, err := Parse(out)
			if err != nil || !reflect.DeepEqual(p2.Steps[0], p.Steps[0]) {
				t.Errorf("%q does not round trip: %v %+v vs %+v", src, err, p2, p)
			}
		}
	}
}

func TestMemoExample(t *testing.T) {
	src := `try click "Reject"
fill "Email" "ralph@example.com"
fill "Password" $secret:shop
click "Sign in"
expect url ~ /account
view interactive budget=800`
	p, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 6 {
		t.Fatalf("steps = %d", len(p.Steps))
	}
	if !p.Steps[0].Try || p.Steps[0].Verb != "click" || p.Steps[0].Args.Target.Text != "Reject" {
		t.Errorf("try wrapper: %+v", p.Steps[0])
	}
	if p.Steps[1].Try {
		t.Error("only the wrapped step is optional")
	}
	if p.String() != src {
		t.Errorf("program render differs:\n%s", p.String())
	}
}

func TestSecretsAreNotExpanded(t *testing.T) {
	p, err := Parse(`fill "Password" $secret:shop-1`)
	if err != nil {
		t.Fatal(err)
	}
	v := p.Steps[0].Args.Value
	if !v.IsSecret() || v.Secret != "shop-1" || v.Text != "" {
		t.Errorf("value = %+v", v)
	}
	// A quoted lookalike is a literal, not a secret.
	p, _ = Parse(`fill "P" "$secret:shop"`)
	if v := p.Steps[0].Args.Value; v.IsSecret() || v.Text != "$secret:shop" {
		t.Errorf("quoted lookalike = %+v", v)
	}
	if got := p.Steps[0].String(); got != `fill "P" "$secret:shop"` {
		t.Errorf("render %q", got)
	}
}

func TestQuotedEscapes(t *testing.T) {
	p, err := Parse(`click "say \"hi\" \\ back"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Steps[0].Args.Target.Text; got != `say "hi" \ back` {
		t.Errorf("text = %q", got)
	}
	again, err := Parse(p.Steps[0].String())
	if err != nil || again.Steps[0].Args.Target != p.Steps[0].Args.Target {
		t.Errorf("round trip: %v %+v", err, again)
	}
}

func TestRefVersusText(t *testing.T) {
	p, _ := Parse("click b5\nclick \"b5\"")
	if a := p.Steps[0].Args.Target; a.Ref != "b5" || a.Text != "" {
		t.Errorf("ref = %+v", a)
	}
	if a := p.Steps[1].Args.Target; a.Text != "b5" || a.Ref != "" {
		t.Errorf("text = %+v", a)
	}
}

func TestCommentsBlankLinesAndLineNumbers(t *testing.T) {
	p, err := Parse("# c\n\n  \r\nback\n   # x\nclick b1\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 2 || p.Steps[0].Line != 4 || p.Steps[1].Line != 6 {
		t.Errorf("steps = %+v", p.Steps)
	}
	empty, err := Parse("# only\n")
	if err != nil || len(empty.Steps) != 0 {
		t.Errorf("empty = %+v %v", empty, err)
	}
}

func TestErrors(t *testing.T) {
	cases := []struct{ src, want string }{
		{"back\nfrobnicate x", "frobnicate"},
		{"back\n\n# c\nclick", "target"},
		{`click "open`, "unterminated"},
		{`click "a\x"`, "escape"},
		{`click "a"b`, "closing quote"},
		{`click ""`, "empty"},
		{`click submit`, "target"},
		{`fill b1 hunter2`, "value"},
		{`fill b1`, "value"},
		{`fill b1 $secret:`, "value"},
		{`back now`, "trailing"},
		{`try`, "statement"},
		{`try try back`, "nested"},
		{`scroll`, "down"},
		{`scroll sideways`, "down, up"},
		{`wait`, "seconds"},
		{`wait seconds -1`, "integer"},
		{`expect url ~`, "regular expression"},
		{`expect url ~ (`, "invalid"},
		{`expect url > x`, "~ or ="},
		{`expect url = /x`, "double-quoted"},
		{`view bogus`, "projection"},
		{`view table`, "ref"},
		{`view budget=x`, "budget"},
		{`view outline read`, "budget"},
		{`eval`, "JavaScript"},
		{`replay x`, "integer"},
		{`goto`, "URL"},
		{`press`, "key"},
		{`upload b1 /tmp/x`, "double-quoted"},
	}
	for _, c := range cases {
		_, err := Parse(c.src)
		var pe *Error
		if !errors.As(err, &pe) {
			t.Errorf("%q: want *Error, got %v", c.src, err)
			continue
		}
		wantLine := strings.Count(c.src, "\n") + 1
		if pe.Line != wantLine {
			t.Errorf("%q: line %d, want %d", c.src, pe.Line, wantLine)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: error %q lacks %q", c.src, err, c.want)
		}
	}
}

func TestUnknownVerbListsValidVerbs(t *testing.T) {
	_, err := Parse("nope")
	for _, v := range Verbs {
		if !strings.Contains(err.Error(), v) {
			t.Errorf("error lacks verb %s: %v", v, err)
		}
	}
}

func TestTryWrapsAnyStatement(t *testing.T) {
	for _, v := range Verbs {
		if v == "try" {
			continue
		}
		for _, src := range verbSamples[v] {
			p, err := Parse("try " + src)
			if err != nil {
				t.Fatalf("try %s: %v", src, err)
			}
			s := p.Steps[0]
			if !s.Try || s.Verb != v || s.String() != "try "+src {
				t.Errorf("try %s -> %+v %q", src, s, s.String())
			}
		}
	}
}

func TestQuotedStringsCanCarryNewlinesAndTabs(t *testing.T) {
	p, err := Parse(`fill "Notes" "line one\nline two\tend \\n"`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := p.Steps[0].Args.Value.Text, "line one\nline two\tend \\n"; got != want {
		t.Errorf("value = %q, want %q", got, want)
	}
}

func TestRenderedProgramKeepsNewlinesAndTabsInOneLine(t *testing.T) {
	p, err := Parse(`fill "Notes" "a\nb\tc"`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := p.String(), `fill "Notes" "a\nb\tc"`; got != want {
		t.Errorf("rendered %q, want %q", got, want)
	}
}

func TestAnAddressWithoutASchemeIsHTTPS(t *testing.T) {
	for in, want := range map[string]string{
		"news.example":              "https://news.example",
		"www.forum.example":         "https://www.forum.example",
		"example.com/path?q=1#frag": "https://example.com/path?q=1#frag",
		"localhost:8080":            "https://localhost:8080",
		"localhost:8080/app":        "https://localhost:8080/app",
		"192.168.0.1":               "https://192.168.0.1",
		"[::1]:9000":                "https://[::1]:9000",
		"https://example.com":       "https://example.com",
		"http://example.com":        "http://example.com",
		"HTTPS://EXAMPLE.COM":       "HTTPS://EXAMPLE.COM",
		"about:blank":               "about:blank",
		"data:text/html,<b>x</b>":   "data:text/html,<b>x</b>",
		"javascript:alert(1)":       "javascript:alert(1)",
		"file:///etc/passwd":        "file:///etc/passwd",
		"mailto:a@b.co":             "mailto:a@b.co",
		"//example.com/x":           "//example.com/x",
		"/relative":                 "/relative",
		"word":                      "word",
	} {
		if got := withScheme(in); got != want {
			t.Errorf("withScheme(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAGotoIsKeptAsTypedSoItRendersAndParsesBackTheSame(t *testing.T) {
	p, err := Parse("goto news.example")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Steps[0].Args.URL; got != "news.example" || p.String() != "goto news.example" {
		t.Errorf("url %q rendered %q", got, p.String())
	}
}

func TestABareAddressFallsBackToHTTPWhenHTTPSIsNotThere(t *testing.T) {
	l := &log{}
	d := &fakeDriver{l: l, loadFail: map[string]string{"https://oldsite.example": "net::ERR_CONNECTION_REFUSED"}}
	sc := &fakeScene{l: l, texts: map[string]bool{}, nodes: map[string]Node{}}
	if r := run(t, "goto oldsite.example", d, sc, Policy{}); r.Failed != nil {
		t.Fatalf("a typed address falls back to http: %s", r)
	}
	if l.count("load http://oldsite.example") != 1 {
		t.Errorf("no http fallback:\n%v", l.calls)
	}
	// An address that says https is never downgraded.
	l2 := &log{}
	d2 := &fakeDriver{l: l2, loadFail: map[string]string{"https://oldsite.example": "net::ERR_CONNECTION_REFUSED"}}
	if r := run(t, "goto https://oldsite.example", d2, &fakeScene{l: l2, texts: map[string]bool{}, nodes: map[string]Node{}}, Policy{}); r.Failed == nil {
		t.Error("an explicit https address must not fall back to http")
	}
	if l2.count("load http://") != 0 {
		t.Errorf("downgraded an explicit https address:\n%v", l2.calls)
	}
}

func TestAWaitEndsBeforeTheCallerGivesUpSoALongerOneIsMadeInALaterCall(t *testing.T) {
	if _, err := Parse(fmt.Sprintf("wait seconds %d", maxWaitSeconds)); err != nil {
		t.Errorf("the longest wait is allowed: %v", err)
	}
	for _, n := range []int{maxWaitSeconds + 1, 3600, 86400, 1000000} {
		_, err := Parse(fmt.Sprintf("wait seconds %d", n))
		if err == nil {
			t.Errorf("wait seconds %d: a wait this long outlasts the call that holds it", n)
			continue
		}
		if !strings.Contains(err.Error(), fmt.Sprint(maxWaitSeconds)) || !strings.Contains(err.Error(), "again") {
			t.Errorf("the error must name the cap and what to do instead: %v", err)
		}
	}
}

func TestAQuotedGotoOpensTheURLInsideTheQuotes(t *testing.T) {
	for src, want := range map[string]string{
		`goto "https://example.com/a?b=1"`: "https://example.com/a?b=1",
		`goto "example.com"`:               "example.com",
		`goto "https://example.com/a b"`:   "https://example.com/a b",
	} {
		p, err := Parse(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if got := p.Steps[0].Args.URL; got != want {
			t.Errorf("%s: url %q, want %q", src, got, want)
		}
		back, err := Parse(p.String())
		if err != nil || back.Steps[0].Args.URL != want {
			t.Errorf("%s rendered %q, which parses back to %+v (%v)", src, p.String(), back, err)
		}
	}
	if _, err := Parse(`goto ""`); err == nil {
		t.Error("an empty quoted URL must not parse")
	}
}

func TestDialogSelectAndScopedTextParse(t *testing.T) {
	for src, want := range map[string]Args{
		`dialog dismiss`:            {Kind: "dismiss"},
		`dialog accept`:             {Kind: "accept"},
		`dialog accept "blue"`:      {Kind: "answer", Text: "blue"},
		`dialog accept ""`:          {Kind: "answer"},
		`select f4 "Red" "Blue"`:    {Target: Target{Ref: "f4"}, Values: []Value{{Text: "Red"}, {Text: "Blue"}}},
		`wait text "40%" in r4`:     {Kind: "text", Text: "40%", Target: Target{Ref: "r4"}},
		`expect gone "x" in "Box"`:  {Kind: "gone", Text: "x", Target: Target{Text: "Box"}},
		`expect text "Done"`:        {Kind: "text", Text: "Done"},
		`select d1 $secret:country`: {Target: Target{Ref: "d1"}, Values: []Value{{Secret: "country"}}},
	} {
		p, err := Parse(src)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if got := p.Steps[0].Args; !reflect.DeepEqual(got, want) {
			t.Errorf("%q: args %+v, want %+v", src, got, want)
		}
	}
	if _, err := Parse(`dialog`); err == nil || !strings.Contains(err.Error(), "end of line") {
		t.Errorf("an empty dialog says what is missing: %v", err)
	}
	for _, bad := range []string{`dialog`, `dialog maybe`, `dialog dismiss "x"`, `select f4`, `wait text "x" in`, `wait text "x" on r4`, `wait seconds 3 in r4`, `expect url = "x" in r4`} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
