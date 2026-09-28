package cmdsurface

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/time/rate"

	"hop.top/kit/go/ai/cmdreflect"
	"hop.top/kit/go/transport/api"
)

// ErrRateLimited is the refusal of the rate-limit gate: the caller
// has spent its tokens for the command's side-effect tier. The error
// the bridge returns is a [*RateLimitedError] wrapping it, which
// carries how long the caller should wait; read that with
// [RetryAfter]. Transports answer it as rate_limited: HTTP 429 with
// Retry-After, Connect ResourceExhausted, an MCP isError result, the
// socket's RATE_LIMITED code.
var ErrRateLimited = errors.New("cmdsurface: rate limited")

// RateTier is the side-effect band a rate limit is counted in. Reads,
// writes and destructive commands draw from separate buckets, so a
// caller browsing freely does not spend the budget that bounds how
// fast it can delete.
type RateTier string

const (
	// RateTierRead counts commands declaring kit/side-effect read.
	RateTierRead RateTier = "read"
	// RateTierWrite counts the write tiers, and every command whose
	// tier is undeclared or unrecognized: nobody said it reads only.
	RateTierWrite RateTier = "write"
	// RateTierDestructive counts the destructive tiers.
	RateTierDestructive RateTier = "destructive"
)

// RateRule is one tier's token bucket: PerMinute tokens refill
// evenly over a minute, up to Burst held at once. A zero field takes
// the tier's default from [DefaultRateLimit].
type RateRule struct {
	PerMinute int
	Burst     int
}

// RateLimit configures the rate-limit gate installed with
// [WithRateLimit]: one rule per side-effect tier.
type RateLimit struct {
	Read        RateRule
	Write       RateRule
	Destructive RateRule
}

// DefaultRateLimit returns the kit defaults, per caller:
//
//	tier          per_minute  burst
//	read          600         60
//	write         120         20
//	destructive   12          3
//
// A caller is its established principal (with its tenant), else its
// client address, else the surface.
func DefaultRateLimit() RateLimit {
	return RateLimit{
		Read:        RateRule{PerMinute: 600, Burst: 60},
		Write:       RateRule{PerMinute: 120, Burst: 20},
		Destructive: RateRule{PerMinute: 12, Burst: 3},
	}
}

// Rule returns the rule for tier, defaults filled in.
func (c RateLimit) Rule(tier RateTier) RateRule {
	def := DefaultRateLimit()
	var r, d RateRule
	switch tier {
	case RateTierRead:
		r, d = c.Read, def.Read
	case RateTierDestructive:
		r, d = c.Destructive, def.Destructive
	default:
		r, d = c.Write, def.Write
	}
	if r.PerMinute <= 0 {
		r.PerMinute = d.PerMinute
	}
	if r.Burst <= 0 {
		r.Burst = d.Burst
	}
	return r
}

// WithRateLimit installs the rate-limit gate: invocation-plane slot 7,
// after the permission gate and before anything that stores or runs.
// Every invocation on a remote surface takes one token from its
// caller's bucket for the leaf's tier, and is refused with a
// [*RateLimitedError] when the bucket is empty. The CLI and library
// surfaces are never limited. Without this option there is no limit.
//
// Buckets live in memory, per bridge: each served service counts its
// own callers.
func WithRateLimit(cfg RateLimit) Option {
	return func(c *bridgeConfig) { c.rateLimit = newRateLimiter(cfg, time.Now) }
}

// RateLimitedError is the rate-limit gate's refusal. It wraps
// [ErrRateLimited].
type RateLimitedError struct {
	// Path is the refused command's path key.
	Path string
	// Surface is the surface the call arrived on.
	Surface Surface
	// Tier is the bucket the call would have drawn from.
	Tier RateTier
	// RetryAfter is how long until the bucket holds a token again.
	RetryAfter time.Duration
}

// Error implements error.
func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("%s: %s on %s: %s tier; retry after %s",
		ErrRateLimited, e.Path, e.Surface, e.Tier, e.RetryAfter.Round(time.Millisecond))
}

// Unwrap makes errors.Is(err, ErrRateLimited) hold.
func (e *RateLimitedError) Unwrap() error { return ErrRateLimited }

// RetryAfter reports how long a refused caller should wait before
// retrying, when err carries such a hint (a [*RateLimitedError] or an
// [*OverloadedError] anywhere in its chain). The duration is never below one
// millisecond, so a surface rounding it up to its own unit never
// tells a caller to retry immediately.
func RetryAfter(err error) (time.Duration, bool) {
	var hint interface{ RetryAfterHint() time.Duration }
	if errors.As(err, &hint) {
		return max(hint.RetryAfterHint(), time.Millisecond), true
	}
	return 0, false
}

