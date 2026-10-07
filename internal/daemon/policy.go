package daemon

import (
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Policy is what the operator lets sessions reach. The zero value is the
// strictest: public web addresses only, no uploads, no eval.
type Policy struct {
	// UploadDir is the one directory files may be uploaded from.
	UploadDir string
	// Origins, when set, are the only origins documents may load from.
	// Entries are scheme://host[:port].
	Origins []string
	// AllowPrivate lets sessions reach loopback, link-local and private
	// network addresses, such as an app under test on localhost.
	AllowPrivate bool
	// AllowEval enables the eval statement.
	AllowEval bool
}

// Flags registers the policy's command-line flags on fs.
func (p *Policy) Flags(fs *flag.FlagSet) {
	fs.BoolVar(&p.AllowPrivate, "allow-private", false, "allow loopback, link-local and private network addresses, such as an app on localhost")
	fs.BoolVar(&p.AllowEval, "allow-eval", false, "allow the eval statement")
	fs.Var((*originList)(&p.Origins), "allow-origin", "restrict documents to this origin, scheme://host[:port]; repeatable")
	fs.StringVar(&p.UploadDir, "upload-dir", "", "the only directory files may be uploaded from")
}

// Args is the policy as the flags Flags reads.
func (p Policy) Args() []string {
	var args []string
	if p.AllowPrivate {
		args = append(args, "-allow-private")
	}
	if p.AllowEval {
		args = append(args, "-allow-eval")
	}
	for _, o := range p.Origins {
		args = append(args, "-allow-origin", o)
	}
	if p.UploadDir != "" {
		args = append(args, "-upload-dir", p.UploadDir)
	}
	return args
}

func (p Policy) String() string {
	if a := p.Args(); len(a) > 0 {
		return strings.Join(a, " ")
	}
	return "the default policy"
}

// Within reports whether p allows nothing that q forbids.
func (p Policy) Within(q Policy) bool {
	switch {
	case p.AllowPrivate && !q.AllowPrivate, p.AllowEval && !q.AllowEval:
		return false
	case p.UploadDir != "" && p.UploadDir != q.UploadDir:
		return false
	case len(q.Origins) == 0:
		return true
	case len(p.Origins) == 0:
		return false
	}
	for _, o := range p.Origins {
		if !slices.ContainsFunc(q.Origins, func(x string) bool { return strings.EqualFold(x, o) }) {
			return false
		}
	}
	return true
}

// originList is a repeatable origin flag.
type originList []string

func (l *originList) String() string     { return strings.Join(*l, ",") }
func (l *originList) Set(v string) error { *l = append(*l, v); return nil }

// Cookie reports whether a kept cookie for domain may be given to a browser.
// It must be one the browser could send: not to a private address unless
// allowed, and, with an origin list, only to a host of an allowed origin
// (RFC 6265 section 5.1.3 domain-match), so no other site's sign-in is live in a
// session held to some origins.
func (p Policy) Cookie(domain string, secure bool) error {
	domain = strings.ToLower(strings.TrimPrefix(domain, "."))
	scheme := "http"
	if secure {
		scheme = "https"
	}
	if err := p.request(scheme+"://"+domain+"/", "Other"); err != nil {
		return err
	}
	if len(p.Origins) == 0 {
		return nil
	}
	for _, o := range p.Origins {
		u, err := url.Parse(o)
		if err != nil {
			continue
		}
		if host := strings.ToLower(u.Hostname()); host == domain || strings.HasSuffix(host, "."+domain) {
			return nil
		}
	}
	return fmt.Errorf("refused: a cookie for %s would reach no allowed origin; allowed: %s", domain, strings.Join(p.Origins, ", "))
}

// Navigate reports whether the agent may send the session to rawURL.
func (p Policy) Navigate(rawURL string) error {
	return p.request(rawURL, "Document")
}

// Request reports whether a page may issue a request of the given resource type.
func (p Policy) Request(rawURL, resourceType string) error {
	return p.request(rawURL, resourceType)
}

func (p Policy) request(rawURL, resourceType string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("refused: %q is not a URL", rawURL)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ws", "wss":
	case "data", "blob", "about":
		if resourceType == "Document" && strings.ToLower(u.Scheme) != "about" {
			return fmt.Errorf("refused: documents cannot load from %s: URLs", u.Scheme)
		}
		return nil
	case "":
		return fmt.Errorf("refused: %q has no scheme; use an http or https address", rawURL)
	default:
		return fmt.Errorf("refused: %s: URLs are not allowed; use http or https", u.Scheme)
	}
	if !p.AllowPrivate && private(u.Hostname()) {
		return fmt.Errorf("refused: %s is a private address; the operator allows private addresses with -allow-private", u.Hostname())
	}
	if resourceType == "Document" && len(p.Origins) > 0 {
		origin := strings.ToLower(u.Scheme + "://" + u.Host)
		for _, o := range p.Origins {
			if strings.ToLower(o) == origin {
				return nil
			}
		}
		return fmt.Errorf("refused: %s is outside the allowed origins; allowed: %s", origin, strings.Join(p.Origins, ", "))
	}
	return nil
}

