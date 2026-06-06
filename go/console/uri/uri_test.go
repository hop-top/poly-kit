package uri_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/cite/handle/generate"
	"hop.top/cite/scheme"
	kitcli "hop.top/kit/go/console/cli"
	uricmd "hop.top/kit/go/console/uri"
)

func runCommand(t *testing.T, cmd *cobra.Command, args ...string) (string, string, error) {
	t.Helper()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func testConfig() uricmd.Config {
	return uricmd.Config{
		Policy: scheme.Policy{
			DefaultNamespaceSegments: 1,
			SchemeNamespaceSegments:  map[string]int{"tlc": 2, "task": 2},
			VanityAliases: []scheme.VanityAlias{{
				From:           "task://mine",
				To:             "task://hop-top/uri/T-0001",
				Prefix:         true,
				PreserveSuffix: true,
			}},
			ActionRoutes: map[string]scheme.ActionRoute{
				"task.claim": {Command: "tlc", Args: []string{"-C", "{namespace}", "task", "claim", "{id}"}},
			},
		},
		Types: []scheme.TypeRegistration{{
			Name: "task",
			Completer: func(_ context.Context, prefix string) ([]string, error) {
				if prefix == "T-" {
					return []string{"hop-top/uri/T-0001", "hop-top/uri/T-0002"}, nil
				}
				return []string{"hop-top/uri/T-0001"}, nil
			},
		}},
		Handler: uricmd.HandlerConfig{
			Vendor:   "hop-top",
			App:      "tlc",
			Language: generate.LanguageGo,
			Scheme:   "tlc",
			AppPath:  "/usr/local/bin/tlc",
		},
	}
}

func TestCommand_ParseJSON(t *testing.T) {
	cmd := uricmd.Command(testConfig())
	out, _, err := runCommand(t, cmd, "parse", "tlc://org/repo/T-0001?cmd=task&verb=claim", "--format", "json")
	require.NoError(t, err)

	var got struct {
		Scheme    string `json:"scheme"`
		Namespace string `json:"namespace"`
		ID        string `json:"id"`
		Action    string `json:"action"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "tlc", got.Scheme)
	assert.Equal(t, "org/repo", got.Namespace)
	assert.Equal(t, "T-0001", got.ID)
	assert.Equal(t, "task.claim", got.Action)
}

func TestCommand_ResolveAction(t *testing.T) {
	cmd := uricmd.Command(testConfig())
	out, _, err := runCommand(t, cmd, "resolve", "tlc://org/repo/T-0001?name=task&action=claim", "--format", "text")
	require.NoError(t, err)
	assert.Equal(t, "tlc -C org/repo task claim T-0001\n", out)
}

func TestCommand_CompleteTypeAndVanity(t *testing.T) {
	cmd := uricmd.Command(testConfig())
	out, _, err := runCommand(t, cmd, "complete", "--type", "task", "--prefix", "T-")
	require.NoError(t, err)
	assert.Equal(t, "hop-top/uri/T-0001\nhop-top/uri/T-0002\n", out)

	cmd = uricmd.Command(testConfig())
	out, _, err = runCommand(t, cmd, "complete", "--input", "task://mune")
	require.NoError(t, err)
	assert.Contains(t, out, "task://mine\tcanonical: task://hop-top/uri/T-0001")
}

func TestCommand_HandlerIDAndGenerate(t *testing.T) {
	cmd := uricmd.Command(testConfig())
	out, _, err := runCommand(t, cmd, "handler", "id")
	require.NoError(t, err)
	assert.Equal(t, "hop-top.tlc.go.tlc\n", out)

	cmd = uricmd.Command(testConfig())
	out, _, err = runCommand(t, cmd, "handler", "generate", "--platform", "linux")
	require.NoError(t, err)
	assert.Contains(t, out, "MimeType=x-scheme-handler/tlc;")
	assert.Contains(t, out, "X-Hop-Handler-ID=hop-top.tlc.go.tlc")
}

func TestCommand_HandlerGenerateWritesAndDryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tlc.desktop")

	root := kitcli.New(kitcli.Config{Name: "fixture", Short: "fixture", DisableValidate: true})
	uricmd.Register(root.Cmd, testConfig())
	root.Cmd.SetOut(&bytes.Buffer{})
	root.Cmd.SetErr(&bytes.Buffer{})
	root.Cmd.SetArgs([]string{"uri", "handler", "generate", "--platform", "linux", "--output", path, "--dry-run"})
	require.NoError(t, root.Execute(context.Background()))
	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err), "dry-run must not write handler artifact")

	root = kitcli.New(kitcli.Config{Name: "fixture", Short: "fixture", DisableValidate: true})
	uricmd.Register(root.Cmd, testConfig())
	root.Cmd.SetOut(&bytes.Buffer{})
	root.Cmd.SetErr(&bytes.Buffer{})
	root.Cmd.SetArgs([]string{"uri", "handler", "generate", "--platform", "linux", "--output", path})
	require.NoError(t, root.Execute(context.Background()))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "MimeType=x-scheme-handler/tlc;")
}

func TestCommand_DisabledCommandsAndRename(t *testing.T) {
	cfg := testConfig()
	cfg.CommandName = "link"
	cfg.DisabledCommands = []string{"resolve", "handler.generate"}
	cmd := uricmd.Command(cfg)
	assert.Equal(t, "link", cmd.Name())
	_, _, err := runCommand(t, cmd, "--help")
	require.NoError(t, err)
	assert.Nil(t, findChild(cmd, "resolve"))
	handler := findChild(cmd, "handler")
	require.NotNil(t, handler)
	assert.Nil(t, findChild(handler, "generate"))
}

func TestCommand_ShellCompletion(t *testing.T) {
	root := &cobra.Command{Use: "fixture"}
	uricmd.Register(root, testConfig())
	out, _, err := runCommand(t, root, "uri", "completion", "bash")
	require.NoError(t, err)
	assert.True(t, strings.Contains(out, "bash completion") || strings.Contains(out, "__start_fixture"))
}

func findChild(cmd *cobra.Command, name string) *cobra.Command {
	for _, child := range cmd.Commands() {
		if child.Name() == name {
			return child
		}
	}
	return nil
}