// RetryAfterHint implements the hint [RetryAfter] reads.
func (e *RateLimitedError) RetryAfterHint() time.Duration { return e.RetryAfter }

// RetryAfterSeconds is d as a Retry-After header value: whole seconds,
// rounded up, never below one.
func RetryAfterSeconds(d time.Duration) int {
	return max(int(math.Ceil(d.Seconds())), 1)
}

// RetryAfterMillis is d as a retry_after_ms value: whole milliseconds,
// rounded up, never below one.
func RetryAfterMillis(d time.Duration) int64 {
	return max(int64(math.Ceil(float64(d)/float64(time.Millisecond))), 1)
}

// rateTierOf maps a leaf's resolved side-effect tier onto its bucket.
func rateTierOf(leaf *Leaf) RateTier {
	if leaf == nil || leaf.Descriptor == nil {
		return RateTierWrite
	}
	switch leaf.Descriptor.Safety.Tier {
	case cmdreflect.TierRead:
		return RateTierRead
	case cmdreflect.TierDestructiveLocal, cmdreflect.TierDestructiveShared:
		return RateTierDestructive
	default:
		return RateTierWrite
	}
}

// rateLimit is slot 7 of the invocation plane. It returns nil when
// the gate is off, the surface is local, or a token was taken.
func (b *Bridge) rateLimit(inv Invocation, leaf *Leaf) error {
	rl := b.cfg.rateLimit
	if rl == nil || !inv.Meta.Surface.remote() {
		return nil
	}
	tier := rateTierOf(leaf)
	wait, ok := rl.take(rateKey(inv.Meta, tier), tier)
	if ok {
		return nil
	}
	return &RateLimitedError{
		Path:       leaf.PathKey(),
		Surface:    inv.Meta.Surface,
		Tier:       tier,
		RetryAfter: wait,
	}
}

// rateKey names the bucket an invocation draws from. The caller is
// the principal (with its tenant: one principal acting for two
// tenants spends two budgets), else the client address, else the
// surface, so bus and cron invocations share one bucket per surface.
//
// The principal is Meta.Caller only when the transport established
// it ([Meta.Authenticated]). A caller or tenant merely claimed never
// selects or splits a bucket: it neither escapes its address's budget
// nor spends a principal's.
func rateKey(meta Meta, tier RateTier) string {
	switch {
	case meta.Authenticated() && meta.Caller != "":
		return "p\x00" + meta.Caller + "\x00" + meta.Tenant + "\x00" + string(tier)
	case clientHost(meta) != "":
		return "a\x00" + clientHost(meta) + "\x00" + string(tier)
	default:
		return "s\x00" + string(meta.Surface) + "\x00" + string(tier)
	}
}

// clientHost is the caller's address without its port — each
// connection has its own ephemeral port, and keying on it would give
// every connection a fresh bucket. An IPv6 address is reduced to its
// /64, the block one subscriber is routinely assigned, for the same
// reason.
func clientHost(meta Meta) string {
	addr := meta.Extra["remote_addr"]
	if addr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if ip.To4() != nil {
		return ip.To4().String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// rateLimiter holds one token bucket per caller and tier.
type rateLimiter struct {
	cfg RateLimit
	now func() time.Time

	mu      sync.Mutex
	buckets map[string]*rateBucket
	// sweepAt is the bucket count at which idle buckets are dropped.
	sweepAt int
}

type rateBucket struct {
	lim  *rate.Limiter
	idle time.Duration // time to refill from empty
	last time.Time
}

// minSweep is the bucket count below which no sweep runs.
const minSweep = 1024

func newRateLimiter(cfg RateLimit, now func() time.Time) *rateLimiter {
	return &rateLimiter{cfg: cfg, now: now, buckets: map[string]*rateBucket{}, sweepAt: minSweep}
}

// take removes one token from key's bucket. When the bucket is empty
// it takes nothing and reports how long until a token is available.
func (r *rateLimiter) take(key string, tier RateTier) (wait time.Duration, ok bool) {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	bk := r.buckets[key]
	if bk == nil {
		rule := r.cfg.Rule(tier)
		every := time.Minute / time.Duration(rule.PerMinute)
		bk = &rateBucket{
			lim:  rate.NewLimiter(rate.Every(every), rule.Burst),
			idle: every * time.Duration(rule.Burst),
		}
		r.sweep(now)
		r.buckets[key] = bk
	}
	bk.last = now
	res := bk.lim.ReserveN(now, 1)
	if !res.OK() {
		return time.Minute, false
	}
	if d := res.DelayFrom(now); d > 0 {
		// Refused: give the token back, so a caller retrying early
		// does not push its own next token further out.
		res.CancelAt(now)
		return d, false
	}
	return 0, true
}

// sweep drops buckets idle long enough to have refilled completely:
// a full bucket is indistinguishable from a new one, so nothing a
// caller earned or owes is lost. It runs when the map has doubled
// since the last sweep, which keeps its cost amortized constant.
func (r *rateLimiter) sweep(now time.Time) {
	if len(r.buckets) < r.sweepAt {
		return
	}
	for k, bk := range r.buckets {
		if now.Sub(bk.last) >= bk.idle {
			delete(r.buckets, k)
		}
	}
	r.sweepAt = max(minSweep, 2*len(r.buckets))
}

// size is the number of live buckets.
func (r *rateLimiter) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buckets)
}

