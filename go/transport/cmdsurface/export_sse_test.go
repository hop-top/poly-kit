package cmdsurface

import "time"

// TestingSSEHeartbeat exports the unexported withSSEHeartbeat option
// so external _test packages can drive the heartbeat interval. The
// identifier ends in `_test.go` so it is only compiled when running
// the test binary; production code cannot reach it.
func TestingSSEHeartbeat(d time.Duration) SSEOption {
	return withSSEHeartbeat(d)
}
