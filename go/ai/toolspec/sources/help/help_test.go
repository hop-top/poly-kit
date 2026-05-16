package help

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/ai/toolspec"
)

const gitHelp = `usage: git [-v | --version] [-h | --help] [-C <path>] [-c <name>=<value>]

These are common Git commands used in various situations:

start a working area
   clone     Clone a repository into a new directory
   init      Create an empty Git repository or reinitialize an existing one

work on the current change
   add       Add file contents to the index
   mv        Move or rename a file, a directory, or a symlink
   restore   Restore working tree files
`

const ghHelp = `Work seamlessly with GitHub from the command line.

USAGE
  gh <command> <subcommand> [flags]

CORE COMMANDS
  browse:     Open the repository in the browser
  codespace:  Connect to and manage codespaces
  issue:      Manage issues
  pr:         Manage pull requests

ADDITIONAL COMMANDS
  alias:      Create command shortcuts
  api:        Make an authenticated GitHub API request

FLAGS
  --help      Show help for command
  --version   Show gh version

EXAMPLES
  gh issue list
  gh pr checkout 123
`

const dockerHelp = `Usage:  docker [OPTIONS] COMMAND

A self-sufficient runtime for containers

Management Commands:
  container   Manage containers
  image       Manage images
  network     Manage networks

Commands:
  build       Build an image from a Dockerfile
  run         Run a command in a new container
  ps          List containers

Global Options:
  -D, --debug              Enable debug mode
  -v, --version            Print version information
  -H, --host <host>        Daemon socket to connect to
`

const mmdcHelp = `Usage: mmdc [options]

Options:
  -t, --theme <theme>      Theme of the chart (default: "default")
  -w, --width <width>      Width of the page (default: 800)
  -i, --input <input>      Input mermaid file. Files ending in .md will be treated as markdown.
  -o, --output <output>    Output file. It should be either md, svg, png or pdf
  --help                   Display help for command
  --version                Print version number
`

func TestParseGitHelp(t *testing.T) {
	spec := ParseHelpOutput("git", gitHelp)
	require.NotNil(t, spec)
	assert.Equal(t, "git", spec.Name)

	names := cmdNames(spec)
	assert.Contains(t, names, "clone")
	assert.Contains(t, names, "init")
	assert.Contains(t, names, "add")
	assert.Contains(t, names, "mv")
	assert.Contains(t, names, "restore")
	assert.Len(t, spec.Flags, 0, "git top-level help has no flags section")
}

func TestParseGhHelp(t *testing.T) {
	spec := ParseHelpOutput("gh", ghHelp)
	require.NotNil(t, spec)
	assert.Equal(t, "gh", spec.Name)

	names := cmdNames(spec)
	assert.Contains(t, names, "browse")
	assert.Contains(t, names, "codespace")
	assert.Contains(t, names, "issue")
	assert.Contains(t, names, "pr")
	assert.Contains(t, names, "alias")
	assert.Contains(t, names, "api")

	assert.Len(t, spec.Flags, 2)
	assert.Equal(t, "--help", spec.Flags[0].Name)
	assert.Equal(t, "--version", spec.Flags[1].Name)
}

func TestParseDockerHelp(t *testing.T) {
	spec := ParseHelpOutput("docker", dockerHelp)
	require.NotNil(t, spec)
	assert.Equal(t, "docker", spec.Name)

	names := cmdNames(spec)
	assert.Contains(t, names, "container")
	assert.Contains(t, names, "image")
	assert.Contains(t, names, "network")
	assert.Contains(t, names, "build")
	assert.Contains(t, names, "run")
	assert.Contains(t, names, "ps")

	require.True(t, len(spec.Flags) >= 3)
	flagNames := make(map[string]bool)
	for _, f := range spec.Flags {
		flagNames[f.Name] = true
	}
	assert.True(t, flagNames["--debug"])
	assert.True(t, flagNames["--version"])
	assert.True(t, flagNames["--host"])

	// Verify short flags parsed.
	for _, f := range spec.Flags {
		if f.Name == "--debug" {
			assert.Equal(t, "-D", f.Short)
		}
	}
}

