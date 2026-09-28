package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cast"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/core/xdg"
	"hop.top/kit/go/transport/api"
)

// Middleware blocks a kit HTTP listener's TLS reads, each under
// services.<svc>.<block> and, as a shared default, under
// services.all.<block>. Their keys are registered in svcconfig.
const (
	tlsBlock      = "tls"
	tlsACMEBlock  = "tls.acme"
	authBlock     = "auth"
	authMTLSBlock = "auth.mtls"
)

// AuthModeMTLS is the services.<svc>.auth.mode value that makes the
// client certificate the listener's credential.
const AuthModeMTLS = "mtls"

// ServeTLS is the resolved TLS and authentication setting of one kit
// HTTP listener — services.<svc>.tls and services.<svc>.auth — and the
// one place such a listener starts serving. The zero value and nil
// serve plain HTTP and select no verifier.
//
// The api service and the services in go/console/cli/rpcserve and
// go/console/cli/mcpserve resolve one in Validate, so a bad
// certificate is a usage error at exit 2, and again at start.
type ServeTLS struct {
	config     *tls.Config
	clientAuth api.AuthFunc
	bearer     api.AuthFunc
}

// Enabled reports whether the listener speaks TLS.
func (t *ServeTLS) Enabled() bool { return t != nil && t.config != nil }

// Scheme is "https" when the listener speaks TLS, else "http".
func (t *ServeTLS) Scheme() string {
	if t.Enabled() {
		return "https"
	}
	return "http"
}

// ClientCertAuth is the verifier auth.mode: mtls configures, nil
// under any other mode.
func (t *ServeTLS) ClientCertAuth() api.AuthFunc {
	if t == nil {
		return nil
	}
	return t.clientAuth
}

// Auth is the verifier auth.mode selects: the client certificate under
// mtls, the bearer token under jwt, jwks and oidc; nil when the mode
// is unset. A service installs it where it installs its code AuthFunc
// (APIConfig.Auth, rpcserve.Config.Auth, mcpserve.Config.Auth), and in
// its place: a configured mode wins, and the code AuthFunc applies
// only while auth.mode is unset.
func (t *ServeTLS) Auth() api.AuthFunc {
	if t == nil {
		return nil
	}
	if t.clientAuth != nil {
		return t.clientAuth
	}
	return t.bearer
}

// Serve serves srv on ln, over TLS when t is enabled, else plain.
//
// Over TLS it offers HTTP/2 and HTTP/1.1 by ALPN — replacing any
// unencrypted HTTP/2 (h2c) srv was built with, which a TLS listener
// never negotiates — and records each connection in its requests'
// context (api.TLSConnContext), so a verifier handed a synthetic
// request still finds the client certificate.
func (t *ServeTLS) Serve(srv *http.Server, ln net.Listener) error {
	if !t.Enabled() {
		return srv.Serve(ln)
	}
	srv.TLSConfig = t.config.Clone()
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	srv.Protocols = protocols
	prev := srv.ConnContext
	srv.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
		if prev != nil {
			ctx = prev(ctx, c)
		}
		return api.TLSConnContext(ctx, c)
	}
	return srv.ServeTLS(ln, "", "")
}

