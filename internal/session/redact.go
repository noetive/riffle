package session

import (
	"html"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	json "github.com/goccy/go-json"

	"github.com/noetive/riffle/internal/pagetext"
	"github.com/noetive/riffle/internal/program"
	"github.com/noetive/riffle/internal/snapshot"
)

// mask replaces a redacted value in every later view.
const mask = "••••"

// guarded is the length from which a secret is watched for on origins other
// than its own. Shorter values, such as a PIN, turn up on ordinary pages by
// chance, and refusing every page that shows four digits would refuse most.
const guarded = 8

// offOrigin replaces a reply that would show a secret away from its origin.
// It says neither where nor which, so a page listing guesses learns nothing
// from the agent's reply about which guess was right.
const offOrigin = "refused: this reply would show a secret outside the origin it is bound to, so none of it is shown; go back to that origin or to another page"

// secret is a value handed out for one origin, with every spelling in which
// a page or a reply can show it.
type secret struct {
	origin string
	value  string
	forms  []string
}

// redactor remembers every secret it hands out and removes it, wherever the
// page echoes it, from the snapshots the view compiler sees and from replies.
// A secret is masked only on its own origin. Masking it anywhere else would
// make the mask an oracle: a page lists guesses and the masked one is the
// secret. Elsewhere a reply that shows it is refused whole. Password fields
// are masked whether or not a secret filled them.
type redactor struct {
	inner program.SecretResolver
	seen  []secret
}

// Resolve passes the lookup through and remembers a successful value.
func (r *redactor) Resolve(name, origin string) (string, error) {
	v, err := r.inner.Resolve(name, origin)
	if err == nil && v != "" && !slices.ContainsFunc(r.seen, func(s secret) bool { return s.value == v && s.origin == origin }) {
		r.seen = append(r.seen, secret{origin: origin, value: v, forms: spellings(v)})
	}
	return v, err
}

// spellings lists v as typed and as each encoding a page or a view applies
// to it: cleaned page text, Go and JSON string escapes, HTML entities and
// URL escapes. Longest first, so a longer spelling is masked before a
// shorter one inside it.
func spellings(v string) []string {
	var out []string
	for _, base := range []string{v, pagetext.Clean(v)} {
		q := strconv.Quote(base)
		j, _ := json.Marshal(base)
		out = append(out, base, q[1:len(q)-1], string(j[1:len(j)-1]), html.EscapeString(base),
			url.QueryEscape(base), url.PathEscape(base), pagetext.Href(base))
	}
	out = slices.DeleteFunc(out, func(s string) bool { return s == "" })
	slices.SortFunc(out, func(a, b string) int { return len(b) - len(a) })
	return slices.Compact(out)
}

// on returns the spellings of every secret bound to origin, longest first.
func (r *redactor) on(origin string) []string {
	var forms []string
	for _, s := range r.seen {
		if s.origin == origin {
			forms = append(forms, s.forms...)
		}
	}
	slices.SortFunc(forms, func(a, b string) int { return len(b) - len(a) })
	return forms
}

// apply masks this origin's secrets and every password value in the
// snapshot, in place.
func (r *redactor) apply(s *snapshot.Snapshot) {
	for i := range s.Value {
		if t, _ := s.Attr(int32(i), "type"); s.Value[i] != "" && strings.EqualFold(t, "password") {
			s.Value[i] = mask
		}
	}
	for _, form := range r.on(program.OriginOf(s.URL)) {
		for i := range s.Value {
			s.Value[i] = strings.ReplaceAll(s.Value[i], form, mask)
			s.Text[i] = strings.ReplaceAll(s.Text[i], form, mask)
		}
		for i := range s.Attrs {
			for j, a := range s.Attrs[i] {
				s.Attrs[i][j].Value = strings.ReplaceAll(a.Value, form, mask)
			}
		}
	}
}

// scrub prepares text bound for the agent or the log, written while the
// session was on origin: this origin's secrets are masked in every spelling,
// and a reply that still shows another origin's secret is refused whole.
func (r *redactor) scrub(text, origin string) string {
	for _, form := range r.on(origin) {
		text = strings.ReplaceAll(text, form, mask)
	}
	for _, s := range r.seen {
		if s.origin == origin || utf8.RuneCountInString(s.value) < guarded {
			continue
		}
		for _, form := range s.forms {
			if strings.Contains(text, form) {
				return offOrigin
			}
		}
	}
	return text
}
