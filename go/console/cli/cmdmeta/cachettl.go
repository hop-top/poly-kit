package cmdmeta

import (
	"fmt"
	"strings"
	"time"
)

// ParseCacheTTL parses a kit/cache-ttl value ([KeyCacheTTL]). Anything
// that is not a Go duration greater than zero is an error.
func ParseCacheTTL(v string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("%s %q: want a Go duration such as 30s or 5m", KeyCacheTTL, v)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s %q: want a duration greater than zero", KeyCacheTTL, v)
	}
	return d, nil
}
