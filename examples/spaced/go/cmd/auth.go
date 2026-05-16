package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli"
)

// AuthStatusCmd returns the `auth status` command.
func AuthStatusCmd(root *cli.Root) *cobra.Command {
	return &cobra.Command{
		Use:   "auth",
		Short: "Show authentication status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cred, err := root.Auth.Inspect(cmd.Context())
			if err != nil {
				return fmt.Errorf("auth inspect: %w", err)
			}

			format := root.Viper.GetString("format")
			if format == "json" || format == "yaml" {
				enc := json.NewEncoder(root.Streams.Data)
				enc.SetIndent("", "  ")
				return enc.Encode(cred)
			}

			w := root.Streams.Human
			fmt.Fprintln(w)
			fmt.Fprintln(w, "  ── AUTH STATUS ─────────────────────────────────")
			fmt.Fprintf(w, "  Source   : %s\n", cred.Source)
			fmt.Fprintf(w, "  Identity : %s\n", cred.Identity)
			if len(cred.Scopes) > 0 {
				fmt.Fprintf(w, "  Scopes   : %v\n", cred.Scopes)
			}
			if cred.ExpiresAt != nil {
				fmt.Fprintf(w, "  Expires  : %s\n",
					cred.ExpiresAt.Format("2006-01-02 15:04:05 UTC"))
			}
			fmt.Fprintf(w, "  Renewable: %v\n", cred.Renewable)
			fmt.Fprintln(w)
			return nil
		},
	}
}
