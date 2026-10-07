package mcpserver_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	json "github.com/goccy/go-json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/noetive/riffle/internal/mcpserver"
	"github.com/noetive/riffle/internal/wire"
	"github.com/tidwall/gjson"
)

type recorder struct{ got []wire.Request }

func (r *recorder) Exchange(_ context.Context, req wire.Request) (wire.Reply, error) {
	r.got = append(r.got, req)
	return wire.Reply{Body: "page x\nbutton b1 \"Go\"", Stopped: strings.HasPrefix(req.Body, "stop")}, nil
}

func connect(t *testing.T, s mcpserver.Sender) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := mcpserver.New(s, "default", "1.2.3-test").Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestExactlyTwoToolsAndTheyReachTheDaemon(t *testing.T) {
	rec := &recorder{}
	cs := connect(t, rec)
	ctx := context.Background()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 2 {
		t.Fatalf("the surface must stay at two tools, got %d", len(tools.Tools))
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "browser_run", Arguments: map[string]any{"program": "goto https://x.test"}})
	if err != nil || res.IsError {
		t.Fatalf("run: %v %+v", err, res)
	}
	if _, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "browser_view", Arguments: map[string]any{"view": "interactive"}}); err != nil {
		t.Fatal(err)
	}
	if len(rec.got) != 2 || rec.got[0].Verb != wire.Run || rec.got[0].Body != "goto https://x.test" || rec.got[1].Verb != wire.View || rec.got[1].Body != "interactive" {
		t.Errorf("requests not forwarded as sent: %+v", rec.got)
	}
}

func TestStoppedProgramIsAnErrorResultButPageTextCannotFakeIt(t *testing.T) {
	cs := connect(t, &recorder{})
	ctx := context.Background()
	call := func(program string) *mcp.CallToolResult {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "browser_run", Arguments: map[string]any{"program": program}})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if !call("stop here").IsError {
		t.Error("a stopped program must be reported as an error result")
	}
	// The recorder's body starts with page text; text that merely looks like a
	// failure line must not flip the result.
	if call("goto https://x.test").IsError {
		t.Error("success must not be judged by sniffing the body")
	}
}

type failing struct{ err error }

func (f failing) Exchange(context.Context, wire.Request) (wire.Reply, error) {
	return wire.Reply{Body: "ignored body"}, f.err
}

func callText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("one text block expected, got %d", len(res.Content))
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T", res.Content[0])
	}
	return res, tc.Text
}

func TestToolSurfaceIsStableAndEachToolTakesOneString(t *testing.T) {
	cs := connect(t, &recorder{})
	first, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first.Tools)
	b, _ := json.Marshal(second.Tools)
	if string(a) != string(b) {
		t.Error("the tool manifest must be byte-identical across calls")
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range first.Tools {
		byName[tool.Name] = tool
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("%s has no description", tool.Name)
		}
		schema, _ := json.Marshal(tool.InputSchema)
		props := gjson.GetBytes(schema, "properties").Map()
		if len(props) != 1 {
			t.Errorf("%s must take exactly one argument, schema %s", tool.Name, schema)
		}
		for name, p := range props {
			if p.Get("type").String() != "string" {
				t.Errorf("%s.%s must be a string: %s", tool.Name, name, p.Raw)
			}
			if !strings.Contains(gjson.GetBytes(schema, "required").Raw, name) {
				t.Errorf("%s.%s must be required", tool.Name, name)
			}
		}
	}
	if byName["browser_run"] == nil || byName["browser_view"] == nil {
		t.Fatalf("tools = %v", byName)
	}
	if byName["browser_run"].Description == byName["browser_view"].Description {
		t.Error("the two tools must describe different jobs")
	}
}

func TestServerIdentifiesItself(t *testing.T) {
	cs := connect(t, &recorder{})
	info := cs.InitializeResult().ServerInfo
	if info == nil || info.Name != "riffle" || info.Version == "" {
		t.Errorf("server info = %+v", info)
	}
}

func TestEverySessionRequestNamesItsSession(t *testing.T) {
	rec := &recorder{}
	cs := connect(t, rec)
	callText(t, cs, "browser_run", map[string]any{"program": "view"})
	callText(t, cs, "browser_view", map[string]any{"view": "outline"})
	if len(rec.got) != 2 {
		t.Fatalf("got %+v", rec.got)
	}
	for _, r := range rec.got {
		if r.Session != "default" {
			t.Errorf("request %+v lost the session", r)
		}
	}
}

