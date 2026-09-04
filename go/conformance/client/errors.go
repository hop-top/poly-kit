package client

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"

	"hop.top/kit/go/console/output"
)

// Code* constants for the conformance-client sentinel set. Code values
// are emitted in the JSON envelope on stderr.
//
// Exit codes follow the reconciled 12fc taxonomy: usage-class
// rejections on the shared slot 2, auth on 5, transient service
// blips on 6, rate-limiting on kit's band code 64, and the grade
// verdicts on the band (68/69, see ExitGradeFail below).
const (
	CodeServiceUnavailable = "SERVICE_UNAVAILABLE"   // exit 6 (transient)
	CodeServiceAuthFailed  = "SERVICE_AUTH_FAILED"   // exit 5 (auth)
	CodeServiceUsage       = "SERVICE_USAGE"         // exit 2 (usage)
	CodeCassettePack       = "CASSETTE_PACK_FAILED"  // exit 1 (general local failure)
	CodeCassetteTooLarge   = "CASSETTE_TOO_LARGE"    // exit 2 (usage)
	CodeManifestParse      = "MANIFEST_PARSE_FAILED" // exit 2 (usage)
	CodeGradeFail          = "GRADE_FAIL"            // exit 68 (band)
	CodeGradeUngradable    = "GRADE_UNGRADABLE"      // exit 69 (band)
	CodeRateLimited        = "RATE_LIMITED"          // exit 64 (kit band, transient)
	CodeUnauthorized       = "UNAUTHORIZED"          // alias for service-auth
)

// Grade-verdict band codes. The 12fc taxonomy reserves 0-6 for the
// shared failure classes and leaves >6 to documented per-tool codes.
// Kit's band so far: 64 RATE_LIMITED + 65 PROVENANCE_MISSING (both in
// go/console/output) and 66 LEAK_DETECTED + 67 CONFIG (conformance
// tree). The two grade verdicts continue that allocation: they are
// tool-specific outcomes — "the graded CLI did not pass" is neither a
// usage error nor any other shared class — so they must not squat on
// the low slots agents branch on.
const (
	// ExitGradeFail is the exit code for verdict=fail: the scenario
	// was graded and did not pass.
	ExitGradeFail = 68

	// ExitGradeUngradable is the exit code for verdict=ungradable:
	// the service could not evaluate the cassette (stale story hash,
	// missing scenario, ...). Distinct from ExitGradeFail so agents
	// can branch to "re-record the cassette" instead of "fix the CLI"
	// without parsing stderr.
	ExitGradeUngradable = 69
)

// Sentinel identity values. Use errors.Is(err, ErrFoo) to match.
// Wrap with the matching constructor (e.g. ServiceUnavailableError)
// to attach detail while preserving identity.
var (
	ErrServiceUnreachable = &sentinel{code: CodeServiceUnavailable, exit: output.ExitTransient, transience: output.TransienceTransient, msg: "grade service unavailable"}
	ErrServiceUnavailable = ErrServiceUnreachable // alias to match design.md vocab
	ErrServiceAuthFailed  = &sentinel{code: CodeServiceAuthFailed, exit: 5, transience: output.TransiencePermanent, msg: "grade service auth failed"}
	ErrUnauthorized       = ErrServiceAuthFailed
	ErrServiceUsage       = &sentinel{code: CodeServiceUsage, exit: 2, transience: output.TransiencePermanent, msg: "grade service rejected request"}
	ErrCassettePack       = &sentinel{code: CodeCassettePack, exit: 1, transience: output.TransiencePermanent, msg: "could not pack cassette"}
	ErrCassetteTooLarge   = &sentinel{code: CodeCassetteTooLarge, exit: 2, transience: output.TransiencePermanent, msg: "cassette exceeds size limit"}
	ErrManifestParse      = &sentinel{code: CodeManifestParse, exit: 2, transience: output.TransiencePermanent, msg: "could not parse manifest.yaml"}
	ErrGradeFail          = &sentinel{code: CodeGradeFail, exit: ExitGradeFail, transience: output.TransiencePermanent, msg: "grade verdict: fail"}
	ErrGradeUngradable    = &sentinel{code: CodeGradeUngradable, exit: ExitGradeUngradable, transience: output.TransiencePermanent, msg: "grade verdict: ungradable"}
	ErrRateLimited        = &sentinel{code: CodeRateLimited, exit: output.ExitRateLimited, transience: output.TransienceTransient, msg: "grade service rate-limited"}
)

// sentinel is the identity-bearing typed error that satisfies error
// + AsCLIError so kit's CLI middleware preserves exit codes.
type sentinel struct {
	code       string
	exit       int
	transience string
	msg        string
}