// ResolveServeTLS resolves the TLS and authentication setting of
// service svc's HTTP listener from services.<svc>.tls,
// services.<svc>.auth and their services.all defaults. Every refusal
// names the key at fault.
//
// TLS is on when tls.enabled is true, or unset and a certificate
// source is configured: cert_file and key_file, or tls.acme. The
// minimum version is TLS 1.2. auth.mode: mtls needs TLS on and
// auth.mtls.ca_file; it asks each client for a certificate, fails the
// handshake on one the CA bundle does not verify, and leaves a
// request that presented none to the verifier, which refuses it as
// unauthenticated — so health probes still answer, and the refusal
// is audited like any other.
//
// auth.mode: jwt, jwks or oidc selects a bearer-token verifier from
// go/transport/authn, configured by auth.jwt, auth.jwks or auth.oidc;
// it needs no TLS of the listener, though a bearer token sent in
// plaintext beyond loopback can be replayed by anyone who sees it.
// A key of a mode's block set under another mode is refused.
func ResolveServeTLS(r *Root, svc string) (*ServeTLS, error) {
	if r == nil || r.Viper == nil {
		return &ServeTLS{}, nil
	}
	cfg := svcconfig.New(r.Viper)
	for _, b := range []string{tlsBlock, tlsACMEBlock} {
		if err := cfg.ValidateBlock(b, svc, svcconfig.Shared); err != nil {
			return nil, err
		}
	}
	res, mode, modeKey, err := resolveAuthMode(cfg, svc)
	if err != nil {
		return nil, err
	}

	tc, err := res.tlsConfig(r.Config.Name)
	if err != nil {
		return nil, err
	}
	if mode != AuthModeMTLS {
		t := &ServeTLS{config: tc}
		if mode != "" {
			v, err := res.bearerVerifier(r, mode)
			if err != nil {
				return nil, err
			}
			t.bearer = v.AuthFunc()
		}
		return t, nil
	}
	if tc == nil {
		return nil, fmt.Errorf("%s: %q needs TLS; set %s and %s, or %s",
			modeKey, AuthModeMTLS,
			svcconfig.Key(svc, tlsBlock, "cert_file"), svcconfig.Key(svc, tlsBlock, "key_file"),
			svcconfig.Key(svc, tlsACMEBlock, "domains"))
	}
	pool, verify, err := res.mtls()
	if err != nil {
		return nil, err
	}
	tc.ClientCAs = pool
	tc.ClientAuth = tls.VerifyClientCertIfGiven
	return &ServeTLS{config: tc, clientAuth: api.ClientCertAuth(verify)}, nil
}

// tlsResolver reads one service's tls and auth keys.
type tlsResolver struct {
	cfg svcconfig.Resolver
	svc string
}

func (t tlsResolver) str(block, key string) (string, string) {
	raw, k, ok := t.cfg.Lookup(t.svc, block, key)
	if !ok {
		return "", svcconfig.Key(t.svc, block, key)
	}
	return strings.TrimSpace(cast.ToString(raw)), k
}

func (t tlsResolver) boolean(block, key string) (val, set bool, err error) {
	raw, k, ok := t.cfg.Lookup(t.svc, block, key)
	if !ok {
		return false, false, nil
	}
	b, err := boolValue(raw)
	if err != nil {
		return false, true, fmt.Errorf("%s: %w", k, err)
	}
	return b, true, nil
}

func (t tlsResolver) list(block, key string) []string {
	raw, _, ok := t.cfg.Lookup(t.svc, block, key)
	if !ok {
		return nil
	}
	var out []string
	for _, s := range cast.ToStringSlice(raw) {
		out = append(out, strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })...)
	}
	return out
}

