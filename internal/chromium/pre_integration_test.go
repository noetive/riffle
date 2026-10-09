//go:build integration

package chromium_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/chromium"
	"github.com/noetive/riffle/internal/facts"
	"github.com/noetive/riffle/internal/view"
)

// Code a person reads as separate, indented lines reaches the agent as those
// lines, however the page writes it: the browser's own text nodes and
// computed white-space, not a model of them.
func TestPreformattedTextKeepsItsLinesInABrowser(t *testing.T) {
	const three = `"line one\nline two\n  indented three"`
	for name, tc := range map[string]struct {
		html    string
		outline string
		read    []string
	}{
		"pre": {
			html:    "<pre>\nline one\nline two\n  indented three\n</pre>",
			outline: "text " + three,
			read:    []string{"```", "line one", "line two", "  indented three", "```"},
		},
		"highlighted": {
			html:    "<pre><code><span style=\"color:#c00;font-weight:bold\">line</span> one\n<span style=\"color:#c00\">line</span> two\n  <b>indented</b> three</code></pre>",
			outline: "text " + three,
			read:    []string{"```", "line one", "line two", "  indented three", "```"},
		},
		"a block per line": {
			html:    "<pre><div>line one</div><div>line two</div><div>  indented three</div></pre>",
			outline: "text " + three,
			read:    []string{"```", "line one", "line two", "  indented three", "```"},
		},
		"br": {
			html:    "<pre>line one<br>line two<br>  indented three</pre>",
			outline: "text " + three,
			read:    []string{"```", "line one", "line two", "  indented three", "```"},
		},
		"line numbers": {
			html:    "<style>pre{counter-reset:n}pre span{display:block;counter-increment:n}pre span::before{content:counter(n) \" \"}</style><pre><span>line one</span><span>line two</span><span>  indented three</span></pre>",
			outline: "text " + three,
			read:    []string{"```", "line one", "line two", "  indented three", "```"},
		},
		"a listing of links": {
			html:    "<pre><a href=\"../\">../</a>   -\n<a href=\"a.tar.gz\">a.tar.gz</a>   2K\n</pre>",
			outline: `text link a1 "../" "   -\n" link a2 "a.tar.gz" "   2K"`,
			read:    []string{"```", "../   -", "a.tar.gz   2K", "```"},
		},
		"white-space: pre": {
			html:    "<div style=\"white-space:pre\">line one\nline two\n  indented three</div>",
			outline: "text " + three,
			read:    []string{"```", "line one", "line two", "  indented three", "```"},
		},
		"pre-line": {
			html:    "<div style=\"white-space:pre-line\">  a   b  \n   c</div>",
			outline: `text "a b\nc"`,
			read:    []string{"```", "a b", "c", "```"},
		},
		"white-space: normal": {
			html:    "<pre style=\"white-space:normal\">a\n  b</pre>",
			outline: `text "a b"`,
			read:    []string{"a b"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			outline, read := compiled(t, "<!doctype html><body>"+tc.html)
			if !strings.Contains(outline, tc.outline) {
				t.Errorf("outline wants %s:\n%s", tc.outline, outline)
			}
			if want := strings.Join(tc.read, "\n"); read != want {
				t.Errorf("read:\n%s\nwant:\n%s", read, want)
			}
		})
	}
}

// compiled loads html in a fresh browser and returns its outline and read
// views.
func compiled(t *testing.T, html string) (outline, read string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(html))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p, err := chromium.Open(ctx, 1280, 800)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Load(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	snap, err := p.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	page := facts.Analyze(snap)
	return view.Compile(page, view.Options{}).String(), view.Compile(page, view.Options{Projection: view.Read}).String()
}