func TestParseMmdcHelp(t *testing.T) {
	spec := ParseHelpOutput("mmdc", mmdcHelp)
	require.NotNil(t, spec)
	assert.Equal(t, "mmdc", spec.Name)

	assert.Len(t, spec.Commands, 0, "mmdc has no commands")
	require.True(t, len(spec.Flags) >= 4)

	flagNames := make(map[string]bool)
	for _, f := range spec.Flags {
		flagNames[f.Name] = true
	}
	assert.True(t, flagNames["--theme"])
	assert.True(t, flagNames["--width"])
	assert.True(t, flagNames["--input"])
	assert.True(t, flagNames["--output"])
	assert.True(t, flagNames["--help"])

	for _, f := range spec.Flags {
		if f.Name == "--theme" {
			assert.Equal(t, "-t", f.Short)
		}
	}
}

func TestParseHelp_Safety_Destructive(t *testing.T) {
	help := `Usage: myctl [command]

Commands:
  delete       Delete a resource
  list         List resources
`
	spec := ParseHelpOutput("myctl", help)
	require.NotNil(t, spec)

	del := findCmd(spec, "delete")
	require.NotNil(t, del)
	require.NotNil(t, del.Safety, "destructive command gets Safety")
	assert.Equal(t, toolspec.SafetyLevelDangerous, del.Safety.Level)
}

func TestParseHelp_Safety_Safe(t *testing.T) {
	help := `Usage: myctl [command]

Commands:
  list         List resources
`
	spec := ParseHelpOutput("myctl", help)
	require.NotNil(t, spec)

	list := findCmd(spec, "list")
	require.NotNil(t, list)
	require.NotNil(t, list.Safety, "safe command gets Safety")
	assert.Equal(t, toolspec.SafetyLevelSafe, list.Safety.Level)
}

func TestParseHelp_Safety_Confirmation(t *testing.T) {
	help := `Usage: myctl delete [flags]

Commands:
  delete       Delete a resource

Flags:
  --yes       Skip confirmation prompt
`
	spec := ParseHelpOutput("myctl", help)
	require.NotNil(t, spec)

	del := findCmd(spec, "delete")
	require.NotNil(t, del)
	require.NotNil(t, del.Safety)
	assert.True(t, del.Safety.RequiresConfirmation,
		"--yes flag implies requires confirmation")
}

func TestParseHelp_PreviewModes_DryRun(t *testing.T) {
	help := `Usage: myctl apply [flags]

Commands:
  apply        Apply changes

Flags:
  --dry-run    Preview changes without applying
`
	spec := ParseHelpOutput("myctl", help)
	require.NotNil(t, spec)

	apply := findCmd(spec, "apply")
	require.NotNil(t, apply)
	assert.Contains(t, apply.PreviewModes, "dryrun",
		"--dry-run flag adds dryrun preview mode")
}

func TestParseHelp_Contract_Destructive(t *testing.T) {
	help := `Usage: myctl [command]

Commands:
  rm           Remove files
  list         List files
`
	spec := ParseHelpOutput("myctl", help)
	require.NotNil(t, spec)

	rm := findCmd(spec, "rm")
	require.NotNil(t, rm)
	require.NotNil(t, rm.Contract, "destructive command gets Contract")
	assert.Contains(t, rm.Contract.SideEffects, "destructive")
}

func findCmd(spec *toolspec.ToolSpec, name string) *toolspec.Command {
	for i := range spec.Commands {
		if spec.Commands[i].Name == name {
			return &spec.Commands[i]
		}
	}
	return nil
}

func cmdNames(spec *toolspec.ToolSpec) []string {
	var out []string
	for _, c := range spec.Commands {
		out = append(out, c.Name)
	}
	return out
}
