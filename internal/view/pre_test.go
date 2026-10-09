package view_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/noetive/riffle/internal/facts"
	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/snapshot"
	"github.com/noetive/riffle/internal/view"
)

// ws sets a computed white-space, which the text inside inherits.
func ws(v string) pb.Opt { return pb.Style(snapshot.WhiteSpace, v) }

// threeLines are the lines every page below shows a person.
const threeLines = `text "line one\nline two\n  indented three"`

// codePages are the ways a page writes the same three lines of code.
func codePages() map[string]func() *pb.Builder {
	return map[string]func() *pb.Builder{
		"pre": func() *pb.Builder {
			b := pb.New(1280, 800)
			b.Text(b.El(b.Body(), "pre", R(0, 0, 600, 60), ws("pre")), "line one\nline two\n  indented three\n")
			return b
		},
		"highlighted spans": func() *pb.Builder {
			b := pb.New(1280, 800)
			pre := b.El(b.Body(), "pre", R(0, 0, 600, 60), ws("pre"))
			code := b.El(pre, "code", R(0, 0, 600, 60), pb.Inline())
			kw := func(s string, y float64) {
				b.Text(b.El(code, "span", R(0, y, 40, 20), pb.Inline(),
					pb.Style(snapshot.FontWeight, "700"), pb.Style(snapshot.Color, "rgb(220, 0, 0)")), s)
			}
			b.Text(code, "\n")
			kw("line", 0)
			b.Text(code, " one\n")
			kw("line", 20)
			b.Text(code, " two\n  ")
			kw("indented", 40)
			b.Text(code, " three")
			return b
		},
		"a block per line": func() *pb.Builder {
			b := pb.New(1280, 800)
			pre := b.El(b.Body(), "pre", R(0, 0, 600, 60), ws("pre"))
			for i, l := range []string{"line one", "line two", "  indented three"} {
				b.Text(b.El(pre, "div", R(0, float64(20*i), 600, 20)), l)
			}
			return b
		},
		"br": func() *pb.Builder {
			b := pb.New(1280, 800)
			pre := b.El(b.Body(), "pre", R(0, 0, 600, 60), ws("pre"))
			b.Text(pre, "line one")
			b.El(pre, "br", R(60, 0, 0, 20), pb.Inline())
			b.Text(pre, "line two")
			b.El(pre, "br", R(60, 20, 0, 20), pb.Inline())
			b.Text(pre, "  indented three")
			return b
		},
		"white-space: pre on a div": func() *pb.Builder {
			b := pb.New(1280, 800)
			b.Text(b.El(b.Body(), "div", R(0, 0, 600, 60), ws("pre")), "line one\nline two\n  indented three")
			return b
		},
		"pre-wrap with a br": func() *pb.Builder {
			b := pb.New(1280, 800)
			d := b.El(b.Body(), "div", R(0, 0, 600, 60), ws("pre-wrap"))
			b.Text(d, "line one")
			b.El(d, "br", R(60, 0, 0, 20), pb.Inline())
			b.Text(d, "line two\n  indented three")
			return b
		},
	}
}

func TestOutlineKeepsTheLinesAndIndentationOfPreformattedText(t *testing.T) {
	for name, page := range codePages() {
		if out := outlineOf(page()).String(); !strings.Contains(out, threeLines) {
			t.Errorf("%s: want %s in\n%s", name, threeLines, out)
		}
	}
}

func TestReadFencesPreformattedTextOfMoreThanOneLine(t *testing.T) {
	for name, page := range codePages() {
		t.Run(name, func(t *testing.T) {
			sameLines(t, readOf(page()), "```", "line one", "line two", "  indented three", "```")
		})
	}
}

func TestPreLineKeepsBreaksAndCollapsesSpaces(t *testing.T) {
	b := pb.New(1280, 800)
	b.Text(b.El(b.Body(), "div", R(0, 0, 600, 60), ws("pre-line")), "  a   b  \n   c")
	if out := outlineOf(b).String(); !strings.Contains(out, `text "a b\nc"`) {
		t.Errorf("pre-line:\n%s", out)
	}
}

