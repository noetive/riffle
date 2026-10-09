// Package daemon holds sessions and serves them over a Unix socket.
package daemon

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/noetive/riffle/internal/audit"
	"github.com/noetive/riffle/internal/chromium"
	"github.com/noetive/riffle/internal/engine"
	"github.com/noetive/riffle/internal/program"
	"github.com/noetive/riffle/internal/quota"
	"github.com/noetive/riffle/internal/session"
	"github.com/noetive/riffle/internal/userdir"
	"github.com/noetive/riffle/internal/wire"
)

// Defaults of the browser lifecycle. A browser is about ten processes and a
// few hundred megabytes; six at once can overload a laptop.
const (
	DefaultMaxBrowsers = 4
	DefaultSessionIdle = 15 * time.Minute
	DefaultDaemonIdle  = 30 * time.Minute
	DefaultSeatWait    = 30 * time.Second
	// DefaultHoldGrace is how long a session outlives its last holder, so a
	// holder that reconnects finds its page still there.
	DefaultHoldGrace = 10 * time.Second
)

// Config is the operator's policy for every session the daemon starts.
type Config struct {
	Socket string
	// AuditPath receives the append-only session log.
	AuditPath string
	// Product is the token every page adds to its user agent, e.g. "Riffle/1.0".
	Product string
	// QuotaDir keeps the machine's browser seats, shared by every daemon of
	// the user; empty is the user's cache directory.
	QuotaDir string
	// StateDir keeps the state of sessions that keep it; empty is beside the
	// socket, one directory for each socket.
	StateDir string
	// Policy limits what sessions may reach and upload.
	Policy Policy
	Width  int
	Height int
	// MaxBrowsers bounds the browsers running at once on this machine,
	// across every daemon of the user; 0 is DefaultMaxBrowsers.
	MaxBrowsers int
	// SessionIdle ends a session nobody holds after this long unused; 0 is
	// DefaultSessionIdle.
	SessionIdle time.Duration
	// DaemonIdle ends the daemon after this long with no session and no
	// client; 0 is DefaultDaemonIdle.
	DaemonIdle time.Duration
	// SeatWait is how long a new session waits for a browser seat before it
	// is refused; 0 is DefaultSeatWait.
	SeatWait time.Duration
	// HoldGrace is how long a session outlives its last holder; 0 is
	// DefaultHoldGrace.
	HoldGrace time.Duration
}

// browsing is a session as the daemon drives it.
type browsing interface {
	Do(ctx context.Context, src string) session.Outcome
	Archive(ctx context.Context) (string, error)
	State(ctx context.Context) (engine.State, error)
	Close()
	Alive() bool
}

// What the next use of a session is told about the browser it lost.
const (
	lostPage    = "its page, cookies and sign-ins are gone, and it starts again on a blank page: run your navigation again"
	noteIdle    = "this session's browser was closed after %s without use; " + lostPage
	noteClosed  = "this session was closed with `riffle close`; " + lostPage
	noteHolder  = "this session's browser was closed when the program holding it stopped; " + lostPage
	noteStopped = "this session's browser had stopped; " + lostPage
)

// keptPage replaces lostPage for a session that keeps its state.
const keptPage = "its page is gone and it starts again on a blank page, with the cookies and storage kept for it: run your navigation again"

// closure is why a session's browser was closed, and when.
type closure struct {
	at  time.Time
	why string
}

// Daemon serves sessions.
type Daemon struct {
	active   time.Time          // guarded by mu: when a client last connected or a request ended
	sessions map[string]*slot   // guarded by mu
	holds    map[string]int     // guarded by mu: connections holding each session
	closed   map[string]closure // guarded by mu: why a session's browser was closed, told on its next use
	keeps    map[string]int     // guarded by mu: connections holding each session that keep its state
	keepers  map[string]*keeper // guarded by mu: the keeper of each name whose state is kept
	log      *audit.Log
	seats    *quota.Seats
	launch   func(ctx context.Context, st engine.State) (browsing, error)
	cfg      Config
	conns    int  // guarded by mu: open client connections
	closing  bool // guarded by mu: the daemon has decided to end and takes no more work

	mu sync.Mutex
}

