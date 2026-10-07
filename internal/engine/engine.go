// Package engine holds the vocabulary exchanged across the engine port:
// the values an engine reports back to the host. The port interface itself
// is declared by its consumer.
package engine

import (
	"errors"

	json "github.com/goccy/go-json"
)

// EventKind classifies something that happened on the page outside the
// agent's direct command.
type EventKind string

// Event kinds reported by the page.
const (
	Navigated    EventKind = "navigate"
	Dialog       EventKind = "dialog"
	ConsoleError EventKind = "console-error"
	FailedFetch  EventKind = "failed-request"
	Download     EventKind = "download"
	SlowLoad     EventKind = "slow-load"
	// BlockedFetch is a request the operator's policy refused.
	BlockedFetch EventKind = "blocked-request"
	// Validation is a field the browser refused to submit, with its message.
	Validation EventKind = "validation"
	// Toast is a node that appeared and vanished within one settle window.
	Toast EventKind = "toast"
	// Unrestored is a site whose kept storage could not be put back; it
	// stays kept.
	Unrestored EventKind = "unrestored"
)

// ErrOffViewport is a hit test at a point outside the visible page, where
// nothing can be hit (CSSOM View, elementFromPoint): the page moved what was
// measured there.
var ErrOffViewport = errors.New("the point is outside the visible page")

// Event is one page event, reported once.
type Event struct {
	Kind EventKind
	Text string
}

// Request is one network exchange the page made on its own behalf.
type Request struct {
	Method string
	URL    string
	Mime   string
	Kind   string // Document, Fetch, XHR, Script, ...
	Status int
	Size   int64
}

// DialogAnswer is how the next dialog a page opens is answered. The zero
// value accepts it, a prompt with the text it offers.
type DialogAnswer struct {
	Text    string // what a prompt is answered with, when Typed
	Dismiss bool   // cancel a confirm or a prompt
	Typed   bool
}

// State is what a browser keeps for the sites it has been to: its cookies, as
// the browser reports them, and the localStorage of each site, by origin
// (scheme://host[:port]).
type State struct {
	Storage map[string][][2]string
	Cookies []json.RawMessage
}

// Empty reports a state with nothing in it.
func (s State) Empty() bool { return len(s.Cookies) == 0 && len(s.Storage) == 0 }