func TestPreWithWhiteSpaceNormalReadsAsProse(t *testing.T) {
	b := pb.New(1280, 800)
	b.Text(b.El(b.Body(), "pre", R(0, 0, 600, 60), ws("normal")), "a\n  b")
	sameLines(t, readOf(b), "a b")
}

func TestCodeHighlightingAddsNoEmphasisOrTone(t *testing.T) {
	b := codePages()["highlighted spans"]()
	for _, v := range []*view.View{outlineOf(b), readOf(b)} {
		if out := v.String(); strings.Contains(out, "**") || strings.Contains(out, " red") {
			t.Errorf("highlighting read as meaning:\n%s", out)
		}
	}
}

func TestNothingInCodeCanCloseItsFence(t *testing.T) {
	for code, want := range map[string][]string{
		"```\n````go\nx":    {"~~~", "```", "````go", "x", "~~~"},
		"```\n~~~~\n``````": {"```````", "```", "~~~~", "``````", "```````"},
		"a `b` ``c``":       {"```", "a `b` ``c``", "```"},
	} {
		b := pb.New(1280, 800)
		b.Text(b.El(b.Body(), "pre", R(0, 0, 600, 60), ws("pre")), code)
		sameLines(t, readOf(b), want...)
	}
}

func TestCodeInAListItemGoesUnderItsBullet(t *testing.T) {
	b := pb.New(1280, 800)
	ul := b.El(b.Body(), "ul", R(0, 0, 600, 200))
	for i, step := range []string{"Install", "Test"} {
		y := float64(100 * i)
		li := b.El(ul, "li", R(0, y, 600, 90))
		b.Text(b.El(li, "p", R(0, y, 600, 20)), step+":")
		b.Text(b.El(li, "pre", R(0, y+20, 600, 60), ws("pre")), "cd app\n  make "+strings.ToLower(step))
	}
	sameLines(t, readOf(b),
		"- Install:",
		"  ```", "  cd app", "    make install", "  ```",
		"- Test:",
		"  ```", "  cd app", "    make test", "  ```",
	)
	if out := outlineOf(b).String(); strings.Contains(out, " | ") || !strings.Contains(out, `text "cd app\n  make test"`) {
		t.Errorf("a code block was squeezed into a row:\n%s", out)
	}
}

func TestACodeBlockThatIsAWholeListEntryKeepsItsFence(t *testing.T) {
	b := pb.New(1280, 800)
	ul := b.El(b.Body(), "ul", R(0, 0, 600, 200))
	for i := range 3 {
		li := b.El(ul, "li", R(0, float64(50*i), 600, 40))
		b.Text(b.El(li, "pre", R(0, float64(50*i), 600, 40), ws("pre")), fmt.Sprintf("step %d\n  go", i))
	}
	got := readOf(b).String()
	fences(t, got)
	if !strings.Contains(got, "- ```\n  step 0\n    go\n  ```") {
		t.Errorf("read:\n%s", got)
	}
}

// codeLines marks the lines of out that sit inside a fenced code block, and
// reports a block left open (CommonMark 0.31, 4.5: a fence of backticks or
// tildes, which may open a list entry, closes on a line of only the same
// character, at least as many as it opened with).
func codeLines(out string) (code []bool, open bool) {
	var fence string
	for _, l := range strings.Split(out, "\n") {
		s := strings.TrimSpace(l)
		if fence == "" {
			s = strings.TrimPrefix(s, "- ")
		}
		run := func(c string) string { return s[:len(s)-len(strings.TrimLeft(s, c))] }
		switch {
		case fence == "" && (len(run("`")) >= 3 || len(run("~")) >= 3):
			fence = run("`") + run("~")
			code = append(code, false)
		case fence != "" && strings.HasPrefix(s, fence) && strings.Trim(s, fence[:1]) == "":
			fence = ""
			code = append(code, false)
		default:
			code = append(code, fence != "")
		}
	}
	return code, fence != ""
}

