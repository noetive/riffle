// Command riffle is the browser for agents: a daemon, a CLI and an MCP server.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/noetive/riffle/internal/daemon"
	"github.com/noetive/riffle/internal/mcpserver"
	"github.com/noetive/riffle/internal/program"
	"github.com/noetive/riffle/internal/userdir"
	"github.com/noetive/riffle/internal/wire"
)

// version is stamped at release time with -X, which only reaches a variable
// initialised to a constant. A `go install` build is not stamped, so init
// reports the module version Go recorded instead.
var version = "dev"

func init() { version = stampedOr(moduleVersion(), version) }

// stampedOr is the release-stamped version, else the module version without
// its "v", else dev. A build from a git checkout records a pseudo-version of
// its commit; one with no version control records "(devel)", not a version.
func stampedOr(module, stamped string) string {
	if stamped != "dev" || !strings.HasPrefix(module, "v") {
		return stamped
	}
	return strings.TrimPrefix(module, "v")
}

func moduleVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return bi.Main.Version
}

// errStopped reports a program that stopped at a failed step. Its reply is
// already on stdout, so it carries no message of its own.
var errStopped = errors.New("program stopped at a failed step")

// exitStopped is the exit status of a program that stopped at a failed step,
// apart from 1, which means Riffle itself could not do what was asked.
const exitStopped = 3

const usage = `riffle: the browser for agents

  riffle serve [-socket PATH] [POLICY] [LIMITS]             the daemon, in the foreground
  riffle run [-s NAME] [-socket PATH] [POLICY]              program on stdin, reply on stdout
  riffle view [-s NAME] [-socket PATH] [POLICY] [PROJECTION...]
                                                            read the page
  riffle close [-s NAME] [-socket PATH]                     end a session and its browser
  riffle stop [-socket PATH]                                end the daemon and every session in it
  riffle archive [-s NAME] [-socket PATH]                   the current page as MHTML on stdout
  riffle mcp [-s NAME | -keep-state NAME] [-socket PATH] [POLICY]
                                                            MCP server on stdio, with a session of its own
  riffle grammar                                            print the program language guide
  riffle doctor                                             check this machine can run Riffle
  riffle version

POLICY limits what pages a session may reach. Without it, sessions reach
public web addresses only, with no uploads and no eval:

  -allow-private      also loopback, link-local and private addresses, such as an app on localhost
  -allow-origin O     documents only from origin O, scheme://host[:port]; repeatable
  -upload-dir DIR     files may be uploaded from DIR
  -allow-eval         allow the eval statement

LIMITS keep browsers from piling up:

  -max-browsers N     browsers running at once on this machine, across every
                      daemon of this user (default 4; with several daemons the
                      largest value applies); a new session waits 30s for one
                      to end, then is refused saying which sessions hold them
  -session-idle D     end a session unused for D, unless an MCP server holds it
                      (default 15m); its next use starts on a blank page and says so
  -daemon-idle D      end the daemon after D with no session and no client
                      (default 30m)

An MCP server holds its session: it ends when the server exits. Each server
has a session of its own, named on its stderr; servers given the same -s NAME
share one browser page.

With -keep-state NAME the server drives session NAME, and its cookies and
storage are kept between runs, until the file it names on stderr is deleted.

Without POLICY, a command uses the running daemon whatever its policy, or
starts one with the policy the last one had. Naming any POLICY flag gives the
whole policy and turns off those left out; -allow-private=false alone asks
for the default. With POLICY named, run, view and mcp refuse a daemon running
with another, and serve stops it, ending its sessions, and takes its place.

riffle run and riffle view exit 3 when a step failed or did not parse, with the
reply on stdout saying which and why, and 1 when Riffle could not run it at all.`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals...)
	defer stop()
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(ctx, os.Stdout, os.Args[2:])
	case "run":
		err = client(ctx, wire.Run, os.Args[2:])
	case "view":
		err = client(ctx, wire.View, os.Args[2:])
	case "close":
		err = closeSession(ctx, os.Args[2:])
	case "stop":
		err = stopDaemon(ctx, os.Stdout, os.Args[2:])
	case "archive":
		err = archive(ctx, os.Args[2:])
	case "grammar":
		fmt.Println(program.Grammar())
	case "doctor":
		err = doctor(ctx)
	case "version", "-v", "--version":
		fmt.Println("riffle", version)
	case "mcp":
		err = mcpMain(ctx, os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if errors.Is(err, errStopped) {
		os.Exit(exitStopped)
	}
	if err != nil {
		// Errors from the daemon's client already say they are Riffle's.
		fmt.Fprintln(os.Stderr, "riffle:", strings.TrimPrefix(err.Error(), "riffle: "))
		os.Exit(1)
	}
}

