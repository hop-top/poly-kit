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

type userOut struct {
	Email  string                          `json:"email"`
	Cohort provenance.Cached[string]       `json:"cohort"`
	Score  provenance.Synthesized[float64] `json:"score"`
}

func TestRender_OffMode_NoEnvelope(t *testing.T) {
	provenance.SetMode(provenance.ModeOff)
	defer provenance.SetMode(provenance.ModeOff)

	in := userOut{Email: "a@b.co", Cohort: provenance.NewCached("beta", provenance.Provenance{
		Source: provenance.SourceCached, URL: "https://x", FetchedAt: time.Now().UTC(),
	}), Score: provenance.NewSynthesized(0.42, provenance.Provenance{
		Source: provenance.SourceInferred, Version: "v1",
	})}
	var buf bytes.Buffer
	require.NoError(t, provenance.Render(context.Background(), &buf, "json", in))
	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	// No envelope in ModeOff — top-level keys are the struct keys.
	assert.Contains(t, got, "email")
	assert.NotContains(t, got, "provenance")
}

func TestRender_StrictMode_HappyPath(t *testing.T) {
	provenance.SetMode(provenance.ModeStrict)
	defer provenance.SetMode(provenance.ModeOff)

	in := userOut{Email: "a@b.co", Cohort: provenance.NewCached("beta", provenance.Provenance{
		Source: provenance.SourceCached, URL: "https://x", FetchedAt: time.Now().UTC(),
	}), Score: provenance.NewSynthesized(0.42, provenance.Provenance{
		Source: provenance.SourceInferred, Version: "v1",
	})}
	var buf bytes.Buffer
	require.NoError(t, provenance.Render(context.Background(), &buf, "json", in))
	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Contains(t, got, "data")
	assert.Contains(t, got, "provenance")
	provBlock := got["provenance"].(map[string]any)
	assert.Contains(t, provBlock, "/cohort")
	assert.Contains(t, provBlock, "/score")
}

func TestRender_StrictMode_MissingProvenance_RefusesEmit(t *testing.T) {
	provenance.SetMode(provenance.ModeStrict)
	defer provenance.SetMode(provenance.ModeOff)

	type missing struct {
		Cohort provenance.Cached[string] `json:"cohort"`
	}
	in := missing{} // wrapper never populated
	var buf bytes.Buffer
	err := provenance.Render(context.Background(), &buf, "json", in)
	require.Error(t, err)
	var oe *output.Error
	require.True(t, errors.As(err, &oe), "expected *output.Error, got %T", err)
	assert.Equal(t, output.CodeProvenanceMissing, oe.Code)
	assert.Equal(t, 6, oe.ExitCode)
	assert.Contains(t, oe.Cause, "/cohort")
	// And NO bytes hit the writer.
	assert.Empty(t, buf.String())
}

func TestRender_WarnMode_EmitsAnyway(t *testing.T) {
	provenance.SetMode(provenance.ModeWarn)
	defer provenance.SetMode(provenance.ModeOff)

	type missing struct {
		Cohort provenance.Cached[string] `json:"cohort"`
	}
	in := missing{}
	var buf bytes.Buffer
	require.NoError(t, provenance.Render(context.Background(), &buf, "json", in))
	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	// Envelope still emitted; provenance block exists (possibly empty)
	assert.Contains(t, got, "data")
}

func TestRender_StrictMode_TrackerSuppliesProvenance(t *testing.T) {
	provenance.SetMode(provenance.ModeStrict)
	defer provenance.SetMode(provenance.ModeOff)

	type bare struct {
		Cohort provenance.Cached[string] `json:"cohort"`
	}
	// Wrapper is "unpopulated" (zero value); Tracker carries the provenance.
	var c provenance.Cached[string]
	in := bare{Cohort: c}

	tr := provenance.NewTracker()
	require.NoError(t, tr.Cache("/cohort", provenance.Provenance{
		Source: provenance.SourceCached, URL: "https://x", FetchedAt: time.Now().UTC(),
	}))
	ctx := provenance.WithTracker(context.Background(), tr)

	var buf bytes.Buffer
	require.NoError(t, provenance.Render(ctx, &buf, "json", in))
	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	provBlock := got["provenance"].(map[string]any)
	assert.Contains(t, provBlock, "/cohort")
}

func TestRender_WithMode_OverridesGlobal(t *testing.T) {
	provenance.SetMode(provenance.ModeOff)
	defer provenance.SetMode(provenance.ModeOff)

	type missing struct {
		Cohort provenance.Cached[string] `json:"cohort"`
	}
	in := missing{}
	ctx := provenance.WithMode(context.Background(), provenance.ModeStrict)
	var buf bytes.Buffer
	err := provenance.Render(ctx, &buf, "json", in)
	require.Error(t, err) // per-ctx mode wins over the global Off
}

func TestRender_EnvelopeKey_Configurable(t *testing.T) {
	provenance.SetMode(provenance.ModeStrict)
	defer provenance.SetMode(provenance.ModeOff)
	provenance.SetEnvelopeKey("result")
	defer provenance.SetEnvelopeKey("data")

	in := userOut{Email: "a@b.co", Cohort: provenance.NewCached("beta", provenance.Provenance{
		Source: provenance.SourceCached, URL: "https://x", FetchedAt: time.Now().UTC(),
	}), Score: provenance.NewSynthesized(0.42, provenance.Provenance{
		Source: provenance.SourceInferred, Version: "v1",
	})}
	var buf bytes.Buffer
	require.NoError(t, provenance.Render(context.Background(), &buf, "json", in))
	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Contains(t, got, "result")
	assert.NotContains(t, got, "data")
}

func TestVerify_FindsMissingPaths(t *testing.T) {
	type holder struct {
		Score provenance.Synthesized[int] `json:"score"`
	}
	var v holder // unpopulated
	err := provenance.Verify(context.Background(), v)
	require.Error(t, err)
	var oe *output.Error
	require.True(t, errors.As(err, &oe))
	assert.Contains(t, oe.Cause, "/score")
}

func TestVerify_NestedStruct(t *testing.T) {
	type inner struct {
		Cohort provenance.Cached[string] `json:"cohort"`
	}
	type outer struct {
		Email string `json:"email"`
		Inner inner  `json:"inner"`
	}
	var v outer
	err := provenance.Verify(context.Background(), v)
	require.Error(t, err)
	var oe *output.Error
	require.True(t, errors.As(err, &oe))
	assert.Contains(t, oe.Cause, "/inner/cohort")
}

func TestVerify_SliceOfStructs(t *testing.T) {
	type item struct {
		Score provenance.Synthesized[int] `json:"score"`
	}
	type list struct {
		Items []item `json:"items"`
	}
	in := list{Items: []item{{}, {}}}
	err := provenance.Verify(context.Background(), in)
	require.Error(t, err)
	var oe *output.Error
	require.True(t, errors.As(err, &oe))
	assert.Contains(t, oe.Cause, "/items/0/score")
	assert.Contains(t, oe.Cause, "/items/1/score")
}

func TestVerify_PopulatedWrapper_OK(t *testing.T) {
	type holder struct {
		Score provenance.Synthesized[int] `json:"score"`
	}
	v := holder{Score: provenance.NewSynthesized(7, provenance.Provenance{
		Source: provenance.SourceInferred, Version: "v1",
	})}
	require.NoError(t, provenance.Verify(context.Background(), v))
}
