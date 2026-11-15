package cli_test

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
)

// A degraded section with nothing else to show is labeled, like the
// other non-ok states, rather than rendering a blank cell.
func TestStatus_TableDegradedIsLabeled(t *testing.T) {
	body := runStatusFixture(t, "table",
		cli.StatusSection{Title: "mail", Status: cli.StatusDegraded})
	assert.Contains(t, body, "degraded")
	assert.Contains(t, body, "(degraded)")
}

// A degraded section's reason, carried in ErrorMessage, is what the
// DETAIL cell shows; without one, its payload is summarized as for ok.
func TestStatus_TableDegradedShowsReasonOrData(t *testing.T) {
	body := runStatusFixture(t, "table",
		cli.StatusSection{
			Title: "receivers", Status: cli.StatusDegraded, Priority: 1,
			Data:         map[string]any{"ready": 180, "failed": 20},
			ErrorMessage: "20/200 receivers failed",
		},
		cli.StatusSection{
			Title: "mail", Status: cli.StatusDegraded, Priority: 2,
			Data: map[string]string{"mode": "polling"},
		},
	)
	assert.Contains(t, body, "20/200 receivers failed")
	assert.Contains(t, body, "mode=polling")
}

// json keeps the section whole: status "degraded", data and reason.
// The run itself succeeds: a degraded section does not fail status.
func TestStatus_JSONDegradedKeepsDataAndReason(t *testing.T) {
	body := runStatusFixture(t, "json", cli.StatusSection{
		Title: "receivers", Status: cli.StatusDegraded,
		Data: map[string]any{"ready": 180}, ErrorMessage: "20/200 receivers failed",
	})
	var payload cli.StatusOutput
	require.NoError(t, json.Unmarshal([]byte(body), &payload))
	require.Len(t, payload.Sections, 1)
	assert.Equal(t, "degraded", payload.Sections[0].Status)
	assert.Equal(t, "20/200 receivers failed", payload.Sections[0].ErrorMessage)
	assert.NotNil(t, payload.Sections[0].Data)
}

// StatusSeverity orders the set from healthiest to worst; an empty
// value is ok, and a value outside the set ranks with error.
func TestStatusSeverity_Order(t *testing.T) {
	got := []string{cli.StatusError, cli.StatusDegraded, cli.StatusOK, cli.StatusUnavailable, cli.StatusEmpty}
	sort.SliceStable(got, func(i, j int) bool {
		return cli.StatusSeverity(got[i]) < cli.StatusSeverity(got[j])
	})
	assert.Equal(t, []string{
		cli.StatusOK, cli.StatusEmpty, cli.StatusDegraded, cli.StatusUnavailable, cli.StatusError,
	}, got)
	assert.Equal(t, cli.StatusSeverity(cli.StatusOK), cli.StatusSeverity(""))
	assert.Equal(t, cli.StatusSeverity(cli.StatusError), cli.StatusSeverity("bogus"))
}