// New builds a daemon; Serve starts it.
func New(cfg Config) (*Daemon, error) {
	if cfg.MaxBrowsers == 0 {
		cfg.MaxBrowsers = DefaultMaxBrowsers
	}
	if cfg.SessionIdle == 0 {
		cfg.SessionIdle = DefaultSessionIdle
	}
	if cfg.DaemonIdle == 0 {
		cfg.DaemonIdle = DefaultDaemonIdle
	}
	if cfg.SeatWait == 0 {
		cfg.SeatWait = DefaultSeatWait
	}
	if cfg.HoldGrace == 0 {
		cfg.HoldGrace = DefaultHoldGrace
	}
	if cfg.QuotaDir == "" {
		cfg.QuotaDir = DefaultQuotaDir()
	}
	if cfg.StateDir == "" {
		cfg.StateDir = stateDirOf(cfg.Socket)
		if cfg.Socket == DefaultSocket() {
			if err := seedState(cfg.StateDir, earlierStateDirs(wire.Protocol)); err != nil {
				return nil, fmt.Errorf("daemon: %w", err)
			}
		}
	}
	seats, err := quota.Open(cfg.QuotaDir, cfg.MaxBrowsers)
	if err != nil {
		return nil, fmt.Errorf("daemon: %w", err)
	}
	d := &Daemon{cfg: cfg, seats: seats, sessions: map[string]*slot{}, holds: map[string]int{}, closed: map[string]closure{}, keeps: map[string]int{}, keepers: map[string]*keeper{}, active: time.Now()}
	d.launch = d.start
	if cfg.AuditPath != "" {
		f, err := os.OpenFile(cfg.AuditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("daemon: open audit log: %w", err)
		}
		d.log = audit.New(f, time.Now)
	}
	return d, nil
}

// DefaultQuotaDir is where the machine's browser seats are kept: one place
// for every daemon of the user, whatever socket each serves.
func DefaultQuotaDir() string { return filepath.Join(riffleDir(), "browsers") }

// Serve accepts connections until ctx ends, or until the daemon has had no
// session and no client for DaemonIdle.
func (d *Daemon) Serve(ctx context.Context) error {
	if c, err := (&net.Dialer{}).DialContext(ctx, "unix", d.cfg.Socket); err == nil {
		_ = c.Close()
		return fmt.Errorf("daemon: another daemon is already serving %s", d.cfg.Socket)
	}
	if err := userdir.Ensure(filepath.Dir(d.cfg.Socket)); err != nil {
		return fmt.Errorf("daemon: socket directory: %w", err)
	}
	// Recorded before anyone can connect, so a client never meets this daemon
	// without its policy beside it.
	if err := writeRecord(d.cfg.Socket, d.cfg.Policy); err != nil {
		return fmt.Errorf("daemon: record policy: %w", err)
	}
	_ = os.Remove(d.cfg.Socket)
	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", d.cfg.Socket)
	if err != nil {
		return fmt.Errorf("daemon: listen: %w", err)
	}
	if err := os.Chmod(d.cfg.Socket, 0o600); err != nil {
		return fmt.Errorf("daemon: chmod socket: %w", err)
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	go d.tend(ctx, stop)
	defer d.closeAll(ctx)
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("daemon: accept: %w", err)
		}
		go d.handle(ctx, c)
	}
}

// tend ends sessions nobody uses and, once nothing is left, the daemon.
func (d *Daemon) tend(ctx context.Context, stop context.CancelFunc) {
	tick := min(max(min(d.cfg.SessionIdle, d.cfg.DaemonIdle, d.cfg.HoldGrace)/4, 10*time.Millisecond), 30*time.Second)
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if d.reap(ctx) {
			stop()
			return
		}
	}
}

