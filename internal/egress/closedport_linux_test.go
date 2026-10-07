package egress_test

import (
	"net"
	"strconv"
	"syscall"
	"testing"
)

// closedPort is an address that refuses connections for the whole test. The
// port stays bound, never listening, which Linux answers with a reset: a port
// that was only released could be taken by a test running alongside, and a
// connection to it would succeed.
func closedPort(t *testing.T) string {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Close(fd) })
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		t.Fatal(err)
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		t.Fatal(err)
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(sa.(*syscall.SockaddrInet4).Port))
}
