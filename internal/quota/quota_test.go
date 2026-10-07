package quota_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/quota"
)

func open(t *testing.T, max int) *quota.Seats {
	t.Helper()
	q, err := quota.Open(t.TempDir(), max)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func soon(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

func TestNoMoreSeatsAreGivenThanTheQuotaAllows(t *testing.T) {
	q := open(t, 2)
	a, err := q.Take(soon(t), quota.Holder{Session: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Take(soon(t), quota.Holder{Session: "b"}); err != nil {
		t.Fatal(err)
	}
	_, err = q.Take(soon(t), quota.Holder{Session: "c"})
	var full *quota.FullError
	if !errors.As(err, &full) || !quota.IsFull(err) {
		t.Fatalf("a third browser is refused, err = %v", err)
	}
	for _, want := range []string{`"a"`, `"b"`, "riffle close -s a", "2 browsers", "idle"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal says %q: %v", want, err)
		}
	}
	a.Release()
	a.Release() // twice is harmless
	if _, err := q.Take(soon(t), quota.Holder{Session: "c"}); err != nil {
		t.Errorf("a released seat is free again: %v", err)
	}
}

// The quota is the machine's: another daemon opening the same directory sees
// the same seats taken.
func TestEveryDaemonSharesTheSameSeats(t *testing.T) {
	dir := t.TempDir()
	one, _ := quota.Open(dir, 1)
	other, _ := quota.Open(dir, 1)
	s, err := one.Take(soon(t), quota.Holder{Session: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Take(soon(t), quota.Holder{Session: "second"}); !quota.IsFull(err) {
		t.Fatalf("a seat taken through one daemon is taken for all, err = %v", err)
	}
	if h := other.Holders(); len(h) != 1 || h[0].Session != "first" {
		t.Errorf("holders seen from elsewhere = %+v", h)
	}
	s.Release()
	if _, err := other.Take(soon(t), quota.Holder{Session: "second"}); err != nil {
		t.Errorf("once released, the seat is free for all: %v", err)
	}
}

// A taker waits for a seat to be given back rather than failing at once.
func TestATakerWaitsForASeatToComeFree(t *testing.T) {
	q := open(t, 1)
	s, _ := q.Take(soon(t), quota.Holder{Session: "busy"})
	go func() { time.Sleep(100 * time.Millisecond); s.Release() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := q.Take(ctx, quota.Holder{Session: "next"}); err != nil {
		t.Errorf("the seat given back is taken: %v", err)
	}
}

func TestHoldersAreListedLeastRecentlyUsedFirst(t *testing.T) {
	q := open(t, 3)
	old, _ := q.Take(soon(t), quota.Holder{Session: "old"})
	time.Sleep(20 * time.Millisecond)
	fresh, _ := q.Take(soon(t), quota.Holder{Session: "fresh"})
	time.Sleep(20 * time.Millisecond)
	fresh.Used()
	h := q.Holders()
	if len(h) != 2 || h[0].Session != "old" || h[1].Session != "fresh" {
		t.Errorf("holders = %+v, want old then fresh", h)
	}
	old.Release()
	if h := q.Holders(); len(h) != 1 || h[0].Session != "fresh" {
		t.Errorf("a released seat is no longer listed: %+v", h)
	}
}

func TestAQuotaAllowsAtLeastOneBrowser(t *testing.T) {
	if _, err := quota.Open(t.TempDir(), 0); err == nil {
		t.Error("a quota of no browsers is refused")
	}
}
