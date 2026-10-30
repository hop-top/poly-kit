package completionspec_test

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/cmdreflect"
	"hop.top/kit/go/console/cli/completionspec"
)

// Cobra-only rules the shared corpus cannot express: a port's CLI
// framework has no nameless command and no NoFileCompletions.

func TestFromCobraSkipsNamelessAndCompleteCommands(t *testing.T) {
	root := &cobra.Command{Use: "tool"}
	root.SetHelpCommand(&cobra.Command{Hidden: true})
	root.AddCommand(&cobra.Command{Use: "run", Run: func(*cobra.Command, []string) {}})
	root.InitDefaultHelpCmd()
	root.AddCommand(&cobra.Command{Use: cobra.ShellCompRequestCmd, Hidden: true})

	spec := completionspec.FromCobra(root)
	require.Len(t, spec.Root.Commands, 1)
	assert.Equal(t, "run", spec.Root.Commands[0].Name)
}

// TestFromCobraNoFileCompletionsOffersNothing: cobra.NoFileCompletions
// is cobra's "no candidates" completer. A generator calling back for
// it would only ever return nothing.
func TestFromCobraNoFileCompletionsOffersNothing(t *testing.T) {
	root := &cobra.Command{Use: "tool"}
	leaf := &cobra.Command{
		Use:               "run <job>",
		ValidArgsFunction: cobra.NoFileCompletions,
		Run:               func(*cobra.Command, []string) {},
	}
	leaf.Flags().String("name", "", "a name")
	require.NoError(t, leaf.RegisterFlagCompletionFunc("name", cobra.NoFileCompletions))
	root.AddCommand(leaf)

	got := completionspec.FromCobra(root).Root.Commands[0]
	require.Len(t, got.Args, 1)
	assert.Equal(t, completionspec.CompletionNone, got.Args[0].Complete.Kind)
	require.NotNil(t, got.Flags[0].Arg)
	assert.Equal(t, completionspec.CompletionNone, got.Flags[0].Arg.Complete.Kind)
}

func TestFromCobraNil(t *testing.T) {
	assert.Equal(t, completionspec.Spec{}, completionspec.FromCobra(nil))
}

// TestKitArgsGrammarMatchesCmdreflect: kit/args has one grammar, the
// one cmdreflect reads (comma list, "?" optional, "..." variadic,
// "...?" optional variadic). Any other suffix is part of the name.
func TestKitArgsGrammarMatchesCmdreflect(t *testing.T) {
	for _, raw := range []string{"id", "id, name?", "files...", " a ,, b? ", "rest...?", "id?...", "name, files...?"} {
		t.Run(raw, func(t *testing.T) {
			root := &cobra.Command{Use: "tool"}
			leaf := &cobra.Command{
				Use:         "run",
				Annotations: map[string]string{"kit/args": raw},
				Run:         func(*cobra.Command, []string) {},
			}
			root.AddCommand(leaf)

			got := completionspec.FromCobra(root).Root.Commands[0].Args
			want := cmdreflect.Reflect(root).Lookup("run").Args
			require.Len(t, got, len(want))
			for i := range want {
				assert.Equal(t, want[i].Name, got[i].Name)
				assert.Equal(t, !want[i].Required, got[i].Optional)
				assert.Equal(t, want[i].Variadic, got[i].Variadic)
			}
		})
	}
}

// TestKitArgsVariadic pins the four kit/args forms on the completion
// spec's own shape.
func TestKitArgsVariadic(t *testing.T) {
	root := &cobra.Command{Use: "tool"}
	leaf := &cobra.Command{
		Use:         "run",
		Annotations: map[string]string{"kit/args": "name, note?, files...?"},
		Run:         func(*cobra.Command, []string) {},
	}
	root.AddCommand(leaf)
	more := &cobra.Command{
		Use:         "cat",
		Annotations: map[string]string{"kit/args": "files..."},
		Run:         func(*cobra.Command, []string) {},
	}
	root.AddCommand(more)

	spec := completionspec.FromCobra(root)
	assert.Equal(t, []completionspec.Arg{{Name: "files", Variadic: true}}, spec.Root.Commands[0].Args)
	assert.Equal(t, []completionspec.Arg{
		{Name: "name"},
		{Name: "note", Optional: true},
		{Name: "files", Optional: true, Variadic: true},
	}, spec.Root.Commands[1].Args)
}
