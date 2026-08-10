package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"hop.top/kit/go/console/output"
)

func TestRenderError_Table(t *testing.T) {
	var buf bytes.Buffer
	err := &output.Error{
		Code:         output.CodeNotFound,
		Message:      "thing missing",
		SuggestedFix: "create it first",
		ExitCode:     3,
	}
	require.NoError(t, output.RenderError(&buf, output.Table, err))
	got := buf.String()
	assert.Contains(t, got, "NOT_FOUND: thing missing")
	assert.Contains(t, got, "Fix: create it first")
}

func TestRenderError_EmptyFormatIsPlain(t *testing.T) {
	var buf bytes.Buffer
	err := &output.Error{
		Code:     output.CodeGeneric,
		Message:  "boom",
		ExitCode: 1,
	}
	require.NoError(t, output.RenderError(&buf, "", err))
	got := buf.String()
	assert.Contains(t, got, "GENERIC: boom")
	// No JSON braces in plain mode.
	assert.False(t, strings.Contains(got, "{"))
}

func TestRenderError_JSON(t *testing.T) {
	var buf bytes.Buffer
	err := &output.Error{
		Code:         output.CodeConflict,
		Message:      "already exists",
		Cause:        "duplicate key",
		SuggestedFix: "use a unique name",
		Alternatives: []string{"foo-2", "foo-3"},
		ExitCode:     4,
	}
	require.NoError(t, output.RenderError(&buf, output.JSON, err))

	var got output.Error
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, output.CodeConflict, got.Code)
	assert.Equal(t, "already exists", got.Message)
	assert.Equal(t, "duplicate key", got.Cause)
	assert.Equal(t, "use a unique name", got.SuggestedFix)
	assert.Equal(t, []string{"foo-2", "foo-3"}, got.Alternatives)
	assert.Equal(t, 4, got.ExitCode)
}

func TestRenderError_YAML(t *testing.T) {
	var buf bytes.Buffer
	err := &output.Error{
		Code:     output.CodeUnauthorized,
		Message:  "forbidden",
		ExitCode: 5,
	}
	require.NoError(t, output.RenderError(&buf, output.YAML, err))
	var got output.Error
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, output.CodeUnauthorized, got.Code)
	assert.Equal(t, 5, got.ExitCode)
}

func TestRenderError_NilIsNoop(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, output.RenderError(&buf, output.JSON, nil))
	assert.Empty(t, buf.String())
}

func TestSentinelConstructors(t *testing.T) {
	tests := []struct {
		name           string
		got            *output.Error
		wantCode       string
		wantExit       int
		wantTransience string
	}{
		{"NotFound", output.NotFoundError("nope"), output.CodeNotFound, 3, output.TransiencePermanent},
		{"Conflict", output.ConflictError("dup"), output.CodeConflict, 4, output.TransiencePermanent},
		{"Unauthorized", output.UnauthorizedError("nope"), output.CodeUnauthorized, 5, output.TransiencePermanent},
		{"Usage", output.UsageError("bad flag"), output.CodeUsage, 2, output.TransiencePermanent},
		{"RateLimited", output.RateLimitedError("budget"), output.CodeRateLimited, 64, output.TransienceTransient},
		{"ProvenanceMissing", output.ProvenanceMissingError("/email"), output.CodeProvenanceMissing, 6, output.TransiencePermanent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.NotNil(t, tc.got)
			assert.Equal(t, tc.wantCode, tc.got.Code)
			assert.Equal(t, tc.wantExit, tc.got.ExitCode)
			assert.Equal(t, tc.wantTransience, tc.got.Transience)
		})
	}
}

func TestTransienceForCode(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{output.CodeUsage, output.TransiencePermanent},
		{output.CodeNotFound, output.TransiencePermanent},
		{output.CodeConflict, output.TransiencePermanent},
		{output.CodeUnauthorized, output.TransiencePermanent},
		{output.CodeProvenanceMissing, output.TransiencePermanent},
		{output.CodeRateLimited, output.TransienceTransient},
		{output.CodeGeneric, output.TransienceUnknown},
		{"ADOPTER_SPECIFIC", output.TransienceUnknown},
		{"", output.TransienceUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.code, func(t *testing.T) {
			assert.Equal(t, tc.want, output.TransienceForCode(tc.code))
		})
	}
}

func TestWrapError_DefaultsTransienceFromCode(t *testing.T) {
	base := assert.AnError
	assert.Equal(t, output.TransiencePermanent,
		output.WrapError(base, output.CodeConflict, 4).Transience)
	assert.Equal(t, output.TransienceTransient,
		output.WrapError(base, output.CodeRateLimited, 64).Transience)
	assert.Equal(t, output.TransienceUnknown,
		output.WrapError(base, output.CodeGeneric, 1).Transience)
}

func TestWithTransience_CopiesAndSets(t *testing.T) {
	orig := &output.Error{Code: "SHARED", Message: "m", ExitCode: 9}
	got := orig.WithTransience(output.TransienceTransient)
	require.NotNil(t, got)
	assert.NotSame(t, orig, got)
	assert.Equal(t, output.TransienceTransient, got.Transience)
	// Shared package-level envelopes must never be mutated in place.
	assert.Empty(t, orig.Transience)
	// Every other rendered field carries over.
	assert.Equal(t, orig.Code, got.Code)
	assert.Equal(t, orig.Message, got.Message)
	assert.Equal(t, orig.ExitCode, got.ExitCode)

	var nilErr *output.Error
	assert.Nil(t, nilErr.WithTransience(output.TransienceTransient))
}

func TestRenderError_StructuredAlwaysCarriesTransience(t *testing.T) {
	// A literal built without Transience must still render a valid
	// transience class in structured formats (spec Factor 4: every
	// structured error carries transient|permanent|unknown).
	var buf bytes.Buffer
	require.NoError(t, output.RenderError(&buf, output.JSON, &output.Error{
		Code: "ADOPTER_SPECIFIC", Message: "m", ExitCode: 9,
	}))
	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, output.TransienceUnknown, got["transience"])

	buf.Reset()
	require.NoError(t, output.RenderError(&buf, output.YAML, &output.Error{
		Code: "ADOPTER_SPECIFIC", Message: "m", ExitCode: 9,
	}))
	assert.Contains(t, buf.String(), "transience: unknown")

	// An explicit class renders untouched.
	buf.Reset()
	require.NoError(t, output.RenderError(&buf, output.JSON,
		output.RateLimitedError("budget")))
	got = nil
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, output.TransienceTransient, got["transience"])
}

func TestError_ImplementsErrorInterface(t *testing.T) {
	// *output.Error should satisfy the error interface so adopters can
	// return it directly from RunE.
	var _ error = (*output.Error)(nil)

	e := output.NotFoundError("missing thing")
	assert.Contains(t, e.Error(), "NOT_FOUND")
	assert.Contains(t, e.Error(), "missing thing")
}

func TestError_AsCLIError_RoundTrips(t *testing.T) {
	e := output.ConflictError("dup")
	got := e.AsCLIError()
	assert.Same(t, e, got)
}
