// Error envelope rendering for kit CLIs.
//
// When a command fails under --format json|yaml, the error is materialized
// as a structured Error and rendered to stderr by RenderError. Plaintext
// mode (--format table or unset) prints "Code: Message\nFix: ...\n" so the
// human-readable behavior matches existing kit/fang output.
//
// The Error shape is part of the tool's evolution-versioned schema; see
// ~/.ops/docs/cli-conventions-with-kit.md §6.4 + §8.1.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// Error is the structured-error envelope rendered to stderr when --format
// json|yaml is set.
type Error struct {
	Code         string   `json:"code" yaml:"code"`
	Message      string   `json:"message" yaml:"message"`
	Cause        string   `json:"cause,omitempty" yaml:"cause,omitempty"`
	SuggestedFix string   `json:"suggested_fix,omitempty" yaml:"suggested_fix,omitempty"`
	Alternatives []string `json:"alternatives,omitempty" yaml:"alternatives,omitempty"`
	ExitCode     int      `json:"exit_code" yaml:"exit_code"`

	// Transience classifies the failure for retry decisions (Factor 4):
	// TransienceTransient (retry-worthy), TransiencePermanent (do not
	// retry), or TransienceUnknown. Constructors and WrapError populate
	// it; RenderError normalizes an unset value to TransienceUnknown so
	// every structured error carries a valid class on the wire.
	Transience string `json:"transience,omitempty" yaml:"transience,omitempty"`

	// err retains the error this envelope was built from so errors.Is
	// still matches sentinels after the RunE middleware wraps a handler
	// failure. Unexported so it stays off the wire: Cause is the
	// human-readable serialized form, this is the machine-matchable one.
	err error
}

// Transience classes carried by Error.Transience. The vocabulary is
// fixed by the spec's corrective-error model (Factor 4): agents branch
// on it to decide whether a retry can help.
const (
	// TransienceTransient marks a failure a retry may clear (rate
	// limit, timeout, upstream blip).
	TransienceTransient = "transient"
	// TransiencePermanent marks a failure retrying cannot clear
	// without changing the input or the environment.
	TransiencePermanent = "permanent"
	// TransienceUnknown marks a failure kit cannot classify. Agents
	// should treat retries as best-effort and bounded.
	TransienceUnknown = "unknown"
)

// TransienceForCode returns the default transience class for one of the
// standard codes above. Unrecognized (adopter-defined) codes map to
// TransienceUnknown; adopters set Error.Transience (or use
// WithTransience) to classify their own codes.
func TransienceForCode(code string) string {
	switch code {
	case CodeUsage, CodeNotFound, CodeConflict, CodeUnauthorized,
		CodeProvenanceMissing:
		return TransiencePermanent
	case CodeRateLimited:
		return TransienceTransient
	}
	return TransienceUnknown
}

// WrapError builds an envelope that preserves err for errors.Is/As while
// rendering as code and message. Use it wherever an existing error is
// converted into the envelope; the plain struct literal is fine when there
// is no underlying error to retain. Transience defaults from the code via
// TransienceForCode; use WithTransience to override.
func WrapError(err error, code string, exitCode int) *Error {
	if err == nil {
		return nil
	}
	return &Error{
		Code:       code,
		Message:    err.Error(),
		ExitCode:   exitCode,
		Transience: TransienceForCode(code),
		err:        err,
	}
}

// WithTransience returns a copy of e with Transience set to t, leaving
// every other field (and the retained error) untouched. Copies rather
// than mutating for the same reason as Retaining: adopters commonly
// share package-level envelopes, and writing to one would race.
func (e *Error) WithTransience(t string) *Error {
	if e == nil {
		return nil
	}
	clone := *e
	clone.Transience = t
	return &clone
}

// Retaining returns a copy of e that additionally retains err for
// errors.Is/As, leaving every rendered field untouched. This is for the
// AsCLIError passthrough path, where the adopter has already built the
// envelope and owns its Code / Message / ExitCode: the middleware only
// needs to reattach the error it came from so sentinel matching survives
// the conversion.
//
// Copies rather than mutating because adopters commonly return a shared
// package-level envelope from AsCLIError; writing to it would be a data
// race and would leak one call's error into the next. An envelope that
// already retains an error is returned unchanged, so an adopter using
// WrapError keeps the chain it chose.
func (e *Error) Retaining(err error) *Error {
	if e == nil {
		return nil
	}
	if err == nil || e.err != nil {
		return e
	}
	clone := *e
	clone.err = err
	return &clone
}

// Unwrap exposes the error this envelope was built from, so callers outside
// the handler can classify failures by sentinel instead of string-matching
// Message. Returns nil when the envelope was constructed without one.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// Error implements the error interface so adopters can return *Error
// directly from RunE without a separate AsCLIError() shim.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// AsCLIError lets *Error satisfy the conversion interface used by the
// RunE middleware so it round-trips through middleware unchanged.
func (e *Error) AsCLIError() *Error { return e }

// Standard codes mapping the cross-tool exit codes from
// ~/.ops/docs/cli-conventions-with-kit.md §8.1.
const (
	CodeOK                = "OK"                 // exit 0
	CodeGeneric           = "GENERIC"            // exit 1
	CodeUsage             = "USAGE"              // exit 2
	CodeNotFound          = "NOT_FOUND"          // exit 3
	CodeConflict          = "CONFLICT"           // exit 4
	CodeUnauthorized      = "UNAUTHORIZED"       // exit 5
	CodeProvenanceMissing = "PROVENANCE_MISSING" // exit 6 — Factor-12 strict-mode refusal
	CodeRateLimited       = "RATE_LIMITED"       // exit 64 — Factor-10 max-ops budget exceeded (§8.6)
)

