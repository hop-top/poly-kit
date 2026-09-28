package cli

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"sync"
	"time"
)

// crlSet is the revocation lists of auth.mtls.crl_file: the serial
// numbers each issuer has revoked. A client certificate — the leaf, or
// an intermediate its chain passes through — whose issuer published a
// list here and whose serial is on it fails the handshake.
//
// A list is trusted only once its signature verifies against the
// certificate that issued the one being checked, so a list naming an
// issuer it was not signed by revokes nothing.
type crlSet struct {
	lists   []*x509.RevocationList
	revoked []map[string]struct{} // serial numbers, by list
	signed  sync.Map              // crlSigner → bool: the list verifies against that issuer
}

type crlSigner struct {
	list   int
	issuer [sha256.Size]byte
}

// parseCRLs parses the lists in raw — PEM "X509 CRL" blocks, or one
// DER list — and checks each one bundle holds the issuer of: a list
// whose issuer is in the CA bundle must be signed by it. A list issued
// by an intermediate the bundle does not hold is checked against the
// chain a client presents.
func parseCRLs(raw []byte, bundle []*x509.Certificate) (*crlSet, error) {
	var ders [][]byte
	rest := raw
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		if b.Type == "X509 CRL" {
			ders = append(ders, b.Bytes)
		}
	}
	if bytes.Contains(rest, []byte("-----BEGIN")) {
		return nil, errors.New("a PEM block is incomplete (was the file read while being written?)")
	}
	if len(ders) == 0 && !bytes.Contains(raw, []byte("-----BEGIN")) {
		ders = [][]byte{raw} // DER
	}
	if len(ders) == 0 {
		return nil, errors.New(`holds no revocation list (PEM "X509 CRL" or DER)`)
	}

	s := &crlSet{}
	for _, der := range ders {
		l, err := x509.ParseRevocationList(der)
		if err != nil {
			return nil, err
		}
		if err := checkCRLIssuer(l, bundle); err != nil {
			return nil, err
		}
		serials := make(map[string]struct{}, len(l.RevokedCertificateEntries))
		for _, e := range l.RevokedCertificateEntries {
			serials[e.SerialNumber.String()] = struct{}{}
		}
		s.lists = append(s.lists, l)
		s.revoked = append(s.revoked, serials)
	}
	return s, nil
}

// checkCRLIssuer refuses a list whose issuer the bundle holds but did
// not sign it: the wrong file, or a list for another CA of that name.
func checkCRLIssuer(l *x509.RevocationList, bundle []*x509.Certificate) error {
	named := false
	for _, ca := range bundle {
		if !bytes.Equal(ca.RawSubject, l.RawIssuer) {
			continue
		}
		named = true
		if l.CheckSignatureFrom(ca) == nil {
			return nil
		}
	}
	if named {
		return fmt.Errorf("the revocation list issued by %q is not signed by that CA in the bundle", l.Issuer)
	}
	return nil
}

// verifyConnection is the listener's tls.Config.VerifyConnection. It
// runs after the chain is verified, and on a resumed session too, so a
// certificate revoked after its session began cannot resume it. A
// handshake with no client certificate passes: it is refused later,
// by the verifier, as unauthenticated.
func (s *crlSet) verifyConnection(cs tls.ConnectionState) error {
	for _, chain := range cs.VerifiedChains {
		if err := s.check(chain); err != nil {
			return err
		}
	}
	return nil
}

// check refuses chain when a certificate in it, below its root, is
// revoked by a list its issuer signed.
func (s *crlSet) check(chain []*x509.Certificate) error {
	for i := 0; i+1 < len(chain); i++ {
		cert, issuer := chain[i], chain[i+1]
		serial := cert.SerialNumber.String()
		for j, l := range s.lists {
			if !bytes.Equal(l.RawIssuer, cert.RawIssuer) {
				continue
			}
			if _, ok := s.revoked[j][serial]; !ok {
				continue
			}
			if s.signedBy(j, issuer) {
				return fmt.Errorf("client certificate %q (serial %s) is revoked", cert.Subject, serial)
			}
		}
	}
	return nil
}

// signedBy reports whether list j verifies against issuer, caching
// the answer: a signature check per handshake would cost more than
// the lookup it guards.
func (s *crlSet) signedBy(j int, issuer *x509.Certificate) bool {
	k := crlSigner{list: j, issuer: sha256.Sum256(issuer.Raw)}
	if v, ok := s.signed.Load(k); ok {
		return v.(bool)
	}
	ok := s.lists[j].CheckSignatureFrom(issuer) == nil
	s.signed.Store(k, ok)
	return ok
}

// stale reports the earliest next update among the lists and whether
// it has passed at now. A list with no next update is never stale.
func (s *crlSet) stale(now time.Time) (time.Time, bool) {
	var first time.Time
	for _, l := range s.lists {
		if l.NextUpdate.IsZero() {
			continue
		}
		if first.IsZero() || l.NextUpdate.Before(first) {
			first = l.NextUpdate
		}
	}
	return first, !first.IsZero() && now.After(first)
}