func private(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if i := strings.IndexByte(host, '%'); i >= 0 { // IPv6 zone
		host = host[:i]
	}
	ip := net.ParseIP(host)
	if ip == nil {
		ip = legacyIPv4(host)
	}
	return ip != nil && PrivateIP(ip)
}

// shared are ranges that reach hosts inside a network or a provider rather
// than the public internet: 0.0.0.0/8 ("this network", which reaches the
// local host) and 100.64.0.0/10 (carrier-grade NAT, used by some cloud
// metadata services).
var shared = []*net.IPNet{
	{IP: net.IPv4(0, 0, 0, 0).To4(), Mask: net.CIDRMask(8, 32)},
	{IP: net.IPv4(100, 64, 0, 0).To4(), Mask: net.CIDRMask(10, 32)},
}

// nat64 is the well-known prefix that embeds an IPv4 address in IPv6.
var nat64 = &net.IPNet{IP: net.ParseIP("64:ff9b::"), Mask: net.CIDRMask(96, 128)}

// PrivateIP reports whether ip reaches a local, private or provider-internal
// host: loopback, link-local (cloud metadata), RFC 1918, unique local, shared
// and unspecified addresses, alone or embedded in IPv6.
func PrivateIP(ip net.IP) bool {
	if nat64.Contains(ip) {
		ip = ip[12:16]
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	return slices.ContainsFunc(shared, func(n *net.IPNet) bool { return n.Contains(ip) })
}

// legacyIPv4 reads the spellings browsers accept for an IPv4 address and
// Go does not: decimal, octal and hex parts, and one to four of them, such as
// 2130706433, 0x7f000001, 0177.0.0.1 and 127.1. It returns nil for anything
// else, including names.
func legacyIPv4(host string) net.IP {
	parts := strings.Split(host, ".")
	if len(parts) == 0 || len(parts) > 4 {
		return nil
	}
	var vals []uint64
	for _, p := range parts {
		base := 10
		switch {
		case strings.HasPrefix(p, "0x") || strings.HasPrefix(p, "0X"):
			base, p = 16, p[2:]
		case len(p) > 1 && p[0] == '0':
			base, p = 8, p[1:]
		}
		v, err := strconv.ParseUint(p, base, 32)
		if err != nil {
			return nil
		}
		vals = append(vals, v)
	}
	// The last part fills the remaining bytes; earlier parts are one byte each.
	last := vals[len(vals)-1]
	room := uint64(1) << (8 * uint(5-len(vals)))
	if last >= room {
		return nil
	}
	n := last
	for i, v := range vals[:len(vals)-1] {
		if v > 255 {
			return nil
		}
		n |= v << (8 * uint(3-i))
	}
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}

// Upload reports whether a file may be attached to a page. Nothing is
// uploadable unless the operator names a directory.
func (p Policy) Upload(path string) error {
	if p.UploadDir == "" {
		return fmt.Errorf("uploads are off for this session; the operator enables them by naming an upload directory")
	}
	root, err := filepath.EvalSymlinks(p.UploadDir)
	if err != nil {
		return fmt.Errorf("upload directory is unusable: %w", err)
	}
	file, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("upload refused: %w", err)
	}
	rel, err := filepath.Rel(root, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("upload refused: %s is outside the upload directory", path)
	}
	return nil
}

// Dial reports whether the browser may connect to ip. It is asked after
// DNS, so it sees the address a name resolved to.
func (p Policy) Dial(ip net.IP) error {
	if !p.AllowPrivate && PrivateIP(ip) {
		return fmt.Errorf("%s is a private address; the operator allows private addresses with -allow-private", ip)
	}
	return nil
}
