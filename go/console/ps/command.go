package ps

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Provider supplies entries for the ps command.
// Each tool implements this interface to report its active processes.
type Provider interface {
	List(ctx context.Context) ([]Entry, error)
}

// Command creates a cobra command wired to a Provider.
//
// The returned command supports:
//   - --json          output as JSON
//   - --all / -a      include done entries (filtered by default)
//   - --quiet / -q    read from viper (parent persistent flag)
//   - --watch / -w    enable watch mode (re-poll)
//   - --interval / -i poll interval in watch mode (default 5s)
func Command(name string, p Provider, v *viper.Viper) *cobra.Command {
	var (
		jsonFlag     bool
		allFlag      bool
		watchFlag    bool
		intervalFlag time.Duration
	)

	cmd := &cobra.Command{
		Use:   "ps",
		Short: fmt.Sprintf("Show active %s processes", name),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			w := cmd.OutOrStdout()

			noColor := v.GetBool("no-color")
			quiet := v.GetBool("quiet")

			format := "table"
			if jsonFlag {
				format = "json"
			}
			if quiet {
				format = "quiet"
			}

			if watchFlag {
				interval := intervalFlag
				if interval <= 0 {
					interval = 5 * time.Second
				}
				return runWatch(ctx, w, p, format, noColor, allFlag, interval)
			}

			entries, err := p.List(ctx)
			if err != nil {
				return err
			}

			if !allFlag {
				entries = filterActive(entries)
			}

			return Render(w, entries, format, noColor)
		},
	}

	cmd.Flags().BoolVar(&jsonFlag, "json", false, "Output as JSON")
	cmd.Flags().BoolVarP(&allFlag, "all", "a", false,
		"Include completed entries")
	cmd.Flags().BoolVarP(&watchFlag, "watch", "w", false,
		"Enable watch mode (re-poll at --interval)")
	cmd.Flags().DurationVarP(&intervalFlag, "interval", "i",
		5*time.Second, "Poll interval for watch mode")

	return cmd
}

// filterActive removes entries with StatusDone.
func filterActive(entries []Entry) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Status != StatusDone {
			out = append(out, e)
		}
	}
	return out
}