// Scenario grader codes. Each maps to one of the existing numeric
// exit codes above (1/2/4/5); no new numeric codes are allocated.
//
//	for the rationale.
const (
	CodeScenarioParseError        = "SCENARIO_PARSE_ERROR"        // exit 2 — malformed YAML or unknown closed-key field
	CodeScenarioValidateError     = "SCENARIO_VALIDATE_ERROR"     // exit 2 — schema parsed but semantically broken
	CodeScenarioSchemaUnsupported = "SCENARIO_SCHEMA_UNSUPPORTED" // exit 1 — scenario.schema_version not in SupportedSchemaVersions
	CodeGraderTooOld              = "GRADER_TOO_OLD"              // exit 1 — engine_min_grader_version > GraderVersion
	CodeStoryHashMismatch         = "STORY_HASH_MISMATCH"         // exit 4 — story_ref.content_hash != sha256(story)
	CodeJudgeUnavailable          = "JUDGE_UNAVAILABLE"           // exit 5 — judge required but no AIJudge / resolver
	CodeJudgePromptUnresolved     = "JUDGE_PROMPT_UNRESOLVED"     // exit 5 — prompt_ref set but resolver returned error
	CodeJudgeModelRejected        = "JUDGE_MODEL_REJECTED"        // exit 5 — judge.model not in judge.model_allowlist
	CodeJudgeParseFailed          = "JUDGE_PARSE_FAILED"          // exit 5 — model returned non-JSON or wrong shape
	CodeGraderInternal            = "GRADER_INTERNAL"             // exit 1 — grader bug
)

// ExitProvenanceMissing is the conventional exit code for Factor-12
// strict-mode refusals. The Render boundary in
// hop.top/kit/go/runtime/provenance returns this when a Synthesized
// or Cached field has no recorded Provenance entry.
const ExitProvenanceMissing = 6

// ExitRateLimited is the conventional exit code for Factor-10 rate-limit
// refusals (--max-ops budget exceeded). See §8.1 / §8.6.
const ExitRateLimited = 64

// NotFoundError returns an *Error with CodeNotFound and ExitCode 3.
func NotFoundError(msg string) *Error {
	return &Error{Code: CodeNotFound, Message: msg, ExitCode: 3, Transience: TransiencePermanent}
}

// ConflictError returns an *Error with CodeConflict and ExitCode 4.
func ConflictError(msg string) *Error {
	return &Error{Code: CodeConflict, Message: msg, ExitCode: 4, Transience: TransiencePermanent}
}

// UnauthorizedError returns an *Error with CodeUnauthorized and ExitCode 5.
func UnauthorizedError(msg string) *Error {
	return &Error{Code: CodeUnauthorized, Message: msg, ExitCode: 5, Transience: TransiencePermanent}
}

// UsageError returns an *Error with CodeUsage and ExitCode 2.
func UsageError(msg string) *Error {
	return &Error{Code: CodeUsage, Message: msg, ExitCode: 2, Transience: TransiencePermanent}
}

// RateLimitedError returns an *Error with CodeRateLimited and
// ExitCode 64. Used by the policy middleware when --max-ops is
// exhausted (§8.6).
func RateLimitedError(msg string) *Error {
	return &Error{Code: CodeRateLimited, Message: msg, ExitCode: ExitRateLimited, Transience: TransienceTransient}
}

// ProvenanceMissingError returns an *Error with CodeProvenanceMissing
// and ExitCode 6. Returned by the kit/provenance Render boundary in
// ModeStrict when one or more Synthesized/Cached fields have no
// recorded Provenance entry.
//
// detail is a free-form string suitable for the Cause slot (typically
// the JSON-pointer list of offending fields).
func ProvenanceMissingError(detail string) *Error {
	return &Error{
		Code:    CodeProvenanceMissing,
		Message: "provenance not recorded for one or more output fields",
		Cause:   detail,
		SuggestedFix: "call provenance.Track(ctx).Synthesize or .Cache at " +
			"the fetch/derivation site; or use the kit-blessed httpwrap/" +
			"sqlwrap/execwrap source wrappers which auto-stamp",
		ExitCode:   ExitProvenanceMissing,
		Transience: TransiencePermanent,
	}
}

// RenderError writes err to w in the requested format. format=="" or
// "table" renders human-readable plain text ("Code: Message\nFix: ...");
// JSON/YAML renders the envelope structurally.
//
// RenderError always returns; the caller decides the exit code from
// err.ExitCode after rendering.
func RenderError(w io.Writer, format string, err *Error) error {
	if err == nil {
		return nil
	}
	if err.Transience == "" {
		// Structured errors must carry a valid transience class on the
		// wire even when built as a bare literal (Factor 4).
		err = err.WithTransience(TransienceUnknown)
	}
	switch format {
	case JSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(err)
	case YAML:
		return yaml.NewEncoder(w).Encode(err)
	}
	return renderErrorPlain(w, err)
}

// renderErrorPlain writes the human-readable form used by --format table
// (and the default empty format). Each populated field appears on its own
// line so the output is grep-friendly.
func renderErrorPlain(w io.Writer, err *Error) error {
	var b strings.Builder
	if err.Code != "" {
		fmt.Fprintf(&b, "%s: %s\n", err.Code, err.Message)
	} else {
		fmt.Fprintf(&b, "%s\n", err.Message)
	}
	if err.Cause != "" {
		fmt.Fprintf(&b, "Cause: %s\n", err.Cause)
	}
	if err.SuggestedFix != "" {
		fmt.Fprintf(&b, "Fix: %s\n", err.SuggestedFix)
	}
	for _, alt := range err.Alternatives {
		fmt.Fprintf(&b, "Alternative: %s\n", alt)
	}
	_, werr := io.WriteString(w, b.String())
	return werr
}
