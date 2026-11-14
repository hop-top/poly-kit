package oauth

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"

	"hop.top/kit/go/storage/secret"
)

// secretKind and secretVersion are the uma "secret" envelope identity.
const (
	secretKind    = "secret"
	secretVersion = 1
)

type wireSecret struct {
	V     int    `json:"v"`
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// encodeSecret returns the uma "secret" envelope holding value.
func encodeSecret(value string) ([]byte, error) {
	if value == "" {
		return nil, invalid("value", "required")
	}
	return marshal(wireSecret{V: secretVersion, Kind: secretKind, Value: value})
}

// getEnvelope reads the raw envelope at key. field names the grant field the
// ref came from. Not found is a ValidationError on that field; any other
// store failure is ErrTransient without the underlying text.
func getEnvelope(ctx context.Context, store secret.Store, key, field string) ([]byte, error) {
	s, err := store.Get(ctx, key)
	switch {
	case errors.Is(err, secret.ErrNotFound):
		return nil, invalid(field, "secret not found")
	case err != nil:
		return nil, fmt.Errorf("oauth: read %s: %w", field, ErrTransient)
	}
	return s.Value, nil
}

// readSecret resolves a uma "secret" envelope: {"v":1,"kind":"secret",
// "value"|"value_base64"}. field names the grant field in errors; errors
// never carry values.
func readSecret(ctx context.Context, store secret.Store, key, field string) (string, error) {
	raw, err := getEnvelope(ctx, store, key, field)
	if err != nil {
		return "", err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return "", invalid(field, "secret envelope is not a JSON object")
	}
	for name := range fields {
		switch name {
		case "v", "kind", "value", "value_base64":
		default:
			return "", invalid(field, "secret envelope has unknown field")
		}
	}
	var w struct {
		V      *int    `json:"v"`
		Kind   *string `json:"kind"`
		Value  *string `json:"value"`
		Base64 *string `json:"value_base64"`
	}
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&w); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) {
			return "", invalid(te.Field, "wrong type in secret envelope")
		}
		return "", invalid(field, "secret envelope malformed")
	}
	switch {
	case w.V == nil:
		return "", invalid("v", "required in secret envelope")
	case *w.V != secretVersion:
		return "", invalid("v", "unsupported secret envelope version")
	case w.Kind == nil || *w.Kind != secretKind:
		return "", invalid("kind", "secret envelope kind must be secret")
	case (w.Value == nil) == (w.Base64 == nil):
		return "", invalid("value", "exactly one of value or value_base64 required")
	}
	out, name := "", "value"
	if w.Value != nil {
		out = *w.Value
	} else {
		name = "value_base64"
		b, err := base64.StdEncoding.DecodeString(*w.Base64)
		if err != nil {
			return "", invalid(name, "not valid base64")
		}
		out = string(b)
	}
	if out == "" {
		return "", invalid(name, "empty")
	}
	return out, nil
}

// readClientCert loads the uma client_cert envelope at ref: an RSA key and
// its certificate. Errors name fields only.
func readClientCert(ctx context.Context, store secret.Store, ref string) (*x509.Certificate, *rsa.PrivateKey, error) {
	raw, err := getEnvelope(ctx, store, ref, "client_cert_ref")
	if err != nil {
		return nil, nil, err
	}
	var env struct {
		V    int    `json:"v"`
		Kind string `json:"kind"`
		Cert string `json:"certificate_pem"`
		Key  string `json:"private_key_pem"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, nil, invalid("client_cert_ref", "not a JSON object")
	}
	if env.V != 1 {
		return nil, nil, invalid("v", "unsupported version")
	}
	if env.Kind != "client_cert" {
		return nil, nil, invalid("kind", "must be client_cert")
	}
	blk, _ := pem.Decode([]byte(env.Cert))
	if blk == nil || blk.Type != "CERTIFICATE" {
		return nil, nil, invalid("certificate_pem", "no PEM certificate")
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return nil, nil, invalid("certificate_pem", "not a valid certificate")
	}
	kb, _ := pem.Decode([]byte(env.Key))
	if kb == nil {
		return nil, nil, invalid("private_key_pem", "no PEM private key")
	}
	var key *rsa.PrivateKey
	switch kb.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(kb.Bytes)
		if err != nil {
			return nil, nil, invalid("private_key_pem", "not a valid PKCS#1 RSA key")
		}
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
		if err != nil {
			return nil, nil, invalid("private_key_pem", "not a valid PKCS#8 key")
		}
		var ok bool
		if key, ok = k.(*rsa.PrivateKey); !ok {
			return nil, nil, invalid("private_key_pem", "RSA key required")
		}
	default:
		return nil, nil, invalid("private_key_pem", "RSA key required")
	}
	if pub, ok := cert.PublicKey.(*rsa.PublicKey); !ok || !pub.Equal(&key.PublicKey) {
		return nil, nil, invalid("private_key_pem", "does not match certificate")
	}
	return cert, key, nil
}
