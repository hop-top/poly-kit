package uri_test

import (
	"testing"

	"hop.top/kit/go/conformance/harness"
	uricmd "hop.top/kit/go/console/uri"
)

// TestCommand_JSONOutputMatchesDeclaredSchema validates what each
// structured uri leaf prints under --format json against the schema
// it declares, so a schema cannot name a row type the leaf does not
// render.
func TestCommand_JSONOutputMatchesDeclaredSchema(t *testing.T) {
	cases := map[string][]string{
		"parse":            {"parse", "tlc://org/repo/T-0001?cmd=task&verb=claim"},
		"resolve":          {"resolve", "tlc://org/repo/T-0001?name=task&action=claim"},
		"complete type":    {"complete", "--type", "task", "--prefix", "T-"},
		"complete vanity":  {"complete", "--input", "task://mune"},
		"handler id":       {"handler", "id"},
		"handler generate": {"handler", "generate", "--platform", "linux"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			harness.AssertJSONSchema(t, uricmd.Command(testConfig()), harness.Args(args...))
		})
	}
}
