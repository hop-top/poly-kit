package oauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/oauth2"
)

// safeCode returns code only when it is a known RFC 6749 section 5.2 error
// token, so server-supplied text never reaches an error message.
func safeCode(code string) string {
	switch code {
	case "invalid_client", "unauthorized_client", "invalid_grant",
		"invalid_request", "invalid_scope", "unsupported_grant_type",
		// OpenID Connect section 3.1.2.6 codes some providers use to say a
		// refresh token needs the user back.
		"interaction_required", "consent_required", "login_required":
		return code
	}
	return ""
}

// mapTokenStatus maps a non-2xx token endpoint reply. The message carries
// the status and an allowlisted code only.
//
//   - 3xx: redirect refused, plain error (no sentinel)
//   - 5xx, 429, 408: ErrTransient
//   - invalid_grant, interaction_required, consent_required, login_required:
//     ErrNeedsReconsent for refresh_token, else ErrCredentialRejected
//   - TLS certificate verification failure (see mapTokenError): plain error
//   - anything else: ErrCredentialRejected
func mapTokenStatus(kind GrantKind, status int, code string) error {
	code = safeCode(code)
	suffix := ""
	if code != "" {
		suffix = " error " + code
	}
	switch {
	case status >= 300 && status < 400:
		return fmt.Errorf("oauth: token endpoint redirected (status %d), not followed", status)
	case status >= 500 || status == http.StatusTooManyRequests || status == http.StatusRequestTimeout:
		return fmt.Errorf("oauth: token endpoint status %d: %w", status, ErrTransient)
	case needsUser(code) && kind == RefreshToken:
		return fmt.Errorf("oauth: token endpoint status %d%s: %w", status, suffix, ErrNeedsReconsent)
	}
	return fmt.Errorf("oauth: token endpoint status %d%s: %w", status, suffix, ErrCredentialRejected)
}

// needsUser reports whether code says the user must grant access again.
func needsUser(code string) bool {
	switch code {
	case "invalid_grant", "interaction_required", "consent_required", "login_required":
		return true
	}
	return false
}

// isTLSVerifyError reports a certificate verification failure: a
// configuration fault that retrying cannot fix.
func isTLSVerifyError(err error) bool {
	var (
		ua x509.UnknownAuthorityError
		ci x509.CertificateInvalidError
		he x509.HostnameError
		cv *tls.CertificateVerificationError
	)
	return errors.As(err, &ua) || errors.As(err, &ci) || errors.As(err, &he) || errors.As(err, &cv)
}

// errTLSVerify: the token endpoint's certificate was not trusted. No
// sentinel: not transient, and the credential is not at fault.
var errTLSVerify = errors.New("oauth: token endpoint certificate verification failed")

// mapTokenError maps an error returned by x/oauth2 token retrieval. A
// RetrieveError carries the HTTP outcome; transport failures and
// cancellation are ErrTransient; a malformed or incomplete 2xx reply is a
// plain error.
func mapTokenError(ctx context.Context, kind GrantKind, err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		status := 0
		if re.Response != nil {
			status = re.Response.StatusCode
		}
		code := re.ErrorCode
		if code == "" {
			var b struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(re.Body, &b)
			code = b.Error
		}
		return mapTokenStatus(kind, status, code)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("oauth: token request: %w: %w", ErrTransient, ctx.Err())
	}
	if isTLSVerifyError(err) {
		return errTLSVerify
	}
	var ne net.Error
	msg := err.Error()
	switch {
	case errors.As(err, &ne),
		// x/oauth2 flattens transport and body-read failures into text
		strings.HasPrefix(msg, "oauth2: cannot fetch token:"):
		return fmt.Errorf("oauth: token request failed: %w", ErrTransient)
	}
	return errors.New("oauth: token response invalid")
}

// seconds parses expires_in given as a JSON number or numeric string; 0 when
// absent or malformed.
func seconds(raw json.RawMessage) int64 {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
