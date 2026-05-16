package projects_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/projects"
)

// TestManualEntriesSurvive guards the contract that wsm-driven rewrites
// never clobber manually-added entries. Critical for future
// `rux project add` (manual entries) coexisting with wsm.
func TestManualEntriesSurvive(t *testing.T) {
	setupXDG(t)

	require.NoError(t, projects.Write("hand-added", projects.Entry{
		Path:       "/tmp/hand-added",
		StartupCmd: "vim",
		Source:     projects.SourceManual,
	}))

	require.NoError(t, projects.Write("wsm-added", projects.Entry{
		Path:   "/tmp/wsm-added",
		Source: projects.SourceWSM,
	}))

	file, err := projects.Read()
	require.NoError(t, err)
	require.Len(t, file.Projects, 2)

	manual, ok := file.Projects["hand-added"]
	require.True(t, ok, "manual entry must survive a subsequent wsm Write")
	assert.Equal(t, projects.SourceManual, manual.Source,
		"manual entry's Source must remain SourceManual")
	assert.Equal(t, "/tmp/hand-added", manual.Path)
	assert.Equal(t, "vim", manual.StartupCmd)

	wsm, ok := file.Projects["wsm-added"]
	require.True(t, ok)
	assert.Equal(t, projects.SourceWSM, wsm.Source)
}

// TestWSMRewriteKeepsManual seeds a mixed file (3 manual + 3 wsm), then
// overwrites one wsm key and asserts every manual entry's Source is
// still SourceManual. Models the wsm sync rebuild path.
func TestWSMRewriteKeepsManual(t *testing.T) {
	setupXDG(t)

	manualNames := []string{"manual-a", "manual-b", "manual-c"}
	wsmNames := []string{"wsm-a", "wsm-b", "wsm-c"}

	for i := 0; i < 3; i++ {
		require.NoError(t, projects.Write(manualNames[i], projects.Entry{
			Path:   fmt.Sprintf("/tmp/%s", manualNames[i]),
			Source: projects.SourceManual,
		}))
		require.NoError(t, projects.Write(wsmNames[i], projects.Entry{
			Path:   fmt.Sprintf("/tmp/%s", wsmNames[i]),
			Source: projects.SourceWSM,
		}))
	}

	// Overwrite a wsm entry — simulates a re-registration via wsm.
	require.NoError(t, projects.Write("wsm-b", projects.Entry{
		Path:       "/tmp/wsm-b-renamed",
		StartupCmd: "fish",
		Source:     projects.SourceWSM,
	}))

	file, err := projects.Read()
	require.NoError(t, err)
	require.Len(t, file.Projects, 6,
		"expected 6 entries after overwrite; manual entries must persist")

	for _, name := range manualNames {
		entry, ok := file.Projects[name]
		require.True(t, ok, "manual entry %q vanished", name)
		assert.Equal(t, projects.SourceManual, entry.Source,
			"manual entry %q had its Source mutated", name)
	}

	overwritten := file.Projects["wsm-b"]
	assert.Equal(t, "/tmp/wsm-b-renamed", overwritten.Path)
	assert.Equal(t, "fish", overwritten.StartupCmd)
	assert.Equal(t, projects.SourceWSM, overwritten.Source)
}
