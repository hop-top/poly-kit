package upgrade

import (
	"context"
	"fmt"
	"time"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
)

// MigrationTopics holds the topics a Migrator publishes on.
//
// Run level: RunStarted, then RunCompleted or RunFailed. Per migration:
// Started, then Applied (Up succeeded) and Verified (its Verify passed),
// or Failed. Reverted follows a rollback that succeeded. The final
// verify (WithVerify) publishes Verified with "final": true, or Failed
// with stage "final".
type MigrationTopics struct {
	RunStarted   bus.Topic
	RunCompleted bus.Topic
	RunFailed    bus.Topic
	Started      bus.Topic
	Applied      bus.Topic
	Verified     bus.Topic
	Failed       bus.Topic
	Reverted     bus.Topic
}

// DefaultMigrationTopics is the kit baseline. Each topic passes
// bus.ValidateTopic.
var DefaultMigrationTopics = MigrationTopics{
	RunStarted:   "kit.core.migration.run_started",
	RunCompleted: "kit.core.migration.run_completed",
	RunFailed:    "kit.core.migration.run_failed",
	Started:      "kit.core.migration.started",
	Applied:      "kit.core.migration.applied",
	Verified:     "kit.core.migration.verified",
	Failed:       "kit.core.migration.failed",
	Reverted:     "kit.core.migration.reverted",
}

// migrationActions is the canonical order passed to bus.PrefixTopics.
var migrationActions = []string{
	"run_started", "run_completed", "run_failed",
	"started", "applied", "verified", "failed", "reverted",
}

// MigrationRunPayload carries run_started, run_completed and run_failed.
type MigrationRunPayload struct {
	Tool       string `json:"tool"`
	To         string `json:"to"`
	Applied    *int   `json:"applied,omitempty"`
	Schema     string `json:"schema,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMS *int64 `json:"duration_ms,omitempty"`
}

// MigrationPayload carries the per-migration events. From is the
// version before the step; To the step's version. For reverted, From
// is the version being attempted and To the stored version restored.
type MigrationPayload struct {
	Tool       string `json:"tool"`
	Schema     string `json:"schema"`
	From       string `json:"from"`
	To         string `json:"to"`
	Stage      string `json:"stage,omitempty"`  // failed: up, verify, final, canceled
	Method     string `json:"method,omitempty"` // reverted: down, restore
	Final      bool   `json:"final,omitempty"`  // verified by WithVerify
	Error      string `json:"error,omitempty"`
	DurationMS *int64 `json:"duration_ms,omitempty"`
}

// WithEventPublisher publishes migration lifecycle events. Without it
// a Migrator is silent. Publishing is synchronous, so subscribers see
// events in order; publisher errors are dropped and never fail a run.
func WithEventPublisher(p domain.EventPublisher) MigratorOption {
	return func(m *Migrator) { m.pub = p }
}

// WithMigrationTopicPrefix sets every migration topic from a 3-segment
// prefix ("myapp.core.migration" → "myapp.core.migration.applied", ...).
// Panics when the prefix fails bus.PrefixTopics validation.
func WithMigrationTopicPrefix(prefix string) MigratorOption {
	tm, err := bus.PrefixTopics(prefix, migrationActions)
	if err != nil {
		panic(fmt.Sprintf("upgrade.WithMigrationTopicPrefix(%q): %v", prefix, err))
	}
	t := MigrationTopics{
		RunStarted: tm["run_started"], RunCompleted: tm["run_completed"], RunFailed: tm["run_failed"],
		Started: tm["started"], Applied: tm["applied"], Verified: tm["verified"],
		Failed: tm["failed"], Reverted: tm["reverted"],
	}
	return func(m *Migrator) { m.topics = t }
}

// WithMigrationTopics replaces individual topics; empty fields keep the
// DefaultMigrationTopics value.
func WithMigrationTopics(t MigrationTopics) MigratorOption {
	d := DefaultMigrationTopics
	keep := func(v *bus.Topic, def bus.Topic) {
		if *v == "" {
			*v = def
		}
	}
	keep(&t.RunStarted, d.RunStarted)
	keep(&t.RunCompleted, d.RunCompleted)
	keep(&t.RunFailed, d.RunFailed)
	keep(&t.Started, d.Started)
	keep(&t.Applied, d.Applied)
	keep(&t.Verified, d.Verified)
	keep(&t.Failed, d.Failed)
	keep(&t.Reverted, d.Reverted)
	return func(m *Migrator) { m.topics = t }
}

func (m *Migrator) emit(ctx context.Context, topic bus.Topic, payload any) {
	if m.pub == nil || topic == "" {
		return
	}
	_ = m.pub.Publish(ctx, string(topic), publishSource, payload)
}

func millis(since time.Time) *int64 {
	ms := time.Since(since).Milliseconds()
	return &ms
}
