package cli_test

// Doc-presence gap tests for `hop.top/kit/go/console/cli`. The cli
// package's doc surface is missing a "minimal sidecar" example —
// review-surfaced as the explanation for why one-file binaries
// (foo-scrape, foo-youtube, etc.) skip kit entirely.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Gap: no minimal-sidecar example in cli docs.
//
// The cli package ships a rich Config + lots of Disable knobs, which
// signals "wedding-cake CLI". One-file utility authors (foo-scrape,
// foo-youtube) read doc.go, see ~50 lines of Config fields, and
// reach for cobra directly. The gap is a single example labeled
// "minimal sidecar" that:
//
//   - uses cli.New with only Name/Version/Short
//   - sets Disable.Format/Hints to skip the output suite
//   - registers a single Command and Execute(ctx)
//
// in <30 lines, so the reader's takeaway is "kit's overhead is
// optional, not mandatory".
//
// The example can live in:
//   - go/console/cli/example_minimal_test.go (preferred; runs as
//     a doc test)
//   - or as an inline doc-comment example on cli.New
func TestGap_MinimalSidecarExample_Missing(t *testing.T) {
	// Probe the package dir for an example_*_test.go matching the
	// minimal-sidecar shape. The file lives next to other doc tests
	// so godoc surfaces ExampleNew_minimal under cli.New.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read pkg dir: %v", err)
	}
	var found string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "example_") {
			continue
		}
		if strings.Contains(e.Name(), "minimal") || strings.Contains(e.Name(), "sidecar") {
			found = filepath.Join(".", e.Name())
			break
		}
	}
	if found == "" {
		t.Fatalf("missing minimal-sidecar example (expected example_minimal_test.go or similar)")
	}
}
