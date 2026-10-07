package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/engine"
	pb "github.com/noetive/riffle/internal/facts/pagebuilder"
	"github.com/noetive/riffle/internal/program"
)

const challengeURL = "https://shop.example/blocked"

// challengePage is a page of one paragraph, an optional frame of the given
// size and a number of buttons.
func challengePage(text string, frameW, frameH float64, buttons int) func(*fakeEngine, *pb.Builder) {
	return func(e *fakeEngine, b *pb.Builder) {
		para(b, b.Body(), 100, text)
		if frameW > 0 {
			b.El(b.Body(), "iframe", pb.Rect(300, 150, frameW, frameH), pb.ID(900))
		}
		for i := 0; i < buttons; i++ {
			button(b, b.Body(), float64(300+40*i), "Option")
		}
	}
}

func goto_(t *testing.T, e *fakeEngine, status int) string {
	t.Helper()
	return gotoWith(t, e, status).Text
}

func gotoWith(t *testing.T, e *fakeEngine, status int) Outcome {
	t.Helper()
	e.reqs = []engine.Request{{Method: "GET", URL: challengeURL, Kind: "Document", Status: status}}
	return run(t, newSession(e, Config{}), "goto "+challengeURL)
}

func TestALoginWallThatRefusesIsReportedAsRefusedNotABotCheck(t *testing.T) {
	wall := func(e *fakeEngine, b *pb.Builder) {
		para(b, b.Body(), 100, "Sign in to continue")
		b.El(b.Body(), "input", pb.Rect(20, 200, 200, 20), pb.Attr("type", "email"), pb.Attr("placeholder", "Email"))
		button(b, b.Body(), 250, "Next")
	}
	out := gotoWith(t, newFake(wall), 403)
	if strings.Contains(out.Text, "bot-check") || out.Stopped {
		t.Errorf("a login wall is a page, not a stop:\n%s", out.Text)
	}
	if !strings.Contains(out.Text, `event refused "403 https://shop.example"`) {
		t.Errorf("the refusal is reported:\n%s", out.Text)
	}
}

func TestEveryRefusalStatusIsReportedForABarePage(t *testing.T) {
	for _, status := range []int{403, 429, 503} {
		out := goto_(t, newFake(challengePage("Down for maintenance", 0, 0, 0)), status)
		if strings.Contains(out, "bot-check") || !strings.Contains(out, `event refused "`+itoa(status)+` https://shop.example"`) {
			t.Errorf("status %d:\n%s", status, out)
		}
	}
}

func itoa(n int) string {
	return strings.TrimSpace(strings.Repeat(" ", 0) + string(rune('0'+n/100)) + string(rune('0'+n/10%10)) + string(rune('0'+n%10)))
}

func TestOtherStatusesAreNotRefusals(t *testing.T) {
	for _, status := range []int{200, 301, 401, 404, 500} {
		if out := goto_(t, newFake(challengePage("Access denied", 0, 0, 1)), status); strings.Contains(out, "refused") || strings.Contains(out, "bot-check") {
			t.Errorf("status %d:\n%s", status, out)
		}
	}
}

func TestARefusalIsReportedOncePerDocument(t *testing.T) {
	e := newFake(challengePage("Access denied", 0, 0, 0))
	e.reqs = []engine.Request{{URL: challengeURL, Kind: "Document", Status: 403}}
	s := newSession(e, Config{})
	if out := run(t, s, "goto "+challengeURL).Text; strings.Count(out, "event refused") != 1 {
		t.Fatalf("first navigation:\n%s", out)
	}
	if out := run(t, s, "view").Text; strings.Contains(out, "event refused") {
		t.Errorf("the same document must not be reported again:\n%s", out)
	}
	if out := run(t, s, "goto "+challengeURL).Text; strings.Count(out, "event refused") != 1 {
		t.Errorf("a new navigation is reported again:\n%s", out)
	}
}

