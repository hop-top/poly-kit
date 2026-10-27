package consent

import (
	"errors"
	"fmt"

	"hop.top/kit/go/console/output/envelope"
)

// Reason says why a check came out the way it did.
type Reason string

const (
	// ReasonAllowed: an allow grant holds.
	ReasonAllowed Reason = "allowed"
	// ReasonDenied: a deny grant holds (or, for telemetry, an env opt-out).
	ReasonDenied Reason = "denied"
	// ReasonUnset: no grant holds and none expired.
	ReasonUnset Reason = "unset"
	// ReasonExpired: the governing grant was an until grant past its expiry.
	ReasonExpired Reason = "expired"
	// ReasonDeclined: the prompt was just answered deny. Refusals only.
	ReasonDeclined Reason = "declined"
)

// ErrRefused matches every *RefusedError through errors.Is.
var ErrRefused = errors.New("consent: refused")

// RefusedError is a fail-closed refusal of one capability for one
// subject. Store and validation failures are never refusals: they come
// back as *GrantError and must also stop the caller.
//
// Exit taxonomy (AsCLIError):
//   - denied: a standing no. UNAUTHORIZED, exit 5, permanent. Retrying,
//     --yes or --confirm=yes cannot clear it; only the user changing or
//     revoking the grant can.
//   - unset, expired, declined: no standing decision. CONSENT_REFUSED,
//     exit 7, transient: cleared by asking again where a prompt can run.
type RefusedError struct {
	Capability Capability
	Subject    string
	Reason     Reason
	// Err is the prompter failure, if one caused the refusal.
	Err error
}

func (e *RefusedError) Error() string {
	who := "any subject"
	if e.Subject != "" {
		who = e.Subject
	}
	msg := fmt.Sprintf("consent: %s for %s refused: %s", e.Capability, who, e.Reason)
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *RefusedError) Unwrap() error { return e.Err }

// Is matches ErrRefused.
func (e *RefusedError) Is(target error) bool { return target == ErrRefused }

// Permanent reports whether the refusal is a standing deny.
func (e *RefusedError) Permanent() bool { return e.Reason == ReasonDenied }

// AsCLIError renders the refusal on the kit exit taxonomy, so a RunE
// can return it as is.
func (e *RefusedError) AsCLIError() *envelope.Error {
	if e.Permanent() {
		out := envelope.UnauthorizedError(e.Error())
		out.SuggestedFix = fmt.Sprintf("%s was denied; only the user can change or revoke that decision", e.Capability)
		return out
	}
	out := envelope.ConsentRefusedError(e.Error())
	out.SuggestedFix = fmt.Sprintf("grant %s where a consent prompt can run, then retry", e.Capability)
	return out
}
