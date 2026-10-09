package daemon

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	json "github.com/goccy/go-json"
	"github.com/noetive/riffle/internal/engine"
	"github.com/noetive/riffle/internal/wire"
	"github.com/tidwall/gjson"
)

// ranState is what a fake browser keeps after running src: a session cookie
// and a stored item, both saying what ran.
func ranState(src string) engine.State {
	c, _ := json.Marshal(map[string]any{"name": "last", "value": src, "domain": "shop.example", "path": "/", "expires": -1, "session": true})
	return engine.State{Cookies: []json.RawMessage{c}, Storage: map[string][][2]string{"https://shop.example": {{"last", src}}}}
}

func lastRan(st engine.State) string {
	for _, c := range st.Cookies {
		if gjson.GetBytes(c, "name").String() == "last" {
			return gjson.GetBytes(c, "value").String()
		}
	}
	return ""
}

func keeping(t *testing.T, c *Client, name string) (*Client, string) {
	t.Helper()
	holder := NewClient(c.socket).Hold(name).KeepState()
	t.Cleanup(holder.Close)
	path, err := holder.Kept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return holder, path
}

func runProgram(t *testing.T, c *Client, session, src string) string {
	t.Helper()
	out, err := c.Do(context.Background(), wire.Request{Verb: wire.Run, Session: session, Body: src})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The promise: what a kept session had is there again in a later Riffle.
func TestAKeptSessionStartsInALaterDaemonFromWhatItsLastBrowserHad(t *testing.T) {
	states := t.TempDir()
	c, _, _ := serving(t, Config{StateDir: states, SessionIdle: time.Hour, DaemonIdle: time.Hour})
	holder, path := keeping(t, c, "shop")
	if filepath.Dir(path) != states {
		t.Errorf("the state is kept where the daemon was told: %s", path)
	}
	runProgram(t, holder, "shop", "sign in")
	holder.Close()

	later, live, _ := serving(t, Config{StateDir: states, SessionIdle: time.Hour, DaemonIdle: time.Hour})
	again, _ := keeping(t, later, "shop")
	runProgram(t, again, "shop", "view")
	if len(live.restored) != 1 || lastRan(live.restored[0]) != "sign in" {
		t.Errorf("the later browser starts from the kept state: %v", live.restored)
	}
	if got := live.restored[0].Storage["https://shop.example"]; len(got) != 1 || got[0][1] != "sign in" {
		t.Errorf("the site's storage is restored too: %v", live.restored[0].Storage)
	}
}

func TestStateIsKeptAfterEveryRequestNotOnlyAtTheEnd(t *testing.T) {
	c, _, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour})
	holder, path := keeping(t, c, "shop")
	runProgram(t, holder, "shop", "add to cart")
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "add to cart") {
		t.Fatalf("the file holds the state while the browser still runs: %q %v", b, err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("only this user may read the kept state: %v %v", fi.Mode(), err)
	}
}

func TestASessionThatKeepsNoStateLeavesNoFile(t *testing.T) {
	states := t.TempDir()
	c, _, _ := serving(t, Config{StateDir: states, SessionIdle: time.Hour, DaemonIdle: time.Hour})
	holder := NewClient(c.socket).Hold("plain")
	t.Cleanup(holder.Close)
	runProgram(t, holder, "plain", "sign in")
	runProgram(t, c, "cli", "sign in")
	if entries, _ := os.ReadDir(states); len(entries) != 0 {
		t.Errorf("nothing is kept for a session that did not ask: %v", entries)
	}
}

func TestAClosedKeptSessionSaysItsStateIsKept(t *testing.T) {
	c, live, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour})
	holder, _ := keeping(t, c, "shop")
	runProgram(t, holder, "shop", "sign in")
	if _, err := c.Do(context.Background(), wire.Request{Verb: wire.Close, Session: "shop"}); err != nil {
		t.Fatal(err)
	}
	out := runProgram(t, holder, "shop", "view")
	if !strings.Contains(out, "cookies and storage kept") || strings.Contains(out, "sign-ins are gone") {
		t.Errorf("the note tells a kept session it kept its state: %q", out)
	}
	if n := len(live.restored); n != 2 || lastRan(live.restored[1]) != "sign in" {
		t.Errorf("the browser after the close starts from what the closed one had: %v", live.restored)
	}
}

