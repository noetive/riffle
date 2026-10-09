package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	json "github.com/goccy/go-json"
	"github.com/noetive/riffle/internal/engine"
	"github.com/noetive/riffle/internal/userdir"
	"github.com/tidwall/gjson"
)

const (
	// maxSites bounds the sites whose storage is kept: each costs a moment
	// of every start. The ones seen longest ago go first.
	maxSites = 50
	// maxStateFile bounds a state file read back.
	maxStateFile = 32 << 20
	// saveTimeout bounds one save, so a browser that stopped answering
	// cannot hold up a reply or the end of a session.
	saveTimeout = 3 * time.Second
)

// StateName reports whether name may name kept state: it names a file, the
// same on every file system, so it is lower case letters, digits, '-' and
// '_', starts with a letter or digit, and is no name Windows reserves.
func StateName(name string) error {
	ok := name != "" && len(name) <= 64 && strings.IndexFunc(name, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_'
	}) < 0 && name[0] != '-' && name[0] != '_'
	if !ok {
		return fmt.Errorf("%q cannot name kept state: use at most 64 lower case letters, digits, '-' and '_', starting with a letter or digit", name)
	}
	switch name {
	case "con", "prn", "aux", "nul", "com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
		"lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9":
		return fmt.Errorf("%q cannot name kept state: Windows reserves it", name)
	}
	return nil
}

// jar is what a state file holds: the browser's cookies as it reported them,
// and the last storage seen of each site.
type jar struct {
	Storage map[string]siteStorage `json:"storage"`
	Cookies []json.RawMessage      `json:"cookies"`
}

// siteStorage is one site's localStorage and when it last changed.
type siteStorage struct {
	Seen  time.Time   `json:"seen"`
	Items [][2]string `json:"items"`
}

// keeper keeps one session name's state in a file. Its browsers take turns:
// the next one starts from what the last one saved on its way out.
type keeper struct {
	turn chan struct{} // held by the one browser of the name at a time
	path string
	jar  jar
	// withheld are kept cookies the policy kept out of the browser; they
	// are kept on, not lost, when the browser's own are saved.
	withheld []json.RawMessage
	written  []byte // the file as last read or written

	mu sync.Mutex // guards jar, withheld and written
}

func newKeeper(dir, name string) *keeper {
	return &keeper{turn: make(chan struct{}, 1), path: filepath.Join(dir, name+".json")}
}

// restore takes the turn and returns the state to start a browser with:
// what the policy lets in. The caller ends the turn with release.
func (k *keeper) restore(wait <-chan struct{}, p Policy, now time.Time) (engine.State, error) {
	select {
	case k.turn <- struct{}{}:
	case <-wait:
		return engine.State{}, errors.New("gave up waiting for this session's earlier browser to save its state")
	}
	st, err := k.admitted(p, now)
	if err != nil {
		k.release()
	}
	return st, err
}

func (k *keeper) release() { <-k.turn }

func (k *keeper) admitted(p Policy, now time.Time) (engine.State, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	// Read again for every browser: a file deleted since is state forgotten.
	if err := k.load(); err != nil {
		return engine.State{}, err
	}
	var st engine.State
	k.withheld = nil
	for _, c := range k.jar.Cookies {
		if expired(c, now) {
			continue
		}
		if p.Cookie(gjson.GetBytes(c, "domain").String(), gjson.GetBytes(c, "secure").Bool()) != nil {
			k.withheld = append(k.withheld, c)
			continue
		}
		st.Cookies = append(st.Cookies, c)
	}
	for origin, s := range k.jar.Storage {
		if p.Navigate(origin+"/") != nil {
			continue // kept in the file, not given to this browser
		}
		if st.Storage == nil {
			st.Storage = map[string][][2]string{}
		}
		st.Storage[origin] = s.Items
	}
	return st, nil
}

// expired reports a cookie whose expiry has passed. A cookie for the session
// only has none, and is kept: keeping it is the point.
func expired(c json.RawMessage, now time.Time) bool {
	exp := gjson.GetBytes(c, "expires").Float()
	return !gjson.GetBytes(c, "session").Bool() && exp > 0 && exp < float64(now.Unix())
}

