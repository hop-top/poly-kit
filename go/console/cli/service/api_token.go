package service

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/authn"
)

// mountTokenIfKeyed mounts the token command when a serve option
// touched the Root and it has something to issue: an identity keypair (cli.WithIdentity),
// API keys (WithAPIKeys) or API auth. A key source mounts it with or
// without WithAPI: a tool that serves only mcp or rpc under auth.mode
// apikey or jwt must be able to issue what its service accepts without
// registering a REST api that exposes the whole tree. It runs from
// OptionsApplied, before the reserved snapshot, so token stays
// kit-reserved and no served surface lists or runs it.
func mountTokenIfKeyed(r *cli.Root) {
	s, served := lookup(r)
	if !served {
		return
	}
	keyed := cli.IdentityConfigured(r) || s.apiKeysCfg != nil || (s.apiCfg != nil && s.apiCfg.Auth != nil)
	if keyed {
		mountTokenCmd(r)
	}
}

// mountTokenCmd completes the kit-owned `token` command with the serve
// plane's leaves. cli.MountTokenCommand mounts the command, or finds
// the one already there (cli-side, from WithIdentity, or the
// adopter's), with claims, decode and create; this adds verify always
// and key create|list|revoke when the tool issues API keys. One token
// command either way, idempotent, whichever order the options ran in.
func mountTokenCmd(r *cli.Root) {
	cmd := cli.MountTokenCommand(r)
	has := map[string]bool{}
	for _, c := range cmd.Commands() {
		has[c.Name()] = true
	}
	if !has["verify"] {
		cmd.AddCommand(tokenVerifyCmd(r))
	}
	if !has["key"] && stateOf(r).apiKeysCfg != nil {
		cmd.AddCommand(tokenKeyCmd(r))
	}
}

// The serve-side token leaves carry the annotations a validating root
// requires of every leaf — kit/side-effect, kit/idempotent, Long —
// because the root mounts them into the adopter's tree. The command is
// kit-reserved: the api, mcp and rpc surfaces withhold it as
// management-only, the socket withholds it by name, and no served
// caller can mint or probe tokens.

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
func tokenVerifyCmd(r *cli.Root) *cobra.Command {
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
	cli.SetSideEffect(cmd, cli.SideEffectRead)
	cli.SetIdempotency(cmd, cli.IdempotencyYes)
	// The verdict prints as JSON on both exits, valid and refused.
	_ = cli.SetOutputSchema(cmd, cli.OutputSchema{Type: &tokenVerdict{}, Version: "1.0"})
	return cmd
}

// tokenVerifier is the verifier token verify uses for svc: the one its
// auth.mode configures, else the tool's identity key; and the mode.
func tokenVerifier(r *cli.Root, svc string) (*authn.Verifier, string, error) {
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
	v, err := authn.NewJWT([]authn.Key{authn.IdentityKey(r.Identity)}, authn.Options{Check: tokenCheck(r)})
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
