package wizard

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderSummary_SortedKeys(t *testing.T) {
	results := map[string]any{
		"zeta":  "z-val",
		"alpha": "a-val",
		"mid":   "m-val",
	}
	step := &Step{Label: "Review", Kind: KindSummary}

	var buf bytes.Buffer
	renderSummary(&buf, step, results)

	out := buf.String()
	ai := indexOf(out, "alpha:")
	mi := indexOf(out, "mid:")
	zi := indexOf(out, "zeta:")
	assert.Greater(t, mi, ai, "mid should appear after alpha")
	assert.Greater(t, zi, mi, "zeta should appear after mid")
}

func TestRenderSummary_SkipsInternalKeys(t *testing.T) {
	results := map[string]any{
		"name":          "alice",
		"__summary_0":   true,
		"__internal_42": "hidden",
	}
	step := &Step{Label: "Review", Kind: KindSummary}

	var buf bytes.Buffer
	renderSummary(&buf, step, results)

	out := buf.String()
	assert.Contains(t, out, "name:")
	assert.NotContains(t, out, "__summary_0")
	assert.NotContains(t, out, "__internal_42")
}

func TestRenderSummary_CustomFormat(t *testing.T) {
	results := map[string]any{"name": "bob"}
	step := &Step{
		Label: "Review",
		Kind:  KindSummary,
		FormatFn: func(r map[string]any) string {
			return "custom: " + r["name"].(string)
		},
	}

	var buf bytes.Buffer
	renderSummary(&buf, step, results)

	out := buf.String()
	assert.Contains(t, out, "custom: bob")
	assert.NotContains(t, out, "name:")
}

func TestRenderSummary_DryRun(t *testing.T) {
	w, err := New(
		TextInput("name", "Name").WithDefault("alice"),
		Summary("Review"),
	)
	require.NoError(t, err)

	var completeCalled bool
	w.SetOnComplete(func(_ map[string]any) error {
		completeCalled = true
		return nil
	})
	w.SetDryRun(true)

	_, err = RunHeadless(context.Background(), w, map[string]any{
		"name": "alice",
	})
	require.NoError(t, err)
	assert.False(t, completeCalled, "OnComplete must not run in dry-run mode")
}

// indexOf returns the byte offset of substr in s, or -1.
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
