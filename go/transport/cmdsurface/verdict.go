package cmdsurface

import (
	"context"
	"fmt"
)

// ReasonInsufficientScope is the discovery reason for a command the
// built-in scope check refuses the calling principal: the command
// declares kit/permissions and the caller's credential lacks a scope
// it names. Only a listing made for an established caller carries it.
const ReasonInsufficientScope = "insufficient-scope"

type probeKey struct{}

// ProbeContext marks ctx as a probe: the permission gate is asked
// what it would answer, for discovery, and nothing is to run or be
// charged. A [PermissionFunc] that charges per-caller state — a
// budget — checks [IsProbe] and only reads it on a probe.
func ProbeContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, probeKey{}, true)
}

// IsProbe reports whether ctx is a probe (see [ProbeContext]).
func IsProbe(ctx context.Context) bool {
	probe, _ := ctx.Value(probeKey{}).(bool)
	return probe
}

type admittedKey struct{}

// withAdmitted stamps ctx with the Meta of the invocation the gates
// admitted, for the command it runs.
func withAdmitted(ctx context.Context, meta Meta) context.Context {
	return context.WithValue(ctx, admittedKey{}, meta)
}

// AdmittedMeta returns the Meta of the invocation the bridge admitted
// and is now running, when ctx is that run's context — the context an
// in-process command sees. It is how a command learns who it runs
// for: the identity the transport established, not one a flag
// claims. ok is false outside a served run.
func AdmittedMeta(ctx context.Context) (Meta, bool) {
	if ctx == nil {
		return Meta{}, false
	}
	meta, ok := ctx.Value(admittedKey{}).(Meta)
	return meta, ok
}

// Verdict answers the permission gate (slot 6) for meta and leaf
// without running, charging or auditing anything: the built-in scope
// check, then the [PermissionFunc], asked on a probe context. It
// returns the refusal the gate would return — an error wrapping
// [ErrInsufficientScope] or [ErrPermissionDenied] — or nil.
//
// Discovery asks it to describe what the calling principal may run.
// The answer is advisory: the call itself still meets every gate.
func (b *Bridge) Verdict(ctx context.Context, meta Meta, leaf *Leaf) error {
	if leaf == nil {
		return nil
	}
	if err := scopeCheck(meta, leaf); err != nil {
		return err
	}
	if dec := b.Permission(ProbeContext(ctx), meta, leaf); !dec.Allowed {
		return fmt.Errorf("%w: %s on %s: %s",
			ErrPermissionDenied, leaf.PathKey(), meta.Surface, dec.Reason)
	}
	return nil
}