func TestAnotherDocumentsStatusDoesNotRefuseThePage(t *testing.T) {
	e := newFake(challengePage("Access denied", 0, 0, 1))
	e.reqs = []engine.Request{
		{URL: "https://shop.example/ad-frame", Kind: "Document", Status: 403},
		{URL: challengeURL + "#top", Kind: "Document", Status: 200},
	}
	if out := run(t, newSession(e, Config{}), "goto "+challengeURL).Text; strings.Contains(out, "refused") {
		t.Errorf("only the page's own answer counts:\n%s", out)
	}
}

func TestOnlyTheMainDocumentsStatusAndOnlyDocumentsCount(t *testing.T) {
	e := newFake(challengePage("Access denied", 0, 0, 1))
	e.reqs = []engine.Request{{URL: challengeURL, Kind: "Fetch", Status: 403}}
	if out := run(t, newSession(e, Config{}), "goto "+challengeURL).Text; strings.Contains(out, "refused") {
		t.Errorf("a refused background request is not a refused page:\n%s", out)
	}
	e = newFake(challengePage("Access denied", 0, 0, 1))
	e.reqs = []engine.Request{{URL: challengeURL, Kind: "Document", Status: 403}}
	if out := run(t, newSession(e, Config{}), "goto "+challengeURL+"#part").Text; !strings.Contains(out, "event refused") {
		t.Errorf("a fragment does not make it another document:\n%s", out)
	}
	e = newFake(challengePage("Access denied", 0, 0, 1))
	e.reqs = []engine.Request{{URL: challengeURL, Kind: "Document", Status: 200}, {URL: "https://shop.example/other", Kind: "Document", Status: 403}}
	if out := run(t, newSession(e, Config{}), "goto "+challengeURL).Text; strings.Contains(out, "refused") {
		t.Errorf("another address's refusal is not this page's:\n%s", out)
	}
}

func TestChallengeWordingWithAFrameIsABotCheckWithoutAnyRefusal(t *testing.T) {
	for _, say := range []string{"We have detected unusual traffic from your network", "Please verify you are human to continue", "Checking your browser before accessing", "Are you a robot?"} {
		out := gotoWith(t, newFake(challengePage(say, 300, 80, 0)), 200)
		if !strings.Contains(out.Text, `event bot-check "https://shop.example"`) || !out.Stopped {
			t.Errorf("%q:\n%s", say, out.Text)
		}
	}
}

func TestChallengeWordingWithARefusalIsABotCheckWithoutAWidget(t *testing.T) {
	out := gotoWith(t, newFake(challengePage("Please verify you are human", 0, 0, 0)), 429)
	if !strings.Contains(out.Text, "event bot-check") || strings.Contains(out.Text, "event refused") {
		t.Errorf("a bot-check replaces the refusal:\n%s", out.Text)
	}
}

func TestAProgramStopsAtABotCheckAndTypesNothing(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		para(b, b.Body(), 100, "Please verify you are human")
		b.El(b.Body(), "iframe", pb.Rect(300, 150, 300, 80), pb.ID(900))
		b.El(b.Body(), "input", pb.Rect(20, 400, 200, 20), pb.Attr("type", "text"), pb.Attr("placeholder", "Search"), pb.Value(e.typed))
	})
	cfg := Config{Policy: program.Policy{Secrets: stubSecrets{}}}
	out := run(t, newSession(e, cfg), "goto "+challengeURL+"\nfill \"Search\" $secret:pw\nfill \"Search\" \"hello\"")
	if !out.Stopped || !strings.Contains(out.Text, `stopped: bot-check at "https://shop.example"`) {
		t.Errorf("the reply names the bot-check:\n%s", out.Text)
	}
	if e.typed != "" || len(e.focused) != 0 {
		t.Errorf("typed %q, focused %v after a bot-check", e.typed, e.focused)
	}
}

