package daemon_test

import (
	"flag"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noetive/riffle/internal/daemon"
)

func TestNavigationAllowsOnlyWebSchemes(t *testing.T) {
	var p daemon.Policy
	for _, ok := range []string{"http://a.test/", "HTTPS://a.test/x", "about:blank"} {
		if err := p.Navigate(ok); err != nil {
			t.Errorf("%s should be allowed: %v", ok, err)
		}
	}
	for _, bad := range []string{"file:///etc/passwd", "javascript:alert(1)", "data:text/html,hi", "ftp://a.test/"} {
		if err := p.Navigate(bad); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
}

func TestSubresourcesMayBeDataButDocumentsMayNot(t *testing.T) {
	var p daemon.Policy
	if err := p.Request("data:image/png;base64,AA==", "Image"); err != nil {
		t.Errorf("inline images are ordinary: %v", err)
	}
	if err := p.Request("data:text/html,x", "Document"); err == nil {
		t.Error("a data: document is a way around the origin list")
	}
}

func TestPrivateAddressesAreRefusedByDefault(t *testing.T) {
	p := daemon.Policy{}
	for _, bad := range []string{"http://127.0.0.1:8080/", "http://localhost/", "http://169.254.169.254/latest", "http://10.0.0.5/", "http://192.168.1.1/", "http://[::1]/", "http://app.localhost/"} {
		if err := p.Request(bad, "XHR"); err == nil {
			t.Errorf("%s must be refused", bad)
		}
	}
	if err := p.Request("http://93.184.216.34/", "XHR"); err != nil {
		t.Errorf("public addresses stay reachable: %v", err)
	}
}

func TestOriginListConstrainsDocumentsOnly(t *testing.T) {
	p := daemon.Policy{Origins: []string{"https://shop.example"}}
	if err := p.Navigate("https://shop.example/cart"); err != nil {
		t.Errorf("listed origin: %v", err)
	}
	if err := p.Navigate("https://evil.example/"); err == nil {
		t.Error("unlisted origin must be refused, including after a redirect")
	}
	if err := p.Request("https://cdn.other.example/app.js", "Script"); err != nil {
		t.Errorf("scripts and images from CDNs are not documents: %v", err)
	}
}

func TestUploadsAreOffUnlessAnUploadDirectoryIsNamed(t *testing.T) {
	if err := (daemon.Policy{}).Upload("/etc/hosts"); err == nil {
		t.Fatal("uploads must default to refused")
	}
	dir := t.TempDir()
	inside := filepath.Join(dir, "cv.txt")
	if err := os.WriteFile(inside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	p := daemon.Policy{UploadDir: dir}
	if err := p.Upload(inside); err != nil {
		t.Errorf("file inside the directory: %v", err)
	}
	if err := p.Upload(outside); err == nil {
		t.Error("file outside the directory must be refused")
	}
	if err := p.Upload(link); err == nil {
		t.Error("a symlink out of the directory must be refused")
	}
}

func TestPrivateAddressesAreRefusedInEveryIPLiteralFormTheParserUnderstands(t *testing.T) {
	p := daemon.Policy{}
	for _, bad := range []string{
		"http://[::1]/", "http://[fe80::1]/", "http://[fc00::1]/", "http://[fd12:3456::1]/",
		"http://[::ffff:127.0.0.1]/", "http://[::ffff:10.0.0.1]/", "http://[::]/", "http://0.0.0.0/",
		"http://172.16.0.1/", "http://[ff02::1]/", "ws://127.0.0.1/", "wss://[::1]/",
		"HTTP://127.0.0.1/", "http://LOCALHOST/", "http://App.LocalHost/",
	} {
		for _, kind := range []string{"Document", "XHR", "WebSocket"} {
			if err := p.Request(bad, kind); err == nil {
				t.Errorf("%s (%s) must be refused", bad, kind)
			}
		}
	}
	for _, ok := range []string{"http://[2606:4700::1111]/", "http://93.184.216.34/", "http://172.32.0.1/", "http://localhost.example/"} {
		if err := p.Request(ok, "XHR"); err != nil {
			t.Errorf("%s is public and must stay reachable: %v", ok, err)
		}
	}
}

// The private-address check reads hosts as written, so spellings that only the browser's
// resolver turns into a private address are not covered by Policy. This test
// states what is NOT covered; it asserts nothing, so a future fix does not
// Browsers accept many spellings of the same private address; each must be
// refused, not only the canonical dotted quad.
func TestPrivateAddressesAreRefusedInNonCanonicalSpellings(t *testing.T) {
	p := daemon.Policy{}
	for _, host := range []string{
		"http://2130706433/",
		"http://0x7f000001/",
		"http://0177.0.0.1/",
		"http://127.1/",
		"http://localhost./",
		"http://[fe80::1%25eth0]/",
		"http://0xa9.0xfe.0xa9.0xfe/",
	} {
		if err := p.Request(host, "XHR"); err == nil {
			t.Errorf("%s reaches a private address and must be refused", host)
		}
	}
	for _, ok := range []string{"http://93.184.216.34/", "http://1.1.1.1/", "http://example.com/", "http://3.5/"} {
		if err := p.Request(ok, "XHR"); err != nil {
			t.Errorf("%s is public and must stay reachable: %v", ok, err)
		}
	}
}

func TestSchemeAndUserinfoCannotSmuggleAHost(t *testing.T) {
	deny := daemon.Policy{}
	for _, bad := range []string{
		"http://good.example@127.0.0.1/",
		"http://good.example:pw@localhost/",
		"http://127.0.0.1@good.example@127.0.0.1/",
	} {
		if err := deny.Navigate(bad); err == nil {
			t.Errorf("%s: the host after the last @ is private and must be refused", bad)
		}
	}
	var open daemon.Policy
	for _, bad := range []string{"FILE:///etc/passwd", "JavaScript:alert(1)", "Data:text/html,x", "FTP://a.test/", "chrome://settings", "view-source:https://a.test/"} {
		if err := open.Navigate(bad); err == nil {
			t.Errorf("%s must be refused whatever its case", bad)
		}
	}
	if err := open.Navigate("http://a.test/%zz"); err == nil {
		t.Error("an unparsable URL must be refused, not allowed")
	}

	p := daemon.Policy{Origins: []string{"https://shop.example"}}
	for _, bad := range []string{
		"https://shop.example@evil.example/",
		"https://shop.example:pw@evil.example/",
		"https://shop.example.evil.example/",
		"https://evil.example/https://shop.example/",
		"http://shop.example/",
		"https://shop.example:8443/",
	} {
		if err := p.Navigate(bad); err == nil {
			t.Errorf("%s is not the allowed origin and must be refused", bad)
		}
	}
	for _, ok := range []string{"HTTPS://SHOP.EXAMPLE/cart", "https://evil.example@shop.example/"} {
		if err := p.Navigate(ok); err != nil {
			t.Errorf("%s is the allowed origin: %v", ok, err)
		}
	}
}

func TestUploadStaysInsideItsDirectory(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "up")
	sibling := filepath.Join(base, "upload2")
	for _, d := range []string{dir, sibling} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path string) string {
		t.Helper()
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	inside := write(filepath.Join(dir, "cv.txt"))
	nested := filepath.Join(dir, "sub")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	deep := write(filepath.Join(nested, "a.txt"))
	sib := write(filepath.Join(sibling, "cv.txt"))
	secret := write(filepath.Join(base, "secret.txt"))
	dirLink := filepath.Join(dir, "dirlink")
	if err := os.Symlink(sibling, dirLink); err != nil {
		t.Fatal(err)
	}
	p := daemon.Policy{UploadDir: dir}

	for _, ok := range []string{inside, deep, filepath.Join(dir, "sub", "..", "cv.txt")} {
		if err := p.Upload(ok); err != nil {
			t.Errorf("%s is inside: %v", ok, err)
		}
	}
	for name, bad := range map[string]string{
		"sibling sharing the name prefix": sib,
		"dot-dot out of the directory":    filepath.Join(dir, "..", "secret.txt"),
		"dot-dot via sibling":             filepath.Join(dir, "..", "upload2", "cv.txt"),
		"parent file":                     secret,
		"through a symlinked directory":   filepath.Join(dirLink, "cv.txt"),
		"the filesystem root":             "/",
		"the parent directory itself":     base,
	} {
		if err := p.Upload(bad); err == nil {
			t.Errorf("%s: %s must be refused", name, bad)
		}
	}
	if err := p.Upload(filepath.Join(dir, "missing.txt")); err == nil {
		t.Error("a file that does not exist must be refused")
	}
	if err := (daemon.Policy{UploadDir: filepath.Join(base, "nope")}).Upload(inside); err == nil {
		t.Error("an unusable upload directory must refuse everything")
	}
}

func TestARefusedAddressWithoutASchemeSaysSoAndNamesIt(t *testing.T) {
	err := daemon.Policy{}.Navigate("/relative/path")
	if err == nil {
		t.Fatal("an address with no scheme cannot be navigated to")
	}
	for _, want := range []string{"/relative/path", "http or https"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "refused: :") {
		t.Errorf("error %q prints an empty scheme", err)
	}
}

func TestAllowPrivateOptsIntoLocalAddresses(t *testing.T) {
	p := daemon.Policy{AllowPrivate: true}
	for _, ok := range []string{"http://127.0.0.1:8080/", "http://localhost/", "http://10.0.0.5/"} {
		if err := p.Request(ok, "XHR"); err != nil {
			t.Errorf("%s: an operator who allows private addresses can test a local app: %v", ok, err)
		}
	}
}

func TestPrivateCoversSharedAndEmbeddedAddresses(t *testing.T) {
	for _, bad := range []string{"100.64.0.1", "100.127.255.254", "0.1.2.3", "64:ff9b::a9fe:a9fe", "64:ff9b::7f00:1", "::ffff:169.254.169.254"} {
		if !daemon.PrivateIP(net.ParseIP(bad)) {
			t.Errorf("%s reaches a local or provider-internal host and is private", bad)
		}
	}
	for _, ok := range []string{"100.128.0.1", "64:ff9b::5db8:d822", "93.184.216.34", "2606:4700::1111"} {
		if daemon.PrivateIP(net.ParseIP(ok)) {
			t.Errorf("%s is public", ok)
		}
	}
}

func TestAPolicyIsWithinOneThatAllowsAtLeastAsMuch(t *testing.T) {
	strict := daemon.Policy{Origins: []string{"https://a.example"}}
	cases := []struct {
		p, q daemon.Policy
		want bool
	}{
		{daemon.Policy{}, daemon.Policy{}, true},
		{strict, daemon.Policy{}, true},
		{daemon.Policy{}, strict, false},
		{strict, daemon.Policy{Origins: []string{"https://b.example", "https://a.example"}}, true},
		{daemon.Policy{Origins: []string{"https://b.example"}}, strict, false},
		{daemon.Policy{AllowPrivate: true}, daemon.Policy{}, false},
		{daemon.Policy{AllowEval: true}, daemon.Policy{}, false},
		{daemon.Policy{UploadDir: "/up"}, daemon.Policy{}, false},
		{daemon.Policy{UploadDir: "/up"}, daemon.Policy{UploadDir: "/other"}, false},
		{daemon.Policy{}, daemon.Policy{UploadDir: "/up", AllowEval: true, AllowPrivate: true}, true},
	}
	for _, c := range cases {
		if got := c.p.Within(c.q); got != c.want {
			t.Errorf("%v within %v = %v, want %v", c.p, c.q, got, c.want)
		}
	}
}

func TestPolicyFlagsRoundTrip(t *testing.T) {
	for _, p := range []daemon.Policy{
		{},
		{AllowPrivate: true},
		{AllowEval: true, UploadDir: "/tmp/up load", Origins: []string{"https://a.example", "http://b.example:8080"}},
	} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		var got daemon.Policy
		got.Flags(fs)
		if err := fs.Parse(p.Args()); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, p) && (len(got.Origins) != 0 || len(p.Origins) != 0 || !got.Within(p) || !p.Within(got)) {
			t.Errorf("%v parsed back as %v", p, got)
		}
	}
}
