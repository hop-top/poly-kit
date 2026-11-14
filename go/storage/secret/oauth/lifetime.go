package oauth

import (
	"crypto/sha256"
	"sync"
	"time"
)

// The cached token format (a uma oauth2 envelope) has no issue time and
// cannot grow one, so the lifetime of a token minted by this process is kept
// here, keyed by the digest of its access token. Sources of the process share
// it. A token minted elsewhere has no entry and gets the full skew.
var (
	lifeMu sync.Mutex
	life   = map[[32]byte]lifeEntry{}
)

type lifeEntry struct {
	lifetime time.Duration
	expires  time.Time // wall clock, for pruning
}

const maxLifeEntries = 256

// recordLifetime notes the lifetime of the token just minted.
func recordLifetime(access string, lifetime time.Duration, expires time.Time) {
	lifeMu.Lock()
	defer lifeMu.Unlock()
	if len(life) >= maxLifeEntries {
		now := time.Now()
		for k, e := range life {
			if e.expires.Before(now) {
				delete(life, k)
			}
		}
		if len(life) >= maxLifeEntries {
			clear(life)
		}
	}
	life[sha256.Sum256([]byte(access))] = lifeEntry{lifetime, expires}
}

// effectiveSkew is skew capped at half the token's known lifetime, so a
// token that lives no longer than the skew is still reused for a while.
func effectiveSkew(skew time.Duration, access string) time.Duration {
	lifeMu.Lock()
	e, ok := life[sha256.Sum256([]byte(access))]
	lifeMu.Unlock()
	if ok && e.lifetime/2 < skew {
		return e.lifetime / 2
	}
	return skew
}
