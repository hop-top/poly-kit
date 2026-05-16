package stage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"hop.top/kit/go/core/projects"
	"hop.top/kit/go/runtime/domain"
)

// Stage names a scope's operating mode. The 6 values are ordered roughly
// from "everything allowed" to "nothing allowed":
//
//	active            — normal mode; no extra restrictions
//	public_feedback   — pre-launch; only feedback-typed tracks may be created
//	feature_freeze    — only fix/chore/docs work; no new tracks
//	maintenance       — fix/chore/docs only; no new tracks
//	sunset            — no creates; updates/deletes still ok
//	archived          — read-only; all mutations blocked
//
// The default ruleset under runtime/policy/stage.yaml encodes the
// semantics above. Adopters override individual rules by name.
type Stage string

// Stage constants. Values are stable; changing a value is a schema break.
const (
	StageActive         Stage = "active"
	StagePublicFeedback Stage = "public_feedback"
	StageFeatureFreeze  Stage = "feature_freeze"
	StageMaintenance    Stage = "maintenance"
	StageSunset         Stage = "sunset"
	StageArchived       Stage = "archived"
)

// allStages is the canonical ordered list (active → archived). Used by
// the CLI's `stage list --help` and validation.
var allStages = []Stage{
	StageActive,
	StagePublicFeedback,
	StageFeatureFreeze,
	StageMaintenance,
	StageSunset,
	StageArchived,
}

// AllStages returns the canonical ordered list of valid Stage values.
// Callers iterate to render help text or validate input.
func AllStages() []Stage {
	out := make([]Stage, len(allStages))
	copy(out, allStages)
	return out
}

// Valid reports whether s is a recognized Stage value.
func (s Stage) Valid() bool {
	for _, v := range allStages {
		if v == s {
			return true
		}
	}
	return false
}

// State is the persisted record on a projects.yaml entry. Zero State
// (Stage == "") is treated as StageActive by Read.
//
// YAML on disk:
//
//	stage:
//	  stage: feature_freeze
//	  since: 2026-05-01T00:00:00Z
//	  until: 2026-06-01T00:00:00Z
//	  reason: "preparing v2 release"
//	  actor: jad
type State struct {
	Stage  Stage      `yaml:"stage"`
	Since  time.Time  `yaml:"since"`
	Until  *time.Time `yaml:"until,omitempty"`
	Reason string     `yaml:"reason,omitempty"`
	Allow  []string   `yaml:"allow,omitempty"`
	Deny   []string   `yaml:"deny,omitempty"`
	Actor  string     `yaml:"actor,omitempty"`
}

// fromRaw converts a projects.StageState (string-typed Stage field) to
// the strongly-typed State. Returns the zero State when raw is nil.
func fromRaw(raw *projects.StageState) State {
	if raw == nil {
		return State{}
	}
	return State{
		Stage:  Stage(raw.Stage),
		Since:  raw.Since,
		Until:  raw.Until,
		Reason: raw.Reason,
		Allow:  raw.Allow,
		Deny:   raw.Deny,
		Actor:  raw.Actor,
	}
}

// toRaw is the inverse of fromRaw — projects.yaml on-disk shape.
func (s State) toRaw() *projects.StageState {
	return &projects.StageState{
		Stage:  string(s.Stage),
		Since:  s.Since,
		Until:  s.Until,
		Reason: s.Reason,
		Allow:  s.Allow,
		Deny:   s.Deny,
		Actor:  s.Actor,
	}
}

// PolicyDeniedError is returned by Propose when a subscriber on the
// proposed topic vetoes the transition. Wraps the subscriber error.
type PolicyDeniedError struct {
	Scope string
	From  Stage
	To    Stage
	Err   error
}

// Error implements error.
func (e *PolicyDeniedError) Error() string {
	return fmt.Sprintf("stage: transition %s → %s on %q denied: %v",
		e.From, e.To, e.Scope, e.Err)
}

// Unwrap exposes the underlying veto error for errors.Is/As.
func (e *PolicyDeniedError) Unwrap() error { return e.Err }

// Manager carries the per-call publisher + topic configuration. Read,
// Set, Propose, and Tick are convenience wrappers that build a Manager
// from the package-level options and delegate.
//
// Tools that already hold a publisher build a Manager once at startup
// and call its methods directly; this avoids re-resolving options on
// every call and lets tests inject test-only publishers cleanly.
type Manager struct {
	pub    domain.EventPublisher
	topics Topics
}

// Option configures a Manager via functional options.
type Option func(*Manager)

// WithPublisher attaches an EventPublisher used to emit stage events.
// Nil publisher (the default) means Set/Propose/Tick do not publish —
// callers preserve current behavior when they don't wire one.
func WithPublisher(p domain.EventPublisher) Option {
	return func(m *Manager) { m.pub = p }
}

// NewManager constructs a Manager with the supplied options. Topics
// default to DefaultTopics.
func NewManager(opts ...Option) *Manager {
	m := &Manager{topics: DefaultTopics}
	for _, o := range opts {
		o(m)
	}
	return m
}

// Read loads State for projectName. Resolution order:
//
//  1. The Stage field on the projects.yaml entry, if present;
//  2. StageActive otherwise.
//
// Missing entry, missing Stage field, and projects.yaml not yet
// created all map to StageActive with a zero Since. Returns a
// non-nil error only when projects.yaml is malformed or unreadable.
func Read(projectName string) (State, error) {
	file, err := projects.Read()
	if err != nil {
		// Treat malformed/unsupported as a hard error — callers should
		// not silently allow when state is unreadable.
		return State{}, fmt.Errorf("stage: read projects: %w", err)
	}
	entry, ok := file.Projects[projectName]
	if !ok {
		return State{Stage: StageActive}, nil
	}
	if entry.Stage == nil || entry.Stage.Stage == "" {
		return State{Stage: StageActive}, nil
	}
	return fromRaw(entry.Stage), nil
}

