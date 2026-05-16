package svc

import (
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"hop.top/kit/go/conformance/svc"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// tokenCmd binds "kit conformance svc token {mint,list,revoke}".
func tokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Operator-only bearer-token claim CRUD",
		Args:  cobra.NoArgs,
	}
	mint := mintCmd()
	list := listCmd()
	revoke := revokeCmd()
	for _, c := range []*cobra.Command{mint, list, revoke} {
		cli.SetExemptValidation(c)
	}
	cmd.AddCommand(mint, list, revoke)
	return cmd
}

func mintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mint",
		Short: "Mint a new bearer token claim (prints plaintext ONCE)",
		Args:  cobra.NoArgs,
		RunE:  runMint,
	}
	cmd.Flags().String("claims-db", os.Getenv("KIT_CONF_SVC_CLAIMS_DB"),
		"SQLite path for the claim store (required)")
	cmd.Flags().String("tenant", "", "Tenant label for accounting")
	cmd.Flags().StringSlice("scope", nil, "Scopes; repeatable (e.g. --scope grade:myteam)")
	cmd.Flags().Int("tier-max", 1, "Maximum tier this claim may request (1-3)")
	cmd.Flags().Duration("expires-in", 0, "Token expiry duration (0 = never)")
	cmd.Flags().String("description", "", "Free-form note for audit")
	cli.SetSideEffect(cmd, cli.SideEffectWriteShared)
	cli.SetIdempotency(cmd, cli.IdempotencyNo)
	return cmd
}

func runMint(cmd *cobra.Command, _ []string) error {
	db, _ := cmd.Flags().GetString("claims-db")
	tenant, _ := cmd.Flags().GetString("tenant")
	scopes, _ := cmd.Flags().GetStringSlice("scope")
	tierMax, _ := cmd.Flags().GetInt("tier-max")
	expiresIn, _ := cmd.Flags().GetDuration("expires-in")
	desc, _ := cmd.Flags().GetString("description")

	if db == "" {
		return &output.Error{Code: "USAGE",
			Message:  "--claims-db is required (or KIT_CONF_SVC_CLAIMS_DB)",
			ExitCode: 2}
	}
	if len(scopes) == 0 {
		return &output.Error{Code: "USAGE",
			Message:  "at least one --scope is required (e.g. grade:myteam)",
			ExitCode: 2}
	}
	store, err := svc.OpenSQLClaimStore(db)
	if err != nil {
		return &output.Error{Code: svc.CodeSvcInternal,
			Message: fmt.Sprintf("open claim store: %v", err), ExitCode: 1}
	}
	defer func() { _ = store.Close() }()

	in := svc.MintInput{
		Tenant:      tenant,
		Scopes:      scopes,
		TierMax:     tierMax,
		Description: desc,
	}
	if expiresIn > 0 {
		in.ExpiresAt = time.Now().Add(expiresIn)
	}
	claim, token, err := store.Mint(cmd.Context(), in)
	if err != nil {
		return &output.Error{Code: svc.CodeSvcInternal,
			Message: fmt.Sprintf("mint: %v", err), ExitCode: 1}
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Token (copy now — it cannot be recovered):")
	fmt.Fprintln(cmd.OutOrStdout(), "")
	fmt.Fprintln(cmd.OutOrStdout(), "  "+token)
	fmt.Fprintln(cmd.OutOrStdout(), "")
	fmt.Fprintf(cmd.OutOrStdout(), "TokenID: %s\n", claim.TokenID)
	fmt.Fprintf(cmd.OutOrStdout(), "Tenant:  %s\n", claim.Tenant)
	fmt.Fprintf(cmd.OutOrStdout(), "Scopes:  %v\n", claim.Scopes)
	fmt.Fprintf(cmd.OutOrStdout(), "TierMax: %d\n", claim.TierMax)
	if !claim.ExpiresAt.IsZero() {
		fmt.Fprintf(cmd.OutOrStdout(), "Expires: %s\n", claim.ExpiresAt.Format(time.RFC3339))
	}
	return nil
}

// claimRow is a flattened view of one claim record for output.Dispatch.
// Field tags pick the same columns regardless of --format
// (table|json|yaml|text|csv).
type claimRow struct {
	TokenID   string   `table:"TOKEN_ID"     json:"token_id"     yaml:"token_id"`
	TokenSHA  string   `table:"SHA256"       json:"token_sha256" yaml:"token_sha256"`
	Tenant    string   `table:"TENANT"       json:"tenant"       yaml:"tenant"`
	Scopes    []string `table:"SCOPES"       json:"scopes"       yaml:"scopes"`
	TierMax   int      `table:"TIER_MAX"     json:"tier_max"     yaml:"tier_max"`
	Status    string   `table:"STATUS"       json:"status"       yaml:"status"`
	CreatedAt string   `table:"CREATED_AT"   json:"created_at"   yaml:"created_at"`
}

func listCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List existing claims (TokenID + sha256 prefix + scopes)",
		Args:  cobra.NoArgs,
		RunE:  runList,
	}
	cmd.Flags().String("claims-db", os.Getenv("KIT_CONF_SVC_CLAIMS_DB"),
		"SQLite path for the claim store (required)")
	cli.SetSideEffect(cmd, cli.SideEffectRead)
	return cmd
}