// rateLimitedConnectError is the Connect answer to a rate-limit
// refusal: ResourceExhausted, which Connect's HTTP mapping reports as
// 429 like REST, with Retry-After in the error's metadata.
func rateLimitedConnectError(err error) *connect.Error {
	return retryableConnectError(connect.CodeResourceExhausted, err)
}

// overloadedConnectError is the Connect answer to a capacity refusal:
// Unavailable, which Connect's HTTP mapping reports as 503 like REST,
// with Retry-After in the error's metadata.
func overloadedConnectError(err error) *connect.Error {
	return retryableConnectError(connect.CodeUnavailable, err)
}

// retryableConnectError is a Connect error of code carrying err's
// retry hint, when it has one, as Retry-After metadata.
func retryableConnectError(code connect.Code, err error) *connect.Error {
	ce := connect.NewError(code, err)
	if wait, ok := RetryAfter(err); ok {
		ce.Meta().Set("Retry-After", strconv.Itoa(RetryAfterSeconds(wait)))
	}
	return ce
}

// writeRateLimited answers a rate-limit refusal on an HTTP surface:
// 429 rate_limited, with Retry-After when the error carries a hint.
func writeRateLimited(w http.ResponseWriter, err error) {
	writeRetryable(w, http.StatusTooManyRequests, api.CodeRateLimited, err)
}

// writeOverloaded answers a capacity refusal on an HTTP surface: 503
// overloaded, with Retry-After when the error carries a hint.
func writeOverloaded(w http.ResponseWriter, err error) {
	writeRetryable(w, http.StatusServiceUnavailable, api.CodeOverloaded, err)
}

// writeRetryable answers a retryable refusal on an HTTP surface with
// status and code, and Retry-After when the error carries a hint.
func writeRetryable(w http.ResponseWriter, status int, code string, err error) {
	if wait, ok := RetryAfter(err); ok {
		api.SetRetryAfter(w.Header(), wait)
	}
	api.Error(w, status, &api.APIError{Status: status, Code: code, Message: err.Error()})
}

// MCPRefusalMetaKey is the tools/call result _meta key under which a
// refusal decided on the invocation plane carries its code and, when
// it has one, its retry hint: {"code", "retry_after_ms"}.
const MCPRefusalMetaKey = "hop.top/refusal"

// MCPRefusal returns how an MCP surface answers err as a tools/call
// result when err is a refusal with a stable code — rate_limited and
// overloaded, with their retry hints; insufficient_scope;
// deadline_exceeded, a run its per-command deadline cut short;
// idempotency_conflict or idempotency_key_reused: the text the isError
// result carries, starting with the code, and the value for the
// result's [MCPRefusalMetaKey] _meta entry. ok is false for any other
// error, which keeps its surface's own answer.
func MCPRefusal(err error) (text string, refusal map[string]any, ok bool) {
	msg := err.Error()
	var code string
	switch {
	case IdempotencyRefusalCode(err) != "":
		code = IdempotencyRefusalCode(err)
		// The bridge's message already names the code after its
		// package prefix; the text leads with the code once.
		msg = strings.TrimPrefix(msg, "cmdsurface: "+code+": ")
	case errors.Is(err, ErrDeadlineExceeded):
		code = CodeDeadlineExceeded
	case errors.Is(err, ErrInsufficientScope):
		code = api.CodeInsufficientScope
	case errors.Is(err, ErrOverloaded):
		code = CodeOverloaded
	case errors.Is(err, ErrRateLimited):
		code = api.CodeRateLimited
	default:
		return "", nil, false
	}
	refusal = map[string]any{"code": code}
	if wait, hinted := RetryAfter(err); hinted {
		refusal["retry_after_ms"] = RetryAfterMillis(wait)
	}
	return code + ": " + msg, refusal, true
}

// mcpRefusalBlock is the tools/call isError result for a gate
// refusal on the hand-rolled MCP handlers.
func mcpRefusalBlock(err error) map[string]any {
	text, refusal, ok := MCPRefusal(err)
	if !ok {
		return errorResultBlock(err.Error())
	}
	block := errorResultBlock(text)
	block["_meta"] = map[string]any{MCPRefusalMetaKey: refusal}
	return block
}