func (k *keeper) load() error {
	f, err := os.Open(k.path)
	if errors.Is(err, fs.ErrNotExist) {
		k.jar, k.written = jar{}, nil
		return nil
	}
	if err != nil {
		return k.unreadable(err)
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxStateFile+1))
	if err != nil {
		return k.unreadable(err)
	}
	if len(b) > maxStateFile {
		return k.unreadable(fmt.Errorf("larger than %d MiB", maxStateFile>>20))
	}
	var j jar
	if err := json.Unmarshal(b, &j); err != nil {
		return k.unreadable(err)
	}
	k.jar, k.written = j, b
	return nil
}

func (k *keeper) unreadable(err error) error {
	return fmt.Errorf("the state kept in %s is unreadable (%w); move or delete it to start this session without it", k.path, err)
}

// save keeps the browser's state: its cookies with the withheld ones, and
// the storage of each site it reports, replacing what was kept of that site.
// A file already holding exactly this is not written again.
func (k *keeper) save(st engine.State, now time.Time) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.jar.Cookies = append(slices.Clip(st.Cookies), k.withheld...)
	for origin, items := range st.Storage {
		if old, ok := k.jar.Storage[origin]; ok && slices.Equal(old.Items, items) {
			continue
		}
		if k.jar.Storage == nil {
			k.jar.Storage = map[string]siteStorage{}
		}
		k.jar.Storage[origin] = siteStorage{Seen: now, Items: items}
	}
	for len(k.jar.Storage) > maxSites {
		var oldest string
		for o, s := range k.jar.Storage {
			if oldest == "" || s.Seen.Before(k.jar.Storage[oldest].Seen) {
				oldest = o
			}
		}
		delete(k.jar.Storage, oldest)
	}
	b, err := json.Marshal(k.jar)
	if err != nil {
		return err
	}
	if bytes.Equal(b, k.written) {
		return nil
	}
	if err := write(k.path, b); err != nil {
		return fmt.Errorf("could not keep this session's state in %s: %w", k.path, err)
	}
	k.written = b
	return nil
}

// write replaces the file at path with b, whole or not at all, readable by
// this user only.
func write(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := userdir.Ensure(dir); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".state-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// stateDirOf is where the daemon on socket keeps sessions' state, unless
// told otherwise: beside the socket, one directory for each socket.
func stateDirOf(socket string) string {
	return strings.TrimSuffix(socket, filepath.Ext(socket)) + ".state"
}

// earlierStateDirs are the state directories of the protocols before p,
// newest first.
func earlierStateDirs(p int) []string {
	var dirs []string
	for _, socket := range earlierSockets(p) {
		dirs = append(dirs, stateDirOf(socket))
	}
	return dirs
}

// seedState starts a protocol's state directory, the first time, from the
// newest earlier one there is, so an upgrade keeps what was kept before it.
// From then on each protocol's daemon keeps its own: an earlier Riffle still
// running goes on saving its copy and never overwrites this one, and what it
// saves after the upgrade stays in its copy. The copy is made beside dir and
// renamed into place, so a directory that exists was seeded whole, and one
// that failed partway is seeded again on the next start.
func seedState(dir string, earlier []string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil // seeded before
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("could not read the kept state in %s: %w", dir, err)
	}
	for _, from := range earlier {
		files, err := os.ReadDir(from)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("could not carry the kept state in %s over to %s: %w", from, dir, err)
		}
		if err := userdir.Ensure(filepath.Dir(dir)); err != nil {
			return err
		}
		tmp, err := os.MkdirTemp(filepath.Dir(dir), ".seed-*")
		if err != nil {
			return fmt.Errorf("could not carry the kept state over to %s: %w", dir, err)
		}
		defer func() { _ = os.RemoveAll(tmp) }() // gone once renamed; otherwise a failed copy
		for _, f := range files {
			if f.IsDir() || filepath.Ext(f.Name()) != ".json" || strings.HasPrefix(f.Name(), ".") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(from, f.Name()))
			if errors.Is(err, fs.ErrNotExist) {
				continue // forgotten since it was listed
			}
			if err != nil {
				return fmt.Errorf("could not carry the kept state in %s over to %s: %w; move it away to start without it", filepath.Join(from, f.Name()), dir, err)
			}
			if err := write(filepath.Join(tmp, f.Name()), b); err != nil {
				return fmt.Errorf("could not carry the kept state over to %s: %w", dir, err)
			}
		}
		if err := os.Rename(tmp, dir); err != nil {
			if _, statErr := os.Stat(dir); statErr == nil {
				return nil // another daemon starting at the same time seeded it
			}
			return fmt.Errorf("could not carry the kept state over to %s: %w", dir, err)
		}
		return nil
	}
	return nil
}