// reap ends the sessions nobody needs any more, and reports whether the
// daemon itself has been idle for DaemonIdle with nothing left to serve; when
// it has, the daemon takes no more work. A session ends when its browser has
// stopped; when its last holder left HoldGrace ago; or, held by none, when it
// was unused for SessionIdle. None ends while a request is running in it.
func (d *Daemon) reap(ctx context.Context) bool {
	now := time.Now()
	var ending []*slot
	d.mu.Lock()
	for name, sl := range d.sessions {
		if !sl.settled() || sl.busy > 0 {
			continue
		}
		var why string
		switch {
		case !sl.s.Alive():
			why = noteStopped
		case d.holds[name] > 0:
			continue
		case !sl.released.IsZero():
			if now.Sub(sl.released) < d.cfg.HoldGrace {
				continue
			}
			why = noteHolder
		case now.Sub(sl.used) >= d.cfg.SessionIdle:
			why = fmt.Sprintf(noteIdle, d.cfg.SessionIdle.Round(time.Second))
		default:
			continue
		}
		delete(d.sessions, name)
		d.closed[name] = closure{why: sl.lost(why), at: now}
		ending = append(ending, sl)
	}
	// A note nobody came back for in DaemonIdle is dropped, as it would be if
	// the daemon had ended for want of work: an MCP server leaves one
	// behind under a name no one will use again.
	for name, c := range d.closed {
		if now.Sub(c.at) >= d.cfg.DaemonIdle {
			delete(d.closed, name)
		}
	}
	done := len(d.sessions) == 0 && len(ending) == 0 && d.conns == 0 && now.Sub(d.active) >= d.cfg.DaemonIdle
	if done {
		d.closing = true
	}
	d.mu.Unlock()
	for _, sl := range ending {
		sl.end(ctx)
	}
	return done
}

func (d *Daemon) closeAll(ctx context.Context) {
	d.mu.Lock()
	d.closing = true
	var all []*slot
	for name, sl := range d.sessions {
		all = append(all, sl)
		delete(d.sessions, name)
	}
	d.mu.Unlock()
	for _, sl := range all {
		<-sl.ready
		sl.end(ctx)
	}
}

func (d *Daemon) handle(ctx context.Context, c net.Conn) {
	d.mu.Lock()
	if d.closing {
		// Ending: a client that finds no daemon starts a new one.
		d.mu.Unlock()
		_ = c.Close()
		return
	}
	d.conns++
	d.active = time.Now()
	d.mu.Unlock()
	held := map[string]bool{}
	defer func() {
		_ = c.Close()
		d.disconnect(held)
	}()
	br := bufio.NewReader(c)
	for {
		req, err := wire.ReadRequest(br)
		if err != nil {
			return
		}
		var rep wire.Reply
		if err := req.Validate(); err != nil {
			rep = wire.Reply{Failed: true, Body: err.Error()}
		} else if req.Verb == wire.Hold {
			rep = d.hold(req.Session, req.Body, held)
		} else {
			rep = d.dispatch(ctx, req)
		}
		if err := wire.WriteReply(c, rep); err != nil {
			return
		}
	}
}

