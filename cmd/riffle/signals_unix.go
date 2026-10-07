//go:build !windows

package main

import (
	"os"
	"syscall"
)

// shutdownSignals end the daemon the orderly way. SIGTERM, which kill and
// pkill send, and SIGHUP, which a closing terminal sends, must close the
// browsers too, or they are left running.
var shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
