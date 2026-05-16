package dispatch_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/ai/ext/dispatch"
	"hop.top/kit/go/console/cli"
)

func writeExec(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0755))
}

func pluginRoot(t *testing.T, binDir string) *cli.Root {
	t.Helper()
	r := cli.New(cli.Config{
		Name:            "foo",
		Version:         "0.1.0",
		Short:           "test tool with plugins",
		DisableValidate: true,
	})
	dispatch.Register(r.Cmd, "foo", binDir)
	return r
}

func TestRegister_DiscoversPlugins(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, dir, "foo-youtube", "#!/bin/sh\necho youtube")
	writeExec(t, dir, "foo-deploy", "#!/bin/sh\necho deploy")
	writeExec(t, dir, "bar-other", "#!/bin/sh\necho other")

	r := pluginRoot(t, dir)

	names := commandNames(r.Cmd)
	assert.Contains(t, names, "youtube")
	assert.Contains(t, names, "deploy")
	assert.NotContains(t, names, "other",
		"binaries with wrong prefix must not appear")
}

func TestRegister_PluginsInHelpOutput(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, dir, "foo-youtube", "#!/bin/sh\necho youtube")

	r := pluginRoot(t, dir)
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetArgs([]string{"--help"})
	err := r.Execute(t.Context())
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "youtube", "plugin should appear in help")
	assert.Contains(t, out, "PLUGINS", "plugin group heading expected")
}

func TestRegister_ExecutesPlugin(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "ran.txt")
	script := "#!/bin/sh\n" +
		"echo \"dispatched: $@\" > '" +
		strings.ReplaceAll(outFile, "'", `'"'"'`) + "'\n"
	writeExec(t, dir, "foo-youtube", script)

	r := pluginRoot(t, dir)
	r.Cmd.SetArgs([]string{"youtube", "https://yt.be/123"})
	err := r.Execute(t.Context())
	require.NoError(t, err)

	data, err := os.ReadFile(outFile)
	require.NoError(t, err)
	assert.Equal(t, "dispatched: https://yt.be/123",
		strings.TrimSpace(string(data)))
}

func TestRegister_UnknownSubcommandShowsHelp(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, dir, "foo-real", "#!/bin/sh\necho real")

	r := pluginRoot(t, dir)
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	r.Cmd.SetArgs([]string{"nonexistent"})
	err := r.Execute(t.Context())

	out := buf.String()
	assert.Contains(t, out, "USAGE",
		"unknown subcommand should show help/usage")
	if err != nil {
		assert.Contains(t, err.Error(), "unknown",
			"if error returned, should mention unknown command")
	}
}

func TestRegister_NoPluginsFound(t *testing.T) {
	dir := t.TempDir()
	r := pluginRoot(t, dir)

	for _, g := range r.Cmd.Groups() {
		assert.NotEqual(t, "Plugins:", g.Title,
			"no plugin group when no plugins found")
	}
}

func TestRegister_EnrichesDescription(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = "--ext-info" ]; then
  echo '{"name":"youtube","version":"2.0.0","description":"Download YouTube videos"}'
  exit 0
fi
echo "running"
`
	writeExec(t, dir, "foo-youtube", script)

	r := pluginRoot(t, dir)

	var buf bytes.Buffer
	for _, c := range r.Cmd.Commands() {
		if c.Name() == "youtube" {
			c.SetOut(&buf)
			c.Help()
			assert.Equal(t, "Download YouTube videos", c.Short,
				"enriched plugin should use --ext-info description")
			return
		}
	}
	t.Fatal("youtube command not found")
}

func TestRegister_PassesArgs(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\n" +
		"echo \"$@\" > '" +
		strings.ReplaceAll(outFile, "'", `'"'"'`) + "'\n"
	writeExec(t, dir, "foo-echo", script)

	r := pluginRoot(t, dir)
	r.Cmd.SetArgs([]string{"echo", "hello", "world"})
	err := r.Execute(t.Context())
	require.NoError(t, err)

	data, err := os.ReadFile(outFile)
	require.NoError(t, err)
	assert.Equal(t, "hello world", strings.TrimSpace(string(data)))
}

func commandNames(cmd *cobra.Command) []string {
	var names []string
	for _, c := range cmd.Commands() {
		if !c.Hidden {
			names = append(names, c.Name())
		}
	}
	return names
}