type stubSecrets struct{}

func (stubSecrets) Resolve(string, string) (string, error) { return "s3cret-value", nil }

func TestWordingIsMatchedAsWholeWords(t *testing.T) {
	for _, say := range []string{
		"Why we like the not a robotic vacuum",
		"Reviews of a notable robot vacuum",
		"unusual trafficking laws",
		"not a robotics course",
		"I cannot a robot sorry",
	} {
		if out := goto_(t, newFake(challengePage(say, 300, 80, 0)), 200); strings.Contains(out, "bot-check") {
			t.Errorf("%q is not challenge wording:\n%s", say, out)
		}
	}
	// Punctuation and case around the phrase do not matter.
	if out := goto_(t, newFake(challengePage("Hmm... NOT A ROBOT!", 300, 80, 0)), 200); !strings.Contains(out, "bot-check") {
		t.Errorf("punctuation around the phrase:\n%s", out)
	}
}

func TestALongArticleMentioningThePhraseIsNotAChallenge(t *testing.T) {
	article := strings.Repeat("A long article about gardens and how to grow them. ", 20) + " Some sites ask you to verify you are human."
	if out := goto_(t, newFake(challengePage(article, 300, 80, 1)), 200); strings.Contains(out, "bot-check") {
		t.Errorf("an article with an ad frame:\n%s", out)
	}
	if out := goto_(t, newFake(challengePage(article, 300, 80, 1)), 503); strings.Contains(out, "bot-check") {
		t.Errorf("an article on a refused page:\n%s", out)
	}
}

func TestTheTextBoundOfAChallengeIsExact(t *testing.T) {
	say := func(n int) string { return "not a robot " + strings.Repeat("x", n-len("not a robot ")) }
	for _, c := range []struct {
		runes int
		want  bool
	}{{600, true}, {601, false}} {
		out := goto_(t, newFake(challengePage(say(c.runes), 300, 80, 0)), 200)
		if got := strings.Contains(out, "bot-check"); got != c.want {
			t.Errorf("%d runes: reported=%v, want %v", c.runes, got, c.want)
		}
	}
}

func TestAFrameNeedsToBeAtLeastTheSizeOfAWidget(t *testing.T) {
	for _, c := range []struct {
		w, h float64
		want bool
	}{{100, 60, true}, {99, 60, false}, {100, 59, false}, {1, 1, false}} {
		out := goto_(t, newFake(challengePage("Please verify you are human", c.w, c.h, 0)), 200)
		if got := strings.Contains(out, "bot-check"); got != c.want {
			t.Errorf("frame %vx%v: reported=%v, want %v", c.w, c.h, got, c.want)
		}
	}
}

func TestChallengeWordingWithATileGridOrASliderIsReported(t *testing.T) {
	tiles := func(visible, hidden int) func(*fakeEngine, *pb.Builder) {
		return func(e *fakeEngine, b *pb.Builder) {
			para(b, b.Body(), 100, "Please verify you are human")
			for i := 0; i < visible; i++ {
				b.El(b.Body(), "img", pb.Rect(float64(20+90*i), 200, 80, 80), pb.ID(int64(700+i)))
			}
			for i := 0; i < hidden; i++ {
				b.El(b.Body(), "img", pb.Rect(0, 0, 0, 0), pb.ID(int64(800+i)))
			}
		}
	}
	if out := goto_(t, newFake(tiles(6, 0)), 200); !strings.Contains(out, "bot-check") {
		t.Errorf("tile grid:\n%s", out)
	}
	if out := goto_(t, newFake(tiles(5, 3)), 200); strings.Contains(out, "bot-check") {
		t.Errorf("one tile short of a grid; tiles nobody sees do not count:\n%s", out)
	}
	slider := func(e *fakeEngine, b *pb.Builder) {
		para(b, b.Body(), 100, "Please verify you are human")
		b.El(b.Body(), "div", pb.Rect(20, 200, 200, 20), pb.Attr("role", "slider"))
	}
	if out := goto_(t, newFake(slider), 200); !strings.Contains(out, "bot-check") {
		t.Errorf("slider:\n%s", out)
	}
}

