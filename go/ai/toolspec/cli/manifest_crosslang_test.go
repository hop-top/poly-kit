package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	kitcli "hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// updateManifestFixture regenerates the cross-language manifest
// goldens from this (reference) implementation:
//
//	go test ./go/ai/toolspec/cli/ -run TestManifestCrossLangFixture \
//	    -update-manifest-fixture
var updateManifestFixture = flag.Bool("update-manifest-fixture", false,
	"rewrite sdk/tests/cross-lang/fixtures/toolspec-manifest*.json")

// crossLangDir is the shared fixture directory every SDK's manifest
// builder replays.
var crossLangDir = filepath.Join("..", "..", "..", "..", "sdk", "tests", "cross-lang", "fixtures")

// treeFixture is the language-neutral command tree each SDK builds
// natively before projecting it into a Manifest.
type treeFixture struct {
	Tool     string        `json:"tool"`
	Version  string        `json:"version"`
	Reserved []string      `json:"reserved"`
	Commands []treeCommand `json:"commands"`
}

type treeCommand struct {
	Name        string            `json:"name"`
	Short       string            `json:"short"`
	Long        string            `json:"long"`
	Hidden      bool              `json:"hidden"`
	Run         bool              `json:"run"`
	Annotations map[string]string `json:"annotations"`
	Flags       []treeFlag        `json:"flags"`
	Commands    []treeCommand     `json:"commands"`
}

type treeFlag struct {
	Name        string          `json:"name"`
	Short       string          `json:"short"`
	Type        string          `json:"type"`
	Default     json.RawMessage `json:"default"`
	Description string          `json:"description"`
	Hidden      bool            `json:"hidden"`
}

func buildFixtureCommand(t *testing.T, tc treeCommand) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: tc.Name, Short: tc.Short, Long: tc.Long, Hidden: tc.Hidden}
	if tc.Run {
		cmd.Run = func(*cobra.Command, []string) {}
	}
	if len(tc.Annotations) > 0 {
		cmd.Annotations = map[string]string{}
		for k, v := range tc.Annotations {
			cmd.Annotations[k] = v
		}
	}
	for _, f := range tc.Flags {
		switch f.Type {
		case "bool":
			cmd.Flags().BoolP(f.Name, f.Short, false, f.Description)
		case "string":
			var def string
			if len(f.Default) > 0 {
				require.NoError(t, json.Unmarshal(f.Default, &def))
			}
			cmd.Flags().StringP(f.Name, f.Short, def, f.Description)
		case "stringSlice":
			var def []string
			if len(f.Default) > 0 {
				require.NoError(t, json.Unmarshal(f.Default, &def))
			}
			cmd.Flags().StringSliceP(f.Name, f.Short, def, f.Description)
		default:
			t.Fatalf("unknown fixture flag type %q", f.Type)
		}
		if f.Hidden {
			require.NoError(t, cmd.Flags().MarkHidden(f.Name))
		}
	}
	for _, child := range tc.Commands {
		cmd.AddCommand(buildFixtureCommand(t, child))
	}
	return cmd
}

// TestManifestCrossLangFixture pins BuildManifest's JSON bytes for the
// shared tree. The TS SDK replays the same tree and must match them.
func TestManifestCrossLangFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(crossLangDir, "toolspec-manifest.tree.json"))
	require.NoError(t, err)
	var tree treeFixture
	require.NoError(t, json.Unmarshal(raw, &tree))

	rootCmd := &cobra.Command{Use: tree.Tool}
	for _, c := range tree.Commands {
		rootCmd.AddCommand(buildFixtureCommand(t, c))
	}
	root := &kitcli.Root{Cmd: rootCmd, Config: kitcli.Config{Name: tree.Tool, Version: tree.Version}}
	for _, name := range tree.Reserved {
		root.MarkReserved(name)
	}

	for _, tc := range []struct {
		file              string
		includeDeprecated bool
	}{
		{"toolspec-manifest.json", false},
		{"toolspec-manifest.include-deprecated.json", true},
	} {
		t.Run(tc.file, func(t *testing.T) {
			var buf bytes.Buffer
			m := BuildManifest(root, "1.1", tc.includeDeprecated)
			require.NoError(t, output.Render(&buf, output.JSON, m))

			path := filepath.Join(crossLangDir, tc.file)
			if *updateManifestFixture {
				require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))
				return
			}
			want, err := os.ReadFile(path)
			require.NoError(t, err, "regenerate with -update-manifest-fixture")
			require.Equal(t, string(want), buf.String())
		})
	}
}
