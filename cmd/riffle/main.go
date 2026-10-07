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

  riffle serve [-socket PATH] [POLICY] [LIMITS]
  riffle run [-s NAME] [-socket PATH]                       program on stdin, reply on stdout
  riffle view [-s NAME] [-socket PATH] [PROJECTION...]      read the page
  riffle close [-s NAME] [-socket PATH]                     end a session and its browser
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

A daemon started for run, view or mcp keeps the policy it last ran with.

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
		err = serve(ctx, os.Args[2:])
	case "run":
		err = client(ctx, wire.Run, os.Args[2:])
	case "view":
		err = client(ctx, wire.View, os.Args[2:])
	case "close":
		err = closeSession(ctx, os.Args[2:])
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
		fmt.Fprintln(os.Stderr, "riffle:", err)
		os.Exit(1)
	}
}

func serve(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfg := daemon.Config{Width: 1280, Height: 800, Product: "Riffle/" + version}
	fs.StringVar(&cfg.Socket, "socket", daemon.DefaultSocket(), "Unix socket path")
	audit := fs.String("audit", "", "session log file (default: next to the socket)")
	fs.IntVar(&cfg.Width, "width", cfg.Width, "viewport width")
	fs.IntVar(&cfg.Height, "height", cfg.Height, "viewport height")
	fs.IntVar(&cfg.MaxBrowsers, "max-browsers", daemon.DefaultMaxBrowsers, "browsers running at once on this machine, across every daemon of this user")
	fs.DurationVar(&cfg.SessionIdle, "session-idle", daemon.DefaultSessionIdle, "end a session unused this long, unless an MCP server holds it")
	fs.DurationVar(&cfg.DaemonIdle, "daemon-idle", daemon.DefaultDaemonIdle, "end the daemon after this long with no session and no client")
	cfg.Policy.Flags(fs)
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
	d, err := daemon.New(cfg)
	if err != nil {
		return err
	}
	return d.Serve(ctx)
}

func client(ctx context.Context, verb string, args []string) error {
	fs := flag.NewFlagSet(verb, flag.ExitOnError)
	sock := fs.String("socket", daemon.DefaultSocket(), "Unix socket path")
	name := fs.String("s", "default", "session name")
	_ = fs.Parse(args)
	body := strings.Join(fs.Args(), " ")
	if verb == wire.Run {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read program: %w", err)
		}
		body = string(b)
	}
	c := daemon.NewClient(*sock)
	defer c.Close()
	rep, err := c.Exchange(ctx, wire.Request{Verb: verb, Session: *name, Body: body})
	if err != nil {
		return err
	}
	return emit(os.Stdout, rep)
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
	policy.Flags(fs)
	_ = fs.Parse(args)
	session, err := mcpSession(*name, *keep)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "riffle mcp: session %s\n", session)
	c := daemon.NewClient(*sock)
	// An editor entry that names a policy is the operator declaring it; one
	// that names none uses whatever the daemon was given.
	declared := false
	fs.Visit(func(f *flag.Flag) {
		declared = declared || f.Name != "socket" && f.Name != "s" && f.Name != "keep-state"
	})
	if declared {
		c.Expect(policy)
	}
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
