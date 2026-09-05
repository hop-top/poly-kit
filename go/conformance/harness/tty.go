package harness

// installTTY swaps in a simulated terminal for the duration of one
// invocation. The implementation is intentionally minimal:
//
//   - When WithTTY() is set, the harness flips a kit-level probe
//     (cli.SetTTYProbe-style seam) if one exists, else falls back
//     to no-op. A real pty allocation is deferred until an adopter
//     test demonstrably needs one.
//   - When NonTTY() (the default), no installation happens; tests
//     already inherit go test's non-tty environment.
//
// The seam check is dynamic: we attempt to call cli.SetTTYProbe via
// the optional ttyProbeFn variable below. If a future kit version
// adds the probe, set ttyProbeFn from an init() in a separate file
// and the harness will pick it up.
func (c *config) installTTY() func() {
	if !c.withTTY || ttyProbeFn == nil {
		return func() {}
	}
	restore := ttyProbeFn(true)
	return restore
}

// ttyProbeFn is the optional kit-side seam install function. Nil
// when the kit binary in use does not expose a TTY probe. Set via
// init() in a future kit version; tests can override too.
//
// The function takes the desired probe value (true = "we have a
// tty") and returns a restore callback that undoes the install.
var ttyProbeFn func(value bool) func()