func TestChallengeWordingAloneIsNotEnough(t *testing.T) {
	// An article about robots, or a help page quoting the phrase, is a page.
	out := goto_(t, newFake(challengePage("Why a captcha says I am not a robot", 0, 0, 1)), 200)
	if strings.Contains(out, "bot-check") {
		t.Errorf("wording without a refusal or a challenge widget:\n%s", out)
	}
}

func TestAFrameWithoutChallengeWordingIsNotABotCheck(t *testing.T) {
	if out := goto_(t, newFake(challengePage("Watch the video below", 300, 80, 0)), 200); strings.Contains(out, "bot-check") {
		t.Errorf("an embedded video is not a challenge:\n%s", out)
	}
}

func TestAHiddenFrameIsNotAChallengeWidget(t *testing.T) {
	hidden := func(e *fakeEngine, b *pb.Builder) {
		para(b, b.Body(), 100, "Please verify you are human")
		b.El(b.Body(), "iframe", pb.Rect(0, 0, 0, 0), pb.ID(900))
	}
	if out := goto_(t, newFake(hidden), 200); strings.Contains(out, "bot-check") {
		t.Errorf("a tracking frame nobody sees:\n%s", out)
	}
}

func TestABotCheckIsReportedOncePerNavigation(t *testing.T) {
	e := newFake(challengePage("We have detected unusual traffic", 300, 80, 0))
	s := newSession(e, Config{})
	if out := run(t, s, "goto "+challengeURL).Text; strings.Count(out, "event bot-check") != 1 {
		t.Fatalf("first navigation:\n%s", out)
	}
	if out := run(t, s, "view").Text; strings.Contains(out, "bot-check") {
		t.Errorf("the same document must not be reported again:\n%s", out)
	}
	if out := run(t, s, "goto "+challengeURL).Text; strings.Count(out, "event bot-check") != 1 {
		t.Errorf("a new navigation is reported again:\n%s", out)
	}
}

func TestTheOriginInAnEventHasNoCredentialsAndOpaqueOnesAreTheirScheme(t *testing.T) {
	e := newFake(challengePage("Please verify you are human", 300, 80, 0))
	e.url = "https://alice:hunter2@shop.example:8443/blocked?q=1"
	out := run(t, newSession(e, Config{}), "view").Text
	if !strings.Contains(out, `event bot-check "https://shop.example:8443"`) || strings.Contains(out, "hunter2") || strings.Contains(out, "alice") {
		t.Errorf("credentials, path and query stay out:\n%s", out)
	}
	e = newFake(challengePage("Please verify you are human", 300, 80, 0))
	e.url = "data:text/html,secret-body"
	out = run(t, newSession(e, Config{}), "view").Text
	if !strings.Contains(out, `event bot-check "data:"`) || strings.Contains(out, `"data:text`) {
		t.Errorf("an opaque address is its scheme:\n%s", out)
	}
}

func TestEngineEventsSurviveBesideTheBotCheck(t *testing.T) {
	e := &eventEngine{fakeEngine: newFake(challengePage("Please verify you are human", 300, 80, 0)), ev: []engine.Event{{Kind: engine.SlowLoad, Text: "slow"}}}
	out := run(t, New(e, Config{Policy: program.Policy{Pacing: &program.Pacing{}}}), "goto "+challengeURL).Text
	if !strings.Contains(out, `event slow-load "slow"`) || !strings.Contains(out, "event bot-check") {
		t.Errorf("both events are reported:\n%s", out)
	}
}

type eventEngine struct {
	*fakeEngine
	ev []engine.Event
}

func (e *eventEngine) Drain() []engine.Event { d := e.ev; e.ev = nil; return d }

