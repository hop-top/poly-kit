package api

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"
)

// ErrRateLimited reports that the invocation plane's rate limit
// refused the call. The projection answers it 429 rate_limited, with
// a Retry-After header when the error carries a retry hint: a value
// in its chain with a RetryAfterHint() time.Duration method, as the
// cmdsurface bridge's refusal has.
var ErrRateLimited = errors.New("api: rate limited")

// CodeRateLimited is the refusal code of the rate-limit gate.
const CodeRateLimited = "rate_limited"

// retryAfterOf returns the retry hint err carries, if any.
func retryAfterOf(err error) (time.Duration, bool) {
	var hint interface{ RetryAfterHint() time.Duration }
	if errors.As(err, &hint) {
		return hint.RetryAfterHint(), true
	}
	return 0, false
}

// SetRetryAfter sets the Retry-After header to d in whole seconds,
// rounded up and never below one: a caller told to wait zero seconds
// would retry into the same refusal.
func SetRetryAfter(h http.Header, d time.Duration) {
	secs := max(int(math.Ceil(d.Seconds())), 1)
	h.Set("Retry-After", strconv.Itoa(secs))
}
