// Package chromium drives stock Chrome over the DevTools protocol on a pipe.
// It never requests frames: no screenshots, no screencast.
package chromium

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

// Browser is one Chrome process. Each Page is its own target.
type Browser struct {
	cmd    *exec.Cmd
	conn   *conn
	exited chan struct{}
	dir    string
	// userAgent is what Chrome sends when nothing overrides it.
	userAgent string
}

// FindChrome locates a Chrome or Chromium binary: RIFFLE_CHROME, then the
// usual install locations, then PATH.
func FindChrome() (string, error) {
	if p := os.Getenv("RIFFLE_CHROME"); p != "" {
		return p, nil
	}
	for _, p := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	for _, n := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", errors.New("chromium: no Chrome found; install Chrome or set RIFFLE_CHROME")
}

// Launch starts Chrome headless with the DevTools pipe. Extra flags go
// before the first page.
func Launch(ctx context.Context, extra ...string) (*Browser, error) {
	sweepOnce.Do(SweepStale)
	bin, err := FindChrome()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "riffle-chrome-")
	if err != nil {
		return nil, fmt.Errorf("chromium: profile dir: %w", err)
	}
	// Chrome reads commands from fd 3 and writes replies to fd 4.
	cr, cw, err := os.Pipe() // chrome reads (fd3), we write
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("chromium: pipe: %w", err)
	}
	rr, rw, err := os.Pipe() // chrome writes (fd4), we read
	if err != nil {
		_ = os.RemoveAll(dir)
		_ = cr.Close()
		_ = cw.Close()
		return nil, fmt.Errorf("chromium: pipe: %w", err)
	}
	// The browser outlives the launch call; Close ends it.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), bin, launchArgs(filepath.Join(dir, "profile"), extra...)...)
	ownGroup(cmd)
	cmd.ExtraFiles = []*os.File{cr, rw}
	cmd.Env = browserEnvironment(os.Environ())
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		for _, f := range []*os.File{cr, cw, rr, rw} {
			_ = f.Close()
		}
		return nil, fmt.Errorf("chromium: start %s: %w", bin, err)
	}
	claim(dir, cmd.Process.Pid)
	_ = cr.Close()
	_ = rw.Close()
	b := &Browser{cmd: cmd, conn: newConn(rr, cw), dir: dir, exited: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(b.exited)
		b.conn.fail(errors.New("chrome exited"))
		_ = cw.Close()
		_ = rr.Close()
	}()
	res, err := b.conn.call(ctx, "", "Browser.getVersion", nil)
	if err != nil {
		b.Close()
		return nil, fmt.Errorf("chromium: handshake: %w", err)
	}
	b.userAgent = gjson.GetBytes(res, "userAgent").String()
	return b, nil
}

// browserEnvironment is the environment Chrome inherits: everything except
// the daemon's secrets, which no browser process has a use for.
func browserEnvironment(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, "RIFFLE_SECRET_") {
			out = append(out, kv)
		}
	}
	return out
}

// Alive reports whether the browser process is still running.
func (b *Browser) Alive() bool {
	select {
	case <-b.exited:
		return false
	default:
		return true
	}
}

// Close terminates Chrome and removes its profile.
func (b *Browser) Close() {
	if b.cmd.Process != nil {
		killTree(b.cmd.Process.Pid)
		_ = b.cmd.Process.Kill()
	}
	select {
	case <-b.exited:
	case <-time.After(5 * time.Second):
	}
	_ = os.RemoveAll(b.dir)
}

// NewPage opens a blank tab attached with a flattened session.
func (b *Browser) NewPage(ctx context.Context, width, height int) (*Page, error) {
	res, err := b.conn.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"})
	if err != nil {
		return nil, err
	}
	targetID := gjson.GetBytes(res, "targetId").String()
	res, err = b.conn.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true})
	if err != nil {
		return nil, err
	}
	p := newPage(ctx, b.conn, gjson.GetBytes(res, "sessionId").String(), targetID)
	if err := p.init(ctx, width, height); err != nil {
		return nil, err
	}
	return p, nil
}

var sweepOnce sync.Once

// launchArgs is the browser's command line. The profile is a throwaway, so
// the browser is kept from the user's keychain and password store: a prompt
// from either would be for secrets this browser never holds, and nobody is
// there to answer it.
func launchArgs(profile string, extra ...string) []string {
	args := []string{
		"--headless=new",
		"--remote-debugging-pipe",
		"--user-data-dir=" + profile,
		"--use-mock-keychain",
		"--password-store=basic",
		// A page restored from the back-forward cache fires no load event,
		// so going back would wait for one that never comes.
		"--disable-features=BackForwardCache",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
		"--disable-extensions",
		"--disable-component-update",
		// Chrome wakes its updater after a while; the updater runs outside the
		// browser's process group, so it would outlive the browser and call home
		// for a session that asked for no traffic of its own.
		"--disable-updater-scheduler",
		"--disable-sync",
		"--mute-audio",
		"--hide-scrollbars",
		"--force-color-profile=srgb",
	}
	return append(append(args, extra...), "about:blank")
}
