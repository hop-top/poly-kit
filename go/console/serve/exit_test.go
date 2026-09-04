package serve_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/runtime/bus"
)

func TestExitCodeFor(t *testing.T) {
	tests := []struct {
		outcome  serve.LifecycleOutcome
		wantCode string
		wantExit int
	}{
		{serve.OutcomeCleanStop, output.CodeOK, 0},
		{serve.OutcomeInvalidSelection, output.CodeUsage, 2},
		{serve.OutcomeConfigInvalid, output.CodeUsage, 2},
		{serve.OutcomeNoServices, output.CodeUsage, 2},
		{serve.OutcomeUnknownService, output.CodeNotFound, 3},
		{serve.OutcomePolicyDenied, output.CodeUnauthorized, 5},
		{serve.OutcomeStartFailed, output.CodeGeneric, 1},
		{serve.OutcomeRuntimeCrash, output.CodeGeneric, 1},
		{serve.OutcomeShutdownTimeout, output.CodeGeneric, 1},
	}

	for _, tc := range tests {
		t.Run(string(tc.outcome), func(t *testing.T) {
			assert.Equal(t, tc.wantExit, serve.ExitCodeFor(tc.outcome))
			assert.Equal(t, tc.wantCode, serve.CodeFor(tc.outcome))
		})
	}
}

func TestExitCodeFor_UnknownOutcomeFailsLoudly(t *testing.T) {
	// An outcome added without a table row must not silently exit 0.
	assert.Equal(t, 1, serve.ExitCodeFor(serve.LifecycleOutcome("invented")))
	assert.Equal(t, output.CodeGeneric, serve.CodeFor(serve.LifecycleOutcome("invented")))
	assert.True(t, serve.IsFailure(serve.LifecycleOutcome("invented")))
}

func TestIsFailure(t *testing.T) {
	assert.False(t, serve.IsFailure(serve.OutcomeCleanStop))
	assert.True(t, serve.IsFailure(serve.OutcomeStartFailed))
	assert.True(t, serve.IsFailure(serve.OutcomePolicyDenied))
}

func TestExitCodeFor_SignalStopIsClean(t *testing.T) {
	// SIGTERM is how a supervisor asks for an orderly exit; a
	// non-zero answer would make every rolling restart look like a
	// crash.
	assert.Equal(t, 0, serve.ExitCodeFor(serve.OutcomeCleanStop))
}

func TestExitCodeFor_StartAndCrashShareOne(t *testing.T) {
	assert.Equal(
		t,
		serve.ExitCodeFor(serve.OutcomeStartFailed),
		serve.ExitCodeFor(serve.OutcomeRuntimeCrash),
	)
}

func TestWorstOutcome(t *testing.T) {
	tests := []struct {
		name     string
		observed []serve.LifecycleOutcome
		want     serve.LifecycleOutcome
	}{
		{
			name:     "nothing observed is a clean stop",
			observed: nil,
			want:     serve.OutcomeCleanStop,
		},
		{
			name:     "all clean stays clean",
			observed: []serve.LifecycleOutcome{serve.OutcomeCleanStop, serve.OutcomeCleanStop},
			want:     serve.OutcomeCleanStop,
		},
		{
			name:     "a single failure wins over clean stops",
			observed: []serve.LifecycleOutcome{serve.OutcomeCleanStop, serve.OutcomeRuntimeCrash},
			want:     serve.OutcomeRuntimeCrash,
		},
		{
			name: "under isolate the first failure survives a later clean stop",
			observed: []serve.LifecycleOutcome{
				serve.OutcomeStartFailed,
				serve.OutcomeCleanStop,
				serve.OutcomeCleanStop,
			},
			want: serve.OutcomeStartFailed,
		},
		{
			name: "the first of several failures explains the rest",
			observed: []serve.LifecycleOutcome{
				serve.OutcomeStartFailed,
				serve.OutcomeRuntimeCrash,
			},
			want: serve.OutcomeStartFailed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, serve.WorstOutcome(tc.observed))
		})
	}
}

func TestDefaultTopics_AllConformant(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		want   bus.Topic
		key    string
	}{
		{"default prefix", "", "kit.serve.service.ready_reported", "service.ready_reported"},
		{"default prefix supervisor", "", "kit.serve.supervisor.stopped", "supervisor.stopped"},
		{"adopter prefix", "myapp.serve", "myapp.serve.service.started", "service.started"},
		{"single-segment prefix falls back", "myapp", "myapp.serve.service.failed", "service.failed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			topics := serve.DefaultTopics(tc.prefix)
			got, ok := topics[tc.key]
			require.True(t, ok, "missing key %q", tc.key)
			assert.Equal(t, tc.want, got)
			assert.NoError(t, bus.ValidateTopic(got))
		})
	}
}

func TestDefaultTopics_EveryTopicValidates(t *testing.T) {
	topics := serve.DefaultTopics(serve.DefaultTopicPrefix)
	require.Len(t, topics, 6)
	for key, topic := range topics {
		assert.NoError(t, bus.ValidateTopic(topic), "key %q topic %q", key, topic)
	}
}

func TestActionSegments_ArePastTense(t *testing.T) {
	// A bare "ready" does not validate; ready_reported does. Pin
	// that so nobody reintroduces the non-conformant form.
	assert.Error(t, bus.ValidateTopic("kit.serve.service.ready"))
	assert.NoError(t, bus.ValidateTopic(
		bus.Topic("kit.serve.service."+serve.ActionReadyReported),
	))

	for _, action := range []string{
		serve.ActionStarted,
		serve.ActionReadyReported,
		serve.ActionFailed,
		serve.ActionStopped,
	} {
		assert.NoError(t, bus.ValidateTopic(bus.Topic("kit.serve.service."+action)), action)
	}
}

func TestDefaultTimeouts(t *testing.T) {
	assert.Equal(t, serve.DefaultReadyTimeout, serve.DefaultStopTimeout)
	assert.Less(t, serve.DefaultStopTimeout, serve.DefaultShutdownTimeout,
		"one service's stop budget must fit inside the total shutdown budget")
}
