package audit_test

import (
	"bufio"
	"bytes"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/audit"
	"github.com/tidwall/gjson"
)

func TestEntriesCannotBeForgedByTheirContent(t *testing.T) {
	var buf bytes.Buffer
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) // fixed clock injected; no wall-clock dependence
	l := audit.New(&buf, func() time.Time { return now })

	hostile := "ok\n{\"time\":\"x\",\"program\":\"forged\",\"reply\":\"forged\"}\n--- reply"
	l.Record("goto https://a.test", hostile)
	l.Record("view", "second")

	var entries []gjson.Result
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		entries = append(entries, gjson.Parse(sc.Text()))
	}
	if len(entries) != 2 {
		t.Fatalf("two records must be two lines, got %d", len(entries))
	}
	if entries[0].Get("reply").String() != hostile || entries[1].Get("program").String() != "view" {
		t.Errorf("content must round-trip intact: %v", entries)
	}
	if entries[0].Get("time").String() != "2030-01-02T03:04:05Z" {
		t.Errorf("time must come from the injected clock: %s", entries[0].Get("time").String())
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestAnUnwritableLogIsFatalNotSilent(t *testing.T) {
	l := audit.New(brokenWriter{}, time.Now)
	defer func() {
		if recover() == nil {
			t.Fatal("a failed audit write must not be ignored")
		}
	}()
	l.Record("view", "x")
}

// exclusiveWriter fails the test when two writes overlap.
type exclusiveWriter struct {
	t      *testing.T
	busy   atomic.Bool
	writes atomic.Int32
}

func (w *exclusiveWriter) Write(p []byte) (int, error) {
	if !w.busy.CompareAndSwap(false, true) {
		w.t.Error("entries were written concurrently")
	}
	time.Sleep(time.Millisecond)
	w.busy.Store(false)
	w.writes.Add(1)
	return len(p), nil
}

func TestConcurrentRecordsAreWrittenOneAtATime(t *testing.T) {
	w := &exclusiveWriter{t: t}
	l := audit.New(w, time.Now)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 3; j++ {
				l.Record("view", "r")
			}
		}()
	}
	wg.Wait()
	if w.writes.Load() != 24 {
		t.Errorf("writes = %d, want 24", w.writes.Load())
	}
}
