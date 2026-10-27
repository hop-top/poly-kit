package completionspec

import (
	"io"

	"github.com/spf13/cobra"
)

// Register adds `fig` and `usage` under completion, cobra's completion
// command, next to its bash|zsh|fish|powershell leaves. Each prints the
// spec of the tree completion belongs to. A nil completion (the tree
// disabled cobra's default command) registers nothing. Idempotent.
func Register(completion *cobra.Command) {
	if completion == nil {
		return
	}
	have := map[string]bool{}
	for _, c := range completion.Commands() {
		have[c.Name()] = true
	}
	if !have["fig"] {
		completion.AddCommand(NewFigCommand())
	}
	if !have["usage"] {
		completion.AddCommand(NewUsageCommand())
	}
}

// NewFigCommand returns the `fig` subcommand: print the Fig spec of the
// command's root.
func NewFigCommand() *cobra.Command {
	return newExportCommand("fig",
		"Generate a Fig completion spec",
		"Print a Fig completion spec for this tool: an ES module whose default\n"+
			"export is the spec. VS Code terminal suggest, Kiro CLI, inshellisense\n"+
			"and Warp read this format. Values a completion function computes are\n"+
			"fetched from this binary when the user asks.",
		RenderFig)
}

// NewUsageCommand returns the `usage` subcommand: print the Usage KDL
// spec of the command's root.
func NewUsageCommand() *cobra.Command {
	return newExportCommand("usage",
		"Generate a Usage (usage.jdx.dev) completion spec",
		"Print a Usage KDL spec for this tool, for usage.jdx.dev and mise:\n"+
			"`usage generate completion <shell>` turns it into a shell script, and\n"+
			"`usage generate markdown` into reference docs. Values a completion\n"+
			"function computes are fetched from this binary when the user asks.",
		RenderUsage)
}

func newExportCommand(name, short, long string, render func(Spec) string) *cobra.Command {
	return &cobra.Command{
		Use:                   name,
		Short:                 short,
		Long:                  long,
		Args:                  cobra.NoArgs,
		DisableFlagsInUseLine: true,
		ValidArgsFunction:     cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := io.WriteString(cmd.OutOrStdout(), render(FromCobra(cmd.Root())))
			return err
		},
	}
}
