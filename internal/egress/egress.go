// Package egress is the one way out of the browser to the network. Chrome is
// started with this proxy as its only route, so every connection a page
// opens, from any frame, popup, worker or socket, is dialed here. The address
// is checked at the moment it is dialed, after the name has been resolved:
// a name that points at a private address, or that is re-pointed at one
// between two requests, is refused like the address itself.
package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"sync"
	"syscall"
	"time"
)

// Guard says whether the browser may connect to ip; a non-nil error is the
// reason it may not.
type Guard func(ip net.IP) error

// refusal is a connection the guard refused, as opposed to one that failed.
type refusal struct{ err error }

func (r refusal) Error() string { return r.err.Error() }
func (r refusal) Unwrap() error { return r.err }

// Proxy is an HTTP proxy on a loopback port for one browser.
type Proxy struct {
	ln      net.Listener
	srv     *http.Server
	dial    *net.Dialer
	refused func(target string, err error) // guarded by mu
	failed  map[string]Failure             // guarded by mu: the latest failure per host:port

	mu sync.Mutex
}

// Start listens on a free loopback port and serves until Close; ctx bounds
// only the start.
func Start(ctx context.Context, guard Guard) (*Proxy, error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("egress: listen: %w", err)
	}
	p := &Proxy{ln: ln}
	p.dial = &net.Dialer{
		Timeout: 30 * time.Second,
		// Control runs for every address the dialer tries, after DNS and
		// just before connect: the address checked is the address used.
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return refusal{err}
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return refusal{fmt.Errorf("%s is not an address", host)}
			}
			if err := guard(ip); err != nil {
				return refusal{err}
			}
			return nil
		},
	}
	forward := &httputil.ReverseProxy{
		// A proxy request already names its destination; nothing is added,
		// so the server learns nothing about this machine.
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL = r.In.URL
			r.Out.Host = r.In.Host
		},
		Transport: &http.Transport{
			DialContext:         p.dial.DialContext,
			Proxy:               nil,
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     90 * time.Second,
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			p.fail(w, r.URL.String(), hostPort(r.URL.Host, r.URL.Scheme), err)
		},
	}
	p.srv = &http.Server{
		ReadHeaderTimeout: 30 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodConnect {
				p.tunnel(w, r)
				return
			}
			forward.ServeHTTP(w, r)
		}),
	}
	go func() { _ = p.srv.Serve(ln) }()
	return p, nil
}

// Failure is a connection that could not be made: refused by the guard, or
// failed on the way.
type Failure struct {
	At      time.Time // when
	Err     error     // why
	Target  string    // where it was going, host:port or a URL
	Refused bool      // the guard refused it, as opposed to it failing
}

// FailureTo is the latest connection to hostport (host:port) that could not
// be made, refused or failed. Chrome
// reports every failed tunnel as ERR_TUNNEL_CONNECTION_FAILED whatever the
// cause, so the cause is only here. A failure to another host is not this one.
func (p *Proxy) FailureTo(hostport string) (Failure, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, ok := p.failed[hostport]
	return f, ok
}

// keep records f as the latest failure to key (host:port).
func (p *Proxy) keep(key string, f Failure) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failed == nil || len(p.failed) >= maxFailures {
		p.failed = map[string]Failure{}
	}
	p.failed[key] = f
}

// maxFailures bounds the failures kept; a page that fails to reach many hosts
// starts the record afresh rather than growing it.
const maxFailures = 64

// hostPort is host with its port, the scheme's default when it has none.
func hostPort(host, scheme string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	if scheme == "https" {
		return net.JoinHostPort(host, "443")
	}
	return net.JoinHostPort(host, "80")
}

// Addr is the proxy's address, host:port.
func (p *Proxy) Addr() string { return p.ln.Addr().String() }

// OnRefused sets the function told of every connection the guard refuses.
func (p *Proxy) OnRefused(f func(target string, err error)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refused = f
}

// Close stops the proxy and ends every connection through it.
func (p *Proxy) Close() error { return p.srv.Close() }

// tunnel serves CONNECT: HTTPS, and WebSockets, which Chrome always tunnels.
func (p *Proxy) tunnel(w http.ResponseWriter, r *http.Request) {
	up, err := p.dial.DialContext(r.Context(), "tcp", r.Host)
	if err != nil {
		p.fail(w, r.Host, hostPort(r.Host, "https"), err)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = up.Close()
		http.Error(w, "egress: cannot tunnel", http.StatusInternalServerError)
		return
	}
	down, buf, err := hj.Hijack()
	if err != nil {
		_ = up.Close()
		return
	}
	if _, err := io.WriteString(down, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		_ = up.Close()
		_ = down.Close()
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, buf); done <- struct{}{} }()
	go func() { _, _ = io.Copy(down, up); done <- struct{}{} }()
	<-done
	_ = up.Close()
	_ = down.Close()
	<-done
}

// fail answers a request that could not be connected. A refusal is reported to
// OnRefused, and either is kept for FailureTo, under key (host:port).
// A connection the browser gave up on itself is no failure of the site.
func (p *Proxy) fail(w http.ResponseWriter, target, key string, err error) {
	if ref, ok := errors.AsType[refusal](err); ok {
		p.keep(key, Failure{Target: target, Err: ref.err, At: time.Now(), Refused: true})
		p.mu.Lock()
		f := p.refused
		p.mu.Unlock()
		if f != nil {
			f(target, ref.err)
		}
		http.Error(w, "refused: "+ref.err.Error(), http.StatusForbidden)
		return
	}
	if !errors.Is(err, context.Canceled) {
		p.keep(key, Failure{Target: target, Err: err, At: time.Now()})
	}
	http.Error(w, "egress: "+err.Error(), http.StatusBadGateway)
}
