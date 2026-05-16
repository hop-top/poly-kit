package scenario

import "hop.top/kit/go/conformance/scenariorules"

// loadDefaultRules returns the canonical rules document embedded in
// the kit binary. Thin wrapper so test code can stub it if needed.
func loadDefaultRules() (*scenariorules.Document, error) {
	return scenariorules.LoadDefault()
}
