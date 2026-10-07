package session

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/noetive/riffle/internal/engine"
	"github.com/noetive/riffle/internal/program"
	"github.com/noetive/riffle/internal/snapshot"
)

// Bounds of a challenge page.
const (
	botCheckMaxText  = 600 // runes of readable text; a challenge says little
	botCheckMinTiles = 6   // images in a grid that asks to pick the matching ones
	// A frame smaller than this is a tracker or a badge, not a widget.
	botCheckMinFrameW, botCheckMinFrameH = 100, 60
)

// botCheckPhrases is the wording of challenge pages: lower-case words, matched
// as whole words. English only: a challenge in another language is not
// recognised.
var botCheckPhrases = []string{
	"unusual traffic",
	"verify you are human",
	"verify you are a human",
	"not a robot",
	"are you a robot",
	"checking your browser",
	"complete the following challenge",
}

// driver is the engine as the interpreter drives it: the page events the
// engine reports, plus the ones the session recognises from the page itself.
type driver struct {
	Engine
	s *Session
}

func (d driver) Drain() []engine.Event {
	ev := d.Engine.Drain()
	return append(ev, (*scene)(d.s).recognise()...)
}

// recognise reports what the page itself says about being let in, once per
// document: a bot-check (the program stops there), or else a refusal of the
// document.
//
// The rule for a bot-check is deliberately narrow, because a false alarm stops
// an agent that could have continued. All three must hold:
//   - challenge wording, as whole words, in the readable text;
//   - little to read (at most botCheckMaxText runes), so an article that
//     mentions the phrase is not a challenge;
//   - the main document was refused (403, 429 or 503), or the page holds a
//     challenge widget: a visible frame of at least 100x60, a slider, or a
//     grid of at least 6 visible images.
//
// A refusal alone (a login wall, a maintenance page, a bare server answer) is
// not a bot-check; it is reported as a refused event and the agent reads the
// page. Wording alone is not enough either: it also matches help pages.
func (c *scene) recognise() []engine.Event {
	if c.page.Snap.Len() == 0 {
		return nil
	}
	doc := documentID(c.page.Snap)
	if c.botReported == doc {
		return nil
	}
	status := c.status()
	refused := status == 403 || status == 429 || status == 503
	origin := program.OriginOf(c.page.Snap.URL)
	if (refused || c.challengeWidget()) && c.challengeWording() {
		c.botReported = doc
		return []engine.Event{{Kind: program.BotCheck, Text: origin}}
	}
	if refused && c.refusedReported != doc {
		c.refusedReported = doc
		return []engine.Event{{Kind: program.Refused, Text: fmt.Sprintf("%d %s", status, origin)}}
	}
	return nil
}

// challengeWording: the page says little and what it says is a challenge.
func (c *scene) challengeWording() bool {
	text := c.readable()
	if len([]rune(text)) > botCheckMaxText {
		return false
	}
	words := " " + strings.Join(strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), " ") + " "
	for _, p := range botCheckPhrases {
		if strings.Contains(words, " "+p+" ") {
			return true
		}
	}
	return false
}

// status is the answer to the main document, 0 when none was seen.
func (c *scene) status() int {
	want := withoutFragment(c.page.Snap.URL)
	status := 0
	for _, r := range c.eng.Requests() {
		if r.Kind == "Document" && withoutFragment(r.URL) == want {
			status = r.Status // the last answer for the address is the page's
		}
	}
	return status
}

// challengeWidget says whether the page holds a visible frame of a size to
// carry a challenge, a slider, or a grid of image tiles.
func (c *scene) challengeWidget() bool {
	snap := c.page.Snap
	tiles := 0
	for i := range c.page.Nodes {
		n := &c.page.Nodes[i]
		if snap.Kind[i] != snapshot.KindElement || !n.Visible() {
			continue
		}
		switch {
		case snap.Tag[i] == "iframe":
			if b := snap.Box[i]; b.W >= botCheckMinFrameW && b.H >= botCheckMinFrameH {
				return true
			}
		case n.Role == "slider":
			return true
		case snap.Tag[i] == "img":
			tiles++
		}
	}
	return tiles >= botCheckMinTiles
}

func withoutFragment(u string) string {
	if i := strings.IndexByte(u, '#'); i >= 0 {
		return u[:i]
	}
	return u
}