func TestNoPageMeansNothingToReport(t *testing.T) {
	e := newFake(challengePage("Access denied", 0, 0, 1))
	e.snapErr = errors.New("page crashed")
	out := run(t, newSession(e, Config{}), "goto "+challengeURL)
	if strings.Contains(out.Text, "bot-check") || strings.Contains(out.Text, "refused") || !out.Stopped {
		t.Errorf("%+v", out)
	}
}

// spinner paints a full-viewport layer with a message and no controls: a
// cover the agent cannot answer, only wait out.
func spinner(b *pb.Builder, text string) {
	w := b.El(b.Body(), "div", pb.Rect(0, 0, 1280, 800), pb.Position("fixed"), pb.Fill("rgba(255, 255, 255, 0.8)"))
	b.Text(w, text)
}

func TestAnActionWaitsForADisabledNodeToBecomeEnabled(t *testing.T) {
	snaps := 0
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		snaps++
		if snaps < 5 {
			button(b, b.Body(), 100, "Checkout", pb.Attr("disabled", ""))
			return
		}
		button(b, b.Body(), 100, "Checkout")
	})
	out := run(t, newSession(e, Config{}), `click "Checkout"`)
	if out.Stopped || len(e.clicks) != 1 {
		t.Errorf("a button enabled a few rounds later must be clicked, clicks=%v:\n%s", e.clicks, out.Text)
	}
}

func TestADisabledNodeThatStaysDisabledFailsNamingTheBlockAndTheWait(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		button(b, b.Body(), 100, "Checkout", pb.Attr("disabled", ""))
	})
	out := run(t, newSession(e, Config{}), `click "Checkout"`)
	if !out.Stopped || !strings.Contains(out.Text, "blocked: b1 disabled (waited 0s)") || len(e.clicks) != 0 {
		t.Errorf("clicks=%v:\n%s", e.clicks, out.Text)
	}
}

func TestAnActionWaitsForAnAnonymousCoverToGoAway(t *testing.T) {
	snaps := 0
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		snaps++
		button(b, b.Body(), 100, "Checkout")
		if snaps < 5 {
			spinner(b, "Loading")
		}
	})
	out := run(t, newSession(e, Config{}), `click "Checkout"`)
	if out.Stopped || len(e.clicks) != 1 {
		t.Errorf("a layer that fades must be waited out, clicks=%v:\n%s", e.clicks, out.Text)
	}
}

func TestAnAnonymousCoverThatStaysFailsAfterTheWait(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		button(b, b.Body(), 100, "Checkout")
		spinner(b, "Loading")
	})
	out := run(t, newSession(e, Config{}), `click "Checkout"`)
	if !out.Stopped || !strings.Contains(out.Text, "covered-by an overlay whose page text is") || !strings.Contains(out.Text, "(waited 0s)") || len(e.clicks) != 0 {
		t.Errorf("clicks=%v:\n%s", e.clicks, out.Text)
	}
}

func TestADialogTheAgentCanAnswerFailsAtOnceWithoutWaiting(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		button(b, b.Body(), 100, "Checkout")
		overlay(b, "Cookie preferences")
	})
	// A wait would sleep for an hour; the deadline turns that into a failure
	// of the assertion below.
	cfg := Config{Policy: program.Policy{Pacing: &program.Pacing{Pause: time.Hour}}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := New(e, cfg).Do(ctx, `click "Checkout"`)
	if !out.Stopped || !strings.Contains(out.Text, `blocked: b1 covered-by`) || !strings.Contains(out.Text, `"Cookie preferences"`) || strings.Contains(out.Text, "waited") || len(e.clicks) != 0 {
		t.Errorf("clicks=%v:\n%s", e.clicks, out.Text)
	}
}

