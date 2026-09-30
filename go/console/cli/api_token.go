package cli

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/transport/authn"
)

// mountTokenCmd mounts the kit-owned `token` command, or completes one
// already mounted: claims, decode and verify always, create when the
// tool has an identity keypair to sign with (cli.WithIdentity), and
// key create|list|revoke when it issues API keys (cli.WithAPIKeys).
// It is idempotent, so WithAPI and New can both call it whichever
// order the options ran in.
func (r *Root) mountTokenCmd() {
	var cmd *cobra.Command
	for _, c := range r.Cmd.Commands() {
		if c.Name() == "token" {
			cmd = c
			break
		}
	}
	if cmd == nil {
		cmd = &cobra.Command{
			Use:   "token",
			Short: "Manage API tokens",
		}
		r.Cmd.AddCommand(cmd)
	}
	has := map[string]bool{}
	for _, c := range cmd.Commands() {
		has[c.Name()] = true
	}
	if !has["claims"] {
		cmd.AddCommand(tokenClaimsCmd(r))
	}
	if !has["decode"] {
		cmd.AddCommand(tokenDecodeCmd(r))
	}
	if !has["verify"] {
		cmd.AddCommand(tokenVerifyCmd(r))
	}
	if !has["create"] && r.identityCfg != nil {
		cmd.AddCommand(tokenCreateCmd(r))
	}
	if !has["key"] && r.apiKeysCfg != nil {
		cmd.AddCommand(tokenKeyCmd(r))
	}
}

// tokenClaims is the JSON structure token claims prints.
type tokenClaims struct {
	Sub    string   `json:"sub"`
	Scopes []string `json:"scopes,omitempty"`
	Iat    int64    `json:"iat"`
	Exp    int64    `json:"exp"`
}

// Every token leaf carries the annotations a validating root requires
// of every leaf — kit/side-effect, kit/idempotent, Long — because
// the root mounts them into the adopter's tree, and a tree that fails
// its own validator would refuse to start. The command is mounted by
// cli.New, so it is kit-reserved: served surfaces withhold it as
// management-only, and no remote caller can mint or probe tokens.
func tokenClaimsCmd(_ *Root) *cobra.Command {
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

// tokenCreateCmd signs a token with the tool's identity keypair — the
// token `auth.mode: jwt` accepts.
func tokenCreateCmd(r *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Sign a token with the tool's identity keypair",
		Long: "Sign a JWT with the tool's identity keypair (EdDSA, its kid in the header)\n" +
			"and print it. A service with auth.mode: jwt accepts it. The scopes are\n" +
			"written twice: scope, space-delimited, as OAuth 2.0 resource servers read\n" +
			"it, and scopes, as a list.",
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
	return cmd
}

// tokenVerdict is what token verify prints.
type tokenVerdict struct {
	Valid     bool           `json:"valid"`
	Error     string         `json:"error,omitempty"`
	Service   string         `json:"service"`
	Mode      string         `json:"mode"`
	KeyID     string         `json:"kid,omitempty"`
	Subject   string         `json:"sub,omitempty"`
	Tenant    string         `json:"tenant,omitempty"`
	Scopes    []string       `json:"scopes,omitempty"`
	ExpiresAt *time.Time     `json:"expires_at,omitempty"`
	Claims    map[string]any `json:"claims,omitempty"`
}

// tokenVerifyCmd checks a token as a service would.
func tokenVerifyCmd(r *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify <token>",
		Short: "Check a token as a service would accept it",
		Long: "Verify a JWT with the verifier services.<service>.auth.mode configures\n" +
			"(jwt, jwks or oidc), or, with no bearer mode, against the tool's identity\n" +
			"keypair. Prints the verdict and the claims as JSON. Exit 0 when the token\n" +
			"is valid, 5 when it is refused, 6 when the key set could not be fetched,\n" +
			"2 when there is nothing to verify with.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _ := cmd.Flags().GetString("service")
			v, mode, err := tokenVerifier(r, svc)
			if err != nil {
				return err
			}
			verdict := tokenVerdict{Service: svc, Mode: mode}
			tok, verr := v.Verify(cmd.Context(), args[0])
			if verr == nil {
				exp := tok.Expiry
				verdict.Valid = true
				verdict.KeyID, verdict.Subject, verdict.Tenant = tok.KeyID, tok.Subject, tok.Tenant
				verdict.Scopes, verdict.ExpiresAt, verdict.Claims = tok.Scopes, &exp, tok.Raw
			} else {
				verdict.Error = verr.Error()
				// The payload, unverified, so the operator can see why:
				// the expiry, the audience, the issuer.
				verdict.Claims = unverifiedPayload(args[0])
			}
			data, err := json.MarshalIndent(verdict, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
			switch {
			case verr == nil:
				return nil
			case errors.Is(verr, authn.ErrKeySetUnavailable):
				return output.WrapError(verr, output.CodeTransient, output.ExitTransient)
			}
			return output.WrapError(verr, output.CodeUnauthorized, output.ExitUnauthorized)
		},
	}
	cmd.Flags().String("service", APIServiceName, "Service whose auth.mode verifies the token")
	SetSideEffect(cmd, SideEffectRead)
	SetIdempotency(cmd, IdempotencyYes)
	// The verdict prints as JSON on both exits, valid and refused.
	_ = SetOutputSchema(cmd, OutputSchema{Type: &tokenVerdict{}, Version: "1.0"})
	return cmd
}

// tokenVerifier is the verifier token verify uses for svc: the one its
// auth.mode configures, else the tool's identity key; and the mode.
func tokenVerifier(r *Root, svc string) (*authn.Verifier, string, error) {
	res, mode, _, err := resolveAuthMode(svcconfig.New(r.Viper), svc)
	if err != nil {
		return nil, "", output.WrapError(err, output.CodeUsage, output.ExitUsage)
	}
	switch mode {
	case AuthModeJWT, AuthModeJWKS, AuthModeOIDC:
		v, err := res.bearerVerifier(r, mode)
		if err != nil {
			return nil, "", output.WrapError(err, output.CodeUsage, output.ExitUsage)
		}
		return v, mode, nil
	}
	if r.Identity == nil {
		return nil, "", output.UsageError(fmt.Sprintf(
			"token verify: nothing to verify with: services.%s.auth.mode is not jwt, jwks or oidc, "+
				"and the tool has no identity keypair", svc))
	}
	v, err := authn.NewJWT([]authn.Key{authn.IdentityKey(r.Identity)}, authn.Options{Check: r.tokenCheck()})
	if err != nil {
		return nil, "", err
	}
	return v, AuthModeJWT, nil
}

// unverifiedPayload decodes a JWT's payload without checking anything;
// nil when it does not decode.
func unverifiedPayload(raw string) map[string]any {
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) != 3 {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return out
}

func tokenDecodeCmd(_ *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decode [token]",
		Short: "Decode and print JWT payload (no signature verification)",
		Long: "Decode the payload segment of a JWT and print it as JSON. The signature\n" +
			"is not verified; this is an inspection aid, not an authentication step.\n" +
			"`token verify` checks it.",
		Args: cobra.ExactArgs(1),
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
	SetSideEffect(cmd, SideEffectRead)
	SetIdempotency(cmd, IdempotencyYes)
	// A JWT payload is a JSON object of claims (RFC 7519 §4), printed
	// as-is; the claim names are the issuer's, so none is fixed here.
	_ = SetOutputSchema(cmd, OutputSchema{Type: &map[string]any{}, Version: "1.0"})
	return cmd
}
