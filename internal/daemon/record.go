package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	json "github.com/goccy/go-json"
)

// record is the policy a daemon was started with, kept beside its socket. A
// client that has to start the daemon again starts it with this policy, so a
// daemon the operator restricted never comes back unrestricted after a
// crash. It lives in the socket's private directory and outlasts the daemon
// on purpose: it is the operator's last word on the socket.
type record struct {
	Policy Policy `json:"policy"`
	PID    int    `json:"pid"`
}

func recordPath(socket string) string { return socket + ".policy" }

// writeRecord replaces the record atomically, so a reader sees the old
// record or the new one and never a part of either.
func writeRecord(socket string, p Policy) error {
	b, err := json.Marshal(record{PID: os.Getpid(), Policy: p})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(socket), ".policy-*")
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	if err := os.Rename(f.Name(), recordPath(socket)); err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	return nil
}

// readRecord returns the record beside socket; ok is false when there is
// none. A record that cannot be read is an error, never the default policy:
// guessing would undo whatever the operator restricted.
func readRecord(socket string) (rec record, ok bool, err error) {
	b, err := os.ReadFile(recordPath(socket))
	if errors.Is(err, fs.ErrNotExist) {
		return record{}, false, nil
	}
	if err != nil {
		return record{}, false, fmt.Errorf("read the daemon's policy: %w", err)
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return record{}, false, fmt.Errorf("the daemon's policy in %s is unreadable; remove it and start riffle serve with the policy you want: %w", recordPath(socket), err)
	}
	return rec, true, nil
}
