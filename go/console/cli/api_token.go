package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func tokenCmd(root *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage API tokens",
	}
	cmd.AddCommand(tokenClaimsCmd(root))
	cmd.AddCommand(tokenDecodeCmd(root))
	return cmd
}

// tokenClaims is the JSON structure printed by token create.
type tokenClaims struct {
	Sub    string   `json:"sub"`
	Scopes []string `json:"scopes,omitempty"`
	Iat    int64    `json:"iat"`
	Exp    int64    `json:"exp"`
}

// Signing and verification are consumer responsibilities.
func tokenClaimsCmd(_ *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claims",
		Short: "Print a structured claims template (not a signed token)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sub, _ := cmd.Flags().GetString("sub")
			scopes, _ := cmd.Flags().GetStringSlice("scopes")
			expires, _ := cmd.Flags().GetDuration("expires")

			now := time.Now()
			claims := tokenClaims{
				Sub:    sub,
				Scopes: scopes,
				Iat:    now.Unix(),
				Exp:    now.Add(expires).Unix(),
			}

			data, err := json.MarshalIndent(claims, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return nil
		},
	}

	cmd.Flags().String("sub", "", "Subject (identity)")
	cmd.Flags().StringSlice("scopes", nil, "Comma-separated scopes")
	cmd.Flags().Duration("expires", 24*time.Hour, "Token lifetime")

	return cmd
}

func tokenDecodeCmd(_ *Root) *cobra.Command {
	return &cobra.Command{
		Use:   "decode [token]",
		Short: "Decode and print JWT payload (no signature verification)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			token := args[0]
			parts := strings.Split(token, ".")
			if len(parts) != 3 {
				return fmt.Errorf("invalid JWT format: expected 3 parts, got %d", len(parts))
			}

			// Decode payload (part 1) without signature verification.
			payload, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil {
				return fmt.Errorf("decode payload: %w", err)
			}

			// Pretty-print.
			var pretty json.RawMessage
			if err := json.Unmarshal(payload, &pretty); err != nil {
				return fmt.Errorf("parse payload: %w", err)
			}
			out, _ := json.MarshalIndent(pretty, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
}