// limitFlags are serve's flags that only a daemon that starts takes.
var limitFlags = []string{"audit", "width", "height", "max-browsers", "session-idle", "daemon-idle"}

// replaceAttempts bounds how often serve takes the place of a daemon that
// is started again with another policy while it does.
const replaceAttempts = 3

// serve runs the daemon on its socket. Naming a policy declares it: a daemon
// running there with another is stopped and this one takes its place, unless
// -start-only, which a client that starts a daemon passes so that it never
// ends another's sessions. Naming none leaves a running daemon as it is, and
// starts one with the policy the socket last ran with.
func serve(ctx context.Context, w io.Writer, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfg := daemon.Config{Width: 1280, Height: 800, Product: "Riffle/" + version}
	fs.StringVar(&cfg.Socket, "socket", daemon.DefaultSocket(), "Unix socket path")
	audit := fs.String("audit", "", "session log file (default: next to the socket)")
	fs.IntVar(&cfg.Width, "width", cfg.Width, "viewport width")
	fs.IntVar(&cfg.Height, "height", cfg.Height, "viewport height")
	fs.IntVar(&cfg.MaxBrowsers, "max-browsers", daemon.DefaultMaxBrowsers, "browsers running at once on this machine, across every daemon of this user")
	fs.DurationVar(&cfg.SessionIdle, "session-idle", daemon.DefaultSessionIdle, "end a session unused this long, unless an MCP server holds it")
	fs.DurationVar(&cfg.DaemonIdle, "daemon-idle", daemon.DefaultDaemonIdle, "end the daemon after this long with no session and no client")
	startOnly := fs.Bool("start-only", false, "start a daemon only if none serves the socket; never take another's place")
	declared := cfg.Policy.Flags(fs)
	_ = fs.Parse(args)
	cfg.AuditPath = *audit
	if cfg.MaxBrowsers < 1 || cfg.SessionIdle <= 0 || cfg.DaemonIdle <= 0 {
		return fmt.Errorf("serve: -max-browsers must be at least 1 and the idle times above zero")
	}
	if cfg.AuditPath == "" {
		cfg.AuditPath = filepath.Join(filepath.Dir(cfg.Socket), "audit.log")
	}
	if err := userdir.Ensure(filepath.Dir(cfg.Socket)); err != nil {
		return fmt.Errorf("socket directory: %w", err)
	}
	cfg.KeepPolicy = !declared()
	d, err := daemon.New(cfg)
	if err != nil {
		return err
	}
	for attempt := 1; ; attempt++ {
		err := d.Serve(ctx)
		var running *daemon.Serving
		if !errors.As(err, &running) {
			return err
		}
		if *startOnly {
			return nil
		}
		if !declared() || running.Runs(cfg.Policy) {
			return leaveRunning(w, fs, running, declared())
		}
		if attempt == replaceAttempts {
			return fmt.Errorf("serve: the daemon on %s was started again with %s while this one was taking its place; an MCP entry or another riffle command asks for that policy", cfg.Socket, running.Policy)
		}
		out, found, err := daemon.Replace(ctx, cfg.Socket, cfg.Policy)
		if err != nil {
			return err
		}
		if found {
			fmt.Fprintln(os.Stderr, "riffle serve:", out)
		}
	}
}

// leaveRunning reports a daemon serve leaves as it is. Limits it was given
// would not reach that daemon, so naming one is an error rather than ignored.
func leaveRunning(w io.Writer, fs *flag.FlagSet, running *daemon.Serving, declared bool) error {
	var limits []string
	fs.Visit(func(f *flag.Flag) {
		if slices.Contains(limitFlags, f.Name) {
			limits = append(limits, "-"+f.Name)
		}
	})
	if len(limits) > 0 {
		return fmt.Errorf("serve: %w; %s take effect only when a daemon starts: stop it with `%s` and run this again", running, strings.Join(limits, ", "), daemon.StopCommand(running.Socket))
	}
	if declared {
		_, err := fmt.Fprintln(w, running)
		return err
	}
	_, err := fmt.Fprintf(w, "%v; riffle serve with a POLICY named takes its place, ending its sessions, and `%s` stops it\n", running, daemon.StopCommand(running.Socket))
	return err
}

func client(ctx context.Context, verb string, args []string) error {
	fs := flag.NewFlagSet(verb, flag.ExitOnError)
	sock := fs.String("socket", daemon.DefaultSocket(), "Unix socket path")
	name := fs.String("s", "default", "session name")
	var policy daemon.Policy
	declared := policy.Flags(fs)
	_ = fs.Parse(args)
	body := strings.Join(fs.Args(), " ")
	if verb == wire.Run {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read program: %w", err)
		}
		body = string(b)
	}
	c := newClient(*sock, policy, declared())
	defer c.Close()
	rep, err := c.Exchange(ctx, wire.Request{Verb: verb, Session: *name, Body: body})
	if err != nil {
		return err
	}
	return emit(os.Stdout, rep)
}

