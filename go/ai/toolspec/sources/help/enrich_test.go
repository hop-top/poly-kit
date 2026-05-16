package help

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec"
)

func TestEnrich_DeprecatedFlag(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Flags: []toolspec.Flag{
			{Name: "--old", Description: "deprecated: do not use"},
			{Name: "--current", Description: "normal flag"},
		},
	}

	EnrichFromHelp(spec, "")

	assert.True(t, spec.Flags[0].Deprecated,
		"flag with 'deprecated' in description is marked")
	assert.False(t, spec.Flags[1].Deprecated,
		"normal flag is not marked deprecated")
}

func TestEnrich_DeprecatedFlag_ReplacedBy(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Flags: []toolspec.Flag{
			{Name: "--old", Description: "deprecated, use --new instead"},
		},
	}

	EnrichFromHelp(spec, "")

	assert.True(t, spec.Flags[0].Deprecated)
	assert.Equal(t, "--new", spec.Flags[0].ReplacedBy,
		"replacement extracted from 'use X instead'")
}

func TestEnrich_OutputSchema_Json(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Flags: []toolspec.Flag{
			{Name: "--json", Description: "Output as JSON"},
		},
		Commands: []toolspec.Command{
			{Name: "list"},
		},
	}

	EnrichFromHelp(spec, "")

	require.NotNil(t, spec.Commands[0].OutputSchema,
		"--json top-level flag populates command OutputSchema")
	assert.Equal(t, "json", spec.Commands[0].OutputSchema.Format)
}

func TestEnrich_OutputSchema_Preserves(t *testing.T) {
	existing := &toolspec.OutputSchema{
		Format: "csv",
		Fields: []string{"name", "value"},
	}
	spec := &toolspec.ToolSpec{
		Flags: []toolspec.Flag{
			{Name: "--json", Description: "Output as JSON"},
		},
		Commands: []toolspec.Command{
			{Name: "list", OutputSchema: existing},
		},
	}

	EnrichFromHelp(spec, "")

	assert.Equal(t, existing, spec.Commands[0].OutputSchema,
		"existing OutputSchema not overwritten by enrichment")
}

func TestEnrich_StateIntrospection(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Commands: []toolspec.Command{
			{Name: "config"},
			{Name: "auth"},
			{Name: "list"},
		},
	}
	helpText := "Set MYCTL_TOKEN and MYCTL_CONFIG_PATH for configuration."

	EnrichFromHelp(spec, helpText)

	require.NotNil(t, spec.StateIntrospection,
		"commands + env vars populate StateIntrospection")
	assert.Contains(t, spec.StateIntrospection.ConfigCommands, "config")
	assert.Contains(t, spec.StateIntrospection.AuthCommands, "auth")
	assert.Contains(t, spec.StateIntrospection.EnvVars, "MYCTL_TOKEN")
	assert.Contains(t, spec.StateIntrospection.EnvVars, "MYCTL_CONFIG_PATH")
}

func TestEnrich_StateIntrospection_Empty(t *testing.T) {
	spec := &toolspec.ToolSpec{
		Commands: []toolspec.Command{
			{Name: "list"},
			{Name: "get"},
		},
	}

	EnrichFromHelp(spec, "no special vars here")

	assert.Nil(t, spec.StateIntrospection,
		"no matching commands or env vars leaves SI nil")
}