func TestAWaitedTargetOnAPageThatNavigatedAwayIsNeverClicked(t *testing.T) {
	snaps := 0
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		snaps++
		button(b, b.Body(), 100, "Checkout")
		if snaps < 3 {
			spinner(b, "Loading")
			return
		}
		if snaps == 3 { // the page redirects to another site that has a button of the same name
			e.url, e.doc = "https://other.example/landing", e.doc+1
		}
	})
	out := run(t, newSession(e, Config{}), `click "Checkout"`)
	if !out.Stopped || !strings.Contains(out.Text, "page changed") || !strings.Contains(out.Text, "https://other.example") || len(e.clicks) != 0 {
		t.Errorf("clicks=%v:\n%s", e.clicks, out.Text)
	}
}

func TestATargetInsideAnEmbeddedFrameIsSaidToBeUnreachable(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		b.El(b.Body(), "iframe", pb.Rect(20, 100, 300, 120), pb.ID(900), pb.Attr("title", "Secure card number"))
		button(b, b.Body(), 300, "Pay")
	})
	out := run(t, newSession(e, Config{}), `fill "Card number" "4242"`)
	if !out.Stopped || !strings.Contains(out.Text, "embedded frame") || !strings.Contains(out.Text, "cannot read or act") || strings.Contains(out.Text, "view interactive") {
		t.Errorf("%s", out.Text)
	}
	out = run(t, newSession(e, Config{}), `click "Nothing like it"`)
	if !strings.Contains(out.Text, "view interactive") || strings.Contains(out.Text, "embedded frame") {
		t.Errorf("a name no frame carries keeps the usual advice:\n%s", out.Text)
	}
}

func TestOnlyAVisibleFrameCarriesATitleThatTheTargetCanBeInside(t *testing.T) {
	e := newFake(func(e *fakeEngine, b *pb.Builder) {
		b.El(b.Body(), "iframe", pb.Rect(0, 0, 0, 0), pb.ID(900), pb.Attr("title", "Hidden frame"))
		b.El(b.Body(), "div", pb.Rect(20, 100, 300, 50), pb.Attr("title", "Plain block"))
		b.El(b.Body(), "iframe", pb.Rect(20, 200, 300, 120), pb.ID(901), pb.Attr("name", "payment-card"))
	})
	s := newSession(e, Config{})
	for _, name := range []string{"Hidden frame", "Plain block"} {
		if out := run(t, s, `click "`+name+`"`); strings.Contains(out.Text, "embedded frame") {
			t.Errorf("%q is not inside a frame you can see:\n%s", name, out.Text)
		}
	}
	if out := run(t, s, `click "payment-card"`); !strings.Contains(out.Text, "embedded frame") {
		t.Errorf("a frame's name counts like its title:\n%s", out.Text)
	}
}

func TestOnlyADialogCoverFailsAtOnce(t *testing.T) {
	cover := func(role string) func(*fakeEngine, *pb.Builder) {
		return func(e *fakeEngine, b *pb.Builder) {
			button(b, b.Body(), 100, "Checkout")
			opts := []pb.Opt{pb.Position("fixed"), pb.Fill("rgb(255, 255, 255)")}
			if role != "" {
				opts = append(opts, pb.Attr("role", role))
			}
			c := b.El(b.Body(), "div", pb.Rect(0, 0, 1280, 800), opts...)
			button(b, c, 300, "Close")
		}
	}
	cfg := Config{Policy: program.Policy{Pacing: &program.Pacing{Pause: 30 * time.Millisecond}}}
	for role, wait := range map[string]bool{"alertdialog": false, "dialog": false, "": true} {
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		out := New(newFake(cover(role)), cfg).Do(ctx, `click "Checkout"`)
		cancel()
		// A wait of 20 rounds at 30ms outlasts the deadline.
		if got := strings.Contains(out.Text, "deadline"); got != wait {
			t.Errorf("role %q: waited=%v, want %v:\n%s", role, got, wait, out.Text)
		}
	}
}
