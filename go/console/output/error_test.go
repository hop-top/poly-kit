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
		{"Generic", output.GenericError("boom"), output.CodeGeneric, 1, output.TransiencePermanent},
		{"NotFound", output.NotFoundError("nope"), output.CodeNotFound, 3, output.TransiencePermanent},
		{"Conflict", output.ConflictError("dup"), output.CodeConflict, 4, output.TransiencePermanent},
		{"Unauthorized", output.UnauthorizedError("nope"), output.CodeUnauthorized, 5, output.TransiencePermanent},
		{"Usage", output.UsageError("bad flag"), output.CodeUsage, 2, output.TransiencePermanent},
		{"RateLimited", output.RateLimitedError("budget"), output.CodeRateLimited, 64, output.TransienceTransient},
		{"Transient", output.TransientError("upstream timeout"), output.CodeTransient, 6, output.TransienceTransient},
		{"ProvenanceMissing", output.ProvenanceMissingError("/email"), output.CodeProvenanceMissing, 65, output.TransiencePermanent},
		{"ConsentRefused", output.ConsentRefusedError("refused"), output.CodeConsentRefused, 7, output.TransienceTransient},
		{"Prerequisite", output.PrerequisiteError("postgres unreachable"), output.CodePrerequisite, 70, output.TransienceTransient},
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

func TestGenericError(t *testing.T) {
	// Exit 1 is the catch-all class; the constructor closes the table so
	// adopters stop hand-rolling {ExitCode: 1} literals with no
	// Transience.
	assert.Equal(t, 1, output.ExitGeneric)

	e := output.GenericError("cache corrupt")
	require.NotNil(t, e)
	assert.Equal(t, output.CodeGeneric, e.Code)
	assert.Equal(t, output.ExitGeneric, e.ExitCode)
	assert.Equal(t, output.TransiencePermanent, e.Transience)
	assert.Equal(t, "cache corrupt", e.Message)
	assert.Equal(t, "GENERIC: cache corrupt", e.Error())
	assert.Nil(t, e.Unwrap())
}

func TestConsentRefusedError(t *testing.T) {
	// Exit 7 sits just past the spec's core 0-6 band. Pinned as a
	// literal so a later renumber has to change this line deliberately.
	assert.Equal(t, 7, output.ExitConsentRefused)
	assert.Equal(t, "CONSENT_REFUSED", output.CodeConsentRefused)

	e := output.ConsentRefusedError("refused: --confirm=no")
	require.NotNil(t, e)
	assert.Equal(t, output.CodeConsentRefused, e.Code)
	assert.Equal(t, output.ExitConsentRefused, e.ExitCode)
	assert.Equal(t, "refused: --confirm=no", e.Message)
	assert.Equal(t, "CONSENT_REFUSED: refused: --confirm=no", e.Error())

	// The whole point of the code: transient by class default, with no
	// WithTransience override at the call site. A caller clears it by
	// re-invoking with --confirm=yes.
	assert.Equal(t, output.TransienceTransient, e.Transience)
	assert.Equal(t, output.TransienceTransient,
		output.TransienceForCode(output.CodeConsentRefused))
}

func TestConsentRefusedIsDistinctFromUnauthorized(t *testing.T) {
	// Guards the separation this code exists to make. An auth or policy
	// denial is permanent; a declined confirmation is not. If these ever
	// collapse back onto one code, exit number or transience class, an
	// agent can no longer tell "never retry" from "retry with
	// --confirm=yes" on $? alone.
	consent := output.ConsentRefusedError("declined")
	auth := output.UnauthorizedError("no credentials")

	assert.NotEqual(t, auth.Code, consent.Code)
	assert.NotEqual(t, auth.ExitCode, consent.ExitCode)
	assert.NotEqual(t, auth.Transience, consent.Transience)

	assert.Equal(t, output.TransiencePermanent, auth.Transience)
	assert.Equal(t, output.TransienceTransient, consent.Transience)
}

