package cmdmeta_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"hop.top/kit/go/console/cli/cmdmeta"
)

// TestParseArgs pins the kit/args grammar every reader shares: a
// comma list, "?" optional, "..." variadic, "...?" optional
// variadic, anything else part of the name.
func TestParseArgs(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []cmdmeta.ArgSpec
	}{
		{"empty", "", nil},
		{"blank", "  ,, ", nil},
		{"required", "name", []cmdmeta.ArgSpec{{Name: "name"}}},
		{"optional", "name?", []cmdmeta.ArgSpec{{Name: "name", Optional: true}}},
		{"variadic required", "files...", []cmdmeta.ArgSpec{{Name: "files", Variadic: true}}},
		{"variadic optional", "files...?", []cmdmeta.ArgSpec{{Name: "files", Optional: true, Variadic: true}}},
		{"mixed list, trimmed", " name , note? , files...? ", []cmdmeta.ArgSpec{
			{Name: "name"},
			{Name: "note", Optional: true},
			{Name: "files", Optional: true, Variadic: true},
		}},
		// The markers are read in one order only: dots, then the
		// question mark. Anything else stays in the name.
		{"dots after the marker stay in the name", "files?...", []cmdmeta.ArgSpec{{Name: "files?", Variadic: true}}},
		{"two dots stay in the name", "files..", []cmdmeta.ArgSpec{{Name: "files.."}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, cmdmeta.ParseArgs(tc.raw))
		})
	}
}

func TestKeyArgs(t *testing.T) {
	assert.Equal(t, "kit/args", cmdmeta.KeyArgs)
}
