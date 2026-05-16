package cli

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/storage/secret"
)

// AuthStatusCmd builds an `<tool> auth status` subcommand that
// introspects the given secret.Store for the named keys and renders
// per-key StoredMeta rows. The command is read-tagged (no network
// round-trips, no state mutation): adopters use it as a no-network
// presence check; auth check (the network variant) lives in adopter
// code where backend semantics are tool-specific.
//
// Wiring example:
//
//	authCmd := &cobra.Command{Use: "auth", Short: "Authentication"}
//	authCmd.AddCommand(cli.AuthStatusCmd(secretStore, []string{
//	    "github_token", "openai_api_key",
//	}))
//	root.Cmd.AddCommand(authCmd)
//
// When store does not implement secret.MetadataReader, the command
// returns an error explaining the configured backend cannot expose
// metadata. When a key is absent, that row reports "missing"; when
// a key is present but the backend itself returns ErrNotSupported,
// that row reports "unsupported" — neither is a fatal error.
//
// Output format follows the standard kit --format flag (table/json/
// yaml/csv/text). The default is table.
func AuthStatusCmd(store secret.Store, keys []string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show authentication status",
		Long: "Inspect the configured secret store for known keys and " +
			"report per-key metadata (source, expiry, scopes, last update). " +
			"Performs no network calls — see `auth check` for live verification.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthStatus(cmd, store, keys)
		},
	}
	SetSideEffect(cmd, SideEffectRead)
	return cmd
}

// AuthStatusRow is one rendered row in the auth status table. Status
// captures the lookup outcome ("ok" / "missing" / "unsupported") so
// adopters can sort/filter without having to inspect ExpiresAt nil-ness.
type AuthStatusRow struct {
	Key        string `json:"key"                  yaml:"key"                  table:"Key,priority=9"`
	Status     string `json:"status"               yaml:"status"               table:"Status,priority=8"`
	Source     string `json:"source,omitempty"     yaml:"source,omitempty"     table:"Source,priority=7"`
	Backend    string `json:"backend,omitempty"    yaml:"backend,omitempty"    table:"Backend,priority=6"`
	UpdatedAt  string `json:"updated_at,omitempty" yaml:"updated_at,omitempty" table:"Updated,priority=5"`
	ExpiresAt  string `json:"expires_at,omitempty" yaml:"expires_at,omitempty" table:"Expires,priority=4"`
	AuthMethod string `json:"auth_method,omitempty" yaml:"auth_method,omitempty" table:"Method,priority=3"`
	Scopes     string `json:"scopes,omitempty"     yaml:"scopes,omitempty"     table:"Scopes,priority=2"`
}

// CollectAuthStatus walks keys and gathers a StoredMeta row for each.
// Errors per-key are coalesced into the row's Status field; an error
// is returned only if the store itself does not implement
// MetadataReader (a configuration mistake adopters must surface).
func CollectAuthStatus(ctx context.Context, store secret.Store, keys []string) ([]AuthStatusRow, error) {
	reader, ok := store.(secret.MetadataReader)
	if !ok {
		return nil, errors.New("auth status: backend does not expose metadata")
	}
	rows := make([]AuthStatusRow, 0, len(keys))
	for _, key := range keys {
		row := AuthStatusRow{Key: key}
		meta, err := reader.Metadata(ctx, key)
		switch {
		case err == nil:
			row.Status = "ok"
			row.Source = meta.Source
			row.Backend = meta.Backend
			row.AuthMethod = meta.AuthMethod
			if !meta.UpdatedAt.IsZero() {
				row.UpdatedAt = meta.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")
			}
			if meta.ExpiresAt != nil && !meta.ExpiresAt.IsZero() {
				row.ExpiresAt = meta.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
			}
			if len(meta.Scopes) > 0 {
				row.Scopes = joinScopes(meta.Scopes)
			}
		case errors.Is(err, secret.ErrNotFound):
			row.Status = "missing"
		case errors.Is(err, secret.ErrNotSupported):
			row.Status = "unsupported"
		default:
			row.Status = "error: " + err.Error()
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// joinScopes concatenates scope tokens with ",". Empty list is the
// caller's signal to omit the column entirely.
func joinScopes(scopes []string) string {
	if len(scopes) == 0 {
		return ""
	}
	out := scopes[0]
	for _, s := range scopes[1:] {
		out += "," + s
	}
	return out
}

func runAuthStatus(cmd *cobra.Command, store secret.Store, keys []string) error {
	rows, err := CollectAuthStatus(cmd.Context(), store, keys)
	if err != nil {
		return err
	}
	format := resolveAuthStatusFormat(cmd)
	return output.Render(cmd.OutOrStdout(), format, rows)
}

// resolveAuthStatusFormat returns the active --format value for the
// command, falling back to "table" when the flag is unregistered or
// unset. Walks the parent chain to discover the format flag wired by
// kit's Root constructor.
func resolveAuthStatusFormat(cmd *cobra.Command) output.Format {
	for c := cmd; c != nil; c = c.Parent() {
		if f := c.Flags().Lookup("format"); f != nil {
			if v := f.Value.String(); v != "" {
				return v
			}
		}
		if pf := c.PersistentFlags().Lookup("format"); pf != nil {
			if v := pf.Value.String(); v != "" {
				return v
			}
		}
	}
	return output.Table
}