// fences checks that every code block in read output closes, and that no line
// Riffle writes, such as an elision count, falls inside one.
func fences(t *testing.T, out string) {
	t.Helper()
	code, open := codeLines(out)
	if open {
		t.Errorf("a code block never closes:\n%s", out)
	}
	for i, l := range strings.Split(out, "\n") {
		if code[i] && strings.HasPrefix(strings.TrimSpace(l), "…") {
			t.Errorf("Riffle's own line inside a code block: %q\n%s", l, out)
		}
	}
}

func TestBudgetNeverLeavesACodeBlockOpen(t *testing.T) {
	b := pb.New(1280, 800)
	main := b.El(b.Body(), "main", R(0, 0, 1280, 3000))
	b.Text(b.El(main, "h1", R(0, 0, 600, 30), pb.FontPx(32)), "Guide")
	var code []string
	for i := range 60 {
		code = append(code, fmt.Sprintf("  key_%02d: value number %d", i, i))
	}
	b.Text(b.El(main, "pre", R(0, 40, 600, 1200), ws("pre")), strings.Join(code, "\n"))
	b.Text(b.El(main, "p", R(0, 1300, 600, 20)), "After the block.")
	p := facts.Analyze(b.Snapshot())
	for budget := 5; budget <= 700; budget += 7 {
		v := view.Compile(p, view.Options{Projection: view.Read, Budget: budget})
		fences(t, v.String())
	}
}

func TestDiffShowsAChangedLineOfCode(t *testing.T) {
	page := func(port string) *view.View {
		b := pb.New(1280, 800)
		b.Text(b.El(b.Body(), "pre", R(0, 0, 600, 60), ws("pre"), pb.ID(77)), "server:\n  port: "+port)
		return outlineOf(b)
	}
	got := view.Diff(page("80"), page("8080")).String()
	if !strings.Contains(got, `port: 8080`) {
		t.Errorf("diff: %q", got)
	}
}

// A highlighter puts each line in a span made a block, and numbers the lines
// with generated text, which is not code.
func TestCodeLinesInBlockSpansWithGeneratedNumbers(t *testing.T) {
	b := pb.New(1280, 800)
	pre := b.El(b.Body(), "pre", R(0, 0, 600, 60), ws("pre"))
	for i, l := range []string{"line one", "line two", "  indented three"} {
		span := b.El(pre, "span", R(0, float64(20*i), 600, 20))
		b.Text(b.El(span, "span", R(0, float64(20*i), 10, 20), pb.Pseudo("before"), pb.Inline()), fmt.Sprint(i+1))
		b.Text(span, l)
	}
	if out := outlineOf(b).String(); !strings.Contains(out, threeLines) {
		t.Errorf("want %s in\n%s", threeLines, out)
	}
}

// A line break next to a link is still a line break.
func TestABreakBesideALinkStays(t *testing.T) {
	b := pb.New(1280, 800)
	d := b.El(b.Body(), "div", R(0, 0, 600, 60), ws("pre-wrap"))
	b.Text(d, "See\n")
	b.Text(b.El(d, "a", R(0, 20, 60, 20), pb.Inline(), pb.Attr("href", "/f"), ws("pre-wrap")), "file.go")
	b.Text(d, "\n  next line")
	if out := outlineOf(b).String(); !strings.Contains(out, `text "See\n" link a1 "file.go" "\n  next line"`) {
		t.Errorf("outline:\n%s", out)
	}
	// In a fence a link is its name: Markdown is not read there.
	sameLines(t, readOf(b), "```", "See", "file.go", "  next line", "```")
}

