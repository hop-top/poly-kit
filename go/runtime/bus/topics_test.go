package bus_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/runtime/bus"
)

func TestValidateTopic_Valid(t *testing.T) {
	cases := []bus.Topic{
		"kit.runtime.entity.created",
		"kit.runtime.entity.updated",
		"kit.runtime.entity.deleted",
		"kit.runtime.state.pre_transitioned",
		"kit.runtime.state.post_transitioned",
		"kit.ai.request.started",
		"kit.ai.response.received",
		"kit.api.request.ended",
		"kit.core.breaker.tripped",
		"kit.core.breaker.half_opened",
		"kit.core.upgrade.snoozed",
		"wsm.runtime.workspace.created",
		"myapp.payments.invoice.paid",
	}
	for _, c := range cases {
		t.Run(string(c), func(t *testing.T) {
			assert.NoError(t, bus.ValidateTopic(c))
		})
	}
}

func TestValidateTopic_Empty(t *testing.T) {
	err := bus.ValidateTopic("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestValidateTopic_WrongSegmentCount(t *testing.T) {
	cases := []struct {
		name  string
		topic bus.Topic
	}{
		{"three", "kit.api.request"},
		{"two", "kit.api"},
		{"five", "kit.runtime.entity.user.created"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := bus.ValidateTopic(c.topic)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "segments")
		})
	}
}

func TestValidateTopic_EmptySegment(t *testing.T) {
	err := bus.ValidateTopic("kit..entity.created")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty segment")
}

func TestValidateTopic_Uppercase(t *testing.T) {
	err := bus.ValidateTopic("kit.runtime.Entity.created")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lowercase")
}

func TestValidateTopic_NonAlpha(t *testing.T) {
	err := bus.ValidateTopic("kit.runtime.entity-thing.created")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lowercase")
}

func TestValidateTopic_PresentTense(t *testing.T) {
	err := bus.ValidateTopic("kit.api.request.start")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "past-tense")
}

func TestValidateTopic_WhitelistedAction(t *testing.T) {
	// "started" doesn't end in "ed" via the simple "rd"+"ed" pattern;
	// it's whitelisted explicitly.
	assert.NoError(t, bus.ValidateTopic("kit.ai.request.started"))
}

func TestValidateTopic_SnakeCaseAction(t *testing.T) {
	assert.NoError(t, bus.ValidateTopic("kit.runtime.state.pre_transitioned"))
	assert.NoError(t, bus.ValidateTopic("kit.runtime.state.post_transitioned"))
	assert.NoError(t, bus.ValidateTopic("kit.core.breaker.half_opened"))
}

func TestPrefixTopics_HappyPath(t *testing.T) {
	got, err := bus.PrefixTopics("wsm.runtime.workspace",
		[]string{"created", "updated", "deleted"})
	require.NoError(t, err)
	assert.Equal(t, bus.Topic("wsm.runtime.workspace.created"), got["created"])
	assert.Equal(t, bus.Topic("wsm.runtime.workspace.updated"), got["updated"])
	assert.Equal(t, bus.Topic("wsm.runtime.workspace.deleted"), got["deleted"])
	assert.Len(t, got, 3)
}

func TestPrefixTopics_EmptyPrefix(t *testing.T) {
	_, err := bus.PrefixTopics("", []string{"created"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestPrefixTopics_TrailingDot(t *testing.T) {
	_, err := bus.PrefixTopics("wsm.runtime.workspace.", []string{"created"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not end")
}

func TestPrefixTopics_WrongPrefixSegments(t *testing.T) {
	cases := []string{
		"wsm.runtime",                   // 2 segments
		"wsm.runtime.workspace.subpart", // 4 segments
		"wsm",                           // 1 segment
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			_, err := bus.PrefixTopics(p, []string{"created"})
			require.Error(t, err)
			assert.True(t,
				strings.Contains(err.Error(), "segments") ||
					strings.Contains(err.Error(), "empty"),
				"want 'segments' or 'empty' in: %v", err)
		})
	}
}

func TestPrefixTopics_InvalidPrefixSegment(t *testing.T) {
	_, err := bus.PrefixTopics("wsm.runtime.Workspace", []string{"created"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lowercase")
}

func TestPrefixTopics_InvalidActionSurfacesValidateError(t *testing.T) {
	_, err := bus.PrefixTopics("wsm.runtime.workspace",
		[]string{"start"}) // present-tense, will fail ValidateTopic
	require.Error(t, err)
	assert.Contains(t, err.Error(), "past-tense")
	assert.Contains(t, err.Error(), "PrefixTopics")
}

func TestPrefixTopics_PartialMapOnError(t *testing.T) {
	// "created" is valid; "start" is not. Partial map should contain
	// "created" but not "start".
	got, err := bus.PrefixTopics("wsm.runtime.workspace",
		[]string{"created", "start"})
	require.Error(t, err)
	assert.Contains(t, got, "created")
	assert.NotContains(t, got, "start")
}
