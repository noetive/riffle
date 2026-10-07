package daemon_test

import (
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noetive/riffle/internal/daemon"
)

// FuzzPolicyRequest throws arbitrary URLs at the network policy. Whatever the
// spelling, a refusal the operator asked for must hold: schemes outside the
// web are refused, documents never load from data: or blob:, a private address
// is refused unless AllowPrivate, and an origin outside the list is refused.
func FuzzPolicyRequest(f *testing.F) {
	for _, u := range []string{
		"http://127.0.0.1/", "http://[::1]/", "http://[fe80::1]/", "http://[fc00::1]/", "http://[::ffff:127.0.0.1]/",
		"http://[::]/", "http://0.0.0.0/", "http://10.0.0.1/", "http://172.16.0.1/", "http://192.168.1.1/",
		"http://169.254.169.254/latest/meta-data/", "http://[fd00:ec2::254]/", "http://100.100.100.200/",
		"http://2130706433/", "http://0x7f000001/", "http://0177.0.0.1/", "http://127.1/", "http://0xa9.0xfe.0xa9.0xfe/",
		"http://localhost/", "http://LOCALHOST./", "http://app.localhost:8080/", "http://localhost.example/",
		"http://[fe80::1%25eth0]/", "http://127。0。0。1/", "http://１２７.０.０.１/", "http://%31%32%37.0.0.1/",
		"http://user:pass@127.0.0.1/", "http://example.com@127.0.0.1/", "http://127.0.0.1@example.com/",
		"http://example.com:80@10.0.0.1:81/", "http://evil.com#@127.0.0.1/", "http://evil.com\\@127.0.0.1/",
		"https://example.com/", "https://example.com:443/path?q=1#frag", "ws://127.0.0.1/", "wss://[::1]/",
		"HTTP://EXAMPLE.COM/", "http://93.184.216.34/", "http://[2606:4700::1111]/", "http://3.5/",
		"file:///etc/passwd", "ftp://example.com/", "javascript:alert(1)", "chrome://settings", "view-source:http://a/",
		"data:text/html,<b>x</b>", "blob:http://example.com/1", "about:blank", "about:srcdoc", "//example.com/", "",
		"http://", "http:///", "http://[", "http://a b/", "http://\x00/", "http://\xff\xfe/", "://", "http://:80/",
		"http://example.com:99999/", "http://example.com:-1/", "http://[::1]:80/", "http://[0:0:0:0:0:ffff:7f00:1]/",
		"http://0.0.0.0:80/", "http://0/", "http://0x0/", "http://00/", "http://[::0]/", "http://[0::1]/", "http://[::127.0.0.1]/",
		"http://[64:ff9b::7f00:1]/", "http://[2002:7f00:1::]/", "http://[fe80::1%eth0]/", "http://[fec0::1]/", "http://[ff00::]/",
		"http://127.0.0.1.nip.io/", "http://localtest.me/", "http://127.000.000.001/", "http://0177.0000.0000.0001/", "http://0x7f.1/",
		"http://127.0.1/", "http://127.256.0.1/", "http://4294967295/", "http://4294967296/", "http://0xffffffff/", "http://1.2.3.4.5/",
		"http://192.168.0.256/", "http://10.0.0.1:8080/admin", "http://172.15.0.1/", "http://172.31.255.255/", "http://172.32.0.0/",
		"http://169.254.0.1/", "http://169.253.0.1/", "http://100.64.0.1/", "http://198.18.0.1/", "http://224.0.0.1/", "http://255.255.255.255/",
		"http://metadata.google.internal/", "http://instance-data/", "http://kubernetes.default.svc/", "http://example.internal/",
		"http://a.localhost/", "http://localhost:3000/", "http://LocalHost/", "http://localhost%2e/", "http://local\u200bhost/",
		"https://user@[::1]:8443/", "https://:@127.0.0.1/", "http://a:b@c:d@127.0.0.1/", "http://127.0.0.1:/", "http://127.0.0.1:0/",
		"http://example.com/%2e%2e/", "http://example.com/?u=http://127.0.0.1/", "http://example.com#http://127.0.0.1/",
		"http://example.com/\r\nHost: 127.0.0.1", "http://example.com\t/", "http://exa mple.com/", " http://127.0.0.1/", "http://127.0.0.1/ ",
		"HtTp://127.0.0.1/", "hTTps://[::1]/", "WS://localhost/", "Wss://10.1.1.1/", "ws://example.com/socket", "wss://example.com:8443/s",
		"ftp://127.0.0.1/", "gopher://127.0.0.1/", "ssh://example.com", "mailto:a@b.c", "tel:123", "sms:123", "intent://x", "chrome-extension://abc/x",
		"filesystem:http://example.com/x", "blob:null/abc", "blob:https://example.com/uuid", "data:,", "data:text/plain;base64,AAAA", "data:image/png;base64,",
		"about:blank#x", "about:neterror", "about:srcdoc", "ABOUT:blank", "javascript:void(0)", "JaVaScRiPt:1", "vbscript:x", "view-source:x",
		"https://xn--n3h.example/", "https://☃.example/", "https://例え.jp/", "https://EXAMPLE.com./", "https://example..com/", "https://-bad-.com/",
		"https://example.com:65535/", "https://example.com:65536/", "https://example.com:0/", "https://[example.com]/", "https://[1.2.3.4]/",
		"//127.0.0.1/", "/relative/path", "relative", "?q=1", "#frag", "\\\\127.0.0.1\\share", "http:127.0.0.1", "http:/127.0.0.1", "http:\\\\127.0.0.1",
	} {
		f.Add(u, "Document", true, "https://example.com")
		f.Add(u, "XHR", true, "")
		f.Add(u, "Image", false, "https://example.com")
	}
	f.Fuzz(func(t *testing.T, raw, resourceType string, deny bool, origin string) {
		var origins []string
		if origin != "" {
			origins = []string{origin}
		}
		p := daemon.Policy{AllowPrivate: !deny, Origins: origins}
		err := p.Request(raw, resourceType)
		u, perr := url.Parse(raw)
		if perr != nil {
			if err == nil {
				t.Fatalf("%q does not parse yet was allowed", raw)
			}
			return
		}
		scheme := strings.ToLower(u.Scheme)
		web := scheme == "http" || scheme == "https" || scheme == "ws" || scheme == "wss"
		switch {
		case !web && scheme != "data" && scheme != "blob" && scheme != "about":
			if err == nil {
				t.Fatalf("scheme %q allowed for %q", scheme, raw)
			}
		case (scheme == "data" || scheme == "blob") && resourceType == "Document":
			if err == nil {
				t.Fatalf("a document may not load from %s:", scheme)
			}
		case web && deny:
			host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
			if i := strings.IndexByte(host, '%'); i >= 0 {
				host = host[:i]
			}
			ip := net.ParseIP(host)
			private := host == "localhost" || strings.HasSuffix(host, ".localhost") ||
				ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified())
			if private && err == nil {
				t.Fatalf("%q reaches a private address and was allowed", raw)
			}
		}
		if web && resourceType == "Document" && len(origins) > 0 && err == nil {
			got := strings.ToLower(u.Scheme + "://" + u.Host)
			if got != strings.ToLower(origin) {
				t.Fatalf("document at %s allowed outside origin %q", got, origin)
			}
		}
	})
}

