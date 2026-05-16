package svc

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"hop.top/kit/go/transport/api"
)

// RateLimiter is the seam for per-claim rate budgets.
type RateLimiter interface {
	// Allow charges cost units against the claim's bucket. Returns
	// allowed=true when the request may proceed. snapshot carries the
	// current bucket state for header emission.
	Allow(ctx context.Context, claimID string, cost int) (allowed bool, retryAfter time.Duration, snap LimitSnapshot, err error)
}

// LimitSnapshot is the per-minute view of a claim's bucket used to set
// X-RateLimit-* response headers.
type LimitSnapshot struct {
	LimitPerMinute int
	Remaining      int
	Reset          time.Time
}

// memoryBucket holds the live state of one of the three buckets per claim.
type memoryBucket struct {
	tokens     float64
	lastRefill time.Time
}

type memoryClaimBuckets struct {
	burst, perMin, perDay memoryBucket
	quota                 RateQuota
	lastSeen              time.Time
}

// MemoryRateLimiter is the in-process driver. It keeps three buckets
// per claim: burst, per-minute, per-day. Buckets refill linearly from
// the last touch.
type MemoryRateLimiter struct {
	mu      sync.Mutex
	now     func() time.Time
	resolve func(claimID string) RateQuota
	entries map[string]*memoryClaimBuckets
}

// NewMemoryRateLimiter constructs a limiter. resolve returns the quota
// for the given claim ID (typically from ClaimStore.LookupByID).
func NewMemoryRateLimiter(resolve func(string) RateQuota) *MemoryRateLimiter {
	return &MemoryRateLimiter{
		now:     time.Now,
		resolve: resolve,
		entries: make(map[string]*memoryClaimBuckets),
	}
}

// Allow implements RateLimiter.
func (m *MemoryRateLimiter) Allow(_ context.Context, claimID string, cost int) (bool, time.Duration, LimitSnapshot, error) {
	if cost <= 0 {
		cost = 1
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	entry := m.entries[claimID]
	if entry == nil {
		quota := m.resolve(claimID)
		if quota == (RateQuota{}) {
			quota = DefaultQuota
		}
		entry = &memoryClaimBuckets{
			quota:  quota,
			burst:  memoryBucket{tokens: float64(quota.Burst), lastRefill: now},
			perMin: memoryBucket{tokens: float64(quota.PerMinute), lastRefill: now},
			perDay: memoryBucket{tokens: float64(quota.PerDay), lastRefill: now},
		}
		m.entries[claimID] = entry
	}
	entry.lastSeen = now

	// Refill each bucket linearly.
	refill := func(b *memoryBucket, capacity int, window time.Duration) {
		if capacity <= 0 {
			return
		}
		elapsed := now.Sub(b.lastRefill)
		if elapsed <= 0 {
			return
		}
		rate := float64(capacity) / window.Seconds()
		b.tokens += rate * elapsed.Seconds()
		if b.tokens > float64(capacity) {
			b.tokens = float64(capacity)
		}
		b.lastRefill = now
	}
	refill(&entry.burst, entry.quota.Burst, time.Second)
	refill(&entry.perMin, entry.quota.PerMinute, time.Minute)
	refill(&entry.perDay, entry.quota.PerDay, 24*time.Hour)

	snap := LimitSnapshot{
		LimitPerMinute: entry.quota.PerMinute,
		Remaining:      int(entry.perMin.tokens),
		Reset:          now.Add(time.Minute),
	}
	costF := float64(cost)

	// Pick the most constrained bucket and compute retry-after if any
	// bucket would be exhausted.
	deny := func(retry time.Duration) (bool, time.Duration, LimitSnapshot, error) {
		return false, retry, snap, nil
	}
	if entry.quota.Burst > 0 && entry.burst.tokens < costF {
		needed := costF - entry.burst.tokens
		rate := float64(entry.quota.Burst) / time.Second.Seconds()
		retry := time.Duration(needed/rate*float64(time.Second)) + 100*time.Millisecond
		return deny(retry)
	}
	if entry.quota.PerMinute > 0 && entry.perMin.tokens < costF {
		needed := costF - entry.perMin.tokens
		rate := float64(entry.quota.PerMinute) / time.Minute.Seconds()
		retry := time.Duration(needed/rate*float64(time.Second)) + time.Second
		return deny(retry)
	}
	if entry.quota.PerDay > 0 && entry.perDay.tokens < costF {
		needed := costF - entry.perDay.tokens
		rate := float64(entry.quota.PerDay) / (24 * time.Hour.Seconds())
		retry := time.Duration(needed/rate*float64(time.Second)) + time.Second
		return deny(retry)
	}
	entry.burst.tokens -= costF
	entry.perMin.tokens -= costF
	entry.perDay.tokens -= costF
	snap.Remaining = int(entry.perMin.tokens)
	return true, 0, snap, nil
}

// RateLimit returns a middleware that consumes 1 token from each
// authenticated claim's bucket. Auth must run before this middleware.
func RateLimit(lim RateLimiter) api.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claim := ClaimFromContext(r.Context())
			if claim == nil {
				// No claim on context: skip (likely a public endpoint).
				next.ServeHTTP(w, r)
				return
			}
			allowed, retry, snap, err := lim.Allow(r.Context(), claim.TokenID, 1)
			if err != nil {
				rid := r.Header.Get("X-Request-ID")
				WriteError(w, SvcError(CodeSvcInternal, err.Error(), "", ""), rid)
				return
			}
			setRateLimitHeaders(w, snap)
			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(retry)))
				rid := r.Header.Get("X-Request-ID")
				WriteError(w, SvcError(CodeRateLimited,
					fmt.Sprintf("rate limit exceeded (retry in %ds)", retryAfterSeconds(retry)),
					"", "wait for the bucket to refill or upgrade your tier"),
					rid)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func setRateLimitHeaders(w http.ResponseWriter, snap LimitSnapshot) {
	if snap.LimitPerMinute > 0 {
		w.Header().Set("X-RateLimit-Limit-Minute", strconv.Itoa(snap.LimitPerMinute))
		w.Header().Set("X-RateLimit-Remaining-Minute", strconv.Itoa(snap.Remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(snap.Reset.Unix(), 10))
	}
}
