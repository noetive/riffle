package main

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/noetive/riffle/internal/wire"
)

func TestAnMCPServerGivenASessionDrivesThatOne(t *testing.T) {
	if got, err := mcpSession("shared", ""); err != nil || got != "shared" {
		t.Errorf("a named session is kept: %q %v", got, err)
	}
}

// Two servers started without a name, as two editor windows are, must never
// drive the same page.
func TestEveryMCPServerWithoutANameHasASessionOfItsOwn(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		name, err := mcpSession("", "")
		if err != nil {
			t.Fatal(err)
		}
		if seen[name] {
			t.Fatalf("two servers share session %q", name)
		}
		seen[name] = true
		if !strings.Contains(name, "-"+strconv.Itoa(os.Getpid())+"-") {
			t.Fatalf("the name %q does not say which program holds it", name)
		}
		if err := (wire.Request{Verb: wire.Run, Session: name}).Validate(); err != nil {
			t.Fatalf("a generated name the daemon would refuse: %v", err)
		}
	}
}

func TestAServerThatKeepsStateDrivesTheSessionItKeeps(t *testing.T) {
	for _, given := range []string{"", "shop"} {
		if got, err := mcpSession(given, "shop"); err != nil || got != "shop" {
			t.Errorf("-s %q -keep-state shop drives shop: %q %v", given, got, err)
		}
	}
}

func TestAServerToldTwoSessionsOrABadNameRefusesToStart(t *testing.T) {
	if _, err := mcpSession("work", "shop"); err == nil || !strings.Contains(err.Error(), "two sessions") {
		t.Errorf("-s work -keep-state shop is refused: %v", err)
	}
	if _, err := mcpSession("", "../shop"); err == nil {
		t.Error("a state name that is not a plain file name is refused")
	}
}

func TestAReleaseStampOutranksTheModuleVersion(t *testing.T) {
	if got := stampedOr("v0.1.0", "0.2.0"); got != "0.2.0" {
		t.Fatalf("got %q", got)
	}
}

func TestAGoInstallBuildReportsItsModuleVersion(t *testing.T) {
	for module, want := range map[string]string{
		"v0.1.0":                             "0.1.0",
		"v0.1.0-rc.1":                        "0.1.0-rc.1",
		"v0.0.0-20261007153921-c2b32e147e74": "0.0.0-20261007153921-c2b32e147e74",
		"(devel)":                            "dev",
		"":                                   "dev",
	} {
		if got := stampedOr(module, "dev"); got != want {
			t.Errorf("module %q: got %q, want %q", module, got, want)
		}
	}
}

func TestAStoppedProgramPrintsItsReplyAndFails(t *testing.T) {
	var out strings.Builder
	err := emit(&out, wire.Reply{Body: "failed line 2: click \"Go\": not found", Stopped: true})
	if !errors.Is(err, errStopped) {
		t.Fatalf("a stopped program must fail, got %v", err)
	}
	if out.String() != "failed line 2: click \"Go\": not found\n" {
		t.Fatalf("the reply must still reach stdout, got %q", out.String())
	}
}

func TestACompletedProgramSucceeds(t *testing.T) {
	var out strings.Builder
	if err := emit(&out, wire.Reply{Body: "page x"}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "page x\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestABuildFromACheckoutSaysDev(t *testing.T) {
	if strings.HasPrefix(moduleVersion(), "v") {
		t.Skip("this toolchain stamps test binaries with a module version")
	}
	if version != "dev" {
		t.Fatalf("an unstamped build without a module version must say dev, got %q", version)
	}
}

type brokenPipe struct{}

func (brokenPipe) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestAReplyThatCannotBePrintedFails(t *testing.T) {
	if err := emit(brokenPipe{}, wire.Reply{Body: "page x"}); err == nil || errors.Is(err, errStopped) {
		t.Fatalf("a reply lost on its way to stdout must fail, got %v", err)
	}
}
