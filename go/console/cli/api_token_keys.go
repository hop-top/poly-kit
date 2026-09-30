package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/authn"
)

// tokenKeyCmd is `token key`: issue, list and revoke the API keys a
// service under auth.mode: apikey accepts. Each verb opens the store
// services.<--service>.auth.apikey names.
func tokenKeyCmd(r *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Issue, list and revoke API keys",
	}
	cmd.AddCommand(tokenKeyCreateCmd(r), tokenKeyListCmd(r), tokenKeyRevokeCmd(r))
	return cmd
}

// withAPIKeys opens the key store of the --service flag's service and
// runs fn over it.
func withAPIKeys(cmd *cobra.Command, r *Root, fn func(context.Context, *authn.APIKeys) error) error {
	svc, _ := cmd.Flags().GetString("service")
	res := tlsResolver{cfg: svcconfig.New(r.Viper), svc: svc}
	if err := res.cfg.ValidateBlock(authAPIKeyBlock, svc, svcconfig.Shared); err != nil {
		return output.WrapError(err, output.CodeUsage, output.ExitUsage)
	}
	cfg, err := res.apiKeyStoreConfig(r)
	if err != nil {
		return output.WrapError(err, output.CodeUsage, output.ExitUsage)
	}
	ctx := cmd.Context()
	store, err := openAPIKeyStore(ctx, cfg)
	if err != nil {
		return fmt.Errorf("api key store %s: %w", cfg.Path, err)
	}
	defer func() { _ = store.Close() }()
	return fn(ctx, authn.NewAPIKeys(store, nil))
}

func addServiceFlag(cmd *cobra.Command) {
	cmd.Flags().String("service", APIServiceName, "Service whose auth.apikey store holds the keys")
}

func tokenKeyCreateCmd(r *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Issue an API key",
		Long: "Issue an API key for a principal and print it. The key is shown once:\n" +
			"the store keeps its hash. A service with auth.mode: apikey accepts it in\n" +
			"X-API-Key or Authorization: Bearer.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sub, _ := cmd.Flags().GetString("sub")
			tenant, _ := cmd.Flags().GetString("tenant")
			scopes, _ := cmd.Flags().GetStringSlice("scopes")
			expires, _ := cmd.Flags().GetDuration("expires")
			if strings.TrimSpace(sub) == "" {
				return output.UsageError("token key create: --sub is required: the principal the key stands for")
			}
			if expires < 0 {
				return output.UsageError("token key create: --expires must not be negative; 0 never expires")
			}
			for _, s := range scopes {
				if s == "" || strings.ContainsAny(s, " \t\r\n") {
					return output.UsageError(fmt.Sprintf(
						"token key create: scope %q: a scope is one non-empty word with no spaces", s))
				}
			}
			return withAPIKeys(cmd, r, func(ctx context.Context, keys *authn.APIKeys) error {
				raw, _, err := keys.Create(ctx, authn.NewAPIKey{
					Principal: sub, Tenant: tenant, Scopes: scopes, TTL: expires,
				})
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), raw)
				return nil
			})
		},
	}
	cmd.Flags().String("sub", "", "Principal the key stands for (required)")
	cmd.Flags().String("tenant", "", "Tenant the principal acts within")
	cmd.Flags().StringSlice("scopes", nil, "Comma-separated scopes")
	cmd.Flags().Duration("expires", 0, "Key lifetime; 0 never expires")
	addServiceFlag(cmd)
	SetSideEffect(cmd, SideEffectWriteLocal)
	SetIdempotency(cmd, IdempotencyNo)
	return cmd
}

// apiKeyRow is one row of token key list.
type apiKeyRow struct {
	ID        string   `json:"id" table:"ID"`
	Principal string   `json:"principal" table:"PRINCIPAL"`
	Tenant    string   `json:"tenant,omitempty" table:"TENANT"`
	Scopes    []string `json:"scopes,omitempty" table:"SCOPES"`
	State     string   `json:"state" table:"STATE"`
	CreatedAt string   `json:"created_at" table:"CREATED"`
	ExpiresAt string   `json:"expires_at,omitempty" table:"EXPIRES"`
	RevokedAt string   `json:"revoked_at,omitempty" table:"REVOKED"`
}

func tokenKeyListCmd(r *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List API keys",
		Long: "List every API key in the store, newest first, with its principal,\n" +
			"tenant, scopes and state: active, expired or revoked. Secrets are never\n" +
			"stored, so none is shown.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withAPIKeys(cmd, r, func(ctx context.Context, keys *authn.APIKeys) error {
				list, err := keys.List(ctx)
				if err != nil {
					return err
				}
				now := time.Now()
				rows := make([]apiKeyRow, 0, len(list))
				for _, k := range list {
					row := apiKeyRow{ID: k.ID, Principal: k.Principal, Tenant: k.Tenant,
						Scopes: k.Scopes, CreatedAt: k.CreatedAt.Format(time.RFC3339), State: "active"}
					if !k.ExpiresAt.IsZero() {
						row.ExpiresAt = k.ExpiresAt.Format(time.RFC3339)
						if !now.Before(k.ExpiresAt) {
							row.State = "expired"
						}
					}
					if !k.RevokedAt.IsZero() {
						row.RevokedAt, row.State = k.RevokedAt.Format(time.RFC3339), "revoked"
					}
					rows = append(rows, row)
				}
				return output.Dispatch(cmd, r.Viper, rows)
			})
		},
	}
	addServiceFlag(cmd)
	SetSideEffect(cmd, SideEffectRead)
	SetIdempotency(cmd, IdempotencyYes)
	_ = SetOutputSchema(cmd, OutputSchema{Type: &[]apiKeyRow{}, Version: "1.0"})
	return cmd
}

func tokenKeyRevokeCmd(r *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke an API key",
		Long: "Revoke the API key with this id; it stops working at once. The record\n" +
			"stays listed as revoked. Exit 3 when no key has the id.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withAPIKeys(cmd, r, func(ctx context.Context, keys *authn.APIKeys) error {
				rec, err := keys.Revoke(ctx, args[0])
				if errors.Is(err, authn.ErrAPIKeyNotFound) {
					return output.NotFoundError(fmt.Sprintf("token key revoke: no API key has id %q", args[0]))
				}
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "revoked %s (%s)\n", rec.ID, rec.Principal)
				return nil
			})
		},
	}
	addServiceFlag(cmd)
	SetSideEffect(cmd, SideEffectWriteLocal)
	SetIdempotency(cmd, IdempotencyYes)
	return cmd
}
