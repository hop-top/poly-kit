package api

import "net/http"

// Exit codes from the kit taxonomy in go/console/output. They are
// repeated as untyped constants rather than imported because
// go/console/output is a console-layer package and the transport
// layer does not otherwise depend on it; the numbers are contract,
// not implementation detail.
const (
	exitOK                = 0
	exitGeneric           = 1
	exitUsage             = 2
	exitNotFound          = 3
	exitConflict          = 4
	exitUnauthorized      = 5
	exitTransient         = 6
	exitRateLimited       = 64
	exitProvenanceMissing = 65
)

// exitStatusTable maps a command's exit code onto the HTTP status the
// projection answers with. It is the single mapping: the handler
// consults it and the README documents it from the same list.
//
// The shape of the mapping follows one rule — the status must tell a
// caller what to DO, not merely restate that something failed:
//
//   - 0 OK is the only success.
//   - USAGE is the caller's request being wrong (400), not the
//     server's fault. It is the one code most likely to be a
//     malformed projection request rather than a command failure.
//   - NOT_FOUND and CONFLICT map to their exact HTTP equivalents.
//   - UNAUTHORIZED maps to 403, not 401. 401 invites the client to
//     retry with credentials, and the projection's auth already ran
//     and passed before the command executed: the refusal is about
//     what this authenticated caller may do, which is 403's meaning.
//   - TRANSIENT and RATE_LIMITED map to 503 and 429, the two statuses
//     a retry wrapper already knows how to back off on.
//   - PROVENANCE_MISSING is a refusal to act on unverifiable input,
//     which is 422: the request was well-formed but cannot be acted
//     upon.
//   - Anything else, GENERIC included, is 500.
var exitStatusTable = map[int]int{
	exitOK:                http.StatusOK,
	exitGeneric:           http.StatusInternalServerError,
	exitUsage:             http.StatusBadRequest,
	exitNotFound:          http.StatusNotFound,
	exitConflict:          http.StatusConflict,
	exitUnauthorized:      http.StatusForbidden,
	exitTransient:         http.StatusServiceUnavailable,
	exitRateLimited:       http.StatusTooManyRequests,
	exitProvenanceMissing: http.StatusUnprocessableEntity,
}

// StatusForExitCode returns the HTTP status a command exiting with
// code should produce. Unmapped codes are 500: an exit code the
// taxonomy does not define is an unclassified failure, and reporting
// it as a server error is the honest answer.
func StatusForExitCode(code int) int {
	if s, ok := exitStatusTable[code]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// ExitStatusPair is one row of the exit-code mapping, for callers
// that render the table (documentation generators, the discovery
// endpoint's legend).
type ExitStatusPair struct {
	ExitCode int `json:"exit_code"`
	Status   int `json:"status"`
}

// ExitStatusTable returns the mapping in ascending exit-code order. A
// caller rendering the table iterates this rather than hard-coding
// rows, so a code added above appears without a second edit.
func ExitStatusTable() []ExitStatusPair {
	codes := []int{
		exitOK, exitGeneric, exitUsage, exitNotFound, exitConflict,
		exitUnauthorized, exitTransient, exitRateLimited,
		exitProvenanceMissing,
	}
	out := make([]ExitStatusPair, 0, len(codes))
	for _, c := range codes {
		out = append(out, ExitStatusPair{ExitCode: c, Status: exitStatusTable[c]})
	}
	return out
}

// Refusal codes the projection answers with when a command is not
// executed at all. They are distinct from an exit-code mapping: the
// command never ran, so there is no exit code to translate.
const (
	// CodeNotInvocable is returned when a caller addresses a
	// command the projection did not mount. The descriptor's reason
	// travels in the message so the answer says WHY, not merely
	// that the route is absent.
	CodeNotInvocable = "not_invocable"
	// CodeDestructiveBlocked is returned when policy refuses a
	// destructive command on this surface.
	CodeDestructiveBlocked = "destructive_blocked"
	// CodePermissionDenied is returned when the permission gate
	// refuses a command for this caller. The message carries the
	// gate's stable reason.
	CodePermissionDenied = "permission_denied"
)

// StatusNotInvocable is the status for addressing a non-invocable
// command.
//
// 404 rather than 403: the projection never mounted the route, so
// from HTTP's point of view the resource does not exist here. The
// body still carries the stable reason, which is what distinguishes
// "no such command" from "that command exists and is withheld" —
// a distinction the status code alone cannot carry without implying
// the caller could fix it by authenticating.
const StatusNotInvocable = http.StatusNotFound
