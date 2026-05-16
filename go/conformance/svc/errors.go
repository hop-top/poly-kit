package svc

import (
	"encoding/json"
	"net/http"
	"time"

	"hop.top/kit/go/console/output"
)

// Symbolic error codes for the svc surface. Per design §10. Each code
// corresponds to an HTTP status and a kit exit-code class (1/2/3/4/5/6/64).
//
// These constants intentionally live in this package rather than
// kit/output so the scen track owns the scenario-internal codes
// (STORY_HASH_MISMATCH, JUDGE_*, GRADER_*). When kit/output gains these
// scen-side codes in a follow-up, svc will continue to reference its
// own.
const (
	CodeSvcInternal             = "SVC_INTERNAL"
	CodeGraderInternal          = "GRADER_INTERNAL"
	CodeCassetteMalformed       = "CASSETTE_MALFORMED"
	CodeCassetteManifestInvalid = "CASSETTE_MANIFEST_INVALID"
	CodeCassetteSizeExceeded    = "CASSETTE_SIZE_EXCEEDED"
	CodeCassetteGzipBomb        = "CASSETTE_GZIP_BOMB"
	CodeScenarioParseError      = "SCENARIO_PARSE_ERROR"
	CodeScenarioValidateError   = "SCENARIO_VALIDATE_ERROR"
	CodeScenarioRefMalformed    = "SCENARIO_REF_MALFORMED"
	CodeScenarioNotFound        = "SCENARIO_NOT_FOUND"
	CodeTierInvalid             = "TIER_INVALID"
	CodeTierExceedsClaim        = "TIER_EXCEEDS_CLAIM"
	CodeAcceptUnsupported       = "ACCEPT_UNSUPPORTED"
	CodeMissingBearer           = "MISSING_BEARER"
	CodeInvalidBearer           = "INVALID_BEARER"
	CodeScopeDenied             = "SCOPE_DENIED"
	CodeIdempotencyKeyConflict  = "IDEMPOTENCY_KEY_CONFLICT"
	CodeRateLimited             = "RATE_LIMITED"
	CodeJudgeQuotaExceeded      = "JUDGE_QUOTA_EXCEEDED"
	CodeJudgeUnavailable        = "JUDGE_UNAVAILABLE"
	CodeJudgeModelRejected      = "JUDGE_MODEL_REJECTED"
	CodeJudgePromptUnresolved   = "JUDGE_PROMPT_UNRESOLVED"
	CodeJudgeParseFailed        = "JUDGE_PARSE_FAILED"
	CodeProvenanceMissing       = "PROVENANCE_MISSING"
	CodeStoryHashMismatch       = "STORY_HASH_MISMATCH"
	CodeL4BNotImplemented       = "L4B_NOT_IMPLEMENTED"
)

// HTTPStatus maps an output.Error.Code to an HTTP status code per
// design §10's table. Unknown codes default to 500 (treat as internal).
func HTTPStatus(code string) int {
	switch code {
	case "OK":
		return http.StatusOK
	case CodeSvcInternal, CodeGraderInternal, CodeJudgePromptUnresolved:
		return http.StatusInternalServerError
	case CodeCassetteMalformed, CodeCassetteManifestInvalid,
		CodeScenarioParseError, CodeScenarioValidateError,
		CodeScenarioRefMalformed, CodeTierInvalid:
		return http.StatusBadRequest
	case CodeAcceptUnsupported:
		return http.StatusUnsupportedMediaType
	case CodeScenarioNotFound:
		return http.StatusNotFound
	case CodeIdempotencyKeyConflict, CodeStoryHashMismatch:
		return http.StatusConflict
	case CodeCassetteSizeExceeded, CodeCassetteGzipBomb:
		return http.StatusRequestEntityTooLarge
	case CodeMissingBearer, CodeInvalidBearer:
		return http.StatusUnauthorized
	case CodeScopeDenied, CodeTierExceedsClaim, CodeJudgeModelRejected:
		return http.StatusForbidden
	case CodeJudgeUnavailable, CodeJudgeParseFailed:
		return http.StatusBadGateway
	case CodeProvenanceMissing:
		return http.StatusUnprocessableEntity
	case CodeRateLimited, CodeJudgeQuotaExceeded:
		return http.StatusTooManyRequests
	case CodeL4BNotImplemented:
		return http.StatusNotImplemented
	}
	return http.StatusInternalServerError
}

// ExitForCode maps a Code to a kit exit code. Reuses the existing 1/2/3/4/5/6/64
// vocabulary per the task's "no new numeric codes" rule.
func ExitForCode(code string) int {
	switch code {
	case "OK":
		return 0
	case CodeSvcInternal, CodeGraderInternal:
		return 1
	case CodeCassetteMalformed, CodeCassetteManifestInvalid,
		CodeScenarioParseError, CodeScenarioValidateError,
		CodeScenarioRefMalformed, CodeTierInvalid, CodeAcceptUnsupported:
		return 2
	case CodeScenarioNotFound:
		return 3
	case CodeIdempotencyKeyConflict, CodeStoryHashMismatch,
		CodeCassetteSizeExceeded, CodeCassetteGzipBomb:
		return 4
	case CodeMissingBearer, CodeInvalidBearer, CodeScopeDenied,
		CodeTierExceedsClaim, CodeJudgeUnavailable, CodeJudgeModelRejected,
		CodeJudgePromptUnresolved, CodeJudgeParseFailed:
		return 5
	case CodeProvenanceMissing:
		return 6
	case CodeRateLimited, CodeJudgeQuotaExceeded:
		return output.ExitRateLimited
	}
	return 1
}

// SvcError returns an *output.Error with the requested code, message,
// and pre-computed ExitCode mapped from ExitForCode. Cause/SuggestedFix
// are optional; pass "" to skip.
func SvcError(code, message, cause, fix string) *output.Error {
	return &output.Error{
		Code:         code,
		Message:      message,
		Cause:        cause,
		SuggestedFix: fix,
		ExitCode:     ExitForCode(code),
	}
}

// errBody is the on-wire shape: {error: {code, message, hint,
// request_id}}. Cause is intentionally not serialized (design §10).
type errBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Hint      string `json:"hint,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// WriteError shapes err as a JSON envelope on w with the matching HTTP
// status, Cache-Control: no-store, and X-Request-ID echo.
func WriteError(w http.ResponseWriter, err *output.Error, requestID string) {
	if err == nil {
		err = SvcError(CodeSvcInternal, "unknown error", "", "")
	}
	status := HTTPStatus(err.Code)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if requestID != "" {
		w.Header().Set("X-Request-ID", requestID)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error errBody `json:"error"`
	}{
		Error: errBody{
			Code:      err.Code,
			Message:   err.Message,
			Hint:      err.SuggestedFix,
			RequestID: requestID,
		},
	})
}

// retryAfterSeconds returns a sane integer Retry-After seconds for a
// duration. Floors to 1 second for sub-second durations.
func retryAfterSeconds(d time.Duration) int {
	if d <= time.Second {
		return 1
	}
	return int(d.Round(time.Second).Seconds())
}