// Preformatted text on one line reads as it did before it kept lines: inline
// code joins its sentence and struck prices stay struck.
func TestOneLineOfPreformattedTextIsWords(t *testing.T) {
	b := pb.New(1280, 800)
	p := b.El(b.Body(), "p", R(0, 0, 600, 20))
	b.Text(p, "call ")
	b.Text(b.El(p, "code", R(30, 0, 10, 20), pb.Inline(), ws("pre-wrap")), "f")
	b.Text(p, "(x) on the ")
	b.Text(b.El(p, "code", R(90, 0, 10, 20), pb.Inline(), ws("break-spaces")), "x")
	b.Text(p, "-axis")
	d := b.El(b.Body(), "div", R(0, 30, 600, 20), ws("pre-wrap"))
	b.Text(d, "   Price: ")
	b.Text(b.El(d, "s", R(50, 30, 30, 20), pb.Inline(), ws("pre-wrap"), pb.Style(snapshot.TextDecorationLine, "line-through")), "$20")
	b.Text(d, " $15")
	sameLines(t, readOf(b), "call f(x) on the x-axis", "Price: ~~$20~~ $15")
}

func TestAListEntryOfLinesKeepsItsBullet(t *testing.T) {
	b := pb.New(1280, 800)
	ul := b.El(b.Body(), "ul", R(0, 0, 600, 200))
	for i, who := range []string{"Ada", "Bob"} {
		li := b.El(ul, "li", R(0, float64(60*i), 600, 50), ws("pre-line"))
		b.Text(li, who+"\n  1 Main St")
	}
	sameLines(t, readOf(b), "- ```", "  Ada", "  1 Main St", "  ```", "- ```", "  Bob", "  1 Main St", "  ```")
}

func TestATableCellOfCodeIsOneLine(t *testing.T) {
	b := pb.New(1280, 800)
	pre := func(s string) func(int32, float64) {
		return func(td int32, y float64) { b.Text(b.El(td, "pre", R(150, y, 150, 30), ws("pre")), s) }
	}
	w := func(s string) func(int32, float64) { return word(b, s) }
	tableOf(b, 0, []func(int32, float64){w("key"), w("value")}, []func(int32, float64){w("k0"), pre("a   b\n  c")})
	sameLines(t, readOf(b), "| key | value |", "| --- | --- |", "| k0 | a b c |")
}

func TestUnreadableCodeStillEndsItsLines(t *testing.T) {
	b := pb.New(1280, 800)
	pre := b.El(b.Body(), "pre", R(0, 0, 600, 60), ws("pre"))
	b.Text(pre, "one")
	b.Text(b.El(pre, "span", R(30, 0, 30, 20), pb.Inline(), pb.FontPx(0)), " hidden\n")
	b.Text(pre, "two")
	if out := outlineOf(b).String(); !strings.Contains(out, `text "one\ntwo"`) {
		t.Errorf("outline:\n%s", out)
	}
}

// A code block in a dialog that alone overruns the budget is closed where the
// budget ends it, so nothing after it reads as code.
func TestACodeBlockCutInADialogIsClosed(t *testing.T) {
	long := func(i int) string { return fmt.Sprintf("term line %d of the agreement text", i) }
	short := func(i int) string { return string(rune('a' + i%26)) } // narrower than a fence
	for _, line := range []func(int) string{long, short} {
		testCodeCutInADialog(t, line)
	}
}