// newClient is a client for sock. A command line that names a policy is the
// operator declaring it, so the client insists on it; one that names none
// takes the daemon's.
func newClient(sock string, policy daemon.Policy, declared bool) *daemon.Client {
	c := daemon.NewClient(sock)
	if declared {
		c.Expect(policy)
	}
	return c
}

// emit prints a reply, and reports a program that stopped at a failed step.
func emit(w io.Writer, rep wire.Reply) error {
	if _, err := fmt.Fprintln(w, rep.Body); err != nil {
		return err
	}
	if rep.Stopped {
		return errStopped
	}
	return nil
}

func mcpMain(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	sock := fs.String("socket", daemon.DefaultSocket(), "Unix socket path")
	name := fs.String("s", "", "session name (default: one of its own, shared with no other server)")
	keep := fs.String("keep-state", "", "drive session `NAME` and keep its cookies and storage between runs")
	var policy daemon.Policy
	declared := policy.Flags(fs)
	_ = fs.Parse(args)
	session, err := mcpSession(*name, *keep)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "riffle mcp: session %s\n", session)
	c := newClient(*sock, policy, declared())
	defer c.Close()
	// The server holds its session: it ends when the server does.
	c.Hold(session)
	if *keep != "" {
		path, err := c.KeepState().Kept(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "riffle mcp: its cookies and storage are kept in %s; delete that file to forget them\n", path)
	}
	return mcpserver.New(c, session, version).Run(ctx, &mcp.StdioTransport{})
}

// mcpSession is the session an MCP server drives: the one whose state it
// keeps, the one it was given, or else one of its own, so agents in two
// editor windows never share a page. The process id says which program holds
// it; the random part keeps a reused process id from inheriting what an
// earlier server left behind.
func mcpSession(given, keep string) (string, error) {
	if keep != "" {
		if err := daemon.StateName(keep); err != nil {
			return "", fmt.Errorf("mcp: -keep-state: %w", err)
		}
		if given != "" && given != keep {
			return "", fmt.Errorf("mcp: -s %s and -keep-state %s name two sessions; -keep-state alone names the session", given, keep)
		}
		return keep, nil
	}
	if given != "" {
		return given, nil
	}
	return "mcp-" + strconv.Itoa(os.Getpid()) + "-" + rand.Text()[:6], nil
}

// closeSession ends a session and its browser. It never starts a daemon: with
// none running there is nothing to close.
func closeSession(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("close", flag.ExitOnError)
	sock := fs.String("socket", daemon.DefaultSocket(), "Unix socket path")
	name := fs.String("s", "default", "session name")
	_ = fs.Parse(args)
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", *sock)
	if err != nil {
		fmt.Printf("no Riffle daemon is running on %s; nothing to close\n", *sock)
		return nil
	}
	_ = conn.Close()
	c := daemon.NewClient(*sock)
	defer c.Close()
	out, err := c.Do(ctx, wire.Request{Verb: wire.Close, Session: *name})
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

// stopDaemon ends the daemon and every session in it. Like close, it never
// starts a daemon: with none running there is nothing to stop.
func stopDaemon(ctx context.Context, w io.Writer, args []string) error {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	sock := fs.String("socket", daemon.DefaultSocket(), "Unix socket path")
	_ = fs.Parse(args)
	out, found, err := daemon.Stop(ctx, *sock)
	if err != nil {
		return err
	}
	if !found {
		out = fmt.Sprintf("no Riffle daemon is running on %s; nothing to stop", *sock)
	}
	_, err = fmt.Fprintln(w, out)
	return err
}

// archive writes the session's current page as one MHTML document. Like
// close, it never starts a daemon: with none running there is no page.
func archive(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("archive", flag.ExitOnError)
	sock := fs.String("socket", daemon.DefaultSocket(), "Unix socket path")
	name := fs.String("s", "default", "session name")
	_ = fs.Parse(args)
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", *sock)
	if err != nil {
		return fmt.Errorf("no Riffle daemon is running on %s, so there is no page to archive", *sock)
	}
	_ = conn.Close()
	c := daemon.NewClient(*sock)
	defer c.Close()
	page, err := c.Do(ctx, wire.Request{Verb: wire.Archive, Session: *name})
	if err != nil {
		return err
	}
	_, err = io.WriteString(os.Stdout, page)
	return err
}