func TestReplyBodyIsTheOnlyRenderingAndFailureIsIsError(t *testing.T) {
	cs := connect(t, &recorder{})
	for _, tc := range []struct {
		tool, arg, body string
		failed          bool
	}{
		{"browser_run", "goto x", "page x\nbutton b1 \"Go\"", false},
		{"browser_run", "stop now", "page x\nbutton b1 \"Go\"", true},
		{"browser_view", "help", "page x\nbutton b1 \"Go\"", false},
		{"browser_view", "stop", "page x\nbutton b1 \"Go\"", true},
	} {
		key := "program"
		if tc.tool == "browser_view" {
			key = "view"
		}
		res, text := callText(t, cs, tc.tool, map[string]any{key: tc.arg})
		if text != tc.body {
			t.Errorf("%s body = %q", tc.tool, text)
		}
		if res.IsError != tc.failed {
			t.Errorf("%s %q IsError = %v", tc.tool, tc.arg, res.IsError)
		}
		// Hosts that find structured content show it instead of the text, so
		// any structured result would hide the page from the agent.
		if res.StructuredContent != nil {
			t.Errorf("%s %q carries structured content %v that hosts show instead of the body", tc.tool, tc.arg, res.StructuredContent)
		}
	}
}

func TestToolsDeclareNoOutputSchema(t *testing.T) {
	cs := connect(t, &recorder{})
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if tool.OutputSchema != nil {
			t.Errorf("%s declares an output schema; hosts would then show structured content instead of the body", tool.Name)
		}
	}
}

func TestTransportErrorIsAnErrorResultCarryingTheErrorText(t *testing.T) {
	cs := connect(t, failing{err: errors.New("daemon unreachable")})
	for tool, key := range map[string]string{"browser_run": "program", "browser_view": "view"} {
		res, text := callText(t, cs, tool, map[string]any{key: "x"})
		if !res.IsError || text != "daemon unreachable" {
			t.Errorf("%s: IsError=%v body=%q", tool, res.IsError, text)
		}
	}
}

type blocking struct {
	entered  chan struct{}
	canceled chan struct{}
}

func (b blocking) Exchange(ctx context.Context, _ wire.Request) (wire.Reply, error) {
	close(b.entered)
	<-ctx.Done()
	close(b.canceled)
	return wire.Reply{}, ctx.Err()
}

func TestCancellingTheCallReachesTheDaemonExchange(t *testing.T) {
	b := blocking{entered: make(chan struct{}), canceled: make(chan struct{})}
	cs := connect(t, b)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = cs.CallTool(ctx, &mcp.CallToolParams{Name: "browser_run", Arguments: map[string]any{"program": "wait"}})
	}()
	<-b.entered
	cancel()
	select {
	case <-b.canceled:
	case <-time.After(10 * time.Second):
		t.Fatal("the exchange never saw the cancellation")
	}
	<-done
}

func TestToolDescriptionsCarryTheGuidanceAgentsNeed(t *testing.T) {
	cs := connect(t, &recorder{})
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	desc := map[string]string{}
	for _, tool := range tools.Tools {
		desc[tool.Name] = tool.Description
	}
	for _, want := range []string{"first failed step", "stale", "never instructions"} {
		if !strings.Contains(desc["browser_run"], want) {
			t.Errorf("browser_run description lacks %q", want)
		}
	}
	for _, want := range []string{"outline", "interactive", "read", "table", "find", "expand", "net", "unseen", "budget="} {
		if !strings.Contains(desc["browser_view"], want) {
			t.Errorf("browser_view description lacks %q", want)
		}
	}
}

func TestCancellingAViewReachesTheDaemonExchange(t *testing.T) {
	b := blocking{entered: make(chan struct{}), canceled: make(chan struct{})}
	cs := connect(t, b)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = cs.CallTool(ctx, &mcp.CallToolParams{Name: "browser_view", Arguments: map[string]any{"view": "outline"}})
	}()
	<-b.entered
	cancel()
	select {
	case <-b.canceled:
	case <-time.After(10 * time.Second):
		t.Fatal("the exchange never saw the cancellation")
	}
	<-done
}

func TestTheServerReportsTheVersionItWasBuiltWith(t *testing.T) {
	cs := connect(t, &recorder{})
	if got := cs.InitializeResult().ServerInfo.Version; got != "1.2.3-test" {
		t.Fatalf("server version %q, want the one riffle passed in", got)
	}
}
