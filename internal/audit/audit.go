// Package audit appends every program and its reply to a session log, enough
// to replay the session. Entries are one JSON object per line, so text in a
// program or a page cannot forge another entry.
package audit

import (
	"fmt"
	"io"
	"sync"
	"time"

	json "github.com/goccy/go-json"
)

// Log is an append-only session log.
type Log struct {
	w   io.Writer
	now func() time.Time
	mu  sync.Mutex
}

// New logs to w, stamping entries from now.
func New(w io.Writer, now func() time.Time) *Log {
	return &Log{w: w, now: now}
}

type entry struct {
	Time    string `json:"time"`
	Program string `json:"program"`
	Reply   string `json:"reply"`
}

// Record appends one program and its reply.
func (l *Log) Record(program, reply string) {
	b, err := json.Marshal(entry{Time: l.now().UTC().Format(time.RFC3339), Program: program, Reply: reply})
	if err != nil {
		panic(fmt.Sprintf("audit: cannot encode entry: %v", err)) // strings always encode; anything else is a bug
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.w.Write(append(b, '\n')); err != nil {
		panic(fmt.Sprintf("audit: log write failed: %v", err)) // an unwritable audit log is an invariant failure
	}
}
