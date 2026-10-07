package chromium

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

func TestTheUserAgentDropsTheHeadlessMarkerAndEndsInTheProduct(t *testing.T) {
	base := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/154.0.0.0 Safari/537.36"
	want := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.0.0 Safari/537.36 Riffle/1.2.3"
	got, err := productUserAgent(base, "Riffle/1.2.3")
	if err != nil || got != want {
		t.Errorf("got %q, %v", got, err)
	}
	if strings.Contains(got, "Headless") {
		t.Errorf("the user agent still says headless: %q", got)
	}
}

func TestAUserAgentWithoutTheHeadlessMarkerOnlyGainsTheProduct(t *testing.T) {
	base := "Mozilla/5.0 (Macintosh) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.0.0 Safari/537.36"
	got, err := productUserAgent(base, "Riffle/1.2.3")
	if err != nil || got != base+" Riffle/1.2.3" {
		t.Errorf("got %q, %v", got, err)
	}
}

const headlessHints = `{"brands":[{"brand":"Not;A=Brand","version":"99"},{"brand":"HeadlessChrome","version":"154"},{"brand":"Chromium","version":"154"}],` +
	`"fullVersionList":[{"brand":"Not;A=Brand","version":"99.0.0.0"},{"brand":"HeadlessChrome","version":"154.0.1.2"},{"brand":"Chromium","version":"154.0.1.2"}],` +
	`"platformVersion":"6.1","architecture":"x86","wow64":false}`

func TestHeadlessBrandLeavesBothBrandListsAndKeepsTheRest(t *testing.T) {
	got, err := withoutHeadlessBrand(headlessHints)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "Headless") {
		t.Fatalf("still headless: %s", got)
	}
	for _, list := range []string{"brands", "fullVersionList"} {
		var names []string
		for _, b := range gjson.Get(got, list).Array() {
			names = append(names, b.Get("brand").String()+"/"+b.Get("version").String())
		}
		if len(names) != 2 || names[0][:12] != "Not;A=Brand/" || !strings.HasPrefix(names[1], "Chromium/154") {
			t.Errorf("%s = %v", list, names)
		}
	}
	if gjson.Get(got, "fullVersionList.1.version").String() != "154.0.1.2" {
		t.Errorf("versions changed: %s", got)
	}
	if gjson.Get(got, "platformVersion").String() != "6.1" || gjson.Get(got, "architecture").String() != "x86" || gjson.Get(got, "wow64").Bool() {
		t.Errorf("unrelated hints changed: %s", got)
	}
}

func TestHeadlessBrandBecomesChromiumWhenNothingElseNamesTheEngine(t *testing.T) {
	in := `{"brands":[{"brand":"HeadlessChrome","version":"154"}],"fullVersionList":[{"brand":"HeadlessChrome","version":"154.0.1.2"}]}`
	got, err := withoutHeadlessBrand(in)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.Get(got, "brands.0.brand").String() != "Chromium" || gjson.Get(got, "brands.0.version").String() != "154" ||
		gjson.Get(got, "fullVersionList.0.brand").String() != "Chromium" || gjson.Get(got, "fullVersionList.0.version").String() != "154.0.1.2" {
		t.Errorf("got %s", got)
	}
}

func TestHintsWithoutTheHeadlessBrandAreReturnedAsTheyCame(t *testing.T) {
	// Unsorted keys and irregular spacing: any re-encoding would change them.
	in := `{ "mobile" : false,  "brands":[ {"version":"154","brand":"Chromium"} ],"architecture":"x86" }`
	got, err := withoutHeadlessBrand(in)
	if err != nil || got != in {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestMalformedHintsFailLoudly(t *testing.T) {
	for _, in := range []string{`not json`, `{"brands":`, `{"brands":"HeadlessChrome"}`, `{"brands":[1],"fullVersionList":[{"brand":"HeadlessChrome"}]}`} {
		if got, err := withoutHeadlessBrand(in); err == nil {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
}

// versionTab is a browser whose throwaway chrome://version tab first holds the
// blank document every new tab starts on: a look at it can fail while the
// document is replaced, and its navigator has no userAgentData until the
// version page has loaded, as on a busy machine.
func versionTab(t *testing.T, loadsAfter int) (*Page, *fakeBrowser, *atomic.Int32) {
	p, f := newTestPage(t)
	var looks, early atomic.Int32
	f.handle = func(_ *fakeBrowser, r rec) (string, string, bool) {
		switch {
		case r.Method == "Target.createTarget":
			return `{"targetId":"V1"}`, "", true
		case r.Method == "Target.attachToTarget":
			return `{"sessionId":"VS"}`, "", true
		case r.Method == "Runtime.evaluate" && r.Session == "VS" && r.Params.Get("expression").String() == onVersionPage:
			n := looks.Add(1)
			if n == 1 {
				return "", "Execution context was destroyed.", true
			}
			return `{"result":{"type":"boolean","value":` + boolJSON(int(n) > loadsAfter) + `}}`, "", true
		case r.Method == "Runtime.evaluate" && r.Session == "VS":
			if int(looks.Load()) <= loadsAfter {
				early.Add(1)
				return `{"result":{"type":"object"},"exceptionDetails":{"exception":{"description":"TypeError: Cannot read properties of undefined (reading 'getHighEntropyValues')"}}}`, "", true
			}
			return `{"result":{"type":"string","value":"{\"brands\":[{\"brand\":\"Chromium\",\"version\":\"154\"}]}"}}`, "", true
		}
		return "{}", "", true
	}
	return p, f, &early
}

func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestClientHintsAreReadOnlyOnceTheVersionPageHasLoaded(t *testing.T) {
	p, f, early := versionTab(t, 3)
	hints, err := p.defaultClientHints(testCtx(t))
	if err != nil {
		t.Fatalf("a version page that loads late is waited for: %v", err)
	}
	if !gjson.Get(hints, "brands").IsArray() {
		t.Errorf("hints = %q, want the browser's brands", hints)
	}
	if n := early.Load(); n != 0 {
		t.Errorf("hints were asked of the blank document %d times", n)
	}
	if n := len(f.find("Target.closeTarget")); n != 1 {
		t.Errorf("the throwaway tab was closed %d times, want 1", n)
	}
}

func TestAVersionPageThatNeverLoadsFailsWithinItsBound(t *testing.T) {
	old := versionPageWait
	versionPageWait = 200 * time.Millisecond
	t.Cleanup(func() { versionPageWait = old })
	p, f, early := versionTab(t, 1<<30)
	start := time.Now()
	_, err := p.defaultClientHints(testCtx(t))
	if err == nil || !strings.Contains(err.Error(), "did not load within") {
		t.Fatalf("err = %v, want the version page reported as never loading", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("gave up after %s, want about %s", took, versionPageWait)
	}
	if n := early.Load(); n != 0 {
		t.Errorf("hints were asked of the blank document %d times", n)
	}
	if n := len(f.find("Target.closeTarget")); n != 1 {
		t.Errorf("the throwaway tab was closed %d times, want 1", n)
	}
}