// FuzzPolicyNavigate holds the agent's own navigations to the same rules as
// a page's requests for documents.
func FuzzPolicyNavigate(f *testing.F) {
	for _, u := range []string{"https://example.com/", "http://127.0.0.1:9/", "file:///etc/passwd", "data:text/html,x", "about:blank", "", "http://[::1]/",
		"http://localhost./", "http://0x7f.1/", "blob:http://a/b", "javascript:1", "http://example.com@10.0.0.1/", "HTTP://LOCALHOST/", "https://example.com/a b"} {
		f.Add(u)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		p := daemon.Policy{}
		if (p.Navigate(raw) == nil) != (p.Request(raw, "Document") == nil) {
			t.Fatalf("Navigate and Request disagree on %q", raw)
		}
	})
}

// FuzzUpload tries arbitrary paths against the one directory uploads may come
// from, with a symlink inside it that points out. No spelling may attach a
// file whose real location is outside the directory.
func FuzzUpload(f *testing.F) {
	root := f.TempDir()
	outside := f.TempDir()
	_ = os.WriteFile(filepath.Join(root, "ok.txt"), []byte("ok"), 0o600)
	_ = os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600)
	_ = os.Symlink(outside, filepath.Join(root, "escape"))
	_ = os.Mkdir(filepath.Join(root, "sub"), 0o700)
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		f.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(root, "ok.txt"), filepath.Join(root, "sub", "..", "ok.txt"), filepath.Join(root, "escape", "secret.txt"),
		filepath.Join(root, "..", "x"), filepath.Join(outside, "secret.txt"), filepath.Join(root, "ok.txt") + "/..",
		"/etc/passwd", "", ".", "..", "/", filepath.Join(root, "nope"), filepath.Join(root, "ok.txt\x00.png"),
		root + "/./sub/../escape/secret.txt", root + "//ok.txt", "~/x", "\xff",
		root + "/sub/./../ok.txt", root + "/sub/../../" + filepath.Base(outside) + "/secret.txt", root + "/escape/../ok.txt", root + "/escape",
		root + "/ok.txt/", root + "/OK.TXT", root + "/ok.txt ", " " + root + "/ok.txt", "file://" + root + "/ok.txt", root + "/\u202etxt.ko",
		root + "/" + strings.Repeat("a/", 50) + "x", strings.Repeat("../", 40) + "etc/passwd", root[:len(root)-1], root + "x/ok.txt",
		"\\\\?\\" + root, "C:\\Windows\\win.ini", "/dev/null", "/proc/self/environ", "/etc/shadow", "~", "$HOME/x", "%HOME%\\x", "*", "?", "ok.txt",
		root + "/sub/./../ok.txt", root + "/sub/../../" + filepath.Base(outside) + "/secret.txt", root + "/escape/../ok.txt", root + "/escape",
		root + "/ok.txt/", root + "/OK.TXT", root + "/ok.txt ", " " + root + "/ok.txt", "file://" + root + "/ok.txt", root + "/\u202etxt.ko",
		root + "/" + strings.Repeat("a/", 50) + "x", strings.Repeat("../", 40) + "etc/passwd", root[:len(root)-1], root + "x/ok.txt",
		"\\\\?\\" + root, "C:\\Windows\\win.ini", "/dev/null", "/proc/self/environ", "/etc/shadow", "~", "$HOME/x", "%HOME%\\x", "*", "?", "ok.txt",
	} {
		f.Add(p)
	}
	pol := daemon.Policy{UploadDir: root}
	f.Fuzz(func(t *testing.T, path string) {
		if pol.Upload(path) != nil {
			return
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatalf("%q was allowed but does not resolve: %v", path, err)
		}
		rel, err := filepath.Rel(rootReal, real)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			t.Fatalf("%q was allowed but lives at %q, outside %q", path, real, rootReal)
		}
	})
}