func TestPrerequisiteError(t *testing.T) {
	// Exit 70 continues kit's contiguous extension band (64-69 are
	// taken). Pinned as a literal so a later renumber has to change
	// this line deliberately.
	assert.Equal(t, 70, output.ExitPrerequisite)
	assert.Equal(t, "PREREQUISITE", output.CodePrerequisite)

	e := output.PrerequisiteError("postgres unreachable at 127.0.0.1:5432")
	require.NotNil(t, e)
	assert.Equal(t, output.CodePrerequisite, e.Code)
	assert.Equal(t, output.ExitPrerequisite, e.ExitCode)
	assert.Equal(t, "postgres unreachable at 127.0.0.1:5432", e.Message)
	assert.Equal(t, "PREREQUISITE: postgres unreachable at 127.0.0.1:5432", e.Error())

	// Transient by class default, no WithTransience at the call site:
	// the operator starts the dependency and the identical command
	// succeeds.
	assert.Equal(t, output.TransienceTransient, e.Transience)
	assert.Equal(t, output.TransienceTransient,
		output.TransienceForCode(output.CodePrerequisite))
}

func TestPrerequisiteIsDistinctFromGenericAndTransient(t *testing.T) {
	// Guards the two separations this code exists to make.
	//
	// vs GENERIC: GENERIC means stop, the failure is uncharacterized.
	// PREREQUISITE means the invocation was correct and kit's logic
	// never ran — repair the environment and re-run verbatim. If these
	// collapse, an agent cannot tell "escalate" from "start the
	// dependency" on $? alone.
	//
	// vs TRANSIENT: TRANSIENT says a retry may clear this on its own.
	// A stopped dependency never comes up on its own, so a backoff loop
	// burns its budget and fails. Same transience class, deliberately
	// different code and exit number.
	prereq := output.PrerequisiteError("nothing listening")
	generic := output.GenericError("store corrupt")
	transient := output.TransientError("upstream timeout")

	assert.NotEqual(t, generic.Code, prereq.Code)
	assert.NotEqual(t, generic.ExitCode, prereq.ExitCode)
	assert.NotEqual(t, generic.Transience, prereq.Transience)

	assert.NotEqual(t, transient.Code, prereq.Code)
	assert.NotEqual(t, transient.ExitCode, prereq.ExitCode)
	// Transience intentionally agrees with TRANSIENT; the exit code is
	// what separates them.
	assert.Equal(t, transient.Transience, prereq.Transience)
}

func TestExtensionBandSlotsAreUnique(t *testing.T) {
	// kit allocates its extension band contiguously so no two features
	// claim the same slot. 64-69 are spoken for (RATE_LIMITED,
	// PROVENANCE_MISSING here; LEAK_DETECTED, CONFIG in
	// go/console/cli/conformance; GRADE_FAIL, GRADE_UNGRADABLE in
	// go/conformance/client). PREREQUISITE takes 70.
	//
	// The literals for the codes owned by other trees are repeated
	// rather than imported: importing them here would make the console
	// leaf depend on the conformance trees. This test's job is to fail
	// if a future allocation lands on a slot already in use.
	band := map[int]string{}
	for _, a := range []struct {
		exit int
		name string
	}{
		{output.ExitRateLimited, "RATE_LIMITED"},
		{output.ExitProvenanceMissing, "PROVENANCE_MISSING"},
		{66, "LEAK_DETECTED"},
		{67, "CONFIG"},
		{68, "GRADE_FAIL"},
		{69, "GRADE_UNGRADABLE"},
		{output.ExitPrerequisite, "PREREQUISITE"},
	} {
		if prior, dup := band[a.exit]; dup {
			t.Fatalf("exit %d claimed by both %s and %s", a.exit, prior, a.name)
		}
		band[a.exit] = a.name
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
		{output.CodeTransient, output.TransienceTransient},
		{output.CodeConsentRefused, output.TransienceTransient},
		{output.CodePrerequisite, output.TransienceTransient},
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
