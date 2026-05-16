//go:build redis

// redis-backed rate limiter. Kept behind a build tag so the base
// dep tree does not pull in github.com/redis/go-redis/v9 by default.
// Operators opt in with:
//
//	go build -tags redis ./cmd/kit
//
// The actual implementation is left as a follow-up; this file simply
// pins the build seam. When the redis follow-up lands, replace the
// stub with INCR + EXPIRE-based bucket logic.

package svc

import (
	"context"
	"fmt"
	"time"
)

// NewRedisRateLimiter returns the redis-backed RateLimiter. The redis
// follow-up wires the real driver; v1 returns ErrRedisUnimplemented.
func NewRedisRateLimiter(_ string) (RateLimiter, error) {
	return nil, fmt.Errorf("redis driver: not implemented in this build (follow-up track)")
}

// redisStub exists only so the file is non-empty when the tag is
// active before the follow-up lands.
type redisStub struct{}

func (redisStub) Allow(context.Context, string, int) (bool, time.Duration, LimitSnapshot, error) {
	return false, 0, LimitSnapshot{}, fmt.Errorf("redis driver: not implemented")
}
