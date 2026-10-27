// Test-only re-exports keep production identifiers unexported while still
// letting llm_test exercise them.

package llm

// TracingEnabled exposes [tracingEnabled] to the external test package.
var TracingEnabled = tracingEnabled

// CountConfigFileReads counts llm.yaml reads until the test ends.
func CountConfigFileReads(t interface{ Cleanup(func()) }) *int {
	n := new(int)
	prev := readConfigFile
	readConfigFile = func(name string) ([]byte, error) {
		*n++
		return prev(name)
	}
	t.Cleanup(func() { readConfigFile = prev })
	return n
}
