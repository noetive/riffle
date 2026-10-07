package chromium

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestASlowLoadStillGivesTheArrivedPageItsMinimum(t *testing.T) {
	p, f, still, _ := livePage(t)
	loadSettle = 300 * time.Millisecond
	var arrived atomic.Int64
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "Page.navigate":
			// The load takes longer than the page's minimum.
			time.AfterFunc(2*loadSettle, func() {
				arrived.Store(time.Now().UnixNano())
				f.emit("S1", "Page.loadEventFired", "")
			})
		case "Runtime.evaluate":
			return evaluation(r, "[]", still.ms.Load().(string)), "", true
		}
		return "{}", "", true
	}
	if err := p.Load(context.Background(), "http://x/"); err != nil {
		t.Fatal(err)
	}
	if after := time.Since(time.Unix(0, arrived.Load())); after < loadSettle {
		t.Errorf("read %s after the page arrived; the time its load took is no time it had to run (minimum %s)", after, loadSettle)
	}
}

func TestAPollOpenSinceBeforeTheSettleIsNotToldAsHung(t *testing.T) {
	p, _, _, skew := livePage(t)
	settleCap = 3 * time.Second
	p.feed("Network.requestWillBeSent", `{"requestId":"poll","type":"Fetch","request":{"method":"GET","url":"http://x/poll"}}`)
	skew.Store(int64(2 * staleRequest))
	p.feed("Network.requestWillBeSent", `{"requestId":"api","type":"XHR","request":{"method":"GET","url":"http://x/api"}}`)
	var answered atomic.Bool
	time.AfterFunc(50*time.Millisecond, func() {
		answered.Store(true)
		p.feed("Network.loadingFinished", `{"requestId":"api"}`)
	})
	if err := p.Settle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !answered.Load() {
		t.Error("the settle did not wait for the request the page was waiting on")
	}
	if notes := slowNotes(p); len(notes) != 0 {
		t.Errorf("a poll the page kept open all along is not news on every action: %v", notes)
	}
}

func TestASettleFollowsANavigationWithoutCallingItsDocumentHung(t *testing.T) {
	p, _, _, _ := livePage(t)
	settleCap, staleRequest = 3*time.Second, 100*time.Millisecond
	p.feed("Page.frameNavigated", `{"frame":{"id":"F","url":"http://x/form"}}`)
	// A form post the server answers slowly: the new document is on its way.
	p.feed("Page.frameStartedLoading", `{"frameId":"F"}`)
	p.feed("Network.requestWillBeSent", `{"requestId":"doc","type":"Document","frameId":"F","request":{"method":"POST","url":"http://x/done"}}`)
	time.AfterFunc(4*staleRequest, func() {
		p.feed("Network.loadingFinished", `{"requestId":"doc"}`)
		p.feed("Page.frameNavigated", `{"frame":{"id":"F","url":"http://x/done"}}`)
		p.feed("Page.frameStoppedLoading", `{"frameId":"F"}`)
	})
	if err := p.Settle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.isLoading() {
		t.Error("the settle returned before the new page arrived")
	}
	for _, n := range slowNotes(p) {
		if strings.Contains(n, "had not answered") {
			t.Errorf("a page that arrived late was read whole, not incomplete: %v", n)
		}
	}
}

func TestTheQuietProbeGivesThePageNoUserGesture(t *testing.T) {
	p, f, _, _ := livePage(t)
	if err := p.Settle(context.Background()); err != nil {
		t.Fatal(err)
	}
	probes := 0
	for _, r := range f.find("Runtime.evaluate") {
		if r.Params.Get("expression").String() != transientStill {
			continue
		}
		probes++
		if r.Params.Get("userGesture").Bool() {
			t.Error("each poll of a settle would let the page open another popup")
		}
	}
	if probes == 0 {
		t.Fatal("the settle never asked whether the page was still")
	}
}

func TestTypingASecretGivesThePageTheActionsMinimum(t *testing.T) {
	p, f, still, _ := livePage(t)
	actionSettle = 300 * time.Millisecond
	f.handle = func(f *fakeBrowser, r rec) (string, string, bool) {
		switch r.Method {
		case "DOM.resolveNode":
			return `{"object":{"objectId":"o1"}}`, "", true
		case "Runtime.callFunctionOn":
			return `{"result":{"type":"boolean","value":true}}`, "", true
		case "Runtime.evaluate":
			return evaluation(r, "[]", still.ms.Load().(string)), "", true
		}
		return "{}", "", true
	}
	took, err := timed(func() error {
		if err := p.SetSecret(context.Background(), 7, "s3cret"); err != nil {
			return err
		}
		return p.Settle(context.Background())
	})
	if err != nil {
		t.Fatal(err)
	}
	if took < actionSettle {
		t.Errorf("read %s after a secret was typed, before the page had %s to check it", took, actionSettle)
	}
}

func TestALoadThatBringsNoDocumentStillOwesTheActionItsMinimum(t *testing.T) {
	p, _, _, _ := livePage(t)
	actionSettle, loadSettle, settleCap = 300*time.Millisecond, 30*time.Second, 3*time.Second
	p.feed("Page.frameNavigated", `{"frame":{"id":"F","url":"http://x/"}}`)
	took, err := timed(func() error {
		if err := p.Click(context.Background(), 1, 1); err != nil {
			return err
		}
		// The click starts a download: a load begins and ends, and no new
		// document commits.
		p.feed("Page.frameStartedLoading", `{"frameId":"F"}`)
		time.AfterFunc(20*time.Millisecond, func() { p.feed("Page.frameStoppedLoading", `{"frameId":"F"}`) })
		return p.Settle(context.Background())
	})
	if err != nil {
		t.Fatal(err)
	}
	if took < actionSettle {
		t.Errorf("read %s after the click, before the page had its %s", took, actionSettle)
	}
	if took >= settleCap/2 {
		t.Errorf("a load that brought no document was given a new document's minimum: %s", took)
	}
}
