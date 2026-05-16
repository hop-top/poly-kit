package stage_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"hop.top/kit/go/core/projects"
	"hop.top/kit/go/core/stage"
	"hop.top/kit/go/runtime/bus"
)

// setupXDG isolates each test under its own XDG_CONFIG_HOME so the
// real user projects.yaml is never touched.
func setupXDG(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

// recPub is a test EventPublisher that records every published event
// in order. Goroutine-safe.
type recPub struct {
	mu     sync.Mutex
	events []recEvent
	veto   error
	on     string // veto only when topic matches this
}

type recEvent struct {
	Topic   string
	Source  string
	Payload any
}

func (r *recPub) Publish(_ context.Context, topic, source string, payload any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recEvent{Topic: topic, Source: source, Payload: payload})
	if r.veto != nil && (r.on == "" || topic == r.on) {
		return r.veto
	}
	return nil
}

func (r *recPub) snapshot() []recEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recEvent, len(r.events))
	copy(out, r.events)
	return out
}

// --- Read semantics ---

func TestRead_NoEntry_ReturnsActive(t *testing.T) {
	setupXDG(t)
	got, err := stage.Read("never-registered")
	require.NoError(t, err)
	assert.Equal(t, stage.StageActive, got.Stage)
}

func TestRead_EntryWithoutStage_ReturnsActive(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Write("ops", projects.Entry{
		Path:   "/tmp/ops",
		Source: projects.SourceWSM,
	}))
	got, err := stage.Read("ops")
	require.NoError(t, err)
	assert.Equal(t, stage.StageActive, got.Stage)
}

func TestRead_RoundTripsSetValue(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Write("ops", projects.Entry{
		Path:   "/tmp/ops",
		Source: projects.SourceWSM,
	}))
	target := stage.State{
		Stage:  stage.StageFeatureFreeze,
		Reason: "v2 release prep",
		Actor:  "jad",
	}
	require.NoError(t, stage.Set("ops", target))

	got, err := stage.Read("ops")
	require.NoError(t, err)
	assert.Equal(t, stage.StageFeatureFreeze, got.Stage)
	assert.Equal(t, "v2 release prep", got.Reason)
	assert.Equal(t, "jad", got.Actor)
	assert.False(t, got.Since.IsZero(), "Set must default Since when zero")
}

// --- Set publishes transitioned + entered ---

func TestSet_PublishesTransitionedAndEntered(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/tmp/ops"}))

	pub := &recPub{}
	require.NoError(t, stage.Set("ops", stage.State{
		Stage:  stage.StageMaintenance,
		Reason: "legacy mode",
		Actor:  "alice",
	}, stage.WithPublisher(pub)))

	evs := pub.snapshot()
	require.Len(t, evs, 2, "expected transitioned + entered")
	assert.Equal(t, string(stage.DefaultTopics.Transitioned), evs[0].Topic)
	assert.Equal(t, string(stage.DefaultTopics.Entered), evs[1].Topic)

	tp, ok := evs[0].Payload.(stage.TransitionedPayload)
	require.True(t, ok, "transitioned payload type, got %T", evs[0].Payload)
	assert.Equal(t, "ops", tp.Scope)
	assert.Equal(t, stage.StageActive, tp.From)
	assert.Equal(t, stage.StageMaintenance, tp.To)

	ep, ok := evs[1].Payload.(stage.EnteredPayload)
	require.True(t, ok)
	assert.Equal(t, stage.StageMaintenance, ep.Stage)
	assert.Equal(t, "alice", ep.Principal)
}

