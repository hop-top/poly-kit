package oauth

import (
	"bytes"
	"encoding/json"
	"net"
	"net/url"
	"strings"
	"unicode"
)

// GrantVersion is the only grant envelope version.
const GrantVersion = 1

// GrantKind selects how an access token is obtained.
type GrantKind string

const (
	ClientCredentials GrantKind = "client_credentials"
	ClientAssertion   GrantKind = "client_assertion"
	JWTBearer         GrantKind = "jwt_bearer"
	RefreshToken      GrantKind = "refresh_token"
)

// Valid reports whether k is a known kind.
func (k GrantKind) Valid() bool {
	switch k {
	case ClientCredentials, ClientAssertion, JWTBearer, RefreshToken:
		return true
	}
	return false
}

// Grant is the long-lived configuration stored at <ref>. Key material is
// referenced, never inlined: SecretRef points at a uma "secret" envelope
// (client secret, service account key JSON, or refresh token) and
// ClientCertRef at a uma "client_cert" envelope. ClientSecretRef is the
// optional client secret of a confidential client, refresh_token only.
type Grant struct {
	Kind            GrantKind
	TokenURL        string
	ClientID        string
	Scopes          []string
	Subject         string
	SecretRef       string
	ClientCertRef   string
	ClientSecretRef string
}

// wireGrant is the JSON shape. Field names are the contract.
type wireGrant struct {
	V               int       `json:"v"`
	Kind            GrantKind `json:"kind"`
	TokenURL        string    `json:"token_url"`
	ClientID        string    `json:"client_id,omitempty"`
	Scopes          []string  `json:"scopes,omitempty"`
	Subject         string    `json:"subject,omitempty"`
	SecretRef       string    `json:"secret_ref,omitempty"`
	ClientCertRef   string    `json:"client_cert_ref,omitempty"`
	ClientSecretRef string    `json:"client_secret_ref,omitempty"`
}

var grantFields = map[string]bool{
	"v": true, "kind": true, "token_url": true, "client_id": true,
	"scopes": true, "subject": true, "secret_ref": true, "client_cert_ref": true, "client_secret_ref": true,
}

// Validate checks the grant per its kind. Errors are *ValidationError naming
// the field only.
//
// Required per kind: token_url always; client_id except jwt_bearer (the
// service account key carries it); secret_ref for client_credentials,
// jwt_bearer and refresh_token; client_cert_ref for client_assertion. The
// other reference is forbidden. client_secret_ref is optional for
// refresh_token and forbidden otherwise. scopes and subject are optional.
func (g Grant) Validate() error {
	if !g.Kind.Valid() {
		return invalid("kind", "must be one of client_credentials, client_assertion, jwt_bearer, refresh_token")
	}
	if err := validTokenURL(g.TokenURL); err != nil {
		return err
	}
	if g.ClientID == "" && g.Kind != JWTBearer {
		return invalid("client_id", "required")
	}
	if hasControl(g.ClientID) {
		return invalid("client_id", "contains control characters")
	}
	if hasControl(g.Subject) {
		return invalid("subject", "contains control characters")
	}
	seen := map[string]bool{}
	for _, s := range g.Scopes {
		switch {
		case s == "":
			return invalid("scopes", "empty scope")
		case strings.IndexFunc(s, unicode.IsSpace) >= 0 || hasControl(s):
			return invalid("scopes", "scope contains whitespace or control characters")
		case seen[s]:
			return invalid("scopes", "duplicate scope")
		}
		seen[s] = true
	}
	needSecret := g.Kind != ClientAssertion
	switch {
	case needSecret && g.SecretRef == "":
		return invalid("secret_ref", "required")
	case !needSecret && g.SecretRef != "":
		return invalid("secret_ref", "not allowed for this kind")
	case !needSecret && g.ClientCertRef == "":
		return invalid("client_cert_ref", "required")
	case needSecret && g.ClientCertRef != "":
		return invalid("client_cert_ref", "not allowed for this kind")
	}
	if hasControl(g.SecretRef) {
		return invalid("secret_ref", "contains control characters")
	}
	if hasControl(g.ClientCertRef) {
		return invalid("client_cert_ref", "contains control characters")
	}
	if g.ClientSecretRef != "" && g.Kind != RefreshToken {
		return invalid("client_secret_ref", "not allowed for this kind")
	}
	if hasControl(g.ClientSecretRef) {
		return invalid("client_secret_ref", "contains control characters")
	}
	return nil
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}

// validTokenURL requires an absolute https URL without userinfo or
// fragment; plain http is accepted for loopback hosts only (tests, local
// IdPs).
func validTokenURL(raw string) error {
	if raw == "" {
		return invalid("token_url", "required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return invalid("token_url", "must be an absolute URL")
	}
	if u.User != nil || u.Fragment != "" {
		return invalid("token_url", "must not contain userinfo or fragment")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Hostname()) {
			return invalid("token_url", "http allowed for loopback hosts only")
		}
	default:
		return invalid("token_url", "scheme must be https")
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// EncodeGrant validates g and returns its stored JSON.
func EncodeGrant(g Grant) ([]byte, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	return marshal(wireGrant{
		V: GrantVersion, Kind: g.Kind, TokenURL: g.TokenURL,
		ClientID: g.ClientID, Scopes: g.Scopes, Subject: g.Subject,
		SecretRef: g.SecretRef, ClientCertRef: g.ClientCertRef,
		ClientSecretRef: g.ClientSecretRef,
	})
}

// DecodeGrant parses and validates a stored grant. Unknown fields, a wrong
// version or trailing data are rejected. Errors never echo stored text.
func DecodeGrant(data []byte) (Grant, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return Grant{}, invalid("", "not a JSON object")
	}
	for name := range fields {
		if !grantFields[name] {
			return Grant{}, invalid("", "unknown field")
		}
	}
	var w wireGrant
	if err := json.Unmarshal(data, &w); err != nil {
		var te *json.UnmarshalTypeError
		if asTypeError(err, &te) {
			// te.Field is a dotted path ("scopes.1" for an element).
			top, _, _ := strings.Cut(te.Field, ".")
			if grantFields[top] {
				return Grant{}, invalid(top, "wrong type")
			}
		}
		return Grant{}, invalid("", "wrong field type")
	}
	if _, ok := fields["v"]; !ok {
		return Grant{}, invalid("v", "required")
	}
	if w.V != GrantVersion {
		return Grant{}, invalid("v", "unsupported version")
	}
	g := Grant{
		Kind: w.Kind, TokenURL: w.TokenURL, ClientID: w.ClientID,
		Scopes: w.Scopes, Subject: w.Subject,
		SecretRef: w.SecretRef, ClientCertRef: w.ClientCertRef,
		ClientSecretRef: w.ClientSecretRef,
	}
	if err := g.Validate(); err != nil {
		return Grant{}, err
	}
	return g, nil
}

// marshal encodes without HTML escaping or a trailing newline.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
