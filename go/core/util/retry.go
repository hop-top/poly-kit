package util

import (
	"context"
	"math"
	"math/rand"
	"time"
)

// RetryConfig controls exponential backoff behavior.
type RetryConfig struct {
	MaxAttempts int           // 0 = infinite
	BaseDelay   time.Duration // default 100ms
	MaxDelay    time.Duration // default 30s
	Jitter      bool          // default true
}

func (c RetryConfig) baseDelay() time.Duration {
	if c.BaseDelay <= 0 {
		return 100 * time.Millisecond
	}
	return c.BaseDelay
}

func (c RetryConfig) maxDelay() time.Duration {
	if c.MaxDelay <= 0 {
		return 30 * time.Second
	}
	return c.MaxDelay
}

// Backoff returns the delay for attempt n (0-indexed).
func (c RetryConfig) Backoff(attempt int) time.Duration {
	base := c.baseDelay()
	max := c.maxDelay()

	delay := time.Duration(float64(base) * math.Pow(2, float64(attempt)))
	if delay > max || delay <= 0 { // overflow guard
		delay = max
	}

	if c.Jitter {
		delay = time.Duration(rand.Int63n(int64(delay) + 1))
	}
	return delay
}

// Retry calls fn until it returns nil or config limits are reached.
func Retry(ctx context.Context, cfg RetryConfig, fn func() error) error {
	for attempt := 0; ; attempt++ {
		if err := fn(); err == nil {
			return nil
		} else if cfg.MaxAttempts > 0 && attempt+1 >= cfg.MaxAttempts {
			return err
		} else {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(cfg.Backoff(attempt)):
			}
		}
	}
}

// RetryWithBackoff is a convenience: infinite attempts, 100ms base, 30s cap, jitter.
func RetryWithBackoff(ctx context.Context, fn func() error) error {
	return Retry(ctx, RetryConfig{Jitter: true}, fn)
}
