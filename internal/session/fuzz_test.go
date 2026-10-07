package session

import (
	"strings"
	"testing"

	"github.com/noetive/riffle/internal/snapshot"
)

type fixedSecret struct{ v string }

func (f fixedSecret) Resolve(string, string) (string, error) { return f.v, nil }

// FuzzRedactor hides a secret wherever a page echoes it. For any secret and
// any page content, nothing the view compiler or the agent can read may
// contain the secret afterwards, and a password field never shows its value.
func FuzzRedactor(f *testing.F) {
	for _, c := range [][2]string{
		{"hunter2!", "your password is hunter2!"},
		{"hunter2!", "hunter2!hunter2!hunter2!"},
		{"abcd1234", "abcabcd1234d1234"},
		{"s3cr3t-token", "https://x/?t=s3cr3t-token&u=1"},
		{"secret", "SECRET Secret secret"},
		{"pässwörd", "pässwörd and pässwörd"},
		{"日本語パス", "日本語パス日本語パス"},
		{"a b c d", "a  b  c  d and a b c d"},
		{"correct horse battery staple", "pw: correct horse battery staple!"},
		{"tok_live_51Hxyz", "Authorization: Bearer tok_live_51Hxyz"},
		{"p@ss w0rd", "p@ss w0rd | p@ss  w0rd | P@SS W0RD"},
		{"abcdefgh", "abcdefghabcdefghabcdefgh"},
		{"abababab", "abababababababab"},
		{"aaaa", "aaaaaaaaaaaa"},
		{"12345678", "x12345678y 1234567 12345678"},
		{"<script>1</script>", "a <script>1</script> b"},
		{"a\nb\nc\nd", "a\nb\nc\nd and a b c d"},
		{"quo\"te'd", `say "quo"te'd" now`},
		{"%41%42%43%44", "%41%42%43%44 ABCD"},
		{"🔑🔑🔑🔑", "key 🔑🔑🔑🔑🔑🔑🔑🔑"},
		{"tab\tsep", "tab\tsep\ttab\tsep"},
		{"café-ünï", "CAFÉ-ÜNÏ café-ünï"},
		{"very-long-secret-" + strings.Repeat("x", 200), "echo very-long-secret-" + strings.Repeat("x", 200)},
		{"end", "the end"}, {"secret", "sec"}, {"secret", "ret"}, {"secret", "secsecretret"},
		{"x", ""}, {"", "anything"},
	} {
		f.Add(c[0], c[1])
	}
	f.Fuzz(func(t *testing.T, secret, page string) {
		if len(secret) < 4 || strings.Contains(secret, mask) || strings.ContainsRune(secret, '•') {
			t.Skip("short or mask-like secrets are refused elsewhere")
		}
		r := &redactor{inner: fixedSecret{secret}}
		if _, err := r.Resolve("n", "https://o.example"); err != nil {
			t.Fatal(err)
		}
		if got := r.scrub(page, "https://o.example"); strings.Contains(got, secret) {
			t.Fatalf("scrub left the secret in %q", got)
		}
		for _, form := range spellings(secret) {
			echo := page + " " + form
			if got := r.scrub(echo, "https://o.example"); strings.Contains(got, form) {
				t.Fatalf("scrub left the spelling %q in %q", form, got)
			}
			if got := r.scrub(echo, "elsewhere"); got != echo && got != offOrigin {
				t.Fatalf("on another origin a reply is shown as it is or refused whole, never masked in place: %q", got)
			}
			if got := r.scrub(echo, "elsewhere"); len([]rune(secret)) >= guarded && got != offOrigin {
				t.Fatalf("a reply showing the secret on another origin must be refused: %q", got)
			}
		}
		s := &snapshot.Snapshot{
			URL:   "https://o.example",
			Value: []string{page, page},
			Text:  []string{page, page},
			Attrs: [][]snapshot.Attr{{{Name: "type", Value: "password"}, {Name: "aria-label", Value: page}}, {{Name: "title", Value: page}}},
		}
		r.apply(s)
		for i := range s.Value {
			if strings.Contains(s.Value[i], secret) || strings.Contains(s.Text[i], secret) {
				t.Fatalf("apply left the secret in node %d", i)
			}
			for _, a := range s.Attrs[i] {
				if strings.Contains(a.Value, secret) && a.Name != "type" {
					t.Fatalf("apply left the secret in attribute %s", a.Name)
				}
			}
		}
		if page != "" && s.Value[0] != mask {
			t.Fatalf("a password field shows %q", s.Value[0])
		}
	})
}
