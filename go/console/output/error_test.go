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
		name     string
		got      *output.Error
		wantCode string
		wantExit int
	}{
		{"NotFound", output.NotFoundError("nope"), output.CodeNotFound, 3},
		{"Conflict", output.ConflictError("dup"), output.CodeConflict, 4},
		{"Unauthorized", output.UnauthorizedError("nope"), output.CodeUnauthorized, 5},
		{"Usage", output.UsageError("bad flag"), output.CodeUsage, 2},
		{"ProvenanceMissing", output.ProvenanceMissingError("/email"), output.CodeProvenanceMissing, 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.NotNil(t, tc.got)
			assert.Equal(t, tc.wantCode, tc.got.Code)
			assert.Equal(t, tc.wantExit, tc.got.ExitCode)
		})
	}
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
