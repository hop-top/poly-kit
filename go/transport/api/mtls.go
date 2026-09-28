package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
)

// Principal sources for [ClientCertConfig.Principal]: where in the
// client certificate the caller's principal is read from.
const (
	// PrincipalSAN is the first URI SAN, else the first DNS SAN, else
	// the first email SAN. It is the default.
	PrincipalSAN = "san"
	// PrincipalSANURI is the first URI SAN (a SPIFFE ID, say).
	PrincipalSANURI = "san_uri"
	// PrincipalSANDNS is the first DNS SAN.
	PrincipalSANDNS = "san_dns"
	// PrincipalSANEmail is the first email SAN.
	PrincipalSANEmail = "san_email"
	// PrincipalCN is the subject common name.
	PrincipalCN = "cn"
)

// PrincipalSources lists the values [ClientCertConfig.Principal]
// accepts.
var PrincipalSources = []string{PrincipalSAN, PrincipalSANURI, PrincipalSANDNS, PrincipalSANEmail, PrincipalCN}

// ClientCertConfig configures [ClientCertAuth].
type ClientCertConfig struct {
	// Principal names where the principal is read from, one of
	// [PrincipalSources]; empty means [PrincipalSAN].
	Principal string
	// TenantOID, when set, reads the tenant from the subject
	// attribute with this OID (2.5.4.10 is O, 2.5.4.11 is OU), else
	// from a certificate extension with this OID holding a string.
	TenantOID asn1.ObjectIdentifier
	// TenantSAN, when set, reads the tenant from the first URI, DNS
	// or email SAN it matches: its first capture group, or the whole
	// match when it has none.
	TenantSAN *regexp.Regexp
}

// ClientCertAuth returns an [AuthFunc] that authenticates a request
// by the client certificate the TLS handshake verified: the principal
// and tenant come from the certificate, as a [Claims].
//
// It refuses a request with no verified certificate — a plain HTTP
// request, or a TLS one whose client presented none — and one whose
// certificate carries no principal where cfg says to look. It checks
// nothing the handshake did not: the chain was verified against the
// server's ClientCAs before the request was read.
//
// The certificate is read from the request's TLS state, or, for a
// request that carries none (the synthetic request an RPC interceptor
// builds), from the connection [TLSConnContext] recorded in its
// context.
func ClientCertAuth(cfg ClientCertConfig) AuthFunc {
	return func(r *http.Request) (any, error) {
		st := TLSState(r)
		if st == nil {
			return nil, errors.New("client certificate required: the request did not arrive over TLS")
		}
		if len(st.VerifiedChains) == 0 || len(st.VerifiedChains[0]) == 0 {
			return nil, errors.New("client certificate required")
		}
		return ClientCertClaims(st.VerifiedChains[0][0], cfg)
	}
}

// ClientCertClaims extracts the principal and tenant cfg describes
// from a verified client certificate.
func ClientCertClaims(cert *x509.Certificate, cfg ClientCertConfig) (Claims, error) {
	principal, err := certPrincipal(cert, cfg.Principal)
	if err != nil {
		return Claims{}, err
	}
	return Claims{Subject: principal, Tenant: certTenant(cert, cfg)}, nil
}

func certPrincipal(cert *x509.Certificate, source string) (string, error) {
	uris := make([]string, 0, len(cert.URIs))
	for _, u := range cert.URIs {
		uris = append(uris, u.String())
	}
	first := func(vals []string) string {
		if len(vals) > 0 {
			return vals[0]
		}
		return ""
	}
	var p string
	switch source {
	case "", PrincipalSAN:
		source = PrincipalSAN
		for _, vals := range [][]string{uris, cert.DNSNames, cert.EmailAddresses} {
			if p = first(vals); p != "" {
				break
			}
		}
	case PrincipalSANURI:
		p = first(uris)
	case PrincipalSANDNS:
		p = first(cert.DNSNames)
	case PrincipalSANEmail:
		p = first(cert.EmailAddresses)
	case PrincipalCN:
		p = cert.Subject.CommonName
	default:
		return "", fmt.Errorf("unknown principal source %q", source)
	}
	if p == "" {
		return "", fmt.Errorf("client certificate carries no principal (%s)", source)
	}
	return p, nil
}

func certTenant(cert *x509.Certificate, cfg ClientCertConfig) string {
	if len(cfg.TenantOID) > 0 {
		for _, n := range cert.Subject.Names {
			if n.Type.Equal(cfg.TenantOID) {
				if s, ok := n.Value.(string); ok {
					return s
				}
			}
		}
		for _, ext := range cert.Extensions {
			if !ext.Id.Equal(cfg.TenantOID) {
				continue
			}
			var s string
			if rest, err := asn1.Unmarshal(ext.Value, &s); err == nil && len(rest) == 0 {
				return s
			}
		}
	}
	if cfg.TenantSAN != nil {
		var sans []string
		for _, u := range cert.URIs {
			sans = append(sans, u.String())
		}
		sans = append(sans, cert.DNSNames...)
		sans = append(sans, cert.EmailAddresses...)
		for _, san := range sans {
			m := cfg.TenantSAN.FindStringSubmatch(san)
			switch {
			case m == nil:
				continue
			case len(m) > 1:
				return m[1]
			default:
				return m[0]
			}
		}
	}
	return ""
}

type tlsConnKey struct{}

// TLSConnContext is an [http.Server.ConnContext] hook that records a
// TLS connection in every request context the server derives from it,
// so [TLSState] can answer for a request that does not carry the
// connection's TLS state itself. A non-TLS connection records nothing.
func TLSConnContext(ctx context.Context, c net.Conn) context.Context {
	if tc, ok := c.(*tls.Conn); ok {
		return context.WithValue(ctx, tlsConnKey{}, tc)
	}
	return ctx
}

// TLSState returns the TLS state of the connection r arrived on: r.TLS
// when set, else that of the connection [TLSConnContext] recorded in
// r's context, once its handshake is complete. Nil when r did not
// arrive over TLS.
func TLSState(r *http.Request) *tls.ConnectionState {
	if r == nil {
		return nil
	}
	if r.TLS != nil {
		return r.TLS
	}
	tc, ok := r.Context().Value(tlsConnKey{}).(*tls.Conn)
	if !ok {
		return nil
	}
	st := tc.ConnectionState()
	if !st.HandshakeComplete {
		return nil
	}
	return &st
}
