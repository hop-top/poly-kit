package cmdsurface

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

// ErrQuotaExceeded is the refusal of the quota gate (slot 9): the
// caller has used its calls or bytes for the current window. The
// error the bridge returns is a [*QuotaExceededError] wrapping it,
// which carries when the window resets; read the wait with
// [RetryAfter].
//
// It wraps [ErrRateLimited], so a transport that predates the class
// answers it as a rate limit. Transports that know it answer
// quota_exceeded: HTTP 429 with Retry-After at the window reset,
// Connect ResourceExhausted, an MCP isError result, the socket's
// QUOTA_EXCEEDED code.
var ErrQuotaExceeded error = quotaExceeded{}

// quotaExceeded is [ErrQuotaExceeded]'s type: a sentinel of its own
// spelling that still unwraps to [ErrRateLimited].
type quotaExceeded struct{}

func (quotaExceeded) Error() string { return "cmdsurface: quota exceeded" }
func (quotaExceeded) Unwrap() error { return ErrRateLimited }

// CodeQuotaExceeded is the refusal code of the quota gate, the same
// string on every surface that knows the class.
const CodeQuotaExceeded = "quota_exceeded"

// QuotaPer is what a quota is counted per.
type QuotaPer string

const (
	// QuotaPerPrincipal counts per established principal, with its
	// tenant: one principal acting for two tenants has two quotas.
	QuotaPerPrincipal QuotaPer = "principal"
	// QuotaPerTenant counts per tenant: every principal of a tenant
	// shares one quota. A caller with no tenant counts per principal.
	QuotaPerTenant QuotaPer = "tenant"
)

// Quota configures the quota gate installed with [WithQuota].
type Quota struct {
	// Scope names the quota in the ledger, so services sharing a
	// ledger keep separate counts: the service name.
	Scope string
	// Per is what the quota is counted per; empty is
	// [QuotaPerPrincipal].
	Per QuotaPer
	// Window is the length of the fixed windows the quota resets
	// on, aligned to the Unix epoch: an hour, a day (from midnight
	// UTC). At least a second.
	Window time.Duration
	// Ops is how many calls a window admits; 0 is no call limit.
	Ops int64
	// Bytes is how many bytes of output — stdout, stderr and
	// structured data — a window's calls may produce; 0 is no byte
	// limit. The call that crosses it completes; the next is refused.
	Bytes int64
}

// WithQuota installs the quota gate: invocation-plane slot 9, after
// the rate limit and any replay, before confirmation. A call on a
// remote surface whose caller has used its quota for the window is
// refused with a [*QuotaExceededError]; one that runs successfully is
// counted in slot 13, after the run: one call, and the bytes it
// produced. A result served from the cache runs nothing and counts
// nothing. The counts live in ledger, so a restart resets nothing.
//
// The caller is its established principal (see [Meta.Authenticated]),
// else its client address, else the surface. The CLI and library
// surfaces are never counted.
func WithQuota(q Quota, ledger *UsageLedger) Option {
	return func(c *bridgeConfig) {
		if ledger == nil || q.Window < time.Second || (q.Ops <= 0 && q.Bytes <= 0) {
			return
		}
		c.quota = &quotaGate{q: q, ledger: ledger}
	}
}

// quotaGate is the installed quota.
type quotaGate struct {
	q      Quota
	ledger *UsageLedger
}

// QuotaExceededError is the quota gate's refusal. It wraps
// [ErrQuotaExceeded].
type QuotaExceededError struct {
	// Path is the refused command's path key.
	Path string
	// Surface is the surface the call arrived on.
	Surface Surface
	// Limit names the limit reached: "ops" or "bytes".
	Limit string
	// Max is the limit; Used is what the window has counted.
	Max, Used int64
	// Window is the quota's window length.
	Window time.Duration
	// Reset is when the window ends and the quota starts over.
	Reset time.Time
	// RetryAfter is how long until Reset.
	RetryAfter time.Duration
}

