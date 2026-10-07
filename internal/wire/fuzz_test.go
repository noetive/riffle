package wire

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func seedFrames(f *testing.F) {
	f.Add([]byte("run s1 3\nabc"))
	f.Add([]byte("view s1 0\n"))
	f.Add([]byte("ok 5\nhello"))
	f.Add([]byte("err 3\nbad"))
	f.Add([]byte("stopped 0\n"))
	f.Add([]byte("run s1 99\nshort"))
	f.Add([]byte("run s1 -1\n"))
	f.Add([]byte("ok 99999999999999999999\n"))
	f.Add([]byte("\n"))
	f.Add([]byte(""))
	f.Add([]byte("run default 11\nclick \"Buy\"\n"))
	f.Add([]byte("run s1 0\n"))
	f.Add([]byte("view s1 11\ninteractive"))
	f.Add([]byte("run s1 3\nabcrun s1 3\nabc"))
	f.Add([]byte("run s1 3\nabc\nrun s1 3\nabc\n"))
	f.Add([]byte("run  s1  3\nabc"))
	f.Add([]byte("run s1 3 extra\nabc"))
	f.Add([]byte("run s1\nabc"))
	f.Add([]byte("run\n"))
	f.Add([]byte("RUN s1 3\nabc"))
	f.Add([]byte("run s1 3\r\nabc"))
	f.Add([]byte("run s1 +3\nabc"))
	f.Add([]byte("run s1 0x3\nabc"))
	f.Add([]byte("run s1 3.0\nabc"))
	f.Add([]byte("run s1 2147483648\n"))
	f.Add([]byte("run s1 4294967296\n"))
	f.Add([]byte("run s1 9223372036854775807\n"))
	f.Add([]byte("run s1 -9223372036854775808\n"))
	f.Add([]byte("run \x00 3\nabc"))
	f.Add([]byte("run ../../etc 3\nabc"))
	f.Add([]byte("run s\xff1 3\nabc"))
	f.Add([]byte("run " + strings.Repeat("s", 70000) + " 3\nabc"))
	f.Add([]byte("ok 0\n"))
	f.Add([]byte("ok 3\nabcdef"))
	f.Add([]byte("ok -1\n"))
	f.Add([]byte("ok\n"))
	f.Add([]byte("err 0\n"))
	f.Add([]byte("err 12\nline1\nline2\n"))
	f.Add([]byte("stopped 3\nabc"))
	f.Add([]byte("failed 3\nabc"))
	f.Add([]byte("ok 4\n\xff\xfe\x00\x01"))
	f.Add([]byte("ok 2\n日本"))
	f.Add([]byte(strings.Repeat("run s1 1\na", 100)))
	f.Add([]byte(strings.Repeat("x", 1<<16)))
	f.Add([]byte{0, 0, 0, 0})
}

// FuzzReadFrames checks that neither reader panics on arbitrary bytes and
// that anything it accepts is within the documented body bound.
func FuzzReadFrames(f *testing.F) {
	seedFrames(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if req, err := ReadRequest(bufio.NewReader(bytes.NewReader(data))); err == nil && len(req.Body) > maxBody {
			t.Fatalf("request body of %d bytes exceeds the bound", len(req.Body))
		}
		if rep, err := ReadReply(bufio.NewReader(bytes.NewReader(data))); err == nil && len(rep.Body) > maxBody {
			t.Fatalf("reply body of %d bytes exceeds the bound", len(rep.Body))
		}
	})
}

// FuzzRequestRoundTrip checks that any request WriteRequest accepts is read
// back unchanged, and that the reader consumes exactly the frame.
func FuzzRequestRoundTrip(f *testing.F) {
	for _, b := range bodies {
		f.Add(Run, "s1", b)
		f.Add(View, "s2", b)
	}
	f.Fuzz(func(t *testing.T, verb, session, body string) {
		// The header is whitespace-split, so a verb is only expressible
		// when it is a single field. WriteRequest does not check this.
		if len(strings.Fields(verb)) != 1 || strings.Fields(verb)[0] != verb {
			t.Skip()
		}
		in := Request{Verb: verb, Session: session, Body: body}
		var buf bytes.Buffer
		if err := WriteRequest(&buf, in); err != nil {
			return
		}
		if len(strings.Fields(session)) != 1 || strings.Fields(session)[0] != session {
			t.Skip()
		}
		buf.WriteString("tail")
		br := bufio.NewReader(&buf)
		out, err := ReadRequest(br)
		if err != nil {
			t.Fatalf("written frame does not read back: %v", err)
		}
		if out != in {
			t.Fatalf("round trip changed the request: %+v -> %+v", in, out)
		}
		if rest, _ := br.Peek(4); string(rest) != "tail" {
			t.Fatalf("reader did not stop at the frame end: %q", rest)
		}
	})
}

// FuzzReplyRoundTrip checks the same for replies.
func FuzzReplyRoundTrip(f *testing.F) {
	for _, b := range bodies {
		f.Add(b, false, false)
		f.Add(b, true, false)
		f.Add(b, false, true)
	}
	f.Fuzz(func(t *testing.T, body string, failed, stopped bool) {
		// Failed takes precedence on the wire, so both set is not a
		// distinct state.
		if failed && stopped {
			t.Skip()
		}
		in := Reply{Body: body, Failed: failed, Stopped: stopped}
		var buf bytes.Buffer
		if err := WriteReply(&buf, in); err != nil {
			t.Fatal(err)
		}
		buf.WriteString("tail")
		br := bufio.NewReader(&buf)
		out, err := ReadReply(br)
		if err != nil {
			t.Fatalf("written frame does not read back: %v", err)
		}
		if out != in {
			t.Fatalf("round trip changed the reply: %+v -> %+v", in, out)
		}
		if rest, _ := br.Peek(4); string(rest) != "tail" {
			t.Fatalf("reader did not stop at the frame end: %q", rest)
		}
	})
}