func TestAStateThatCannotBeReadIsReportedNotSaved(t *testing.T) {
	c, live, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour})
	holder, path := keeping(t, c, "shop")
	runProgram(t, holder, "shop", "sign in")
	live.mu.Lock()
	live.unreadable = true
	live.mu.Unlock()
	out := runProgram(t, holder, "shop", "sign out")
	if !strings.Contains(out, "did sign out") || !strings.Contains(out, "note: could not read this session's state") {
		t.Errorf("the reply still comes, with a note that the state was not kept: %q", out)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "sign in") {
		t.Errorf("what was kept before is still kept: %q", b)
	}
}

func TestAKeptStateFileThatIsUnreadableStopsTheSessionAndSaysWhatToDo(t *testing.T) {
	states := t.TempDir()
	if err := os.WriteFile(filepath.Join(states, "shop.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, live, _ := serving(t, Config{StateDir: states, SessionIdle: time.Hour, DaemonIdle: time.Hour})
	holder, _ := keeping(t, c, "shop")
	_, err := holder.Do(context.Background(), wire.Request{Verb: wire.Run, Session: "shop", Body: "x"})
	if err == nil || !strings.Contains(err.Error(), "unreadable") || !strings.Contains(err.Error(), "move or delete it") {
		t.Errorf("the refusal names the file and what to do: %v", err)
	}
	if live.get() != 0 {
		t.Error("no browser starts without the state it was to keep")
	}
}

func TestASessionOpenWithoutKeptStateCannotStartKeepingIt(t *testing.T) {
	c, _, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour})
	runProgram(t, c, "shop", "browse")
	_, err := NewClient(c.socket).Hold("shop").KeepState().Kept(context.Background())
	if err == nil || !strings.Contains(err.Error(), "open without kept state") || !strings.Contains(err.Error(), "riffle close -s shop") {
		t.Errorf("keeping is refused, saying how to start over: %v", err)
	}
}

func TestOnlyAPlainFileNameCanNameKeptState(t *testing.T) {
	for _, bad := range []string{"", "Shop", "../shop", "a/b", "-shop", "_x", "con", "lpt1", strings.Repeat("a", 65), "shop.json"} {
		if StateName(bad) == nil {
			t.Errorf("%q is refused", bad)
		}
	}
	for _, good := range []string{"shop", "a", "work-2", "x_y", strings.Repeat("a", 64)} {
		if err := StateName(good); err != nil {
			t.Errorf("%q is allowed: %v", good, err)
		}
	}
	c, _, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour})
	if _, err := NewClient(c.socket).Hold("Shop").KeepState().Kept(context.Background()); err == nil {
		t.Error("the daemon refuses a bad name too, whoever sends it")
	}
}

func TestADaemonThatDoesNotKeepStateIsRefused(t *testing.T) {
	old := newFakeServer(t, func(wire.Request) wire.Reply { return wire.Reply{Body: "held"} }, 0)
	_, err := NewClient(old.sock).Hold("shop").KeepState().Kept(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cannot keep a session's state") {
		t.Errorf("a daemon that only holds is not taken to keep state: %v", err)
	}
}

// The next browser of a name waits for the last one to save, so it never
// starts from state older than the last one left.
func TestTheNextBrowserOfANameWaitsForTheLastToSave(t *testing.T) {
	k := newKeeper(t.TempDir(), "shop")
	now := time.Now()
	if _, err := k.restore(nil, Policy{}, now); err != nil {
		t.Fatal(err)
	}
	next := make(chan engine.State, 1)
	go func() {
		st, err := k.restore(nil, Policy{}, now)
		if err != nil {
			t.Error(err)
		}
		next <- st
	}()
	select {
	case <-next:
		t.Fatal("the next browser started while the last still ran")
	case <-time.After(100 * time.Millisecond):
	}
	if err := k.save(ranState("checked out"), now); err != nil {
		t.Fatal(err)
	}
	k.release()
	if st := <-next; lastRan(st) != "checked out" {
		t.Errorf("the next browser starts from what the last saved: %v", st)
	}
}

