package projects_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/projects"
)

// TestConcurrentWrite spawns N goroutines each writing a distinct project
// name with a distinct path. The gofrs/flock guard inside Write must
// serialize the read-modify-write cycles so every entry survives.
func TestConcurrentWrite(t *testing.T) {
	setupXDG(t)

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("proj-%d", i)
			err := projects.Write(name, projects.Entry{
				Path:   fmt.Sprintf("/tmp/%s", name),
				Source: projects.SourceWSM,
			})
			assert.NoError(t, err, "concurrent Write %s", name)
		}()
	}
	wg.Wait()

	file, err := projects.Read()
	require.NoError(t, err)

	require.Len(t, file.Projects, n,
		"all %d concurrent writes must persist; got %d entries",
		n, len(file.Projects))

	for i := 0; i < n; i++ {
		name := fmt.Sprintf("proj-%d", i)
		entry, ok := file.Projects[name]
		require.True(t, ok, "missing entry %q", name)
		assert.Equal(t, fmt.Sprintf("/tmp/%s", name), entry.Path,
			"entry %q has wrong path; flock may be broken", name)
		assert.Equal(t, projects.SourceWSM, entry.Source,
			"entry %q lost source", name)
	}
}