func testCodeCutInADialog(t *testing.T, line func(int) string) {
	b := pb.New(1280, 800)
	wrap := b.El(b.Body(), "div", R(0, 0, 1280, 800), pb.Position("fixed"), pb.Fill("rgba(0, 0, 0, 0.6)"))
	dlg := b.El(wrap, "div", R(300, 100, 600, 600), pb.Attr("role", "dialog"), pb.Attr("aria-label", "Terms"), pb.Position("fixed"), pb.Fill("rgb(255, 255, 255)"))
	var terms []string
	for i := range 60 {
		terms = append(terms, line(i))
	}
	b.Text(b.El(dlg, "div", R(300, 100, 600, 600), ws("pre-wrap")), strings.Join(terms, "\n"))
	p := facts.Analyze(b.Snapshot())
	for budget := 1; budget <= 400; budget += 2 {
		v := view.Compile(p, view.Options{Projection: view.Read, Budget: budget})
		out := v.String()
		fences(t, out)
		within(t, v, budget)
		// The count after the block is the lines of it not shown.
		code, _ := codeLines(out)
		shown := 0
		for _, c := range code {
			if c {
				shown++
			}
		}
		if len(v.Lines) == 0 {
			continue // nothing fits a budget this small
		}
		last := v.Lines[len(v.Lines)-1].Text()
		var n int
		if _, err := fmt.Sscanf(last, "… %d more line", &n); err == nil && n != len(terms)-shown {
			t.Errorf("budget %d: %q after %d of %d lines", budget, last, shown, len(terms))
		}
	}
	// What the budget left out is counted after the block.
	v := view.Compile(p, view.Options{Projection: view.Read, Budget: 60})
	if last := v.Lines[len(v.Lines)-1].Text(); len(v.Lines) < len(terms)+2 && (!strings.HasPrefix(last, "… ") || !strings.HasSuffix(last, "more lines")) {
		t.Errorf("budget 60 drops lines without a count:\n%s", v)
	}
}

// A document of one long line, as JSON an API returns, is cut to the budget
// and says how to read the rest.
func TestALongLineOfCodeIsCutNotDropped(t *testing.T) {
	b := pb.New(1280, 800)
	body := `{"items":[` + strings.Repeat(`{"id":1,"name":"widget"},`, 400) + `]}`
	b.Text(b.El(b.Body(), "pre", R(0, 0, 1280, 800), ws("pre-wrap")), body)
	v := view.Compile(facts.Analyze(b.Snapshot()), view.Options{Projection: view.Read, Budget: 300})
	out := v.String()
	fences(t, out)
	lines := strings.Split(out, "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[1], `{"items":[{"id":1`) || !strings.HasSuffix(lines[1], "…") || !strings.HasPrefix(lines[3], "… 1 line cut r") {
		t.Fatalf("read:\n%s", out)
	}
	ref := strings.TrimPrefix(lines[3], "… 1 line cut ")
	whole := view.Compile(facts.Analyze(b.Snapshot()), view.Options{Projection: view.Expand, Target: ref, Previous: v.Refs})
	if !strings.Contains(whole.String(), `]}"`) {
		t.Errorf("expand %s does not hold the rest:\n%.300s", ref, whole)
	}
}

// A listing of files is a <pre> of links: each keeps its ref, so the agent
// can follow it, and read names it where it stands.
func TestLinksInCodeKeepTheirRefs(t *testing.T) {
	b := pb.New(1280, 800)
	pre := b.El(b.Body(), "pre", R(0, 0, 600, 60), ws("pre"))
	for i, f := range []string{"../", "file-1.tar.gz"} {
		b.Text(b.El(pre, "a", R(0, float64(20*i), 100, 20), pb.Inline(), pb.Attr("href", f), ws("pre")), f)
		b.Text(pre, fmt.Sprintf("   %dK\n", i))
	}
	if out := outlineOf(b).String(); !strings.Contains(out, `text link a1 "../" "   0K\n" link a2 "file-1.tar.gz" "   1K"`) {
		t.Errorf("outline:\n%s", out)
	}
	sameLines(t, readOf(b), "```", "../   0K", "file-1.tar.gz   1K", "```")
}

