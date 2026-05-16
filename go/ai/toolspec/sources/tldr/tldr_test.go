package tldr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const gitTldr = `# git

> Distributed version control system.
> More information: <https://git-scm.com/>.

- Clone a repository:

` + "`git clone {{url}}`" + `

- Show changes:

` + "`git diff`" + `

- Stage all changes:

` + "`git add .`" + `
`

const dockerTldr = `# docker

> Manage Docker containers and images.
> More information: <https://docs.docker.com/engine/reference/commandline/cli/>.

- List running containers:

` + "`docker ps`" + `

- Run a container from an image:

` + "`docker run {{image}}`" + `
`

func TestParseGitTldr(t *testing.T) {
	spec := ParseTldrPage("git", gitTldr)
	require.NotNil(t, spec)
	assert.Equal(t, "git", spec.Name)

	require.Len(t, spec.Workflows, 3)

	assert.Equal(t, "Clone a repository", spec.Workflows[0].Name)
	assert.Equal(t, []string{"git clone {{url}}"}, spec.Workflows[0].Steps)

	assert.Equal(t, "Show changes", spec.Workflows[1].Name)
	assert.Equal(t, []string{"git diff"}, spec.Workflows[1].Steps)

	assert.Equal(t, "Stage all changes", spec.Workflows[2].Name)
	assert.Equal(t, []string{"git add ."}, spec.Workflows[2].Steps)
}

func TestParseDockerTldr(t *testing.T) {
	spec := ParseTldrPage("docker", dockerTldr)
	require.NotNil(t, spec)
	assert.Equal(t, "docker", spec.Name)

	require.Len(t, spec.Workflows, 2)

	assert.Equal(t, "List running containers", spec.Workflows[0].Name)
	assert.Equal(t, []string{"docker ps"}, spec.Workflows[0].Steps)

	assert.Equal(t, "Run a container from an image", spec.Workflows[1].Name)
	assert.Equal(t, []string{"docker run {{image}}"}, spec.Workflows[1].Steps)
}

func TestParseEmptyPage(t *testing.T) {
	spec := ParseTldrPage("empty", "")
	require.NotNil(t, spec)
	assert.Equal(t, "empty", spec.Name)
	assert.Len(t, spec.Workflows, 0)
}
