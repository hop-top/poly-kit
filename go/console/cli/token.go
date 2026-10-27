package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/core/identity"
)

// tokenCommandName is the kit-owned token command's word.
const tokenCommandName = "token"

// MountTokenCommand mounts the kit-owned `token` command on r, or
// completes the one already there (an adopter's own, mounted through an
// option, gets the kit's leaves grafted into it), and returns it. It
// adds claims and decode always, and create when the tool has an
// identity keypair to sign with (WithIdentity). Leaves already present
// by name are left alone, so it is idempotent.
//
// New calls it once every option has run when WithIdentity is set, so
// a tool that serves nothing can still mint the self-signed tokens
// another tool's `auth.mode: jwt` accepts through
// `auth.jwt.public_key_files`. The serve plane (go/console/cli/service)
// calls it too and adds verify and key to the same command. Either way
// it is mounted before New's snapshot, so token is kit-reserved and no
// served surface lists or runs it. Everything here builds on
// go/core/identity alone.
func MountTokenCommand(r *Root) *cobra.Command {
	var cmd *cobra.Command
	for _, c := range r.Cmd.Commands() {
		if c.Name() == tokenCommandName {
			cmd = c
			break
		}
	}
	if cmd == nil {
		cmd = &cobra.Command{
			Use:   tokenCommandName,
			Short: "Manage API tokens",
		}
		r.Cmd.AddCommand(cmd)
	}
	has := map[string]bool{}
	for _, c := range cmd.Commands() {
		has[c.Name()] = true
	}
	if !has["claims"] {
		cmd.AddCommand(tokenClaimsCmd())
	}
	if !has["decode"] {
		cmd.AddCommand(tokenDecodeCmd())
	}
	if !has["create"] && IdentityConfigured(r) {
		cmd.AddCommand(tokenCreateCmd(r))
	}
	return cmd
}

// tokenClaims is the JSON structure token claims prints.
type tokenClaims struct {
	Sub    string   `json:"sub"`
	Scopes []string `json:"scopes,omitempty"`
	Iat    int64    `json:"iat"`
	Exp    int64    `json:"exp"`
}

// Every token leaf carries the annotations a validating root requires
// of every leaf — kit/side-effect, kit/idempotent, Long — because the
// root mounts them into the adopter's tree, and a tree that fails its
// own validator would refuse to start.
func tokenClaimsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claims",
		Short: "Print a structured claims template (not a signed token)",
		Long: "Print a JSON claims template — subject, scopes, issued-at and expiry —\n" +
			"for an external signer to turn into a token. Nothing is signed here;\n" +
			"`token create` signs with the tool's own identity keypair.",
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

	SetSideEffect(cmd, SideEffectRead)
	SetIdempotency(cmd, IdempotencyYes)
	// JSON whatever --format says: the template is for a signer.
	_ = SetOutputSchema(cmd, OutputSchema{Type: &tokenClaims{}, Version: "1.0"})
	return cmd
}

// tokenCreated is what token create renders under a data format (json,
// yaml). Human formats print the bare token instead, so the
// $(tool token create) scripts depend on keeps working.
type tokenCreated struct {
	Token string `json:"token" yaml:"token"`
}

// tokenCreateCmd signs a token with the tool's identity keypair — the
// token `auth.mode: jwt` accepts, from the tool's own identity or from
// a key in `auth.jwt.public_key_files`.
func tokenCreateCmd(r *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Sign a token with the tool's identity keypair",
		Long: "Sign a JWT with the tool's identity keypair (EdDSA, its kid in the header)\n" +
			"and print it. A service with auth.mode: jwt accepts it: this tool's own,\n" +
			"or another tool's that names this tool's public key in\n" +
			"auth.jwt.public_key_files. The scopes are written twice: scope,\n" +
			"space-delimited, as OAuth 2.0 resource servers read it, and scopes, as a\n" +
			"list.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if r.Identity == nil {
				return output.UsageError("token create: the tool has no identity keypair to sign with")
			}
			sub, _ := cmd.Flags().GetString("sub")
			scopes, _ := cmd.Flags().GetStringSlice("scopes")
			tenant, _ := cmd.Flags().GetString("tenant")
			audience, _ := cmd.Flags().GetStringSlice("audience")
			issuer, _ := cmd.Flags().GetString("issuer")
			expires, _ := cmd.Flags().GetDuration("expires")

			if strings.TrimSpace(sub) == "" {
				return output.UsageError("token create: --sub is required: the principal the token names")
			}
			if expires <= 0 {
				return output.UsageError("token create: --expires must be positive: every token expires")
			}
			for _, s := range scopes {
				if s == "" || strings.IndexFunc(s, unicode.IsSpace) >= 0 {
					return output.UsageError(fmt.Sprintf(
						"token create: scope %q: a scope is one non-empty word with no spaces", s))
				}
			}

			now := time.Now()
			raw, err := r.Identity.SignJWT(identity.Claims{
				Subject:   sub,
				Issuer:    issuer,
				Audience:  identity.Audience(audience),
				Tenant:    tenant,
				Scopes:    scopes,
				Scope:     strings.Join(scopes, " "),
				IssuedAt:  now.Unix(),
				ExpiresAt: now.Add(expires).Unix(),
			})
			if err != nil {
				return err
			}
			if f := ActiveFormat(cmd, r.Viper); f == output.JSON || f == output.YAML {
				return output.Dispatch(cmd, r.Viper, tokenCreated{Token: raw})
			}
			fmt.Fprintln(cmd.OutOrStdout(), raw)
			return nil
		},
	}
	cmd.Flags().String("sub", "", "Subject: the principal the token names (required)")
	cmd.Flags().StringSlice("scopes", nil, "Comma-separated scopes")
	cmd.Flags().String("tenant", "", "Tenant the principal acts within")
	cmd.Flags().StringSlice("audience", nil, "Audiences (aud) the token is for")
	cmd.Flags().String("issuer", "", "Issuer (iss)")
	cmd.Flags().Duration("expires", 24*time.Hour, "Token lifetime")

	SetSideEffect(cmd, SideEffectRead)
	SetIdempotency(cmd, IdempotencyNo)
	// Describes --format json|yaml; human formats print the bare token.
	_ = SetOutputSchema(cmd, OutputSchema{Type: &tokenCreated{}, Version: "1.0"})
	return cmd
}

func tokenDecodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decode [token]",
		Short: "Decode and print JWT payload (no signature verification)",
		Long: "Decode the payload segment of a JWT and print it as JSON. The signature\n" +
			"is not verified; this is an inspection aid, not an authentication step.\n" +
			"`token verify`, mounted with the serve plane, checks it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			parts := strings.Split(args[0], ".")
			if len(parts) != 3 {
				return fmt.Errorf("invalid JWT format: expected 3 parts, got %d", len(parts))
			}
			// Decode payload (part 1) without signature verification.
			payload, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil {
				return fmt.Errorf("decode payload: %w", err)
			}
			var pretty json.RawMessage
			if err := json.Unmarshal(payload, &pretty); err != nil {
				return fmt.Errorf("parse payload: %w", err)
			}
			out, _ := json.MarshalIndent(pretty, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
	SetSideEffect(cmd, SideEffectRead)
	SetIdempotency(cmd, IdempotencyYes)
	// A JWT payload is a JSON object of claims (RFC 7519 §4), printed
	// as-is; the claim names are the issuer's, so none is fixed here.
	_ = SetOutputSchema(cmd, OutputSchema{Type: &map[string]any{}, Version: "1.0"})
	return cmd
}
