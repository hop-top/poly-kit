// Error envelope re-exports for kit CLIs.
//
// The envelope itself lives in the child package
// hop.top/kit/go/console/output/envelope, which is a leaf: stdlib plus
// the YAML encoder, no lipgloss and no TTY probe. Library packages that
// only need to name a failure — a policy denial, a provenance refusal —
// import that package directly and stay off the terminal-UI stack.
//
// Everything is aliased back here so `output.Error`, `output.CodeUsage`,
// `output.ConflictError` and the rest keep resolving unchanged. These are
// type aliases, not defined types, so identity is preserved: an
// *output.Error IS an *envelope.Error, and errors.As matches across both
// spellings.
//
// The Error shape is part of the tool's evolution-versioned schema; see
// ~/.ops/docs/cli-conventions-with-kit.md §6.4 + §8.1.
package output

import (
	"io"

	"hop.top/kit/go/console/output/envelope"
)

// Error is the structured-error envelope rendered to stderr when --format
// json|yaml is set. Alias of [envelope.Error].
type Error = envelope.Error

// Transience classes carried by Error.Transience (Factor 4).
const (
	TransienceTransient = envelope.TransienceTransient
	TransiencePermanent = envelope.TransiencePermanent
	TransienceUnknown   = envelope.TransienceUnknown
)

// Standard codes mapping the cross-tool exit codes from
// ~/.ops/docs/cli-conventions-with-kit.md §8.1.
const (
	CodeOK                = envelope.CodeOK                // exit 0
	CodeGeneric           = envelope.CodeGeneric           // exit 1
	CodeUsage             = envelope.CodeUsage             // exit 2
	CodeNotFound          = envelope.CodeNotFound          // exit 3
	CodeConflict          = envelope.CodeConflict          // exit 4
	CodeUnauthorized      = envelope.CodeUnauthorized      // exit 5
	CodeTransient         = envelope.CodeTransient         // exit 6
	CodeProvenanceMissing = envelope.CodeProvenanceMissing // exit 65
	CodeRateLimited       = envelope.CodeRateLimited       // exit 64
)

// Scenario grader codes. Each maps to one of the existing numeric
// exit codes (1/2/4/5); no new numeric codes are allocated.
const (
	CodeScenarioParseError        = envelope.CodeScenarioParseError
	CodeScenarioValidateError     = envelope.CodeScenarioValidateError
	CodeScenarioSchemaUnsupported = envelope.CodeScenarioSchemaUnsupported
	CodeGraderTooOld              = envelope.CodeGraderTooOld
	CodeStoryHashMismatch         = envelope.CodeStoryHashMismatch
	CodeJudgeUnavailable          = envelope.CodeJudgeUnavailable
	CodeJudgePromptUnresolved     = envelope.CodeJudgePromptUnresolved
	CodeJudgeModelRejected        = envelope.CodeJudgeModelRejected
	CodeJudgeParseFailed          = envelope.CodeJudgeParseFailed
	CodeGraderInternal            = envelope.CodeGraderInternal
)

// Spec-assigned exit codes. See §8.1 / §8.6.
const (
	ExitGeneric           = envelope.ExitGeneric
	ExitTransient         = envelope.ExitTransient
	ExitProvenanceMissing = envelope.ExitProvenanceMissing
	ExitRateLimited       = envelope.ExitRateLimited
)

// TransienceForCode returns the default transience class for one of the
// standard codes. See [envelope.TransienceForCode].
func TransienceForCode(code string) string { return envelope.TransienceForCode(code) }

// WrapError builds an envelope that preserves err for errors.Is/As while
// rendering as code and message. See [envelope.WrapError].
func WrapError(err error, code string, exitCode int) *Error {
	return envelope.WrapError(err, code, exitCode)
}

// GenericError returns an *Error with CodeGeneric and ExitCode 1.
func GenericError(msg string) *Error { return envelope.GenericError(msg) }

// NotFoundError returns an *Error with CodeNotFound and ExitCode 3.
func NotFoundError(msg string) *Error { return envelope.NotFoundError(msg) }

// ConflictError returns an *Error with CodeConflict and ExitCode 4.
func ConflictError(msg string) *Error { return envelope.ConflictError(msg) }

// UnauthorizedError returns an *Error with CodeUnauthorized and ExitCode 5.
func UnauthorizedError(msg string) *Error { return envelope.UnauthorizedError(msg) }

// UsageError returns an *Error with CodeUsage and ExitCode 2.
func UsageError(msg string) *Error { return envelope.UsageError(msg) }

// TransientError returns an *Error with CodeTransient and ExitCode 6.
func TransientError(msg string) *Error { return envelope.TransientError(msg) }

// RateLimitedError returns an *Error with CodeRateLimited and ExitCode 64.
func RateLimitedError(msg string) *Error { return envelope.RateLimitedError(msg) }

// ProvenanceMissingError returns an *Error with CodeProvenanceMissing
// and ExitCode 65.
func ProvenanceMissingError(detail string) *Error { return envelope.ProvenanceMissingError(detail) }

// RenderError writes err to w in the requested format. format=="" or
// "table" renders human-readable plain text; JSON/YAML renders the
// envelope structurally. See [envelope.RenderError].
func RenderError(w io.Writer, format string, err *Error) error {
	return envelope.RenderError(w, format, err)
}
