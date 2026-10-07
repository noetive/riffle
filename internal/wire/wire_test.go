package wire

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

var bodies = []string{
	"",
	"one line",
	"line1\nline2\n\nline4\n",
	"\n",
	"héllo wörld ☃ 日本語 \U0001F600",
	"ok 5\nnot a header",
	"err 3\nrun s 99\n",
	"trailing space ",
}

func TestRequestRoundTrip(t *testing.T) {
	for _, b := range bodies {
		for _, verb := range []string{Run, View} {
			var buf bytes.Buffer
			in := Request{Verb: verb, Session: "s1", Body: b}
			if err := WriteRequest(&buf, in); err != nil {
				t.Fatal(err)
			}
			out, err := ReadRequest(bufio.NewReader(&buf))
			if err != nil {
				t.Fatalf("%q: %v", b, err)
			}
			if out != in {
				t.Fatalf("got %+v want %+v", out, in)
			}
		}
	}
}

func TestReplyRoundTrip(t *testing.T) {
	for _, b := range bodies {
		for _, failed := range []bool{false, true} {
			var buf bytes.Buffer
			in := Reply{Failed: failed, Body: b}
			if err := WriteReply(&buf, in); err != nil {
				t.Fatal(err)
			}
			out, err := ReadReply(bufio.NewReader(&buf))
			if err != nil {
				t.Fatalf("%q: %v", b, err)
			}
			if out != in {
				t.Fatalf("got %+v want %+v", out, in)
			}
		}
	}
}

func TestFramesBackToBackOnOneStream(t *testing.T) {
	var buf bytes.Buffer
	for _, b := range bodies {
		if err := WriteReply(&buf, Reply{Body: b}); err != nil {
			t.Fatal(err)
		}
	}
	br := bufio.NewReader(&buf)
	for _, b := range bodies {
		r, err := ReadReply(br)
		if err != nil || r.Body != b {
			t.Fatalf("got %q, %v want %q", r.Body, err, b)
		}
	}
}

func TestWriteRequestRejectsBadSession(t *testing.T) {
	for _, s := range []string{"", "a b", "a\nb"} {
		if err := WriteRequest(&bytes.Buffer{}, Request{Verb: Run, Session: s}); err == nil {
			t.Fatalf("session %q accepted", s)
		}
	}
}

func TestMalformedRequestHeaders(t *testing.T) {
	for _, h := range []string{
		"\n", "run\n", "run s\n", "run s 1 extra\n", "run s abc\n", "run s -1\n", "run s 1.5\n",
		fmt.Sprintf("run s %d\n", maxBody+1), "run s 99999999999999999999\n",
	} {
		if _, err := ReadRequest(bufio.NewReader(strings.NewReader(h + "x"))); err == nil {
			t.Errorf("header %q accepted", h)
		}
	}
}

func TestMalformedReplyHeaders(t *testing.T) {
	for _, h := range []string{
		"\n", "ok\n", "maybe 1\n", "ok 1 2\n", "ok abc\n", "err -1\n", "OK 1\n",
		fmt.Sprintf("ok %d\n", maxBody+1),
	} {
		if _, err := ReadReply(bufio.NewReader(strings.NewReader(h + "x"))); err == nil {
			t.Errorf("header %q accepted", h)
		}
	}
}

func TestShortBodyIsError(t *testing.T) {
	if _, err := ReadReply(bufio.NewReader(strings.NewReader("ok 10\nabc"))); err == nil || !strings.Contains(err.Error(), "short body") {
		t.Fatalf("reply: %v", err)
	}
	if _, err := ReadRequest(bufio.NewReader(strings.NewReader("run s 10\nabc"))); err == nil || !strings.Contains(err.Error(), "short body") {
		t.Fatalf("request: %v", err)
	}
}

func TestMissingHeaderNewlineIsError(t *testing.T) {
	if _, err := ReadReply(bufio.NewReader(strings.NewReader("ok 3"))); err == nil {
		t.Fatal("accepted header without newline")
	}
}

func TestMaxBodyBoundaryHeaderAccepted(t *testing.T) {
	// A header at the limit passes length validation and fails only on missing data.
	_, err := ReadReply(bufio.NewReader(strings.NewReader(fmt.Sprintf("ok %d\nabc", maxBody))))
	if err == nil || !strings.Contains(err.Error(), "short body") {
		t.Fatalf("got %v", err)
	}
}

func TestStoppedReplyRoundTrips(t *testing.T) {
	for _, b := range bodies {
		var buf bytes.Buffer
		in := Reply{Stopped: true, Body: b}
		if err := WriteReply(&buf, in); err != nil {
			t.Fatal(err)
		}
		out, err := ReadReply(bufio.NewReader(&buf))
		if err != nil || out != in {
			t.Fatalf("got %+v, %v want %+v", out, err, in)
		}
	}
}

func TestFailedOutranksStoppedOnTheWire(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteReply(&buf, Reply{Failed: true, Stopped: true, Body: "x"}); err != nil {
		t.Fatal(err)
	}
	out, err := ReadReply(bufio.NewReader(&buf))
	if err != nil || !out.Failed || out.Stopped {
		t.Fatalf("got %+v, %v", out, err)
	}
}

func TestSuccessfulReplyIsNeitherFailedNorStopped(t *testing.T) {
	out, err := ReadReply(bufio.NewReader(strings.NewReader("ok 1\nx")))
	if err != nil || out.Failed || out.Stopped || out.Body != "x" {
		t.Fatalf("got %+v, %v", out, err)
	}
}

func TestClosedStreamSurfacesEOFUnchanged(t *testing.T) {
	if _, err := ReadRequest(bufio.NewReader(strings.NewReader(""))); !errors.Is(err, io.EOF) {
		t.Errorf("request: %v", err)
	}
	if _, err := ReadReply(bufio.NewReader(strings.NewReader(""))); !errors.Is(err, io.EOF) {
		t.Errorf("reply: %v", err)
	}
}

func TestBodyLimitIsLargeEnoughForAPageArchiveAndNoMore(t *testing.T) {
	over := "ok 134217729\n"
	if _, err := ReadReply(bufio.NewReader(strings.NewReader(over))); err == nil || strings.Contains(err.Error(), "short body") {
		t.Errorf("one byte over the limit must be refused at the header: %v", err)
	}
	_, err := ReadReply(bufio.NewReader(strings.NewReader("ok 134217728\nabc")))
	if err == nil || !strings.Contains(err.Error(), "short body") {
		t.Errorf("exactly the limit passes the header check: %v", err)
	}
}

func TestValidateRejectsUnframeableSessions(t *testing.T) {
	if err := (Request{Verb: Run, Session: "s1"}).Validate(); err != nil {
		t.Errorf("good session rejected: %v", err)
	}
	for _, s := range []string{"", "a b", "a\nb", " "} {
		if err := (Request{Verb: Run, Session: s}).Validate(); err == nil {
			t.Errorf("session %q accepted", s)
		}
	}
}