// hold marks the session as held by this connection, and, when the holder
// asks, as one whose state is kept: held maps each session the connection
// holds to whether it asked. The reply to a keeping holder says where the
// state is kept, so a holder knows the daemon keeps it.
func (d *Daemon) hold(name, body string, held map[string]bool) wire.Reply {
	keep := body == wire.KeepState
	if body != "" && !keep {
		return wire.Reply{Failed: true, Body: fmt.Sprintf("a hold carries nothing or %q, not %q", wire.KeepState, body)}
	}
	if keep {
		if err := StateName(name); err != nil {
			return wire.Reply{Failed: true, Body: err.Error()}
		}
	}
	d.mu.Lock()
	sl := d.sessions[name]
	if keep && sl != nil && sl.keeper == nil {
		d.mu.Unlock()
		return wire.Reply{Failed: true, Body: fmt.Sprintf("session %q is open without kept state, so it cannot start keeping it now; close it with `riffle close -s %s -socket %s` and start again", name, name, d.cfg.Socket)}
	}
	if was, ok := held[name]; ok && was != keep {
		d.mu.Unlock()
		return wire.Reply{Failed: true, Body: fmt.Sprintf("this connection already holds session %q, and the second hold asks differently whether to keep its state", name)}
	} else if !ok {
		held[name] = keep
		d.holds[name]++
		if keep {
			d.keeps[name]++
		}
	}
	if sl != nil {
		sl.released = time.Time{}
	}
	d.mu.Unlock()
	if sl != nil && sl.settled() && sl.seat != nil {
		sl.seat.Held(true)
	}
	if keep {
		return wire.Reply{Body: wire.HeldKeepingState + filepath.Join(d.cfg.StateDir, name+".json")}
	}
	return wire.Reply{Body: "held"}
}

// disconnect lets go of the sessions a closing connection held. A session
// whose last holder is gone ends after HoldGrace, unless a holder comes back.
func (d *Daemon) disconnect(held map[string]bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.conns--
	d.active = time.Now()
	for name, keep := range held {
		if keep {
			if d.keeps[name]--; d.keeps[name] == 0 {
				delete(d.keeps, name)
			}
		}
		if d.holds[name]--; d.holds[name] > 0 {
			continue
		}
		delete(d.holds, name)
		if sl := d.sessions[name]; sl != nil {
			sl.released = time.Now()
			if sl.settled() && sl.seat != nil {
				go sl.seat.Held(false)
			}
		}
	}
}

func (d *Daemon) dispatch(ctx context.Context, req wire.Request) wire.Reply {
	switch req.Verb {
	case wire.Close:
		return d.close(ctx, req.Session)
	case wire.Archive:
		return d.archive(ctx, req.Session)
	case wire.Run, wire.View:
	default:
		// handle took only verbs of wire.Verbs, and hold before this.
		panic(fmt.Sprintf("daemon: verb %q is in wire.Verbs but nothing serves it", req.Verb))
	}
	sl, note, err := d.session(ctx, req.Session)
	if err != nil {
		return wire.Reply{Failed: true, Body: err.Error()}
	}
	defer d.done(sl)
	var out session.Outcome
	if req.Verb == wire.Run {
		out = sl.s.Do(ctx, req.Body)
	} else {
		out = sl.s.Do(ctx, strings.TrimSpace("view "+req.Body))
	}
	if note != "" {
		out.Text = "note: " + note + "\n" + out.Text
	}
	// Kept after every request, so a browser that stops loses nothing the
	// last reply showed. The request is still running: no close or reap
	// ends the browser meanwhile.
	if err := sl.keep(ctx); err != nil {
		out.Text += "\nnote: " + err.Error()
	}
	return wire.Reply{Body: out.Text, Stopped: out.Stopped}
}

// close ends the named session now, unless a request is running in it.
func (d *Daemon) close(ctx context.Context, name string) wire.Reply {
	d.mu.Lock()
	sl := d.sessions[name]
	if sl == nil {
		d.mu.Unlock()
		return wire.Reply{Body: fmt.Sprintf("no session %q is open here; nothing to close", name)}
	}
	if sl.busy > 0 {
		d.mu.Unlock()
		return wire.Reply{Failed: true, Body: fmt.Sprintf("session %q is running a program now; close it once that finishes", name)}
	}
	delete(d.sessions, name)
	d.closed[name] = closure{why: sl.lost(noteClosed), at: time.Now()}
	d.mu.Unlock()
	sl.end(ctx)
	return wire.Reply{Body: fmt.Sprintf("closed session %q; its browser has ended", name)}
}

