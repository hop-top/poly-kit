package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sectionsInstance starts an api instance whose root registers the
// tool's service status providers; depth is state only the instance
// holds.
func sectionsInstance(t *testing.T, depth *atomic.Int64) (*Root, func()) {
	t.Helper()
	d := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}), WithStatus(StatusConfig{}))
	d.RegisterServiceStatusProvider("queue", func(context.Context) (StatusSection, error) {
		return StatusSection{Title: "queue", Priority: 1000, Data: map[string]any{"depth": depth.Load()}}, nil
	})
	d.RegisterServiceStatusProvider("credentials", func(context.Context) (StatusSection, error) {
		return StatusSection{Title: "credentials", Priority: 1100, Sensitive: true,
			Data: map[string]string{"api_token": "s3cr3t", "owner": "ops"}}, nil
	})
	d.RegisterServiceStatusProvider("broken", func(context.Context) (StatusSection, error) {
		return StatusSection{Priority: 1200}, errors.New("store unreachable")
	})
	_, stop := serveAPI(t, d)
	return d, stop
}

// The tool's sections come from the running instance, after kit's
// rows: ordered by priority, sensitive values redacted, a failing
// provider reported as an error section.
func TestServiceStatus_SectionsFromTheInstanceJSON(t *testing.T) {
	isolateRuntime(t)
	var depth atomic.Int64
	depth.Store(7)
	d, stop := sectionsInstance(t, &depth)
	defer stop()

	// The client registers nothing: what it prints is the instance's.
	got := instanceStatus(t)
	require.Len(t, got.Services, 1)
	assert.Equal(t, apiSvc(t, d).Addr(), got.Services[0].Addr, "kit's rows stay")
	require.Len(t, got.Sections, 3)
	assert.Equal(t, []string{"queue", "credentials", "broken"},
		[]string{got.Sections[0].Title, got.Sections[1].Title, got.Sections[2].Title})

	assert.Equal(t, StatusOK, got.Sections[0].Status)
	assert.Equal(t, map[string]any{"depth": float64(7)}, got.Sections[0].Data)

	assert.Equal(t, map[string]any{"api_token": "[redacted]", "owner": "ops"}, got.Sections[1].Data)

	assert.Equal(t, StatusError, got.Sections[2].Status)
	assert.Equal(t, "store unreachable", got.Sections[2].ErrorMessage)

	// Read live on each call.
	depth.Store(9)
	got = instanceStatus(t)
	assert.Equal(t, map[string]any{"depth": float64(9)}, got.Sections[0].Data)

	// The root status is the client's own and does not run them.
	r := authRoot(t, WithStatus(StatusConfig{}))
	out, _, code, err := run(t, r, "status", "--format", "json")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	var rootStatus StatusOutput
	require.NoError(t, json.Unmarshal([]byte(out), &rootStatus), out)
	for _, sec := range rootStatus.Sections {
		assert.NotEqual(t, "queue", sec.Title)
	}
}

// The tabular formats print kit's rows, then one row per section.
func TestServiceStatus_SectionsTable(t *testing.T) {
	isolateRuntime(t)
	var depth atomic.Int64
	depth.Store(7)
	d, stop := sectionsInstance(t, &depth)
	defer stop()

	out, _, code, err := run(t, client(t, ""), "service", "status", "--format", "table")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	svcAt := strings.Index(out, "SERVICE")
	secAt := strings.Index(out, "SECTION")
	require.GreaterOrEqual(t, svcAt, 0, out)
	require.Greater(t, secAt, svcAt, "sections below the rows\n%s", out)
	assert.Contains(t, out[svcAt:secAt], apiSvc(t, d).Addr())
	assert.Contains(t, out[secAt:], "depth=7")
	assert.Contains(t, out[secAt:], "api_token=[redacted]")
	assert.NotContains(t, out, "s3cr3t")
	assert.Contains(t, out[secAt:], "store unreachable")
}

// Without sections the table is kit's rows alone.
func TestServiceStatus_TableWithoutSections(t *testing.T) {
	isolateRuntime(t)
	d := authRoot(t, WithAPI(APIConfig{Addr: "127.0.0.1:0"}))
	_, stop := serveAPI(t, d)
	defer stop()

	out, _, _, err := run(t, client(t, ""), "service", "status", "--format", "table")
	require.NoError(t, err)
	assert.Contains(t, out, "SERVICE")
	assert.NotContains(t, out, "SECTION")
}