// A frame or a dialog inside a <pre> is not words of code: the outline still
// says what it is.
func TestFramesAndDialogsInAPreAreNotCode(t *testing.T) {
	b := pb.New(1280, 800)
	pre := b.El(b.Body(), "pre", R(0, 0, 1280, 800), ws("pre"))
	b.Text(pre, "intro\n")
	b.El(pre, "iframe", R(0, 20, 300, 100), pb.Attr("title", "Payment"))
	wrap := b.El(pre, "div", R(0, 0, 1280, 800), pb.Position("fixed"), pb.Fill("rgba(0, 0, 0, 0.6)"))
	dlg := b.El(wrap, "div", R(300, 300, 400, 200), pb.Attr("role", "dialog"), pb.Attr("aria-label", "Cookies"), pb.Position("fixed"), pb.Fill("rgb(255, 255, 255)"))
	b.Text(dlg, "We use cookies")
	out := outlineOf(b).String()
	for _, want := range []string{`frame "Payment" content-not-shown`, `modal d1 "Cookies"`} {
		if !strings.Contains(out, want) {
			t.Errorf("want %s in\n%s", want, out)
		}
	}
}

func TestPreLineKeepsTheSpacesBesideALink(t *testing.T) {
	b := pb.New(1280, 800)
	d := b.El(b.Body(), "div", R(0, 0, 600, 40), ws("pre-line"))
	b.Text(d, "see ")
	b.Text(b.El(d, "a", R(30, 0, 40, 20), pb.Inline(), pb.Attr("href", "/d"), ws("pre-line")), "docs")
	b.Text(d, "  now\n  next")
	sameLines(t, readOf(b), "```", "see docs now", "next", "```")
}

// within: the view holds no more characters than its budget allows, which a
// count of tokens, rounded, would not show.
func within(t *testing.T, v *view.View, budget int) {
	t.Helper()
	if n := utf8.RuneCountInString(v.String()) + 1; n > budget*4 {
		t.Errorf("budget %d: %d characters, over by %d:\n%s", budget, n, n-budget*4, v)
	}
}

// A page with a dialog, a long list, and code whose first line is a document
// on one line: whatever the budget, every block closes, the note that a line
// was cut follows its block, and the budget holds.
func TestBudgetsOverCodeAmongOtherContent(t *testing.T) {
	b := pb.New(1280, 800)
	wrap := b.El(b.Body(), "div", R(0, 0, 1280, 800), pb.Position("fixed"), pb.Fill("rgba(0, 0, 0, 0.6)"))
	dlg := b.El(wrap, "div", R(300, 300, 400, 100), pb.Attr("role", "dialog"), pb.Attr("aria-label", "Notice"), pb.Position("fixed"), pb.Fill("rgb(255, 255, 255)"))
	b.Text(b.El(dlg, "p", R(310, 310, 300, 20)), "Saved")
	main := b.El(b.Body(), "main", R(0, 0, 1280, 3000))
	b.Text(b.El(main, "h1", R(0, 0, 600, 30), pb.FontPx(32)), "Results")
	doc := `{"items":[` + strings.Repeat(`{"id":1,"name":"widget"},`, 80) + `]}`
	b.Text(b.El(main, "pre", R(0, 40, 1280, 200), ws("pre-wrap")), doc+"\nsecond line\nthird line")
	ul := b.El(main, "ul", R(0, 250, 600, 750))
	for i := range 25 {
		b.Text(b.El(ul, "li", R(0, float64(250+30*i), 600, 30)), fmt.Sprintf("result number %d of the search", i))
	}
	b.Text(b.El(main, "p", R(0, 1100, 600, 20)), "After the code.")
	p := facts.Analyze(b.Snapshot())
	cut := 0
	for budget := 5; budget <= 1200; budget += 3 {
		v := view.Compile(p, view.Options{Projection: view.Read, Budget: budget})
		out := v.String()
		fences(t, out)
		within(t, v, budget)
		// A line of code cut short ends in "…", and the line after its block
		// says how to read the rest.
		lines := strings.Split(out, "\n")
		code, _ := codeLines(out)
		short := false
		for i, l := range lines {
			switch {
			case code[i] && strings.HasSuffix(l, "…"):
				short = true
			case short && !code[i]:
				cut++
				short = false
				if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "… ") {
					t.Errorf("budget %d: a cut line of code is not noted after its block:\n%s", budget, out)
				}
			case !code[i] && strings.HasPrefix(l, "… 1 line cut ") && (i == 0 || strings.Trim(lines[i-1], "`~") != ""):
				t.Errorf("budget %d: the cut is noted away from its block:\n%s", budget, out)
			}
		}
	}
	if cut == 0 {
		t.Error("no budget cut the long line of code")
	}
}

