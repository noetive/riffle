// Package wire is the text-frame protocol between the CLI, the MCP facade
// and the daemon. A frame is one header line and a body of exactly the
// stated byte length. There is no JSON on this path.
//
//	request: "<verb> <session> <bytes>\n<body>"   verb is run, view, close, archive or hold
//	reply:   "<ok|err> <bytes>\n<body>"
package wire

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Verbs of the protocol.
const (
	Run  = "run"
	View = "view"
	// Close ends a session and its browser now.
	Close = "close"
	// Archive returns the session's current page as one MHTML document.
	Archive = "archive"
	// Hold keeps a session open for as long as the connection that sent it
	// stays open: an MCP server holds its session, and when its last holder
	// goes the session ends.
	Hold = "hold"
)

// KeepState is the body of a hold that asks for the session's cookies and
// storage to be kept between its browsers. A daemon that keeps them replies
// HeldKeepingState followed by where; a reply without it is from a daemon
// that does not keep state.
const (
	KeepState        = "keep-state"
	HeldKeepingState = "held keep-state "
)

// Request asks a session to do something.
type Request struct {
	Verb    string
	Session string
	Body    string
}

// Reply is the daemon's answer. Failed marks protocol or session errors.
type Reply struct {
	Body   string
	Failed bool
	// Stopped marks a program that ran but stopped at a failed step; the body
	// explains which and why.
	Stopped bool
}

// WriteRequest sends a request frame.
func WriteRequest(w io.Writer, r Request) error {
	if strings.ContainsAny(r.Session, " \n") || r.Session == "" {
		return fmt.Errorf("wire: bad session name %q", r.Session)
	}
	_, err := fmt.Fprintf(w, "%s %s %d\n%s", r.Verb, r.Session, len(r.Body), r.Body)
	return err
}

// ReadRequest reads a request frame.
func ReadRequest(br *bufio.Reader) (Request, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return Request{}, err
	}
	f := strings.Fields(line)
	if len(f) != 3 {
		return Request{}, fmt.Errorf("wire: malformed request header %q", strings.TrimSpace(line))
	}
	body, err := readBody(br, f[2])
	if err != nil {
		return Request{}, err
	}
	return Request{Verb: f[0], Session: f[1], Body: body}, nil
}

// WriteReply sends a reply frame.
func WriteReply(w io.Writer, r Reply) error {
	status := "ok"
	switch {
	case r.Failed:
		status = "err"
	case r.Stopped:
		status = "stopped"
	}
	_, err := fmt.Fprintf(w, "%s %d\n%s", status, len(r.Body), r.Body)
	return err
}

// ReadReply reads a reply frame.
func ReadReply(br *bufio.Reader) (Reply, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return Reply{}, err
	}
	f := strings.Fields(line)
	if len(f) != 2 || (f[0] != "ok" && f[0] != "err" && f[0] != "stopped") {
		return Reply{}, fmt.Errorf("wire: malformed reply header %q", strings.TrimSpace(line))
	}
	body, err := readBody(br, f[1])
	if err != nil {
		return Reply{}, err
	}
	return Reply{Failed: f[0] == "err", Stopped: f[0] == "stopped", Body: body}, nil
}

// maxBody bounds a frame so a corrupt header cannot exhaust memory. It is
// large enough for the archive of a heavy page.
const maxBody = 128 << 20

func readBody(br *bufio.Reader, n string) (string, error) {
	size, err := strconv.Atoi(n)
	if err != nil || size < 0 || size > maxBody {
		return "", fmt.Errorf("wire: bad body length %q", n)
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(br, buf); err != nil {
		return "", fmt.Errorf("wire: short body: %w", err)
	}
	return string(buf), nil
}

// Validate reports a request that cannot be framed.
func (r Request) Validate() error {
	if strings.ContainsAny(r.Session, " \n") || r.Session == "" || len(r.Session) > MaxSession {
		return fmt.Errorf("wire: bad session name %q: one word of at most %d bytes", r.Session, MaxSession)
	}
	return nil
}

// MaxSession bounds a session name.
const MaxSession = 64