func (s *sentinel) Error() string { return s.msg }

// AsCLIError satisfies the kit CLI conversion interface so the RunE
// middleware can render the structured envelope and main() can exit
// with the sentinel's code.
func (s *sentinel) AsCLIError() *output.Error {
	return &output.Error{Code: s.code, Message: s.msg, ExitCode: s.exit, Transience: s.transience}
}

// wrappedSentinel decorates a base sentinel with caller-supplied
// detail (message, cause, suggested fix) while preserving identity
// via Unwrap so errors.Is(err, ErrX) keeps working.
type wrappedSentinel struct {
	base    *sentinel
	message string
	cause   string
	fix     string
}

func (w *wrappedSentinel) Error() string {
	if w.message == "" {
		return w.base.msg
	}
	return w.base.msg + ": " + w.message
}

func (w *wrappedSentinel) Unwrap() error { return w.base }

func (w *wrappedSentinel) AsCLIError() *output.Error {
	return &output.Error{
		Code:         w.base.code,
		Message:      w.Error(),
		Cause:        w.cause,
		SuggestedFix: w.fix,
		ExitCode:     w.base.exit,
		Transience:   w.base.transience,
	}
}

// ServiceUnavailableError returns a wrapped ErrServiceUnavailable.
// detail summarizes the call site; cause is the underlying transport
// error message; fix nudges the operator.
func ServiceUnavailableError(detail, cause, fix string) error {
	return &wrappedSentinel{base: ErrServiceUnavailable, message: detail, cause: cause, fix: fix}
}

// ServiceAuthFailedError returns a wrapped ErrServiceAuthFailed.
func ServiceAuthFailedError(detail, cause, fix string) error {
	return &wrappedSentinel{base: ErrServiceAuthFailed, message: detail, cause: cause, fix: fix}
}

// ServiceUsageError returns a wrapped ErrServiceUsage. Covers
// well-formed-but-rejected requests (404 unknown scenario, 422
// malformed manifest, etc.).
func ServiceUsageError(detail, cause, fix string) error {
	return &wrappedSentinel{base: ErrServiceUsage, message: detail, cause: cause, fix: fix}
}

// CassettePackError returns a wrapped ErrCassettePack.
func CassettePackError(detail, cause string) error {
	return &wrappedSentinel{base: ErrCassettePack, message: detail, cause: cause}
}

// CassetteTooLargeError returns a wrapped ErrCassetteTooLarge.
func CassetteTooLargeError(detail string) error {
	return &wrappedSentinel{base: ErrCassetteTooLarge, message: detail,
		fix: "increase --max-cassette-size or split into multiple cassettes"}
}

// ManifestParseError returns a wrapped ErrManifestParse.
func ManifestParseError(detail, cause string) error {
	return &wrappedSentinel{base: ErrManifestParse, message: detail, cause: cause}
}

// GradeFailError returns a wrapped ErrGradeFail. scenarioID identifies
// the scenario; reason carries the svc-supplied summary (if any).
func GradeFailError(scenarioID, reason string) error {
	msg := "scenario " + scenarioID + " failed"
	if reason != "" {
		msg += ": " + reason
	}
	return &wrappedSentinel{base: ErrGradeFail, message: msg}
}

// GradeUngradableError returns a wrapped ErrGradeUngradable.
func GradeUngradableError(scenarioID, reason string) error {
	msg := "scenario " + scenarioID + " ungradable"
	if reason != "" {
		msg += ": " + reason
	}
	return &wrappedSentinel{base: ErrGradeUngradable, message: msg}
}

// RateLimitedError returns a wrapped ErrRateLimited.
func RateLimitedError(detail string) error {
	return &wrappedSentinel{base: ErrRateLimited, message: detail,
		fix: "wait for Retry-After or reduce request rate"}
}

// IsRetryable reports whether an error returned from one of the
// internal HTTP helpers should be re-attempted by the retry loop.
// Service-unavailable + rate-limited + transient network errors are
// retryable; auth/usage/grade-verdict errors are terminal.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrServiceUnavailable) {
		return true
	}
	if errors.Is(err, ErrRateLimited) {
		return true
	}
	return isNetworkRetryable(err)
}

// isNetworkRetryable inspects a transport error and returns true for
// the well-known "try again" signals (timeout, reset, EOF mid-read,
// transient TLS).
func isNetworkRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	// String-match fallback for transport errors that don't expose a
	// typed identity (e.g. "EOF" wrapped in net/http internals).
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "transport connection broken"):
		return true
	}
	return false
}
