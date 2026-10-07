//go:build !linux

package egress_test

import (
	"net"
	"testing"
)

// closedPort is an address nothing listens on. Not every system refuses a
// connection to a port that is bound but not listening (some drop it until it
// times out), so here the port is released, and another test could take it.
func closedPort(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}
