package adapters

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec"
)

// --- Render: fallback projection is lossless ---------------------

// TestKitManifest_Render_FallbackPassesThroughArgsExitCodesFlags pins
// that the ToolSpec→Manifest fallback (no prebuilt manifest) carries
// every per-command fact ToolSpec holds that the Manifest can
// express: args (required, variadic), exit codes, flag default and
// choices, retryable.
func TestKitManifest_Render_FallbackPassesThroughArgsExitCodesFlags(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Name: "mytool",
		Commands: []toolspec.Command{{
			Name: "attach",
			Args: []toolspec.Arg{
				{Name: "name", Required: true},
				{Name: "note"},
				{Name: "files", Variadic: true},
			},
			ExitCodes: []string{"OK", "NOT_FOUND"},
			Flags: []toolspec.Flag{{
				Name: "status", Short: "s", Type: "string", Description: "filter",
				Default: "TODO", Choices: []string{"TODO", "DONE"}, Required: true,
			}},
			Contract: &toolspec.Contract{Retryable: true},
		}},
	}
	var buf bytes.Buffer
	require.NoError(t, KitManifest().Render(&buf, spec))

	var m toolspec.Manifest
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	require.Len(t, m.Commands, 1)
	c := m.Commands[0]
	assert.Equal(t, []toolspec.ManifestArg{
		{Name: "name", Required: true},
		{Name: "note"},
		{Name: "files", Variadic: true},
	}, c.Args)
	assert.Equal(t, []string{"OK", "NOT_FOUND"}, c.ExitCodes)
	require.Len(t, c.Flags, 1)
	assert.Equal(t, toolspec.ManifestFlag{
		Name: "status", Short: "s", Type: "string", Description: "filter",
		Default: "TODO", Required: true, Choices: []string{"TODO", "DONE"},
	}, c.Flags[0])
	assert.True(t, c.Retryable)
}

// TestKitManifest_Render_FallbackIdempotentFaithful pins the
// idempotent mapping: the class when ToolSpec carries one, else the
// bool (true → yes), else unknown. "no" and "conditional" survive.
func TestKitManifest_Render_FallbackIdempotentFaithful(t *testing.T) {
	cases := []struct {
		name     string
		contract *toolspec.Contract
		want     string
	}{
		{"no contract", nil, toolspec.IdempotentUnknown},
		{"bool only", &toolspec.Contract{Idempotent: true}, "yes"},
		{"bool false, no class", &toolspec.Contract{Idempotent: false}, toolspec.IdempotentUnknown},
		{"class yes", &toolspec.Contract{Idempotent: true, IdempotentClass: "yes"}, "yes"},
		{"class no", &toolspec.Contract{IdempotentClass: "no"}, "no"},
		{"class conditional", &toolspec.Contract{IdempotentClass: "conditional"}, "conditional"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := &toolspec.ToolSpec{
				Name:     "mytool",
				Commands: []toolspec.Command{{Name: "run", Contract: tc.contract}},
			}
			var buf bytes.Buffer
			require.NoError(t, KitManifest().Render(&buf, spec))
			var m toolspec.Manifest
			require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
			require.Len(t, m.Commands, 1)
			assert.Equal(t, tc.want, m.Commands[0].Idempotent)
		})
	}
}
