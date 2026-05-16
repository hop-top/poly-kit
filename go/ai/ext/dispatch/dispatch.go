// Package dispatch bridges ext/discover and cobra, registering
// discovered plugin binaries as transparent subcommands. Import this
// package only in binaries that need plugin dispatch — it pulls in
// ext/discover.
package dispatch

import (
	"io"
	"os/exec"
	"sync"

	"github.com/spf13/cobra"
	"hop.top/kit/go/ai/ext/discover"
)

const groupID = "plugins"

// Register scans for <prefix>-* binaries in searchDir (or $PATH when
// empty) and adds each as a cobra subcommand under a "Plugins" group.
// Metadata enrichment via --ext-info is deferred until help is rendered.
func Register(cmd *cobra.Command, prefix string, searchDir string) {
	if prefix == "" {
		return
	}
	s := &discover.Scanner{Prefix: prefix + "-"}
	if searchDir != "" {
		s.Paths = []string{searchDir}
	}

	found, err := s.Scan()
	if err != nil || len(found) == 0 {
		return
	}

	cmd.AddGroup(&cobra.Group{
		ID:    groupID,
		Title: "Plugins:",
	})

	for _, f := range found {
		f := f
		var enrichOnce sync.Once

		sub := &cobra.Command{
			Use:                f.Name,
			GroupID:            groupID,
			DisableFlagParsing: true,
			SilenceUsage:       true,
			SilenceErrors:      true,
			RunE: func(c *cobra.Command, args []string) error {
				return runPlugin(c, f.Path, args)
			},
		}

		original := sub.HelpFunc()
		sub.SetHelpFunc(func(c *cobra.Command, args []string) {
			enrichOnce.Do(func() {
				if err := f.Enrich(); err == nil {
					c.Short = f.Meta().Description
				}
			})
			original(c, args)
		})

		cmd.AddCommand(sub)
	}
}

func runPlugin(cmd *cobra.Command, binPath string, args []string) error {
	c := exec.CommandContext(cmd.Context(), binPath, args...)
	c.Stdin = stdinFrom(cmd)
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.ErrOrStderr()
	return c.Run()
}

func stdinFrom(cmd *cobra.Command) io.Reader {
	if r := cmd.InOrStdin(); r != nil {
		return r
	}
	return nil
}
