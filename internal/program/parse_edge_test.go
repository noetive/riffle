package program

import (
	"strings"
	"testing"
)

func TestErrorsNameWhatWasExpectedAndWhatWasFound(t *testing.T) {
	cases := []struct{ src, want string }{
		{`click`, "end of line"},
		{`click   `, "end of line"},
		{`fill b1`, "end of line"},
		{`fill b1   `, "end of line"},
		{`fill nope9x "v"`, `got "nope9x"`},
		{`fill nope9x`, `got "nope9x"`}, // the target's problem is reported, not the missing value
		{`select nope9x`, `got "nope9x"`},
		{`upload nope9x`, `got "nope9x"`},
		{`upload b1`, "double-quoted"},
		{`wait text foo`, `expected the text text as a double-quoted string, got "foo"`},
		{`wait gone foo`, `expected the gone text as a double-quoted string, got "foo"`},
		{`expect text foo`, `expected the text text as a double-quoted string, got "foo"`},
		{`expect gone foo`, `expected the gone text as a double-quoted string, got "foo"`},
		{`wait text`, "double-quoted"},
		{`expect text`, "double-quoted"},
		{`expect text   `, "double-quoted"},
		{`wait seconds x`, `expected seconds as a non-negative integer, got "x"`},
		{`wait seconds 99999999999999999999`, `expected seconds as a non-negative integer, got "99999999999999999999"`},
		{`wait seconds`, "non-negative integer"},
		{`scroll 99999999999999999999`, "non-negative integer"},
		{`scroll to`, "end of line"},
		{`scroll to nope9x`, `got "nope9x"`},
		{`scroll b1`, "down, up"},
		{`scroll "x"`, "down, up"},
		{`scroll b1 sideways`, `got "sideways"`},
		{`click "abc\`, "unterminated escape"},
		{`click "abc\q"`, `unknown escape \q`},
		{`click "abc`, "unterminated quoted"},
		{`try   `, "after try"},
		{`view budget=-5`, "budget"},
		{`view budget=+5`, "budget"},
		{`view budget=`, "budget"},
		{`view budget=1 budget=2`, "twice"},
		{`view interactive budget=1 budget=2`, "twice"},
		{`view read interactive`, "budget=N"},
		{`view expand`, "ref"},
		{`view table "b1"`, "ref"},
		{`view find x`, `got "x"`},
		{`replay 99999999999999999999`, "integer"},
	}
	for _, c := range cases {
		_, err := Parse(c.src)
		if err == nil {
			t.Errorf("%q: accepted", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: error %q lacks %q", c.src, err, c.want)
		}
	}
}

func TestTrailingSpaceNeverChangesAStatement(t *testing.T) {
	for _, src := range []string{"back  ", "click b1   ", "click \"x\"  ", "fill b1 \"v\"\t", "view interactive   ", "view budget=3  "} {
		if _, err := Parse(src); err != nil {
			t.Errorf("%q: %v", src, err)
		}
	}
}

func TestViewBudgetCanComeFirstOrLast(t *testing.T) {
	for _, src := range []string{"view budget=5 interactive", "view interactive budget=5"} {
		p, err := Parse(src)
		if err != nil || len(p.Steps) != 1 {
			t.Fatalf("%q: %v", src, err)
		}
		a := p.Steps[0].Args
		if a.Kind != "interactive" || a.Budget != 5 {
			t.Errorf("%q parsed as %+v", src, a)
		}
	}
}

func TestZeroBudgetIsAllowedButMeansUnset(t *testing.T) {
	p, err := Parse("view budget=0")
	if err != nil {
		t.Fatal(err)
	}
	if p.Steps[0].Args.Budget != 0 {
		t.Errorf("budget = %d", p.Steps[0].Args.Budget)
	}
	if got := p.Steps[0].String(); got != "view" {
		t.Errorf("an unset budget is not written back: %q", got)
	}
}

func TestSmallestBudgetSurvivesTheRoundTrip(t *testing.T) {
	p, err := Parse("view find \"x\" budget=1")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Steps[0].String(); got != `view find "x" budget=1` {
		t.Errorf("got %q", got)
	}
}

func TestEscapedQuotesAndBackslashesSurviveInsideQuotes(t *testing.T) {
	p, err := Parse(`click "a\"b\\c"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Steps[0].Args.Target.Text; got != `a"b\c` {
		t.Errorf("text = %q", got)
	}
}

func TestStepStringRendersTryAndEveryKind(t *testing.T) {
	cases := map[string]string{
		`try click "Reject"`:     `try click "Reject"`,
		`scroll "Feed" up`:       `scroll "Feed" up`,
		`scroll to "Footer"`:     `scroll to "Footer"`,
		`scroll 40`:              `scroll 40`,
		`expect url ~ /a(/\d+)?`: `expect url ~ /a(/\d+)?`,
		`wait seconds 0`:         `wait seconds 0`,
	}
	for in, want := range cases {
		p, err := Parse(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got := p.String(); got != want {
			t.Errorf("%q rendered as %q", in, got)
		}
	}
}
