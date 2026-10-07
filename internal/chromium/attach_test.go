package chromium

import (
	"strings"
	"testing"

	"github.com/noetive/riffle/internal/engine"
)

// A target that refuses the page's user agent would run with the stock
// headless identity and no Riffle token, so it stays paused and the agent is
// told.
func TestATargetThatRefusesTheIdentityStaysPausedAndIsReported(t *testing.T) {
	p, f := newTestPage(t)
	p.override = map[string]any{"userAgent": "Mozilla/5.0 Chrome/1 Riffle/1"}
	f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
		if r.Method == "Network.setUserAgentOverride" && r.Session == "W1" {
			return "", "'Network.setUserAgentOverride' wasn't found", true
		}
		return "{}", "", true
	}
	p.release("W1", "worker", "http://x/w.js")
	if n := len(f.find("Runtime.runIfWaitingForDebugger")); n != 0 {
		t.Errorf("the target was resumed %d times without its identity", n)
	}
	var told bool
	for _, e := range p.Drain() {
		told = told || (e.Kind == engine.BlockedFetch && strings.Contains(e.Text, "http://x/w.js") && strings.Contains(e.Text, "identity"))
	}
	if !told {
		t.Error("the agent must be told the target was not started")
	}
}

func TestATargetThatTakesTheIdentityIsGivenItBeforeItRuns(t *testing.T) {
	p, f := newTestPage(t)
	p.override = map[string]any{"userAgent": "Mozilla/5.0 Chrome/1 Riffle/1"}
	p.release("W1", "worker", "http://x/w.js")
	ms := f.methods()
	set, run := -1, -1
	for i, m := range ms {
		if m == "Network.setUserAgentOverride" {
			set = i
		}
		if m == "Runtime.runIfWaitingForDebugger" {
			run = i
		}
	}
	if set < 0 || run < 0 || set > run {
		t.Errorf("identity must be set before the target is resumed: %v", ms)
	}
}

// A dedicated worker offers no Fetch domain of its own: its requests pause in
// the interception of whatever started it, so it runs. Anything else that
// cannot take the filter, and a worker that refuses it for another reason,
// stays paused.
func TestOnlyADedicatedWorkerRunsWithoutAFetchDomainOfItsOwn(t *testing.T) {
	for _, c := range []struct {
		kind, fetchErr string
		runs           bool
	}{
		{"worker", "'Fetch.enable' wasn't found", true},
		{"worker", "Fetch.enable is not allowed here", false},
		{"iframe", "'Fetch.enable' wasn't found", false},
		{"service_worker", "'Fetch.enable' wasn't found", false},
	} {
		t.Run(c.kind+": "+c.fetchErr, func(t *testing.T) {
			p, f := newTestPage(t)
			p.filter = func(string, string) error { return nil }
			f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
				if r.Method == "Fetch.enable" && r.Session == "W1" {
					return "", c.fetchErr, true
				}
				return "{}", "", true
			}
			p.release("W1", c.kind, "http://x/w.js")
			ran := len(f.find("Runtime.runIfWaitingForDebugger")) > 0
			if ran != c.runs {
				t.Errorf("resumed = %v, want %v", ran, c.runs)
			}
			var told bool
			for _, e := range p.Drain() {
				told = told || (e.Kind == engine.BlockedFetch && strings.Contains(e.Text, "not started"))
			}
			if told == c.runs {
				t.Errorf("told the agent it was not started = %v, want %v", told, !c.runs)
			}
		})
	}
}

// A target that closed or navigated away before it was given the page's rules
// was not held back: there is nothing to tell the agent.
func TestATargetGoneBeforeItsIdentityIsNotReportedHeld(t *testing.T) {
	for _, gone := range []string{"Session with given id not found.", "Inspected target navigated or closed"} {
		for _, step := range []string{"Fetch.enable", "Network.setUserAgentOverride"} {
			p, f := newTestPage(t)
			p.filter = func(string, string) error { return nil }
			p.override = map[string]any{"userAgent": "Mozilla/5.0 Chrome/1 Riffle/1"}
			f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
				if r.Method == step && r.Session == "W1" {
					return "", gone, true
				}
				return "{}", "", true
			}
			p.release("W1", "iframe", "http://x/f.html")
			for _, e := range p.Drain() {
				if strings.Contains(e.Text, "not started") {
					t.Errorf("%s %q: a target that is gone was reported held: %s", step, gone, e.Text)
				}
			}
		}
	}
}