func TestEveryFenceFitsItsCode(t *testing.T) {
	for code, want := range map[string][]string{
		"```js\nx\n```": {"~~~", "```js", "x", "```", "~~~"},
		"``x``":         {"```", "``x``", "```"},
		"```\n~~~":      {"````", "```", "~~~", "````"},
	} {
		b := pb.New(1280, 800)
		b.Text(b.El(b.Body(), "pre", R(0, 0, 600, 60), ws("pre")), code)
		sameLines(t, readOf(b), want...)
	}
}

func TestAnEmptyPreSaysNothing(t *testing.T) {
	b := pb.New(1280, 800)
	b.Text(b.El(b.Body(), "pre", R(0, 0, 600, 20), ws("pre")), "  \n \n")
	b.Text(b.El(b.Body(), "p", R(0, 30, 600, 20)), "after")
	if out := outlineOf(b).String(); strings.Contains(out, "text \"\"") || !strings.Contains(out, `text "after"`) {
		t.Errorf("outline:\n%s", out)
	}
	sameLines(t, readOf(b), "after")
}

// A link alone on its line is a line of the code, not a blank one.
func TestALinkAloneOnItsLine(t *testing.T) {
	b := pb.New(1280, 800)
	pre := b.El(b.Body(), "pre", R(0, 0, 600, 100), ws("pre"))
	b.Text(pre, "\n")
	b.Text(b.El(pre, "a", R(0, 0, 20, 20), pb.Inline(), pb.Attr("href", "/a"), ws("pre")), "a")
	b.Text(pre, "\n\n")
	b.Text(b.El(pre, "a", R(0, 40, 20, 20), pb.Inline(), pb.Attr("href", "/b"), ws("pre")), "b")
	b.Text(pre, "\n\nend")
	if out := outlineOf(b).String(); !strings.Contains(out, `text link a1 "a" "\n\n" link a2 "b" "\n\nend"`) {
		t.Errorf("outline:\n%s", out)
	}
}

func TestBlocksInCodeBreakLinesAndContentsDoesNot(t *testing.T) {
	b := pb.New(1280, 800)
	pre := b.El(b.Body(), "pre", R(0, 0, 600, 60), ws("pre"))
	b.Text(pre, "a")
	b.Text(b.El(pre, "div", R(0, 20, 600, 20), ws("pre")), "b")
	b.Text(pre, "c ")
	b.Text(b.El(pre, "span", R(10, 40, 10, 20), ws("pre"), pb.Style(snapshot.Display, "contents")), "d")
	b.Text(pre, "e")
	if out := outlineOf(b).String(); !strings.Contains(out, `text "a\nb\nc de"`) {
		t.Errorf("outline:\n%s", out)
	}
}