func TestSet_RejectsInvalidStage(t *testing.T) {
	setupXDG(t)
	err := stage.Set("ops", stage.State{Stage: "bogus"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid Stage")
}

// --- Propose veto path ---

func TestPropose_NilPublisher_NoOp(t *testing.T) {
	setupXDG(t)
	err := stage.Propose("ops", stage.State{Stage: stage.StageArchived})
	assert.NoError(t, err, "no publisher = no-op (preserves current behavior)")
}

func TestPropose_VetoReturnsPolicyDeniedError(t *testing.T) {
	setupXDG(t)
	pub := &recPub{
		veto: errors.New("not allowed"),
		on:   string(stage.DefaultTopics.Proposed),
	}
	err := stage.Propose("ops", stage.State{Stage: stage.StageArchived, Actor: "bob"}, stage.WithPublisher(pub))
	require.Error(t, err)

	var pde *stage.PolicyDeniedError
	require.True(t, errors.As(err, &pde), "expected *stage.PolicyDeniedError, got %T", err)
	assert.Equal(t, "ops", pde.Scope)
	assert.Equal(t, stage.StageActive, pde.From)
	assert.Equal(t, stage.StageArchived, pde.To)
}

func TestPropose_AllowedPublishesProposedOnly(t *testing.T) {
	setupXDG(t)
	pub := &recPub{}
	require.NoError(t, stage.Propose("ops", stage.State{Stage: stage.StageSunset}, stage.WithPublisher(pub)))
	evs := pub.snapshot()
	require.Len(t, evs, 1)
	assert.Equal(t, string(stage.DefaultTopics.Proposed), evs[0].Topic)
}

// --- Tick scans for expiry ---

func TestTick_NoExpired_ReturnsEmpty(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/tmp/ops"}))
	got, err := stage.Tick(time.Now())
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestTick_PastUntil_EmitsExpired(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/tmp/ops"}))

	until := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, stage.Set("ops", stage.State{
		Stage: stage.StageMaintenance,
		Until: &until,
	}))

	pub := &recPub{}
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	got, err := stage.Tick(now, stage.WithPublisher(pub))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "ops", got[0].Scope)
	assert.Equal(t, stage.StageMaintenance, got[0].Stage)

	evs := pub.snapshot()
	require.Len(t, evs, 1)
	assert.Equal(t, string(stage.DefaultTopics.Expired), evs[0].Topic)
}

func TestTick_DoesNotMutate(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/tmp/ops"}))

	until := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, stage.Set("ops", stage.State{
		Stage: stage.StageMaintenance,
		Until: &until,
	}))

	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	_, err := stage.Tick(now)
	require.NoError(t, err)

	got, err := stage.Read("ops")
	require.NoError(t, err)
	assert.Equal(t, stage.StageMaintenance, got.Stage,
		"Tick must not mutate state")
}

// --- Schema upgrade: read v(N) then v(N+1) preserves entries ---

