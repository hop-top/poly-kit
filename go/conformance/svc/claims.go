package svc

import (
	"strings"
	"time"
)

// TokenPrefix is the canonical leading string on every minted token.
// Servers do not parse it; it exists for adopters' eyeball grep.
const TokenPrefix = "kit-conf-"

// Claim is the bearer-token authorization context. The token plaintext
// is never stored on the server; only TokenSHA256 persists.
type Claim struct {
	TokenID            string        `json:"token_id"`
	TokenSHA256        []byte        `json:"-"`
	Tenant             string        `json:"tenant"`
	Scopes             []string      `json:"scopes"`
	TierMax            int           `json:"tier_max"`
	RateQuota          RateQuota     `json:"rate_quota"`
	JudgeTokenCapDaily int           `json:"judge_token_cap_daily"`
	JudgeCacheTTL      time.Duration `json:"judge_cache_ttl"`
	CreatedAt          time.Time     `json:"created_at"`
	ExpiresAt          time.Time     `json:"expires_at"`
	Revoked            bool          `json:"revoked"`
	Description        string        `json:"description"`
}

// RateQuota is the per-claim bucket configuration. Burst is the
// instantaneous ceiling; PerMinute/PerDay are sliding-window budgets.
type RateQuota struct {
	Burst     int `json:"burst"`
	PerMinute int `json:"per_minute"`
	PerDay    int `json:"per_day"`
}

// DefaultQuota is the v1 default per-claim quota (design §8).
var DefaultQuota = RateQuota{Burst: 5, PerMinute: 30, PerDay: 1500}

// HasScope reports whether the claim grants the given scope. Wildcard
// support: scope "grade:*" matches "grade:<anything>"; "meta:*" the
// same; "list:all" matches itself only.
func (c *Claim) HasScope(want string) bool {
	if c == nil {
		return false
	}
	for _, have := range c.Scopes {
		if have == want {
			return true
		}
		// "grade:*" matches "grade:<anything>"
		if strings.HasSuffix(have, ":*") {
			head := strings.TrimSuffix(have, "*")
			if strings.HasPrefix(want, head) {
				return true
			}
		}
	}
	return false
}

// IsExpired reports whether the claim has expired relative to now.
// Zero ExpiresAt means "never expires".
func (c *Claim) IsExpired(now time.Time) bool {
	if c == nil {
		return true
	}
	if c.ExpiresAt.IsZero() {
		return false
	}
	return !c.ExpiresAt.After(now)
}
