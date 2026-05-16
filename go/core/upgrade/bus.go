package upgrade

import (
	"context"
	"fmt"
	"time"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
)

// Topics holds the per-action topic strings emitted by the Checker.
//
// The Checker publishes one event per lifecycle moment:
//   - Released   — Check observes a new latest version
//   - Downloaded — Upgrade fetched the asset successfully
//   - Installed  — Upgrade replaced the running binary
//   - Snoozed    — user deferred notification
//
// Adopters override individual actions with WithTopics or replace
// all four with WithTopicPrefix.
type Topics struct {
	Released   bus.Topic
	Downloaded bus.Topic
	Installed  bus.Topic
	Snoozed    bus.Topic
}

// DefaultTopics is the kit baseline used when no override is supplied.
// Each topic conforms to the kit 4-segment past-tense convention and
// passes bus.ValidateTopic.
var DefaultTopics = Topics{
	Released:   "kit.core.upgrade.released",
	Downloaded: "kit.core.upgrade.downloaded",
	Installed:  "kit.core.upgrade.installed",
	Snoozed:    "kit.core.upgrade.snoozed",
}

// upgradeActions is the canonical action list passed to bus.PrefixTopics
// when expanding a 3-segment prefix. Order is fixed so error messages
// from PrefixTopics report a predictable first-failing action.
var upgradeActions = []string{"released", "downloaded", "installed", "snoozed"}

// publishSource is the Source field set on every published event.
const publishSource = "core.upgrade"

// ReleasedPayload carries data for kit.core.upgrade.released.
type ReleasedPayload struct {
	Latest     string    `json:"latest"`
	Current    string    `json:"current"`
	ReleasedAt time.Time `json:"released_at"`
}

// DownloadedPayload carries data for kit.core.upgrade.downloaded.
type DownloadedPayload struct {
	Version string `json:"version"`
	Path    string `json:"path"`
	Bytes   int64  `json:"bytes"`
}

// InstalledPayload carries data for kit.core.upgrade.installed.
type InstalledPayload struct {
	Version string `json:"version"`
	From    string `json:"from"`
	To      string `json:"to"`
}

// SnoozedPayload carries data for kit.core.upgrade.snoozed.
type SnoozedPayload struct {
	Version string    `json:"version"`
	Until   time.Time `json:"until"`
}

// WithPublisher attaches an EventPublisher used to emit lifecycle
// events. Bus emission is opt-in: a nil publisher (the default)
// preserves the historical behavior — Check/Upgrade/Snooze emit
// nothing on the bus.
//
// Publishing is best-effort, fire-and-forget on a goroutine: it
// must not block or fail the upgrade flow. Errors from the
// publisher are dropped.
func WithPublisher(p domain.EventPublisher) Option {
	return func(c *Config) { c.pub = p }
}

// WithTopicPrefix sets all four lifecycle topics from a 3-segment
// prefix of the form "source.category.object". Composed topics are
// "<prefix>.released", "<prefix>.downloaded", "<prefix>.installed",
// "<prefix>.snoozed".
//
// Example:
//
//	upgrade.New(upgrade.WithTopicPrefix("myapp.core.upgrade"))
//
// Panics if prefix fails bus.PrefixTopics validation. Constructors
// are wired at boot, so a misconfigured prefix is a programmer error
// — fail-loud is preferred over silent default fallback that would
// hide subscribers missing events at runtime.
func WithTopicPrefix(prefix string) Option {
	tm, err := bus.PrefixTopics(prefix, upgradeActions)
	if err != nil {
		panic(fmt.Sprintf("upgrade.WithTopicPrefix(%q): %v", prefix, err))
	}
	t := Topics{
		Released:   tm["released"],
		Downloaded: tm["downloaded"],
		Installed:  tm["installed"],
		Snoozed:    tm["snoozed"],
	}
	return func(c *Config) { c.topics = t }
}

// WithTopics replaces individual lifecycle topics. Empty bus.Topic
// fields keep the corresponding DefaultTopics value, so callers can
// override one action without restating the others.
//
// Example:
//
//	upgrade.New(upgrade.WithTopics(upgrade.Topics{
//	    Released: "myapp.core.upgrade.released",
//	}))
func WithTopics(t Topics) Option {
	if t.Released == "" {
		t.Released = DefaultTopics.Released
	}
	if t.Downloaded == "" {
		t.Downloaded = DefaultTopics.Downloaded
	}
	if t.Installed == "" {
		t.Installed = DefaultTopics.Installed
	}
	if t.Snoozed == "" {
		t.Snoozed = DefaultTopics.Snoozed
	}
	return func(c *Config) { c.topics = t }
}

// publish performs a best-effort, non-blocking publish. nil publisher
// is a no-op so adopters who never opt in pay nothing.
func (c *Checker) publish(ctx context.Context, topic bus.Topic, payload any) {
	if c.cfg.pub == nil || topic == "" {
		return
	}
	go func() {
		_ = c.cfg.pub.Publish(ctx, string(topic), publishSource, payload)
	}()
}