// Error implements error.
func (e *QuotaExceededError) Error() string {
	return fmt.Sprintf("%s: %s on %s: %d of %d %s per %s used; resets at %s",
		ErrQuotaExceeded, e.Path, e.Surface, e.Used, e.Max, e.Limit, e.Window,
		e.Reset.UTC().Format(time.RFC3339))
}

// Unwrap makes errors.Is(err, ErrQuotaExceeded) hold, and through it
// errors.Is(err, ErrRateLimited).
func (e *QuotaExceededError) Unwrap() error { return ErrQuotaExceeded }

// RetryAfterHint implements the hint [RetryAfter] reads: the time
// until the window resets.
func (e *QuotaExceededError) RetryAfterHint() time.Duration { return e.RetryAfter }

// QuotaKey is the ledger key the quota q counts meta's caller under.
// The management verbs use it to find and reset a caller's count.
func QuotaKey(q Quota, meta Meta) string {
	prefix := "quota/" + url.PathEscape(q.Scope) + "/"
	switch {
	case meta.Authenticated() && q.Per == QuotaPerTenant && meta.Tenant != "":
		return prefix + "tenant/" + url.PathEscape(meta.Tenant)
	case meta.Authenticated() && meta.Caller != "":
		return prefix + "principal/" + url.PathEscape(meta.Caller) + "/" + url.PathEscape(meta.Tenant)
	case clientHost(meta) != "":
		return prefix + "address/" + url.PathEscape(clientHost(meta))
	default:
		return prefix + "surface/" + url.PathEscape(string(meta.Surface))
	}
}

// quotaCheck is slot 9. It returns nil when the gate is off, the
// surface is local, or the caller has quota left, and records on adm
// the key slot 13 counts the run under.
func (b *Bridge) quotaCheck(ctx context.Context, adm *Admission) error {
	g := b.cfg.quota
	if g == nil || !adm.inv.Meta.Surface.remote() {
		return nil
	}
	key := QuotaKey(g.q, adm.inv.Meta)
	u, err := g.ledger.Usage(ctx, key, g.q.Window)
	if err != nil {
		// A quota nobody can count is not enforced by admitting
		// everything.
		return fmt.Errorf("cmdsurface: quota: %s on %s: %w", adm.leaf.PathKey(), adm.inv.Meta.Surface, err)
	}
	limit, max, used := "", int64(0), int64(0)
	switch {
	case g.q.Ops > 0 && u.Ops >= g.q.Ops:
		limit, max, used = "ops", g.q.Ops, u.Ops
	case g.q.Bytes > 0 && u.Bytes >= g.q.Bytes:
		limit, max, used = "bytes", g.q.Bytes, u.Bytes
	default:
		adm.quotaKey = key
		return nil
	}
	return &QuotaExceededError{
		Path:       adm.leaf.PathKey(),
		Surface:    adm.inv.Meta.Surface,
		Limit:      limit,
		Max:        max,
		Used:       used,
		Window:     g.q.Window,
		Reset:      u.Reset,
		RetryAfter: max0(time.Until(u.Reset)),
	}
}

// recordQuota is slot 13's quota half: one call and n bytes against
// the key the admission was checked under. Best-effort: the run has
// happened, and a count that cannot be written is not a reason to
// fail it.
func (a *Admission) recordQuota(ctx context.Context, n int64) {
	g := a.b.cfg.quota
	if g == nil || a.quotaKey == "" {
		return
	}
	_, _ = g.ledger.Add(context.WithoutCancel(ctx), a.quotaKey, g.q.Window, 1, n)
}

// resultBytes is the output a result carries: stdout, stderr, and the
// structured data as JSON.
func resultBytes(res Result) int64 {
	n := int64(len(res.Stdout) + len(res.Stderr))
	if res.Data != nil {
		if raw, err := json.Marshal(res.Data); err == nil {
			n += int64(len(raw))
		}
	}
	return n
}

func max0(d time.Duration) time.Duration {
	if d < time.Millisecond {
		return time.Millisecond
	}
	return d
}