// tlsConfig is the server configuration, nil when TLS is off.
func (t tlsResolver) tlsConfig(tool string) (*tls.Config, error) {
	certFile, certKey := t.str(tlsBlock, "cert_file")
	keyFile, keyKey := t.str(tlsBlock, "key_file")
	domains := t.list(tlsACMEBlock, "domains")
	acmeOn, acmeSet, err := t.boolean(tlsACMEBlock, "enabled")
	if err != nil {
		return nil, err
	}
	if !acmeSet {
		acmeOn = len(domains) > 0
	}
	files := certFile != "" || keyFile != ""

	on, set, err := t.boolean(tlsBlock, "enabled")
	if err != nil {
		return nil, err
	}
	if !set {
		on = files || acmeOn
	}
	if !on {
		return nil, nil
	}
	enabledKey := svcconfig.Key(t.svc, tlsBlock, "enabled")
	switch {
	case files && acmeOn:
		return nil, fmt.Errorf("%s: both cert_file/key_file and tls.acme are set; use one certificate source",
			svcconfig.Key(t.svc, tlsBlock, ""))
	case !files && !acmeOn:
		return nil, fmt.Errorf("%s: TLS is on but has no certificate; set %s and %s, or %s",
			enabledKey, certKey, keyKey, svcconfig.Key(t.svc, tlsACMEBlock, "domains"))
	}

	minVersion, err := t.minVersion()
	if err != nil {
		return nil, err
	}
	if files {
		if certFile == "" || keyFile == "" {
			return nil, fmt.Errorf("%s, %s: set both, or neither", certKey, keyKey)
		}
		pair, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("%s, %s: %w", certKey, keyKey, err)
		}
		return &tls.Config{MinVersion: minVersion, Certificates: []tls.Certificate{pair}}, nil
	}

	domainsKey := svcconfig.Key(t.svc, tlsACMEBlock, "domains")
	if len(domains) == 0 {
		return nil, fmt.Errorf("%s: ACME is on but names no domain", domainsKey)
	}
	cacheDir, _ := t.str(tlsACMEBlock, "cache_dir")
	if cacheDir == "" {
		dir, err := xdg.RawStateDir(tool)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", svcconfig.Key(t.svc, tlsACMEBlock, "cache_dir"), err)
		}
		cacheDir = filepath.Join(dir, "acme")
	}
	email, _ := t.str(tlsACMEBlock, "email")
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(cacheDir),
		HostPolicy: autocert.HostWhitelist(domains...),
		Email:      email,
	}
	if dirURL, _ := t.str(tlsACMEBlock, "directory_url"); dirURL != "" {
		m.Client = &acme.Client{DirectoryURL: dirURL}
	}
	tc := m.TLSConfig()
	tc.MinVersion = minVersion
	return tc, nil
}

// minVersion resolves tls.min_version: "1.2" (the default) or "1.3".
func (t tlsResolver) minVersion() (uint16, error) {
	v, k := t.str(tlsBlock, "min_version")
	switch v {
	case "", "1.2":
		return tls.VersionTLS12, nil
	case "1.3":
		return tls.VersionTLS13, nil
	}
	return 0, fmt.Errorf("%s: %q is not a supported version; use \"1.2\" or \"1.3\"", k, v)
}

// mtls resolves the auth.mtls block: the CA bundle client certificates
// must chain to, and where the identity is read from.
func (t tlsResolver) mtls() (*x509.CertPool, api.ClientCertConfig, error) {
	var verify api.ClientCertConfig
	caFile, caKey := t.str(authMTLSBlock, "ca_file")
	if caFile == "" {
		return nil, verify, fmt.Errorf("%s: auth.mode %q needs the CA bundle client certificates chain to",
			caKey, AuthModeMTLS)
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, verify, fmt.Errorf("%s: %w", caKey, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, verify, fmt.Errorf("%s: %s holds no PEM certificate", caKey, caFile)
	}

	principal, pKey := t.str(authMTLSBlock, "principal")
	principal = strings.ToLower(principal)
	if principal != "" && !slices.Contains(api.PrincipalSources, principal) {
		return nil, verify, fmt.Errorf("%s: unknown source %q; use one of %s",
			pKey, principal, strings.Join(api.PrincipalSources, ", "))
	}
	verify.Principal = principal

	oid, oidKey := t.str(authMTLSBlock, "tenant_oid")
	pattern, patKey := t.str(authMTLSBlock, "tenant_san_pattern")
	if oid != "" && pattern != "" {
		return nil, verify, fmt.Errorf("%s, %s: set one tenant source, not both", oidKey, patKey)
	}
	if oid != "" {
		if verify.TenantOID, err = parseOID(oid); err != nil {
			return nil, verify, fmt.Errorf("%s: %w", oidKey, err)
		}
	}
	if pattern != "" {
		if verify.TenantSAN, err = regexp.Compile(pattern); err != nil {
			return nil, verify, fmt.Errorf("%s: %w", patKey, err)
		}
	}
	return pool, verify, nil
}

// parseOID parses a dotted object identifier, "2.5.4.11".
func parseOID(s string) (asn1.ObjectIdentifier, error) {
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("%q is not a dotted object identifier", s)
	}
	oid := make(asn1.ObjectIdentifier, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("%q is not a dotted object identifier", s)
		}
		oid[i] = n
	}
	return oid, nil
}