func runList(cmd *cobra.Command, _ []string) error {
	db, _ := cmd.Flags().GetString("claims-db")
	if db == "" {
		return &output.Error{Code: "USAGE",
			Message:  "--claims-db is required (or KIT_CONF_SVC_CLAIMS_DB)",
			ExitCode: 2}
	}
	store, err := svc.OpenSQLClaimStore(db)
	if err != nil {
		return &output.Error{Code: svc.CodeSvcInternal,
			Message: fmt.Sprintf("open claim store: %v", err), ExitCode: 1}
	}
	defer func() { _ = store.Close() }()

	claims, err := store.List(cmd.Context())
	if err != nil {
		return &output.Error{Code: svc.CodeSvcInternal,
			Message: fmt.Sprintf("list: %v", err), ExitCode: 1}
	}

	rows := make([]claimRow, 0, len(claims))
	for _, c := range claims {
		status := "active"
		if c.Revoked {
			status = "revoked"
		}
		rows = append(rows, claimRow{
			TokenID:   c.TokenID,
			TokenSHA:  hex.EncodeToString(c.TokenSHA256),
			Tenant:    c.Tenant,
			Scopes:    c.Scopes,
			TierMax:   c.TierMax,
			Status:    status,
			CreatedAt: c.CreatedAt.Format(time.RFC3339),
		})
	}
	return output.Dispatch(cmd, viper.GetViper(), rows)
}

func revokeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke <token_id>",
		Short: "Revoke a claim by TokenID",
		Args:  cobra.ExactArgs(1),
		RunE:  runRevoke,
	}
	cmd.Flags().String("claims-db", os.Getenv("KIT_CONF_SVC_CLAIMS_DB"),
		"SQLite path for the claim store (required)")
	cli.SetSideEffect(cmd, cli.SideEffectWriteShared)
	cli.SetIdempotency(cmd, cli.IdempotencyYes)
	return cmd
}

func runRevoke(cmd *cobra.Command, args []string) error {
	db, _ := cmd.Flags().GetString("claims-db")
	id := args[0]
	if db == "" {
		return &output.Error{Code: "USAGE",
			Message:  "--claims-db is required (or KIT_CONF_SVC_CLAIMS_DB)",
			ExitCode: 2}
	}
	store, err := svc.OpenSQLClaimStore(db)
	if err != nil {
		return &output.Error{Code: svc.CodeSvcInternal,
			Message: fmt.Sprintf("open claim store: %v", err), ExitCode: 1}
	}
	defer func() { _ = store.Close() }()
	if err := store.Revoke(cmd.Context(), id); err != nil {
		return &output.Error{Code: svc.CodeSvcInternal,
			Message: fmt.Sprintf("revoke: %v", err), ExitCode: 1}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "revoked %s\n", id)
	return nil
}
