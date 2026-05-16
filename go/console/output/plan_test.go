package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"hop.top/kit/go/console/output"
)

// localEffect mirrors cli.Effect. Defined here to keep the output
// package free of any dependency on cli (RenderPlan accepts any).
type localEffect struct {
	Kind       string `json:"kind"             table:"KIND"`
	Target     string `json:"target"           table:"TARGET"`
	Reversible bool   `json:"reversible"       table:"REVERSIBLE"`
	Detail     string `json:"detail,omitempty" table:"DETAIL"`
}

// localPlan mirrors cli.Plan. Same field set + same JSON tags so
// RenderPlan's reflection-based table renderer treats it identically.
type localPlan struct {
	Command              string         `json:"command"                          table:"COMMAND"`
	Args                 map[string]any `json:"args,omitempty"`
	Effects              []localEffect  `json:"effects"                          table:"-"`
	PrerequisitesChecked []string       `json:"prerequisites_checked,omitempty"`
	Warnings             []string       `json:"warnings,omitempty"`
	GeneratedAt          time.Time      `json:"generated_at"`
}

func samplePlan() localPlan {
	return localPlan{
		Command: "tool create thing",
		Args:    map[string]any{"name": "alpha"},
		Effects: []localEffect{
			{Kind: "create", Target: "thing:alpha", Reversible: true, Detail: "new resource"},
			{Kind: "update", Target: "index", Reversible: false, Detail: ""},
		},
		PrerequisitesChecked: []string{"auth"},
		Warnings:             []string{"replaces existing"},
		GeneratedAt:          time.Date(2026, 5, 2, 15, 0, 0, 0, time.UTC),
	}
}

// TestRenderPlan_Table_FormatsEffects confirms the table form prints
// the header block and an effects table containing each effect's
// fields.
func TestRenderPlan_Table_FormatsEffects(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, output.RenderPlan(&buf, output.Table, samplePlan()))

	out := buf.String()

	// Header block: COMMAND + GENERATED.
	assert.Contains(t, out, "COMMAND")
	assert.Contains(t, out, "tool create thing")
	assert.Contains(t, out, "GENERATED")

	// Warnings block.
	assert.Contains(t, out, "WARNINGS")
	assert.Contains(t, out, "replaces existing")

	// Effects table — column headers + values.
	assert.Contains(t, out, "EFFECTS")
	assert.Contains(t, out, "KIND")
	assert.Contains(t, out, "TARGET")
	assert.Contains(t, out, "REVERSIBLE")
	assert.Contains(t, out, "DETAIL")
	assert.Contains(t, out, "create")
	assert.Contains(t, out, "thing:alpha")
	assert.Contains(t, out, "true")
	assert.Contains(t, out, "new resource")
	assert.Contains(t, out, "update")
	assert.Contains(t, out, "index")
}

// TestRenderPlan_JSON_StableField asserts every JSON key declared on
// the Plan/Effect shape is present and round-trips with stable values.
func TestRenderPlan_JSON_StableField(t *testing.T) {
	var buf bytes.Buffer
	plan := samplePlan()
	require.NoError(t, output.RenderPlan(&buf, output.JSON, plan))

	// Top-level keys must all be present.
	for _, key := range []string{
		"command", "args", "effects", "prerequisites_checked",
		"warnings", "generated_at",
	} {
		assert.Contains(t, buf.String(), `"`+key+`"`,
			"JSON output must contain key %q", key)
	}

	// Effect-level keys.
	for _, key := range []string{"kind", "target", "reversible", "detail"} {
		assert.Contains(t, buf.String(), `"`+key+`"`,
			"JSON output must contain effect key %q", key)
	}

	// Round-trip into a generic map to verify field values.
	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "tool create thing", got["command"])

	effects, ok := got["effects"].([]any)
	require.True(t, ok)
	require.Len(t, effects, 2)
	first := effects[0].(map[string]any)
	assert.Equal(t, "create", first["kind"])
	assert.Equal(t, "thing:alpha", first["target"])
	assert.Equal(t, true, first["reversible"])
	assert.Equal(t, "new resource", first["detail"])
}

// TestRenderPlan_JSON_OmitsEmptyOptional confirms omitempty fires for
// the optional fields when they are zero, so JSON output stays minimal
// for trivial plans.
func TestRenderPlan_JSON_OmitsEmptyOptional(t *testing.T) {
	plan := localPlan{
		Command:     "tool ping",
		Effects:     []localEffect{},
		GeneratedAt: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
	}
	var buf bytes.Buffer
	require.NoError(t, output.RenderPlan(&buf, output.JSON, plan))

	out := buf.String()
	assert.NotContains(t, out, `"args"`)
	assert.NotContains(t, out, `"prerequisites_checked"`)
	assert.NotContains(t, out, `"warnings"`)
	// Required fields still present.
	assert.Contains(t, out, `"command"`)
	assert.Contains(t, out, `"effects"`)
	assert.Contains(t, out, `"generated_at"`)
}

// TestRenderPlan_YAML serializes the plan as YAML.
func TestRenderPlan_YAML(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, output.RenderPlan(&buf, output.YAML, samplePlan()))

	var got map[string]any
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "tool create thing", got["command"])
}

// TestRenderPlan_UnknownFormat returns an error matching Render's
// wording for diagnostic consistency.
func TestRenderPlan_UnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	err := output.RenderPlan(&buf, "xml", samplePlan())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown output format")
	assert.Contains(t, strings.ToLower(err.Error()), "xml")
}

// TestRenderPlan_NoEffects_TableNotEmpty confirms the (no effects)
// notice surfaces when the Effects slice is empty.
func TestRenderPlan_NoEffects_TableNotEmpty(t *testing.T) {
	plan := localPlan{
		Command:     "tool noop",
		Effects:     []localEffect{},
		GeneratedAt: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
	}
	var buf bytes.Buffer
	require.NoError(t, output.RenderPlan(&buf, output.Table, plan))
	assert.Contains(t, buf.String(), "(no effects)")
}
