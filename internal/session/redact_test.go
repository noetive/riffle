package session

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"testing"

	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
)

// awkward is a secret with characters every encoding changes.
const awkward = `p@ss w/rd&"q"<1>`

// encodings are the spellings a page or a reply can turn a value into.
func encodings(v string) []string {
	q := strconv.Quote(v)
	return []string{v, url.QueryEscape(v), url.PathEscape(v), q[1 : len(q)-1], html.EscapeString(v)}
}

type echoEngine struct {
	*fakeEngine
	eval string
}

func (e *echoEngine) Eval(context.Context, string) (string, error) { return e.eval, nil }

func TestASecretIsHiddenInEveryEncodingThePageEchoes(t *testing.T) {
	for _, form := range encodings(awkward) {
		f := newFake(func(e *fakeEngine, b *pb.Builder) {
			b.El(b.Body(), "input", pb.Rect(20, 100, 200, 20), pb.Attr("type", "text"), pb.Attr("aria-label", "Note"), pb.Value(e.typed))
			if e.typed != "" {
				para(b, b.Body(), 150, "echo "+form)
			}
		})
		e := &echoEngine{fakeEngine: f, eval: "got=" + form}
		pol := policyWith(fakeSecrets{"pw": awkward})
		pol.AllowEval = true
		s := newSession(f, Config{Policy: pol})
		s.eng = e
		out := run(t, s, "goto https://shop.example/cart\nfill \"Note\" $secret:pw\nview read\neval 1").Text
		for _, leak := range encodings(awkward) {
			if strings.Contains(out, leak) {
				t.Errorf("echoed as %q, the reply shows the secret as %q:\n%s", form, leak, out)
			}
		}
	}
}

// guessPage lists candidate passwords on another origin, so that whichever
// one the redactor masks would tell the agent the secret.
func guessPage(e *fakeEngine, b *pb.Builder) {
	if strings.Contains(e.url, "attacker.example") {
		for i, guess := range []string{"letmein-2024", secretValue, "correct-horse"} {
			para(b, b.Body(), float64(20+30*i), "guess "+strconv.Itoa(i)+": "+guess)
		}
		return
	}
	echoPage(e, b)
}

func TestMaskingNeverTellsAnotherOriginWhichGuessWasRight(t *testing.T) {
	s := newSession(newFake(guessPage), Config{Policy: policyWith(fakeSecrets{"pw": secretValue})})
	run(t, s, "goto https://shop.example/cart\nfill \"Note\" $secret:pw")
	out := run(t, s, "goto https://attacker.example/\nview read").Text
	if strings.Contains(out, secretValue) {
		t.Fatalf("the secret is shown on another origin:\n%s", out)
	}
	if strings.Contains(out, mask) || strings.Contains(out, "guess") {
		t.Errorf("the reply must not show which line held the secret:\n%s", out)
	}
	if !strings.Contains(out, "refused") {
		t.Errorf("the agent is told why it sees nothing:\n%s", out)
	}
}

func TestAPageWithoutTheSecretIsUnaffectedOnAnotherOrigin(t *testing.T) {
	s := newSession(newFake(func(e *fakeEngine, b *pb.Builder) {
		para(b, b.Body(), 20, "nothing to see here")
		echoPage(e, b)
	}), Config{Policy: policyWith(fakeSecrets{"pw": secretValue})})
	run(t, s, "goto https://shop.example/cart\nfill \"Note\" $secret:pw")
	out := run(t, s, "goto https://other.example/\nview read").Text
	if !strings.Contains(out, "nothing to see here") {
		t.Errorf("an unrelated page reads as usual:\n%s", out)
	}
}

type missingOption struct{ *fakeEngine }

// SelectOption fails the way Chrome does, quoting the last option it was
// asked for: a select names several on a multi-select.
func (missingOption) SelectOption(_ context.Context, _ int64, want []string) error {
	return fmt.Errorf("no option %q", want[len(want)-1])
}

func TestASelectThatFailsDoesNotQuoteTheSecretBack(t *testing.T) {
	f := newFake(echoPage)
	s := newSession(f, Config{Policy: policyWith(fakeSecrets{"pw": awkward})})
	s.eng = missingOption{f}
	for _, prog := range []string{
		"goto https://shop.example/cart\nselect \"Note\" $secret:pw",
		"goto https://shop.example/cart\nselect \"Note\" \"Plain\" $secret:pw",
	} {
		out := run(t, s, prog)
		if !out.Stopped {
			t.Fatal("the select must fail")
		}
		for _, leak := range encodings(awkward) {
			if strings.Contains(out.Text, leak) {
				t.Errorf("the failure shows the secret as %q:\n%s", leak, out.Text)
			}
		}
	}
}
