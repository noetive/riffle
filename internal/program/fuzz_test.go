package program

import (
	"reflect"
	"strings"
	"testing"
)

// FuzzParse checks two properties on arbitrary source: Parse never panics,
// and the canonical rendering of a parsed program parses back to the same
// steps. Line numbers are excluded because rendering drops blank lines and
// comments.
func FuzzParse(f *testing.F) {
	for _, group := range verbSamples {
		for _, src := range group {
			f.Add(src)
		}
	}
	f.Add("try click \"Reject\"\nview interactive budget=800")
	f.Add("# comment\n\nclick \"say \\\"hi\\\" \\\\ back\"")
	f.Add("")
	for _, src := range []string{
		"goto https://shop.example/cart\nclick \"Reject\"\nfill \"Email\" \"ralph@example.com\"\nfill \"Password\" $secret:shop\nclick \"Sign in\"\nexpect url ~ /account\nview interactive budget=800",
		"# login\ntry click \"Accept all\"\n\n  fill   f1   \"a\"  \nwait text \"Welcome\"\nexpect text \"Welcome\"\nexpect gone \"Loading\"",
		"fill \"Notes\" \"line one\\nline two\\ttab\"\nfill \"Q\" \"quote \\\" and slash \\\\ and \\n\"",
		"press Shift+Tab\npress Control+a\npress Meta+Backspace\npress Enter\npress +\npress Shift++",
		"scroll down\nscroll up\nscroll 400\nscroll -200\nscroll b5 down\nscroll to \"Footer\"\nscroll to a9",
		"wait seconds 0\nwait seconds 3\nwait gone \"Spinner\"\nwait text \"Done\"",
		"expect url = \"https://example.com/done\"\nexpect url ~ ^https://example\\.com/(a|b)+$\nexpect visible \"Checkout\"\nexpect visible b3",
		"view\nview outline\nview interactive\nview read budget=300\nview table r2\nview find \"price\"\nview expand r3\nview net\nview unseen\nview help",
		"upload \"Resume\" \"/tmp/cv.pdf\"\nselect \"Country\" \"Sweden\"\ncheck \"Terms\"\nhover \"Menu\"\nback",
		"eval document.title\neval 1 + 1\neval ({a: 'b'}).a",
		"replay 3",
		"try try back", "try", "click", "click b", "click b0", "click b-1", "click B5", "click \"\"", "click \"\"\"", "click \"a\"b", "click \"a\" extra",
		"goto", "goto \"https://a\"", "goto https://a b", "goto http://[::1]:80/x?y#z", "goto javascript:alert(1)", "goto data:text/html,<b>x</b>",
		"fill \"a\"", "fill \"a\" b", "fill \"a\" $secret:", "fill \"a\" $secret:ok-name_1.x", "fill \"a\" $secret:bad name", "fill \"a\" $SECRET:x",
		"wait", "wait seconds", "wait seconds -1", "wait seconds 1.5", "wait seconds 99999999999999999999", "wait forever", "wait text", "wait text x",
		"expect", "expect url", "expect url ~", "expect url ~ (", "expect url ~ [", "expect url ~ (?<n>a)", "expect url ~ \\", "expect url > x", "expect text", "expect visible",
		"view budget=", "view budget=-1", "view budget=1e3", "view budget=0x10", "view budget=1 budget=2", "view bogus", "view table", "view find", "view find x y",
		"scroll sideways", "scroll b1 sideways", "scroll to", "scroll 1 2", "scroll \"x\"", "press", "press Hyper+a", "press Shift+", "press +Shift",
		"FILL \"a\" \"b\"", "Goto https://a", "\tclick b1", "click b1\t", "click\tb1", "click\u00a0b1", "click b1\r\nclick b2", "click b1\rclick b2", "\ufeffclick b1",
		"click \"\u202e\"", "click \"\x00\"", "click \"\xff\"", "click \"😀\"", "click \"日本語\"", "click \"a\\\"", "click \"a\\", "click \"\\u0041\"", "click \"\\x41\"",
		strings.Repeat("click b1\n", 200), "click \"" + strings.Repeat("a", 5000) + "\"", strings.Repeat("try ", 50) + "back", "#" + strings.Repeat("#", 1000),
		"\n\n\n", "   ", "#", "# only comment", "back\nback\nback", "view read read", "view interactive interactive",
	} {
		f.Add(src)
	}

	f.Fuzz(func(t *testing.T, src string) {
		p, err := Parse(src)
		if err != nil {
			return
		}
		rendered := p.String()
		again, err := Parse(rendered)
		if err != nil {
			t.Fatalf("rendering of a parsed program does not parse: %v\nsource:   %q\nrendered: %q", err, src, rendered)
		}
		if !reflect.DeepEqual(stripLines(p), stripLines(again)) {
			t.Fatalf("round trip changed the program\nsource:   %q\nrendered: %q\nfirst:  %+v\nsecond: %+v", src, rendered, p.Steps, again.Steps)
		}
	})
}

func stripLines(p Program) []Step {
	steps := make([]Step, len(p.Steps))
	copy(steps, p.Steps)
	for i := range steps {
		steps[i].Line = 0
	}
	return steps
}
