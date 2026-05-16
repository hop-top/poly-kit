package ps_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/ps"
)

func TestStatusColor_NonEmpty(t *testing.T) {
	statuses := []ps.Status{
		ps.StatusRunning,
		ps.StatusPending,
		ps.StatusBlocked,
		ps.StatusDone,
	}
	for _, s := range statuses {
		style := ps.StatusColor(s)
		rendered := style.Render(string(s))
		assert.NotEmpty(t, rendered, "StatusColor(%q) should produce non-empty render", s)
	}
}

func TestStatusColor_Unknown(t *testing.T) {
	style := ps.StatusColor(ps.Status("unknown"))
	rendered := style.Render("unknown")
	assert.Equal(t, "unknown", rendered)
}

func TestDurationFrom_Seconds(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 30, 0, time.UTC)
	e := ps.Entry{Started: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	assert.Equal(t, "30s", e.DurationFrom(now))
}

func TestDurationFrom_Minutes(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)
	e := ps.Entry{Started: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	assert.Equal(t, "5m", e.DurationFrom(now))
}

func TestDurationFrom_Hours(t *testing.T) {
	now := time.Date(2026, 1, 1, 2, 30, 0, 0, time.UTC)
	e := ps.Entry{Started: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	assert.Equal(t, "2h30m", e.DurationFrom(now))
}

func TestProgressString_Nil(t *testing.T) {
	e := ps.Entry{}
	assert.Equal(t, "", e.ProgressString())
}

func TestProgressString_WithProgress(t *testing.T) {
	e := ps.Entry{Progress: &ps.Progress{Done: 3, Total: 10}}
	assert.Equal(t, "3/10 (30%)", e.ProgressString())
}

func TestProgressString_ZeroTotal(t *testing.T) {
	e := ps.Entry{Progress: &ps.Progress{Done: 0, Total: 0}}
	assert.Equal(t, "0/0 (0%)", e.ProgressString())
}

func TestProgressString_Complete(t *testing.T) {
	e := ps.Entry{Progress: &ps.Progress{Done: 10, Total: 10}}
	assert.Equal(t, "10/10 (100%)", e.ProgressString())
}

func TestTruncateScope(t *testing.T) {
	tests := []struct {
		input  string
		max    int
		expect string
	}{
		{"short", 40, "short"},
		{"this is a very long scope string that exceeds the limit", 20, "this is a very lo..."},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc"},
		{"anything", 0, ""},
		{"anything", -1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ps.TruncateScope(tt.input, tt.max)
			require.Equal(t, tt.expect, got)
		})
	}
}
