// Package upgrade provides standardized self-upgrade logic for hop family CLIs.
//
// Features:
//   - Version check against GitHub releases (or custom URL)
//   - XDG-compliant state (snooze, cache)
//   - Safe binary self-replacement
//   - Multi-interface: CLI, TUI (Bubble Tea), REPL, SKILL preamble
//   - Opt-in bus emission for lifecycle moments (released, downloaded,
//     installed, snoozed). Adopters wire WithPublisher to subscribe
//     for UI, telemetry, or CI gates; topics are overridable via
//     WithTopicPrefix or WithTopics. Without a publisher, the Checker
//     never touches the bus and behaves exactly as before.
package upgrade

import (
	"context"
	"time"
)

// Checker performs version checks and drives upgrade flows.
type Checker struct {
	cfg Config
}

// New returns a Checker configured with the given options.
func New(opts ...Option) *Checker {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	return &Checker{cfg: cfg}
}

// Check fetches the latest release info, applies cache rules, and returns
// a Result. Always non-nil; errors are embedded in Result.Err.
// Snooze is handled separately by ShouldNotify.
//
// When a publisher is configured AND the result is fresh (not served
// from cache) AND an update is available, Check emits a Released event
// on the bus. Cached hits are silent to avoid spam on repeat polls.
func (c *Checker) Check(ctx context.Context) *Result {
	cached, err := loadCachedResult(c.cfg.StateDir, c.cfg.BinaryName)
	if err == nil && cached != nil && time.Since(cached.CheckedAt) < c.cfg.CacheTTL {
		return cached
	}

	latest, err := fetchLatest(ctx, c.cfg)
	if err != nil {
		return &Result{Err: err, CheckedAt: time.Now()}
	}

	r := &Result{
		Current:     c.cfg.CurrentVersion,
		Latest:      latest.Version,
		URL:         latest.URL,
		ChecksumURL: latest.ChecksumURL,
		Notes:       latest.Notes,
		CheckedAt:   time.Now(),
		UpdateAvail: isNewer(c.cfg.CurrentVersion, latest.Version),
	}

	_ = saveCachedResult(c.cfg.StateDir, c.cfg.BinaryName, r)

	if r.UpdateAvail {
		c.publish(ctx, c.cfg.topics.Released, ReleasedPayload{
			Latest:     r.Latest,
			Current:    r.Current,
			ReleasedAt: r.CheckedAt,
		})
	}
	return r
}

// ShouldNotify returns true when an update is available AND the user has not
// snoozed (or the snooze has expired).
func (c *Checker) ShouldNotify(ctx context.Context) bool {
	r := c.Check(ctx)
	if !r.UpdateAvail {
		return false
	}
	snoozed, err := isSnoozed(c.cfg.StateDir, c.cfg.BinaryName)
	if err != nil {
		return true
	}
	return !snoozed
}

// Upgrade downloads and installs the latest binary in-place.
//
// When a publisher is configured, Upgrade emits Downloaded after the
// asset lands on disk and Installed after the running binary is
// replaced. Each event fires only on its own success boundary; a
// download that fails install still emits Downloaded.
func (c *Checker) Upgrade(ctx context.Context) error {
	r := c.Check(ctx)
	if r.Err != nil {
		return r.Err
	}
	if !r.UpdateAvail {
		return nil
	}
	hooks := replaceHooks{
		onDownloaded: func(path string, bytes int64) {
			c.publish(ctx, c.cfg.topics.Downloaded, DownloadedPayload{
				Version: r.Latest,
				Path:    path,
				Bytes:   bytes,
			})
		},
		onInstalled: func(from, to string) {
			c.publish(ctx, c.cfg.topics.Installed, InstalledPayload{
				Version: r.Latest,
				From:    from,
				To:      to,
			})
		},
	}
	return replaceBinary(ctx, c.cfg, r.URL, r.ChecksumURL, hooks)
}

// Snooze records a snooze for the configured duration.
//
// When a publisher is configured, Snooze emits a Snoozed event on
// success. The Version field is the cached latest version when known,
// or the configured CurrentVersion as a fallback.
func (c *Checker) Snooze() error {
	if err := writeSnooze(c.cfg.StateDir, c.cfg.BinaryName, c.cfg.SnoozeDuration); err != nil {
		return err
	}
	until := time.Now().Add(c.cfg.SnoozeDuration)
	version := c.snoozeVersion()
	c.publish(context.Background(), c.cfg.topics.Snoozed, SnoozedPayload{
		Version: version,
		Until:   until,
	})
	return nil
}

// snoozeVersion picks the version to report on Snoozed events. Prefers
// the cached latest version (the one the user is dismissing); falls
// back to the configured CurrentVersion if no cache exists.
func (c *Checker) snoozeVersion() string {
	cached, err := loadCachedResult(c.cfg.StateDir, c.cfg.BinaryName)
	if err == nil && cached != nil && cached.Latest != "" {
		return cached.Latest
	}
	return c.cfg.CurrentVersion
}

// WhatsNew returns the release notes for the latest version.
func (c *Checker) WhatsNew(ctx context.Context) string {
	r := c.Check(ctx)
	if r.Err != nil || r.Notes == "" {
		return ""
	}
	return r.Notes
}