// The same rules hold where white-space, not a <pre>, keeps the lines.
func TestPreformattedRunsOutsideAPre(t *testing.T) {
	t.Run("unreadable text still ends its lines", func(t *testing.T) {
		b := pb.New(1280, 800)
		d := b.El(b.Body(), "div", R(0, 0, 600, 60), ws("pre"))
		b.Text(d, "one")
		b.Text(b.El(d, "span", R(30, 0, 30, 20), pb.Inline(), ws("pre"), pb.FontPx(0)), " hidden\n")
		b.Text(d, "two")
		if out := outlineOf(b).String(); !strings.Contains(out, `text "one\ntwo"`) {
			t.Errorf("outline:\n%s", out)
		}
	})
	t.Run("prose around a broken line is collapsed", func(t *testing.T) {
		b := pb.New(1280, 800)
		p := b.El(b.Body(), "p", R(0, 0, 600, 40))
		b.Text(p, "see\nthis ")
		b.Text(b.El(p, "code", R(60, 0, 30, 40), pb.Inline(), ws("pre-wrap")), "a\nb")
		sameLines(t, readOf(b), "```", "see this a", "b", "```")
	})
	t.Run("a br is the only break", func(t *testing.T) {
		b := pb.New(1280, 800)
		d := b.El(b.Body(), "div", R(0, 0, 600, 40), ws("pre-wrap"))
		b.Text(d, "a")
		b.El(d, "br", R(10, 0, 0, 20), pb.Inline(), ws("pre-wrap"))
		b.Text(d, "b")
		sameLines(t, readOf(b), "```", "a", "b", "```")
	})
	t.Run("boxes apart add no space", func(t *testing.T) {
		b := pb.New(1280, 800)
		d := b.El(b.Body(), "div", R(0, 0, 600, 40), ws("pre-wrap"))
		b.Text(d, "x\n")
		b.Text(b.El(d, "span", R(0, 20, 10, 20), pb.Inline(), ws("pre-wrap")), "a")
		b.Text(b.El(d, "span", R(30, 20, 10, 20), pb.Inline(), ws("pre-wrap")), "b")
		sameLines(t, readOf(b), "```", "x", "ab", "```")
	})
	t.Run("highlighting is one stretch, strikethrough is not", func(t *testing.T) {
		b := pb.New(1280, 800)
		d := b.El(b.Body(), "div", R(0, 0, 600, 60), ws("pre"))
		hl := func(s string, y float64) {
			b.Text(b.El(d, "span", R(0, y, 40, 20), pb.Inline(), ws("pre"),
				pb.Style(snapshot.FontWeight, "700"), pb.Style(snapshot.Color, "rgb(220, 0, 0)")), s)
		}
		hl("def", 0)
		b.Text(d, " foo():\n    ")
		hl("return", 20)
		b.Text(d, " 1\n")
		b.Text(b.El(d, "s", R(0, 40, 40, 20), pb.Inline(), ws("pre"), pb.Style(snapshot.TextDecorationLine, "line-through")), "old")
		if out := outlineOf(b).String(); !strings.Contains(out, `text "def foo():\n    return 1\n" "old" strike`) {
			t.Errorf("outline:\n%s", out)
		}
	})
	t.Run("whitespace after a link leaves no empty stretch", func(t *testing.T) {
		b := pb.New(1280, 800)
		d := b.El(b.Body(), "div", R(0, 0, 600, 40), ws("pre-wrap"))
		b.Text(d, "x\n")
		b.Text(b.El(d, "a", R(0, 20, 30, 20), pb.Inline(), pb.Attr("href", "/d"), ws("pre-wrap")), "docs")
		b.Text(d, "   ")
		if out := outlineOf(b).String(); !strings.HasSuffix(out, "\n"+`text "x\n" link a1 "docs"`) {
			t.Errorf("outline:\n%s", out)
		}
	})
}

// Two line breaks in a row end a paragraph.
func TestTwoBreaksEndAParagraph(t *testing.T) {
	b := pb.New(1280, 800)
	p := b.El(b.Body(), "p", R(0, 0, 600, 60))
	b.Text(p, "a")
	b.El(p, "br", R(10, 0, 0, 20), pb.Inline())
	b.El(p, "br", R(0, 20, 0, 20), pb.Inline())
	b.Text(p, "b")
	sameLines(t, readOf(b), "a", "b")
}

// A <pre> with a button in it is not read as code: the button keeps its ref.
func TestAButtonInAPreStaysAControl(t *testing.T) {
	b := pb.New(1280, 800)
	pre := b.El(b.Body(), "pre", R(0, 0, 600, 60), ws("pre"))
	b.Text(pre, "npm install\n")
	b.Text(b.El(pre, "button", R(500, 0, 60, 20), pb.Inline()), "Copy")
	if out := outlineOf(b).String(); !strings.Contains(out, `button b1 "Copy"`) {
		t.Errorf("outline:\n%s", out)
	}
}