func TestWaitingForTheTurnEndsWhenTheCallerGivesUp(t *testing.T) {
	k := newKeeper(t.TempDir(), "shop")
	if _, err := k.restore(nil, Policy{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	gaveUp := make(chan struct{})
	close(gaveUp)
	if _, err := k.restore(gaveUp, Policy{}, time.Now()); err == nil {
		t.Error("a caller that gave up is not kept waiting")
	}
}

func cookie(name, domain string, expires float64, secure bool) json.RawMessage {
	c, _ := json.Marshal(map[string]any{"name": name, "value": "v", "domain": domain, "path": "/", "expires": expires, "session": expires < 0, "secure": secure})
	return c
}

// What the policy keeps out of the browser is not given to it, and is not
// lost either: it is there again for a browser the policy lets have it.
func TestStateThePolicyKeepsOutIsWithheldNotLost(t *testing.T) {
	dir := t.TempDir()
	k := newKeeper(dir, "shop")
	now := time.Now()
	if _, err := k.restore(nil, Policy{AllowPrivate: true}, now); err != nil {
		t.Fatal(err)
	}
	both := engine.State{
		Cookies: []json.RawMessage{cookie("pub", "shop.example", -1, true), cookie("lan", "127.0.0.1", -1, false)},
		Storage: map[string][][2]string{"https://shop.example": {{"a", "1"}}, "http://127.0.0.1:8080": {{"b", "2"}}},
	}
	if err := k.save(both, now); err != nil {
		t.Fatal(err)
	}
	k.release()

	k = newKeeper(dir, "shop")
	st, err := k.restore(nil, Policy{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Cookies) != 1 || gjson.GetBytes(st.Cookies[0], "name").String() != "pub" {
		t.Errorf("a cookie for a private address is withheld: %s", st.Cookies)
	}
	if _, ok := st.Storage["http://127.0.0.1:8080"]; ok || len(st.Storage) != 1 {
		t.Errorf("storage of a private address is withheld: %v", st.Storage)
	}
	// The browser had only what it was given; saving that keeps the rest.
	if err := k.save(st, now); err != nil {
		t.Fatal(err)
	}
	k.release()
	b, _ := os.ReadFile(k.path)
	if !strings.Contains(string(b), `"lan"`) || !strings.Contains(string(b), "127.0.0.1:8080") {
		t.Errorf("the withheld state is still kept: %s", b)
	}
}

func TestAnExpiredCookieIsNotRestored(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	k := newKeeper(dir, "shop")
	if _, err := k.restore(nil, Policy{}, now); err != nil {
		t.Fatal(err)
	}
	past, future := float64(now.Add(-time.Hour).Unix()), float64(now.Add(time.Hour).Unix())
	if err := k.save(engine.State{Cookies: []json.RawMessage{cookie("old", "shop.example", past, true), cookie("new", "shop.example", future, true), cookie("tab", "shop.example", -1, true)}}, now); err != nil {
		t.Fatal(err)
	}
	k.release()
	st, err := newKeeper(dir, "shop").restore(nil, Policy{}, now)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range st.Cookies {
		names = append(names, gjson.GetBytes(c, "name").String())
	}
	if strings.Join(names, ",") != "new,tab" {
		t.Errorf("the expired cookie is left out and the session cookie kept: %v", names)
	}
}

func TestASiteThatClearedItsStorageStaysCleared(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	k := newKeeper(dir, "shop")
	if _, err := k.restore(nil, Policy{}, now); err != nil {
		t.Fatal(err)
	}
	_ = k.save(engine.State{Storage: map[string][][2]string{"https://shop.example": {{"token", "t"}}}}, now)
	_ = k.save(engine.State{Storage: map[string][][2]string{"https://shop.example": {}}}, now)
	k.release()
	st, _ := newKeeper(dir, "shop").restore(nil, Policy{}, now)
	if got, ok := st.Storage["https://shop.example"]; !ok || len(got) != 0 {
		t.Errorf("signing out of a site is kept: %v %v", got, ok)
	}
}

func TestOnlyTheSitesSeenLatestAreKept(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	k := newKeeper(dir, "shop")
	if _, err := k.restore(nil, Policy{}, start); err != nil {
		t.Fatal(err)
	}
	for i := range maxSites + 1 {
		site := "https://s" + strconv.Itoa(i) + ".example"
		if err := k.save(engine.State{Storage: map[string][][2]string{site: {{"k", "v"}}}}, start.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	k.release()
	st, _ := newKeeper(dir, "shop").restore(nil, Policy{}, start)
	if len(st.Storage) != maxSites {
		t.Errorf("%d sites kept, want %d", len(st.Storage), maxSites)
	}
	if _, ok := st.Storage["https://s0.example"]; ok {
		t.Error("the site seen longest ago goes first")
	}
	if _, ok := st.Storage["https://s"+strconv.Itoa(maxSites)+".example"]; !ok {
		t.Error("the site seen last is kept")
	}
}

func TestAStateUnchangedIsNotWrittenAgain(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	k := newKeeper(dir, "shop")
	if _, err := k.restore(nil, Policy{}, now); err != nil {
		t.Fatal(err)
	}
	if err := k.save(ranState("one"), now); err != nil {
		t.Fatal(err)
	}
	long := now.Add(-time.Hour)
	if err := os.Chtimes(k.path, long, long); err != nil {
		t.Fatal(err)
	}
	if err := k.save(ranState("one"), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(k.path); !fi.ModTime().Equal(long) {
		t.Error("the same state is not written again")
	}
}

func TestAStateFileTooLargeIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shop.json")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxStateFile + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if _, err := newKeeper(dir, "shop").restore(nil, Policy{}, time.Now()); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("an oversized state file is refused: %v", err)
	}
}

// FuzzStateFile reads arbitrary bytes as a state file: it is read or refused,
// never a crash.
func FuzzStateFile(f *testing.F) {
	f.Add([]byte(`{"cookies":[{"name":"a","domain":"x.example","expires":-1,"session":true}],"storage":{"https://x.example":{"seen":"2026-01-01T00:00:00Z","items":[["k","v"]]}}}`))
	f.Add([]byte(`{"cookies":[1,"a",null,{"domain":5}],"storage":{"javascript:x":{"items":[["k"]]}}}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, b []byte) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "x.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
		k := newKeeper(dir, "x")
		if _, err := k.restore(nil, Policy{}, time.Now()); err == nil {
			_ = k.save(engine.State{}, time.Now())
			k.release()
		}
	})
}

// Deleting the file forgets the state, even while the daemon that kept it
// still runs.
func TestDeletingTheStateFileForgetsItWithoutAStopOfTheDaemon(t *testing.T) {
	c, live, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour})
	holder, path := keeping(t, c, "shop")
	runProgram(t, holder, "shop", "sign in")
	if _, err := c.Do(context.Background(), wire.Request{Verb: wire.Close, Session: "shop"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	runProgram(t, holder, "shop", "view")
	if n := len(live.restored); n != 2 || !live.restored[1].Empty() {
		t.Errorf("the browser after the file was deleted starts with nothing: %v", live.restored)
	}
}

func TestAKeptCookieReachesOnlyTheOriginsASessionIsHeldTo(t *testing.T) {
	p := Policy{Origins: []string{"https://www.shop.example"}}
	for domain, ok := range map[string]bool{
		"www.shop.example": true, ".shop.example": true, "shop.example": true,
		"bank.example": false, "evil-shop.example": false, "pay.shop.example": false, "127.0.0.1": false,
	} {
		if err := p.Cookie(domain, true); (err == nil) != ok {
			t.Errorf("a cookie for %s given to a session held to www.shop.example: %v, want allowed=%v", domain, err, ok)
		}
	}
	if err := (Policy{}).Cookie("bank.example", false); err != nil {
		t.Errorf("without an origin list any public site's cookie is kept: %v", err)
	}
}

func TestAConnectionCannotHoldASessionBothWays(t *testing.T) {
	c, _, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour})
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", c.socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	ask := func(body string) wire.Reply {
		if err := wire.WriteRequest(conn, wire.Request{Verb: wire.Hold, Session: "shop", Body: body}); err != nil {
			t.Fatal(err)
		}
		rep, err := wire.ReadReply(br)
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	if rep := ask(""); rep.Failed {
		t.Fatal(rep.Body)
	}
	if rep := ask(wire.KeepState); !rep.Failed || strings.HasPrefix(rep.Body, wire.HeldKeepingState) {
		t.Errorf("a second hold that now asks to keep state is refused, not told it is kept: %+v", rep)
	}
	if rep := ask("anything"); !rep.Failed {
		t.Errorf("a hold carrying anything else is refused: %+v", rep)
	}
}

// Once the servers that kept a session's state are gone, the name is just a
// name: a later session of it keeps nothing and starts from nothing.
func TestASessionStartedAfterItsKeepersLeftKeepsNothing(t *testing.T) {
	c, live, _ := serving(t, Config{SessionIdle: time.Hour, DaemonIdle: time.Hour, HoldGrace: 50 * time.Millisecond})
	holder, path := keeping(t, c, "shop")
	runProgram(t, holder, "shop", "sign in")
	holder.Close()
	eventually(t, "the kept session ends with its holder", func() bool { return live.get() == 0 })
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	runProgram(t, c, "shop", "browse")
	if n := len(live.restored); n != 2 || !live.restored[1].Empty() {
		t.Errorf("the later session starts from nothing: %v", live.restored)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Errorf("the later session writes nothing over the kept state: %s", after)
	}
}

// A new protocol's daemon starts from what the newest earlier one kept, once;
// from then on each keeps its own, so an earlier Riffle still running never
// overwrites what the new one keeps.
func TestANewProtocolStartsFromTheNewestEarlierStateOnce(t *testing.T) {
	root := t.TempDir()
	oldest, older, dir := filepath.Join(root, "p2.state"), filepath.Join(root, "p3.state"), filepath.Join(root, "p4.state")
	put := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put(filepath.Join(oldest, "mail.json"), "oldest")
	put(filepath.Join(older, "shop.json"), "signed in")
	put(filepath.Join(older, ".state-123"), "a save in progress")
	put(filepath.Join(older, ".hidden.json"), "not state")
	put(filepath.Join(older, "dir.json", "x"), "not state")

	if err := seedState(dir, []string{filepath.Join(root, "missing"), older, oldest}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "shop.json")); err != nil || string(b) != "signed in" {
		t.Errorf("the newest earlier state is carried over: %q %v", b, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "shop.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("only this user may read the carried state: %v", err)
	}
	for _, name := range []string{"mail.json", ".state-123", ".hidden.json", "dir.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s is carried over, but only the newest earlier directory's kept state is: %v", name, err)
		}
	}

	put(filepath.Join(older, "shop.json"), "signed out by the earlier Riffle")
	if err := seedState(dir, []string{older}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "shop.json")); string(b) != "signed in" {
		t.Errorf("an earlier Riffle's later saves never overwrite the new protocol's state: %q", b)
	}
}

func TestAProtocolWithNothingEarlierStartsEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p2.state")
	if err := seedState(dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("nothing to carry over leaves nothing behind: %v", err)
	}
}

// A copy that fails leaves nothing that looks seeded, so the next start
// carries everything over once what was in the way is moved.
func TestASeedThatFailsIsTriedAgainWhole(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}
	root := t.TempDir()
	earlier, dir := filepath.Join(root, "p2.state"), filepath.Join(root, "p3.state")
	if err := os.MkdirAll(earlier, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"mail.json": "mail", "shop.json": "shop"} {
		if err := os.WriteFile(filepath.Join(earlier, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(earlier, "shop.json"), 0); err != nil {
		t.Fatal(err)
	}
	if err := seedState(dir, []string{earlier}); err == nil || !strings.Contains(err.Error(), "move it away") {
		t.Fatalf("an unreadable earlier file stops the seed and says what to do: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("a failed seed left %s looking seeded: %v", dir, err)
	}
	if err := os.Rename(filepath.Join(earlier, "shop.json"), filepath.Join(root, "shop.json")); err != nil {
		t.Fatal(err)
	}
	if err := seedState(dir, []string{earlier}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "mail.json")); err != nil || string(b) != "mail" {
		t.Errorf("the seed tried again carries the rest over: %q %v", b, err)
	}
	if left, _ := filepath.Glob(filepath.Join(root, ".seed-*")); len(left) != 0 {
		t.Errorf("a failed seed leaves its copy behind: %v", left)
	}
}

// Each socket keeps state beside it, and an earlier protocol's default
// daemon kept its state beside its own socket.
func TestStateIsKeptBesideItsSocket(t *testing.T) {
	privateCache(t)
	other := filepath.Join(t.TempDir(), "mine.sock")
	d, err := New(Config{Socket: other, QuotaDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(filepath.Dir(other), "mine.state"); d.cfg.StateDir != want {
		t.Errorf("state kept in %s, want %s", d.cfg.StateDir, want)
	}
	if got, want := earlierStateDirs(3), []string{filepath.Join(filepath.Dir(socketFor(2)), "riffle.state")}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("protocol 3 seeds from %v, want %v", got, want)
	}
	if got := earlierStateDirs(2); len(got) != 0 {
		t.Errorf("protocol 2 has nothing earlier to seed from: %v", got)
	}
}

// What stops a seed is an error with the place it happened, never a quiet
// start without the kept state.
func TestASeedThatCannotReadOrWriteSaysWhere(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads and writes whatever the mode")
	}
	root := t.TempDir()
	notADir := filepath.Join(root, "p2.state")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedState(filepath.Join(root, "p3.state"), []string{notADir}); err == nil || !strings.Contains(err.Error(), notADir) {
		t.Errorf("an earlier state that is not a directory is named: %v", err)
	}

	earlier := filepath.Join(root, "e")
	if err := os.MkdirAll(earlier, 0o700); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(root, "shared")
	if err := os.MkdirAll(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := seedState(filepath.Join(shared, "p3.state"), []string{earlier}); err == nil {
		t.Error("state is never seeded into a directory others can write to")
	}

	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(locked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if err := seedState(filepath.Join(locked, "p3.state"), []string{earlier}); err == nil || !strings.Contains(err.Error(), "p3.state") {
		t.Errorf("a state directory that cannot be looked at is named: %v", err)
	}
	if err := seedState(filepath.Join(locked, "p3.state"), nil); err == nil {
		t.Error("a state directory that cannot be looked at is an error even with nothing to carry over")
	}
}
