// Package quota bounds how many browsers run at once on this machine, across
// every Riffle daemon of the user. A browser is about ten processes; a machine
// that runs out of them fails every program, not only Riffle's.
//
// The quota is a directory of lock files, one per seat. A daemon holds a lock
// for as long as its browser lives; the system releases it when the holder
// exits, however it exits, so a crashed daemon never keeps a seat.
package quota

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	json "github.com/goccy/go-json"

	"github.com/noetive/riffle/internal/userdir"
)

// Holder says who sits in a seat, for the agent told that none is free.
type Holder struct {
	Since   time.Time `json:"since"`
	Used    time.Time `json:"used"`
	Session string    `json:"session"`
	Socket  string    `json:"socket"`
	PID     int       `json:"pid"`
	// Held: a program, such as an MCP server, holds the session open, so it
	// does not end for being unused.
	Held bool `json:"held"`
}

// Seats is the machine's quota of browsers.
type Seats struct {
	dir string
	max int
}

// Open is the quota of max browsers kept in dir.
func Open(dir string, max int) (*Seats, error) {
	if max < 1 {
		return nil, fmt.Errorf("quota: at least one browser must be allowed, not %d", max)
	}
	if err := userdir.Ensure(dir); err != nil {
		return nil, fmt.Errorf("quota: %w", err)
	}
	return &Seats{dir: dir, max: max}, nil
}

// Max is how many browsers the quota allows.
func (q *Seats) Max() int { return q.max }

// Seat is one browser's place in the quota.
type Seat struct {
	wrote  time.Time // guarded by mu: when the note was last written
	q      *Seats
	lock   *os.File // guarded by mu
	holder Holder   // guarded by mu
	n      int
	mu     sync.Mutex
}

// FullError is a quota with no free seat, and who holds the seats.
type FullError struct {
	Holders []Holder
	Max     int
	Waited  time.Duration
}

func (e *FullError) Error() string {
	var who []string
	var idle *Holder
	for i, h := range e.Holders {
		state := fmt.Sprintf("idle %s", time.Since(h.Used).Round(time.Second))
		if h.Held {
			state = "held open by a program such as an MCP server"
		} else if idle == nil {
			idle = &e.Holders[i]
		}
		who = append(who, fmt.Sprintf("%q on %s (%s)", h.Session, h.Socket, state))
	}
	if len(who) == 0 {
		who = append(who, "none that say who they are")
	}
	msg := fmt.Sprintf("riffle: %d browsers already run on this machine, the most it allows, and none came free in %s; held by sessions %s. ", e.Max, e.Waited.Round(time.Second), strings.Join(who, ", "))
	if idle != nil {
		return msg + fmt.Sprintf("Close one you no longer need, such as `riffle close -s %s -socket %s`, or retry later: a session nobody holds ends once unused for a while", idle.Session, idle.Socket)
	}
	return msg + "Each is held open by a running program; stop one of those programs, or retry later"
}

// poll is how often a full quota is looked at again.
const poll = 250 * time.Millisecond

// Take waits for a free seat until ctx ends, then refuses with a *FullError.
func (q *Seats) Take(ctx context.Context, h Holder) (*Seat, error) {
	began := time.Now()
	for {
		for n := 1; n <= q.max; n++ {
			f, ok, err := q.try(n)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			s := &Seat{q: q, n: n, lock: f, holder: h}
			s.holder.PID = os.Getpid()
			s.holder.Since, s.holder.Used = time.Now(), time.Now()
			s.mu.Lock()
			s.write()
			s.mu.Unlock()
			return s, nil
		}
		select {
		case <-ctx.Done():
			return nil, &FullError{Holders: q.Holders(), Max: q.max, Waited: time.Since(began)}
		case <-time.After(poll):
		}
	}
}

// try locks seat n without waiting; ok is false when someone holds it.
func (q *Seats) try(n int) (*os.File, bool, error) {
	f, err := os.OpenFile(q.lockPath(n), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("quota: %w", err)
	}
	held, err := tryLock(f)
	if err != nil {
		_ = f.Close()
		return nil, false, fmt.Errorf("quota: lock seat %d: %w", n, err)
	}
	if !held {
		_ = f.Close()
		return nil, false, nil
	}
	return f, true, nil
}

// Holders are the sessions in the seats that are held now.
func (q *Seats) Holders() []Holder {
	var out []Holder
	for n := 1; n <= q.max; n++ {
		f, free, err := q.try(n)
		if err != nil {
			continue
		}
		if free {
			_ = f.Close() // closing releases the lock just taken
			continue
		}
		b, err := readNote(q.notePath(n))
		if err != nil {
			continue
		}
		var h Holder
		if json.Unmarshal(b, &h) == nil {
			out = append(out, h)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Used.Before(out[j].Used) })
	return out
}

// Used records that the seat's browser was just used. The note is written at
// most once a second: it only informs.
func (s *Seat) Used() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holder.Used = time.Now()
	if time.Since(s.wrote) >= time.Second {
		s.write()
	}
}

// Held records whether a program holds the seat's session open.
func (s *Seat) Held(held bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holder.Held = held
	s.write()
}

// Release gives the seat back. Releasing twice is harmless.
func (s *Seat) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return
	}
	_ = os.Remove(s.q.notePath(s.n))
	_ = s.lock.Close() // closing releases the lock
	s.lock = nil
}

// write saves who holds the seat beside its lock, through a temporary file
// of its own so a reader never sees half a note. It only informs: the lock,
// not the note, says whether the seat is held. The caller holds mu.
func (s *Seat) write() {
	if s.lock == nil {
		return
	}
	b, err := json.Marshal(s.holder)
	if err != nil {
		return
	}
	f, err := os.CreateTemp(s.q.dir, strconv.Itoa(s.n)+".json.*")
	if err != nil {
		return
	}
	_, werr := f.Write(b)
	if cerr := f.Close(); werr != nil || cerr != nil || os.Rename(f.Name(), s.q.notePath(s.n)) != nil {
		_ = os.Remove(f.Name())
		return
	}
	s.wrote = time.Now()
}

// maxNote bounds a note read back: anything larger is no note of ours.
const maxNote = 4 << 10

func readNote(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxNote+1))
	if err != nil || len(b) > maxNote {
		return nil, fmt.Errorf("quota: note %s unreadable or too large", path)
	}
	return b, nil
}

func (q *Seats) lockPath(n int) string { return filepath.Join(q.dir, strconv.Itoa(n)+".lock") }
func (q *Seats) notePath(n int) string { return filepath.Join(q.dir, strconv.Itoa(n)+".json") }

// IsFull reports a refusal for want of a free seat.
func IsFull(err error) bool {
	var f *FullError
	return errors.As(err, &f)
}
