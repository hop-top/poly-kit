package toolspec_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"hop.top/kit/go/ai/toolspec"
)

// TestNegotiateSchemaVersion pins the KIT_TOOLSPEC_SCHEMA rule shared
// by `kit toolspec` and every `<tool> spec`: the declared version is a
// floor, a well-formed request raises the answer to the highest known
// version it allows, and nothing ever resolves below the declared one.
func TestNegotiateSchemaVersion(t *testing.T) {
	cases := []struct {
		name      string
		declared  string
		requested string
		want      string
	}{
		// Author declared 1.0 (the scaffold default).
		{"1.0/unset", "1.0", "", "1.0"},
		{"1.0/request-1.0", "1.0", "1.0", "1.0"},
		{"1.0/request-1.1", "1.0", "1.1", "1.1"},
		{"1.0/request-above-latest", "1.0", "2.0", "1.1"},
		{"1.0/request-minor-above-latest", "1.0", "1.9", "1.1"},
		{"1.0/request-below", "1.0", "0.9", "1.0"},
		{"1.0/malformed", "1.0", "garbage", "1.0"},
		{"1.0/partial", "1.0", "1", "1.0"},
		{"1.0/trailing-junk", "1.0", "1.1x", "1.0"},
		{"1.0/signed", "1.0", "+1.1", "1.0"},
		{"1.0/non-numeric-minor", "1.0", "1.x", "1.0"},
		{"1.0/three-part", "1.0", "1.1.0", "1.0"},
		{"1.0/whitespace-trimmed", "1.0", " 1.1 ", "1.1"},

		// Author declared 1.1 (what `kit toolspec` claims): never
		// downgrades, whatever the request.
		{"1.1/unset", "1.1", "", "1.1"},
		{"1.1/request-1.0", "1.1", "1.0", "1.1"},
		{"1.1/request-1.1", "1.1", "1.1", "1.1"},
		{"1.1/request-2.0", "1.1", "2.0", "1.1"},
		{"1.1/malformed", "1.1", "junk", "1.1"},

		// A declared version kit does not know is the author's call:
		// emitted as declared, never lowered to a known one.
		{"2.0/request-1.1", "2.0", "1.1", "2.0"},
		{"2.0/request-3.0", "2.0", "3.0", "2.0"},

		// A malformed declaration has no floor to compare against:
		// the label is emitted verbatim.
		{"malformed-declared", "v1", "1.1", "v1"},
		{"empty-declared", "", "1.1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := toolspec.NegotiateSchemaVersion(tc.declared, tc.requested)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestNegotiateSchemaVersion_NeverDowngrades pins the rule for the
// newest layout: every request, well-formed or not, at, above or
// below it, resolves to the declared version.
func TestNegotiateSchemaVersion_NeverDowngrades(t *testing.T) {
	for _, requested := range []string{"", "1.0", "1.1", "0.9", "2.0", "junk"} {
		t.Run(requested, func(t *testing.T) {
			got := toolspec.NegotiateSchemaVersion(toolspec.LatestSchemaVersion, requested)
			assert.Equal(t, toolspec.LatestSchemaVersion, got)
		})
	}
}

func TestLatestSchemaVersion(t *testing.T) {
	assert.Equal(t, "1.1", toolspec.LatestSchemaVersion)
	assert.Equal(t, "KIT_TOOLSPEC_SCHEMA", toolspec.SchemaVersionEnv)
}