// slot is one named session while it starts and after.
type slot struct {
	used     time.Time     // guarded by Daemon.mu: when a request last ended
	released time.Time     // guarded by Daemon.mu: when its last holder left, zero while held or never held
	ready    chan struct{} // closed when s, seat, err and kept are set
	s        browsing
	err      error
	seat     *quota.Seat
	// keeper keeps the session's state, nil for a session that keeps none.
	keeper *keeper
	busy   int // guarded by Daemon.mu: requests in flight
	// saving orders the saves of requests that end together: each reads the
	// state and keeps it before the next reads, so an older read never
	// overwrites a newer one.
	saving sync.Mutex
	// kept marks a browser started from the keeper's state, whose turn it
	// holds until it ends.
	kept bool
}

// settled reports a slot whose start has finished, successfully or not.
func (sl *slot) settled() bool {
	select {
	case <-sl.ready:
		return true
	default:
		return false
	}
}

// end keeps the slot's state, closes its browser and gives back its seat. A
// browser that already stopped has nothing to keep: what its last request
// left is kept already.
func (sl *slot) end(ctx context.Context) {
	if sl.kept {
		if sl.s.Alive() {
			_ = sl.keep(ctx)
		}
		sl.keeper.release()
	}
	if sl.s != nil {
		sl.s.Close()
	}
	if sl.seat != nil {
		sl.seat.Release()
	}
}

// keep saves the browser's state, if the slot keeps it. A caller that gave
// up still has it saved: what the page did is done either way.
func (sl *slot) keep(ctx context.Context) error {
	if !sl.kept {
		return nil
	}
	sl.saving.Lock()
	defer sl.saving.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveTimeout)
	defer cancel()
	st, err := sl.s.State(ctx)
	if err != nil {
		return fmt.Errorf("could not read this session's state to keep it: %w", err)
	}
	return sl.keeper.save(st, time.Now())
}

// lost is the note for a session whose browser ended, as kept or not.
func (sl *slot) lost(why string) string {
	if sl.keeper != nil {
		return strings.Replace(why, lostPage, keptPage, 1)
	}
	return why
}

// session returns the named session, starting it on first use, and a note
// for the agent when the session it used before was closed. A session whose
// browser stopped is replaced. Browsers start outside the lock so a slow
// launch never blocks other sessions. The caller marks the request done.
func (d *Daemon) session(ctx context.Context, name string) (*slot, string, error) {
	d.mu.Lock()
	sl := d.sessions[name]
	if sl != nil && sl.settled() && sl.busy == 0 && (sl.err != nil || !sl.s.Alive()) {
		delete(d.sessions, name)
		d.closed[name] = closure{why: sl.lost(noteStopped), at: time.Now()}
		d.mu.Unlock()
		sl.end(ctx)
		d.mu.Lock()
		sl = d.sessions[name]
	}
	note := d.closed[name].why
	delete(d.closed, name)
	if sl == nil {
		sl = &slot{ready: make(chan struct{}), used: time.Now()}
		if d.keeps[name] > 0 {
			if d.keepers[name] == nil {
				d.keepers[name] = newKeeper(d.cfg.StateDir, name)
			}
			sl.keeper = d.keepers[name]
		}
		sl.busy++
		d.sessions[name] = sl
		d.mu.Unlock()
		sl.s, sl.seat, sl.kept, sl.err = d.open(ctx, name, sl.keeper)
		close(sl.ready)
		if sl.err != nil {
			d.mu.Lock()
			if d.sessions[name] == sl {
				delete(d.sessions, name)
			}
			sl.busy--
			d.mu.Unlock()
			return nil, "", sl.err
		}
		return sl, note, nil
	}
	sl.busy++
	d.mu.Unlock()
	select {
	case <-sl.ready:
	case <-ctx.Done():
		d.leave(sl)
		return nil, "", ctx.Err()
	}
	if sl.err != nil {
		d.leave(sl)
		return nil, "", sl.err
	}
	return sl, note, nil
}

