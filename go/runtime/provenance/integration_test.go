package provenance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/runtime/provenance"
)

// TestIntegration_RoundTripWithProvenance verifies the full
// adopter happy path: a CLI populates wrappers + a Tracker, calls
// Render, the JSON envelope round-trips through json.Unmarshal back
// into the same struct shape, and the consumer can re-pair via the
// envelope provenance block.
func TestIntegration_RoundTripWithProvenance(t *testing.T) {
	provenance.SetMode(provenance.ModeStrict)
	defer provenance.SetMode(provenance.ModeOff)

	type userOut struct {
		Email  string                          `json:"email"`
		Cohort provenance.Cached[string]       `json:"cohort"`
		Score  provenance.Synthesized[float64] `json:"score"`
	}
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	in := userOut{
		Email: "user@example.com",
		Cohort: provenance.NewCached("beta", provenance.Provenance{
			Source: provenance.SourceCached, URL: "https://api/cohort/u1", FetchedAt: now,
		}),
		Score: provenance.NewSynthesized(0.42, provenance.Provenance{
			Source: provenance.SourceInferred, Version: "scorer-v3",
		}),
	}

	var buf bytes.Buffer
	require.NoError(t, provenance.Render(context.Background(), &buf, "json", in))

	// Re-parse the envelope.
	var env struct {
		Data       userOut                          `json:"data"`
		Provenance map[string]provenance.Provenance `json:"provenance"`
	}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &env))

	// Data was decoded; wrapper inner values intact.
	assert.Equal(t, "user@example.com", env.Data.Email)
	assert.Equal(t, "beta", env.Data.Cohort.Value())
	assert.Equal(t, 0.42, env.Data.Score.Value())

	// Wrapper IsSet is false after unmarshal (consumers re-pair via
	// the envelope block; design §2 contract).
	assert.False(t, env.Data.Cohort.IsSet())
	assert.False(t, env.Data.Score.IsSet())

	// Envelope provenance block carries the metadata.
	assert.Contains(t, env.Provenance, "/cohort")
	assert.Contains(t, env.Provenance, "/score")
	assert.Equal(t, "https://api/cohort/u1", env.Provenance["/cohort"].URL)
	assert.Equal(t, provenance.SourceCached, env.Provenance["/cohort"].Source)
	assert.Equal(t, provenance.SourceInferred, env.Provenance["/score"].Source)
}

// TestIntegration_StrictRefusal_FullEnvelope verifies the strict-mode
// refusal returns the *output.Error envelope shape callers expect.
func TestIntegration_StrictRefusal_FullEnvelope(t *testing.T) {
	provenance.SetMode(provenance.ModeStrict)
	defer provenance.SetMode(provenance.ModeOff)

	type out struct {
		A provenance.Cached[string]       `json:"a"`
		B provenance.Synthesized[float64] `json:"b"`
	}
	in := out{} // both wrappers unpopulated

	var buf bytes.Buffer
	err := provenance.Render(context.Background(), &buf, "json", in)
	require.Error(t, err)
	var oe *output.Error
	require.True(t, errors.As(err, &oe))
	assert.Equal(t, output.CodeProvenanceMissing, oe.Code)
	assert.Equal(t, 6, oe.ExitCode)
	assert.Contains(t, oe.Cause, "/a")
	assert.Contains(t, oe.Cause, "/b")
	assert.NotEmpty(t, oe.SuggestedFix)
	// Nothing hit the writer.
	assert.Empty(t, buf.String())

	// The kit's output.RenderError can format this directly.
	var ebuf bytes.Buffer
	require.NoError(t, output.RenderError(&ebuf, "json", oe))
	assert.Contains(t, ebuf.String(), "PROVENANCE_MISSING")
}