// Set persists target as the new State for projectName. Set DOES NOT
// run the proposed pre-event — callers wire Propose before Set when
// they want runtime/policy gating. Set publishes:
//
//   - Topics.Transitioned (post)
//   - Topics.Entered      (post)
//
// Both are best-effort: subscriber errors are swallowed (post events
// are notifications, not gates).
//
// Set is the package-level convenience wrapper; it builds a Manager
// from opts on every call. Tools that hold a Manager call its Set
// method directly.
func Set(projectName string, target State, opts ...Option) error {
	return NewManager(opts...).Set(context.Background(), projectName, target)
}

// Propose runs the pre-transition seam: emits Topics.Proposed
// synchronously and returns a PolicyDeniedError when a subscriber
// vetoes. Propose does NOT persist; callers chain Propose → Set when
// they want a gated transition.
func Propose(projectName string, target State, opts ...Option) error {
	return NewManager(opts...).Propose(context.Background(), projectName, target)
}

// Tick scans every projects.yaml entry for State.Until <= now and
// emits Topics.Expired for each match. Tick does NOT mutate state —
// callers decide whether to auto-Set back to StageActive (e.g. in a
// daemon ticker or on `<tool> stage show`).
//
// Returns the list of ExpiredEvent objects (one per matched scope) +
// any read error.
func Tick(now time.Time, opts ...Option) ([]ExpiredEvent, error) {
	return NewManager(opts...).Tick(context.Background(), now)
}

// ExpiredEvent records a scope whose Until has elapsed.
type ExpiredEvent struct {
	Scope     string
	Stage     Stage
	ExpiredAt time.Time
}

// Set persists target for projectName via core/projects, then emits
// transitioned + entered post events through m.pub. See package-level
// Set for the semantics.
func (m *Manager) Set(ctx context.Context, projectName string, target State) error {
	if !target.Stage.Valid() {
		return fmt.Errorf("stage: invalid Stage %q", target.Stage)
	}
	if target.Since.IsZero() {
		target.Since = time.Now().UTC()
	}

	// Read the current state (used for the From field on the
	// transitioned payload). Errors here are "projects.yaml is
	// unreadable" — propagate.
	cur, err := Read(projectName)
	if err != nil {
		return err
	}

	// Persist. Read-modify-write under the projects flock.
	file, err := projects.Read()
	if err != nil && !errors.Is(err, projects.ErrMalformed) && !errors.Is(err, projects.ErrSchemaUnsupported) {
		return fmt.Errorf("stage: read projects: %w", err)
	}
	entry := file.Projects[projectName]
	entry.Stage = target.toRaw()
	if err := projects.Write(projectName, entry); err != nil {
		return fmt.Errorf("stage: write projects: %w", err)
	}

	// Emit transitioned + entered (best-effort).
	if m.pub != nil {
		_ = m.pub.Publish(ctx, string(m.topics.Transitioned), "core.stage", TransitionedPayload{
			Scope:     projectName,
			From:      cur.Stage,
			To:        target.Stage,
			Principal: target.Actor,
			Reason:    target.Reason,
			Since:     target.Since,
		})
		_ = m.pub.Publish(ctx, string(m.topics.Entered), "core.stage", EnteredPayload{
			Scope:     projectName,
			Stage:     target.Stage,
			Principal: target.Actor,
			Since:     target.Since,
		})
	}
	return nil
}

// Propose emits Topics.Proposed and wraps a subscriber veto as
// PolicyDeniedError. With no publisher wired, Propose is a no-op.
func (m *Manager) Propose(ctx context.Context, projectName string, target State) error {
	if m.pub == nil {
		return nil
	}
	cur, err := Read(projectName)
	if err != nil {
		return err
	}
	payload := ProposedPayload{
		Scope:     projectName,
		From:      cur.Stage,
		To:        target.Stage,
		Principal: target.Actor,
		Reason:    target.Reason,
	}
	if err := m.pub.Publish(ctx, string(m.topics.Proposed), "core.stage", payload); err != nil {
		return &PolicyDeniedError{
			Scope: projectName,
			From:  cur.Stage,
			To:    target.Stage,
			Err:   err,
		}
	}
	return nil
}

// Tick scans every entry in projects.yaml for an expired State and
// emits Topics.Expired for each match. Returns the list (possibly
// empty) and any read error.
func (m *Manager) Tick(ctx context.Context, now time.Time) ([]ExpiredEvent, error) {
	file, err := projects.Read()
	if err != nil {
		return nil, fmt.Errorf("stage: read projects: %w", err)
	}
	var out []ExpiredEvent
	for name, entry := range file.Projects {
		if entry.Stage == nil || entry.Stage.Until == nil {
			continue
		}
		if entry.Stage.Until.After(now) {
			continue
		}
		ev := ExpiredEvent{
			Scope:     name,
			Stage:     Stage(entry.Stage.Stage),
			ExpiredAt: *entry.Stage.Until,
		}
		out = append(out, ev)
		if m.pub != nil {
			//nolint:staticcheck // S1016 — ExpiredEvent and ExpiredPayload differ by json tags; direct conversion not allowed
			_ = m.pub.Publish(ctx, string(m.topics.Expired), "core.stage", ExpiredPayload{
				Scope:     ev.Scope,
				Stage:     ev.Stage,
				ExpiredAt: ev.ExpiredAt,
			})
		}
	}
	return out, nil
}