// done marks a request on a started slot finished.
func (d *Daemon) done(sl *slot) {
	d.leave(sl)
	sl.seat.Used()
}

// leave marks a request on the slot finished, started or not.
func (d *Daemon) leave(sl *slot) {
	d.mu.Lock()
	sl.busy--
	sl.used = time.Now()
	d.active = sl.used
	d.mu.Unlock()
}

// open starts a browser for the session once a seat in the machine's quota
// is free, waiting SeatWait for one. A session that keeps its state starts
// from it, once its earlier browser has saved it; it reports whether it did.
func (d *Daemon) open(ctx context.Context, name string, k *keeper) (browsing, *quota.Seat, bool, error) {
	var st engine.State
	if k != nil {
		var err error
		if st, err = k.restore(ctx.Done(), d.cfg.Policy, time.Now()); err != nil {
			return nil, nil, false, err
		}
	}
	s, seat, err := d.seated(ctx, name, st)
	if err != nil {
		if k != nil {
			k.release()
		}
		return nil, nil, false, err
	}
	return s, seat, k != nil, nil
}

func (d *Daemon) seated(ctx context.Context, name string, st engine.State) (browsing, *quota.Seat, error) {
	wait, cancel := context.WithTimeout(ctx, d.cfg.SeatWait)
	defer cancel()
	seat, err := d.seats.Take(wait, quota.Holder{Session: name, Socket: d.cfg.Socket})
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err() // the caller gave up, not the quota
		}
		return nil, nil, err
	}
	d.mu.Lock()
	held := d.holds[name] > 0
	d.mu.Unlock()
	seat.Held(held)
	s, err := d.launch(ctx, st)
	if err != nil {
		seat.Release()
		return nil, nil, err
	}
	return s, seat, nil
}

func (d *Daemon) start(ctx context.Context, st engine.State) (browsing, error) {
	opts := d.browserOptions()
	if !st.Empty() {
		opts = append(opts, chromium.WithState(st))
	}
	page, err := chromium.Open(ctx, d.cfg.Width, d.cfg.Height, opts...)
	if err != nil {
		return nil, err
	}
	cfg := session.Config{Policy: program.Policy{
		AllowEval:   d.cfg.Policy.AllowEval,
		AllowURL:    d.cfg.Policy.Navigate,
		AllowUpload: d.cfg.Policy.Upload,
		Secrets:     EnvSecrets{},
	}}
	if d.log != nil {
		cfg.Log = d.log
	}
	return session.New(page, cfg), nil
}

// browserOptions applies the policy to a browser: every request is filtered
// as written, and unless private addresses are allowed, every connection is
// checked against the address it is made to.
func (d *Daemon) browserOptions() []chromium.Option {
	opts := []chromium.Option{chromium.WithRequestFilter(d.cfg.Policy.Request), chromium.WithProduct(d.cfg.Product)}
	if !d.cfg.Policy.AllowPrivate {
		opts = append(opts, chromium.WithDialGuard(d.cfg.Policy.Dial))
	}
	return opts
}

// archive returns the named session's current page as MHTML. It never starts
// a browser: a session that is not open has no page to keep.
func (d *Daemon) archive(ctx context.Context, name string) wire.Reply {
	d.mu.Lock()
	sl := d.sessions[name]
	if sl == nil || !sl.settled() || sl.err != nil {
		d.mu.Unlock()
		return wire.Reply{Failed: true, Body: fmt.Sprintf("no session %q is open here, so there is no page to archive; open one with `riffle run -s %s`", name, name)}
	}
	sl.busy++
	d.mu.Unlock()
	defer d.done(sl)
	page, err := sl.s.Archive(ctx)
	if err != nil {
		return wire.Reply{Failed: true, Body: err.Error()}
	}
	return wire.Reply{Body: page}
}
