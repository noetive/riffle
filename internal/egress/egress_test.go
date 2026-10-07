package egress_test

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noetive/riffle/internal/egress"
)

func allowAll(net.IP) error { return nil }

func denyLoopback(ip net.IP) error {
	if ip.IsLoopback() {
		return fmt.Errorf("%s is a private address", ip)
	}
	return nil
}

func start(t *testing.T, g egress.Guard) (*egress.Proxy, *http.Client) {
	t.Helper()
	p, err := egress.Start(t.Context(), g)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	proxyURL, _ := url.Parse("http://" + p.Addr())
	return p, &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // the test server's own certificate
	}}
}

func hello() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hello "+r.Host)
	}))
}

func get(t *testing.T, c *http.Client, u string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestAnAllowedAddressIsReachedOverHTTPAndThroughATunnel(t *testing.T) {
	plain := hello()
	defer plain.Close()
	secure := httptest.NewTLSServer(plain.Config.Handler)
	defer secure.Close()
	_, c := start(t, allowAll)
	for _, u := range []string{plain.URL, secure.URL} {
		if code, body := get(t, c, u+"/x"); code != http.StatusOK || !strings.HasPrefix(body, "hello ") {
			t.Errorf("%s: %d %q", u, code, body)
		}
	}
}

func TestARefusedAddressIsNeverConnectedAndIsReported(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
	}))
	defer srv.Close()
	tls := httptest.NewTLSServer(srv.Config.Handler)
	defer tls.Close()
	p, c := start(t, denyLoopback)
	var reported []string
	p.OnRefused(func(target string, err error) {
		mu.Lock()
		defer mu.Unlock()
		reported = append(reported, target+": "+err.Error())
	})
	if code, _ := get(t, c, srv.URL); code != http.StatusForbidden {
		t.Errorf("plain request: status %d, want 403", code)
	}
	if code, _ := get(t, c, tls.URL); code == http.StatusOK {
		t.Error("a tunnel to a refused address must not open")
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Errorf("the refused server was reached %d times", hits)
	}
	if len(reported) != 2 || !strings.Contains(reported[0], "private address") {
		t.Errorf("each refusal is reported with its reason: %q", reported)
	}
}

// A name is checked by the address it resolves to, not by how it is spelled:
// localhost here stands for any name an attacker points at a private address.
func TestANameIsJudgedByTheAddressItResolvesTo(t *testing.T) {
	srv := hello()
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	_, c := start(t, denyLoopback)
	if code, body := get(t, c, "http://localhost:"+port+"/"); code == http.StatusOK {
		t.Errorf("a name for a private address was reached: %q", body)
	}
	_, open := start(t, allowAll)
	if code, body := get(t, open, "http://localhost:"+port+"/"); code != http.StatusOK || body != "hello localhost:"+port {
		t.Errorf("the request keeps its host: %d %q", code, body)
	}
}

func TestAFailedConnectionIsNotReportedAsARefusal(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // nothing listens there now
	p, c := start(t, allowAll)
	p.OnRefused(func(target string, err error) { t.Errorf("%s was not refused, it failed: %v", target, err) })
	if code, _ := get(t, c, "http://"+addr+"/"); code != http.StatusBadGateway {
		t.Errorf("status %d, want 502", code)
	}
}

func TestATunnelCarriesBytesBothWays(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		line, _ := bufio.NewReader(c).ReadString('\n')
		_, _ = io.WriteString(c, "echo "+line)
	}()
	p, _ := start(t, allowAll)
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", p.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", ln.Addr(), ln.Addr())
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT: %v", resp.Status)
	}
	_, _ = io.WriteString(conn, "ping\n")
	got, err := br.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) || got != "echo ping\n" {
		t.Errorf("tunnel returned %q, %v", got, err)
	}
}

func TestAConnectionThatFailsIsKeptWithWhereItWentAndWhy(t *testing.T) {
	for _, via := range []string{"tunnel", "http"} {
		t.Run(via, func(t *testing.T) {
			addr := closedPort(t)
			p, c := start(t, allowAll)
			if _, ok := p.FailureTo(addr); ok {
				t.Fatal("a new proxy has no failure to tell of")
			}
			before := time.Now()
			u := "http://" + addr + "/"
			if via == "tunnel" {
				u = "https://" + addr + "/"
			}
			_, _ = get(t, c, u)
			f, ok := p.FailureTo(addr)
			if !ok {
				t.Fatal("the failed connection was not kept")
			}
			if !strings.Contains(f.Target, addr) {
				t.Errorf("target = %q, want it to name %s", f.Target, addr)
			}
			if f.Err == nil || !strings.Contains(f.Err.Error(), "refused") {
				t.Errorf("cause = %v, want the connection refused", f.Err)
			}
			if f.At.Before(before) {
				t.Errorf("failure dated %v, before the request at %v", f.At, before)
			}
		})
	}
}

func TestARefusalIsKeptAsARefusalAndStillReported(t *testing.T) {
	srv := hello()
	defer srv.Close()
	p, c := start(t, denyLoopback)
	var told string
	p.OnRefused(func(target string, _ error) { told = target })
	_, _ = get(t, c, srv.URL)
	f, ok := p.FailureTo(strings.TrimPrefix(srv.URL, "http://"))
	if !ok || !f.Refused || !strings.Contains(f.Err.Error(), "private") {
		t.Errorf("the refusal is kept as one, with its reason: %+v", f)
	}
	if told == "" {
		t.Error("a refusal is still reported as it happens")
	}
}

func TestAFailureIsKeptForItsOwnHostOnly(t *testing.T) {
	down, other := closedPort(t), closedPort(t)
	p, c := start(t, allowAll)
	_, _ = get(t, c, "https://"+down+"/")
	if _, ok := p.FailureTo(other); ok {
		t.Errorf("a failure to %s was told of as one to %s", down, other)
	}
	_, _ = get(t, c, "https://"+other+"/")
	for _, hp := range []string{down, other} {
		if f, ok := p.FailureTo(hp); !ok || !strings.Contains(f.Target, hp) {
			t.Errorf("the failure to %s was lost or renamed: %+v", hp, f)
		}
	}
}