// FuzzEnvSecrets looks secrets up by arbitrary name and origin. A value is
// released only to the origin it is bound to, and never appears in an error.
func FuzzEnvSecrets(f *testing.F) {
	f.Setenv("RIFFLE_SECRET_PAY", "sekrit-value")
	f.Setenv("RIFFLE_SECRET_PAY_ORIGIN", "https://pay.example")
	f.Setenv("RIFFLE_SECRET_LOOSE", "unbound-value")
	f.Setenv("RIFFLE_SECRET_A_B_C", "dotted-value")
	f.Setenv("RIFFLE_SECRET_A_B_C_ORIGIN", "https://abc.example")
	for _, n := range []string{"pay", "PAY", "Pay", "loose", "a-b-c", "a.b.c", "a_b_c", "", "pay_origin", "PAY_ORIGIN", "pay\x00", "p=ay", "../pay", "pay ", "nonexistent", "\xff",
		"a-b.c", "A.B-C", "a--b-c", "-pay", "pay-", ".pay", "pay.", "pay origin", "pay\n", "pay\t", "päy", "ＰＡＹ", "pay_", "_pay", "path/to/pay", "pay;", "pay$", "$secret:pay", "secret:pay", "RIFFLE_SECRET_PAY", "riffle_secret_pay", "l00se", "LOOSE_ORIGIN", "loose-origin"} {
		for _, o := range []string{"https://pay.example", "https://evil.example", "", "https://pay.example/", "HTTPS://PAY.EXAMPLE", "https://abc.example", "https://pay.example:443", "http://pay.example", "https://pay.example.evil.example", "https://evil.example/https://pay.example", "https://pay.example\x00", " https://pay.example", "null", "*"} {
			f.Add(n, o)
		}
	}
	f.Fuzz(func(t *testing.T, name, origin string) {
		val, err := daemon.EnvSecrets{}.Resolve(name, origin)
		if err != nil {
			for _, s := range []string{"sekrit-value", "unbound-value", "dotted-value"} {
				if strings.Contains(err.Error(), s) {
					t.Fatalf("error leaks a secret: %v", err)
				}
			}
			if val != "" {
				t.Fatalf("a failed lookup returned %q", val)
			}
			return
		}
		switch val {
		case "sekrit-value":
			if origin != "https://pay.example" {
				t.Fatalf("pay secret released to %q", origin)
			}
		case "dotted-value":
			if origin != "https://abc.example" {
				t.Fatalf("a-b-c secret released to %q", origin)
			}
		default:
			t.Fatalf("lookup of %q returned %q", name, val)
		}
	})
}
