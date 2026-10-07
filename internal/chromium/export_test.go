package chromium

import "time"

// SetCallTimeout shortens how long one protocol command may take, for tests
// of a site that never answers; the returned func restores it.
func SetCallTimeout(d time.Duration) func() {
	old := callTimeout
	callTimeout = d
	return func() { callTimeout = old }
}

// SettleCap is the longest a settle waits on a pending request.
var SettleCap = settleCap
