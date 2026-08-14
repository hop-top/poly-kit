package output

import "io"

// SetStderrWriterForTest redirects the package-level provenance footer
// destination to w and returns a func restoring the previous value.
//
// Declared in a _test.go file, so it is compiled only into this
// package's test binaries and is not part of the public API.
func SetStderrWriterForTest(w io.Writer) func() {
	prev := stderrWriter
	stderrWriter = w
	return func() { stderrWriter = prev }
}