func TestSchemaUpgrade_PreservesUnrelatedEntries(t *testing.T) {
	dir := setupXDG(t)
	path := filepath.Join(dir, "rux", "projects.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))

	// Hand-craft a v1 file (no stage block, schema 1).
	v1 := `schema: 1
projects:
  legacy:
    path: /tmp/legacy
    startup_cmd: zsh
    source: wsm
`
	require.NoError(t, os.WriteFile(path, []byte(v1), 0o644))

	// Read transparently treats missing stage as active.
	got, err := stage.Read("legacy")
	require.NoError(t, err)
	assert.Equal(t, stage.StageActive, got.Stage)

	// Write a stage on a different scope; v1's `legacy` entry must
	// be preserved verbatim except for the schema bump.
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/tmp/ops"}))
	require.NoError(t, stage.Set("ops", stage.State{Stage: stage.StageSunset}))

	file, err := projects.Read()
	require.NoError(t, err)
	assert.Equal(t, projects.SchemaVersion, file.Schema)
	legacy, ok := file.Projects["legacy"]
	require.True(t, ok, "legacy entry must survive schema bump")
	assert.Equal(t, "/tmp/legacy", legacy.Path)
	assert.Equal(t, "zsh", legacy.StartupCmd)
	assert.Nil(t, legacy.Stage, "legacy entry must not gain a stage block")

	ops, ok := file.Projects["ops"]
	require.True(t, ok)
	require.NotNil(t, ops.Stage)
	assert.Equal(t, string(stage.StageSunset), ops.Stage.Stage)
}

// --- Topics validation + override ---

func TestDefaultTopics_AllValid(t *testing.T) {
	for _, topic := range []bus.Topic{
		stage.DefaultTopics.Proposed,
		stage.DefaultTopics.Transitioned,
		stage.DefaultTopics.Entered,
		stage.DefaultTopics.Expired,
		stage.DefaultTopics.Violated,
	} {
		assert.NoError(t, bus.ValidateTopic(topic), "topic %q failed validation", topic)
	}
}

func TestWithTopicPrefix_ProducesCustomTopics(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/tmp/ops"}))

	pub := &recPub{}
	mgr := stage.NewManager(
		stage.WithPublisher(pub),
		stage.WithTopicPrefix("myapp.runtime.stage"),
	)
	require.NoError(t, mgr.Set(context.Background(), "ops", stage.State{Stage: stage.StageActive}))

	evs := pub.snapshot()
	require.Len(t, evs, 2)
	assert.Equal(t, "myapp.runtime.stage.transitioned", evs[0].Topic)
	assert.Equal(t, "myapp.runtime.stage.entered", evs[1].Topic)
}

func TestWithTopics_PartialOverrideFallsBack(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/tmp/ops"}))

	pub := &recPub{}
	mgr := stage.NewManager(
		stage.WithPublisher(pub),
		stage.WithTopics(stage.Topics{
			Transitioned: "tlc.runtime.stage.transitioned",
			// All other fields empty — fall back to DefaultTopics.
		}),
	)
	require.NoError(t, mgr.Set(context.Background(), "ops", stage.State{Stage: stage.StageActive}))

	evs := pub.snapshot()
	require.Len(t, evs, 2)
	assert.Equal(t, "tlc.runtime.stage.transitioned", evs[0].Topic, "overridden")
	assert.Equal(t, string(stage.DefaultTopics.Entered), evs[1].Topic, "fall-back")
}

func TestWithTopicPrefix_PanicsOnInvalidPrefix(t *testing.T) {
	defer func() {
		r := recover()
		assert.NotNil(t, r, "expected panic on bad prefix")
	}()
	stage.WithTopicPrefix("not.three.parts.too.many")
}

func TestNilPublisher_PreservesBehavior(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/tmp/ops"}))
	// No WithPublisher option — Set must persist without panicking.
	require.NoError(t, stage.Set("ops", stage.State{Stage: stage.StageActive}))

	got, err := stage.Read("ops")
	require.NoError(t, err)
	assert.Equal(t, stage.StageActive, got.Stage)
}

// --- AllStages / Valid ---

func TestAllStages_Returns6(t *testing.T) {
	got := stage.AllStages()
	assert.Len(t, got, 6)
	assert.Equal(t, stage.StageActive, got[0])
	assert.Equal(t, stage.StageArchived, got[5])
}

func TestStage_Valid(t *testing.T) {
	for _, s := range stage.AllStages() {
		assert.True(t, s.Valid(), "%q should be valid", s)
	}
	assert.False(t, stage.Stage("").Valid())
	assert.False(t, stage.Stage("bogus").Valid())
}

// --- YAML round-trip sanity for State on a real projects.yaml ---

func TestState_YAMLRoundTrip(t *testing.T) {
	dir := setupXDG(t)
	path := filepath.Join(dir, "rux", "projects.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))

	until := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/tmp/ops"}))
	require.NoError(t, stage.Set("ops", stage.State{
		Stage:  stage.StageFeatureFreeze,
		Until:  &until,
		Reason: "freeze for v2",
		Actor:  "jad",
	}))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, raw)

	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &parsed))

	// Verify YAML shape: schema bump + nested stage block on the entry.
	assert.EqualValues(t, projects.SchemaVersion, parsed["schema"])

	got, err := stage.Read("ops")
	require.NoError(t, err)
	assert.Equal(t, stage.StageFeatureFreeze, got.Stage)
	require.NotNil(t, got.Until)
	assert.True(t, got.Until.Equal(until), "Until round-tripped")
	assert.Equal(t, "freeze for v2", got.Reason)
	assert.Equal(t, "jad", got.Actor)
}

// --- example: read on a malformed file surfaces the projects sentinel ---

func TestRead_MalformedProjectsFile(t *testing.T) {
	dir := setupXDG(t)
	path := filepath.Join(dir, "rux", "projects.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("::: not yaml :::\n  - [unbalanced\n"), 0o644))

	_, err := stage.Read("anything")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stage: read projects")
}

func TestPolicyDeniedError_Format(t *testing.T) {
	e := &stage.PolicyDeniedError{
		Scope: "ops",
		From:  stage.StageActive,
		To:    stage.StageArchived,
		Err:   fmt.Errorf("boom"),
	}
	assert.Contains(t, e.Error(), "active")
	assert.Contains(t, e.Error(), "archived")
	assert.Contains(t, e.Error(), "ops")
}
