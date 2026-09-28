// Package testpki issues throwaway certificates for tests: an
// in-memory CA, and server and client leaves it signs, written to a
// test's temporary directory when a test needs files.
package testpki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// CA is a self-signed certificate authority.
type CA struct {
	Cert *x509.Certificate
	key  *ecdsa.PrivateKey
	// PEM is the CA certificate, PEM-encoded.
	PEM []byte
}

// Leaf describes a certificate to issue.
type Leaf struct {
	CN     string
	DNS    []string
	IPs    []net.IP
	URIs   []string
	Emails []string
	// Names are extra subject attributes (an OU, a custom OID).
	Names []pkix.AttributeTypeAndValue
	// Extensions are extra certificate extensions.
	Extensions []pkix.Extension
	// Client issues a client-auth certificate; otherwise server-auth.
	Client bool
}

// Issued is a certificate and its key.
type Issued struct {
	CertPEM, KeyPEM []byte
	TLS             tls.Certificate
}

// NewCA returns a fresh CA named cn.
func NewCA(t testing.TB, cn string) *CA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial(t),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &CA{Cert: cert, key: key, PEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// Pool is a pool holding only the CA.
func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.Cert)
	return p
}

// Issue signs a leaf.
func (ca *CA) Issue(t testing.TB, l Leaf) Issued {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	usage := x509.ExtKeyUsageServerAuth
	if l.Client {
		usage = x509.ExtKeyUsageClientAuth
	}
	tmpl := &x509.Certificate{
		SerialNumber:    serial(t),
		Subject:         pkix.Name{CommonName: l.CN, ExtraNames: l.Names},
		NotBefore:       time.Now().Add(-time.Hour),
		NotAfter:        time.Now().Add(time.Hour),
		KeyUsage:        x509.KeyUsageDigitalSignature,
		ExtKeyUsage:     []x509.ExtKeyUsage{usage},
		DNSNames:        l.DNS,
		IPAddresses:     l.IPs,
		EmailAddresses:  l.Emails,
		ExtraExtensions: l.Extensions,
	}
	for _, s := range l.URIs {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		tmpl.URIs = append(tmpl.URIs, u)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	out := Issued{
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	}
	if out.TLS, err = tls.X509KeyPair(out.CertPEM, out.KeyPEM); err != nil {
		t.Fatal(err)
	}
	return out
}

// Localhost issues a server certificate for 127.0.0.1, ::1 and
// localhost.
func (ca *CA) Localhost(t testing.TB) Issued {
	t.Helper()
	return ca.Issue(t, Leaf{
		CN:  "localhost",
		DNS: []string{"localhost"},
		IPs: []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	})
}

// Write writes the certificate and key into dir as <name>.crt and
// <name>.key and returns their paths.
func (i Issued) Write(t testing.TB, dir, name string) (certFile, keyFile string) {
	t.Helper()
	certFile = filepath.Join(dir, name+".crt")
	keyFile = filepath.Join(dir, name+".key")
	write(t, certFile, i.CertPEM)
	write(t, keyFile, i.KeyPEM)
	return certFile, keyFile
}

// WriteCA writes the CA certificate into dir as <name>.crt and returns
// its path.
func (ca *CA) WriteCA(t testing.TB, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name+".crt")
	write(t, p, ca.PEM)
	return p
}

// Cert is the issued certificate, parsed.
func (i Issued) Cert() *x509.Certificate { return i.TLS.Leaf }

// Replace writes data to path the way a deployment tool does: into a
// temporary file beside it, renamed over it, so no reader sees a
// partial write.
func Replace(t testing.TB, path string, data []byte) {
	t.Helper()
	tmp := path + ".tmp"
	write(t, tmp, data)
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

// ClientTLS is a client configuration trusting ca, presenting certs.
func (ca *CA) ClientTLS(certs ...tls.Certificate) *tls.Config {
	return &tls.Config{RootCAs: ca.Pool(), Certificates: certs, MinVersion: tls.VersionTLS12}
}

func write(t testing.TB, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func serial(t testing.TB) *big.Int {
	t.Helper()
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		t.Fatal(err)
	}
	return n
}
