package cli

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/log/v2"
	"github.com/fsnotify/fsnotify"
)

// tlsReloadSettle is how long a listener waits after the last change
// in a watched directory before it reloads: long enough for a tool
// that writes the certificate and then the key to finish both.
const tlsReloadSettle = 250 * time.Millisecond

// tlsFiles is the part of a listener's TLS setting read from files —
// the certificate and key, the client CA bundle and its revocation
// lists — and the configuration last built from them, which every new
// handshake is served with.
//
// The files are read at resolution and again whenever their
// directories change, in two sets: the certificate and key, and the
// bundle with its lists. A set that does not load — a half-written
// file, a key that does not match its certificate, a bundle with no
// certificate — is rejected and the one in force keeps serving, so the
// listener never serves a pair it has not verified; the other set
// still reloads, so a broken certificate write does not hold back a
// revocation.
type tlsFiles struct {
	svc string
	// template is the configuration every snapshot starts from: the
	// minimum version, the ALPN protocols, the ACME manager's
	// GetCertificate when certificates come from ACME, and the
	// client-certificate policy under auth.mode: mtls.
	template *tls.Config

	certFile, keyFile, certKey, keyKey string
	caFile, caKey                      string
	crlFile, crlKey                    string

	current atomic.Pointer[tlsSnapshot]
}

// tlsSnapshot is the configuration built from the sets in force.
type tlsSnapshot struct {
	config *tls.Config
	pair   *tlsPairSet     // nil without cert_file
	cas    *tlsClientCASet // nil without ca_file
}

// tlsPairSet is a loaded certificate and key.
type tlsPairSet struct {
	cert tls.Certificate
	sum  [sha256.Size]byte
}

// tlsClientCASet is a loaded CA bundle and its revocation lists.
type tlsClientCASet struct {
	pool *x509.CertPool
	crl  *crlSet // nil without crl_file
	sum  [sha256.Size]byte
}

// configForClient is the listener's GetConfigForClient: every
// handshake is served with the latest snapshot.
func (f *tlsFiles) configForClient(*tls.ClientHelloInfo) (*tls.Config, error) {
	return f.current.Load().config, nil
}

// load reads every configured file and builds a snapshot from them,
// or returns the first error. Each error names the key whose file is
// at fault.
func (f *tlsFiles) load() (*tlsSnapshot, error) {
	pair, err := f.loadPair()
	if err != nil {
		return nil, err
	}
	cas, err := f.loadClientCAs()
	if err != nil {
		return nil, err
	}
	return f.build(pair, cas), nil
}

// loadPair reads cert_file and key_file; nil when not configured.
func (f *tlsFiles) loadPair() (*tlsPairSet, error) {
	if f.certFile == "" {
		return nil, nil
	}
	certPEM, err := os.ReadFile(f.certFile)
	if err != nil {
		return nil, fmt.Errorf("%s, %s: %w", f.certKey, f.keyKey, err)
	}
	keyPEM, err := os.ReadFile(f.keyFile)
	if err != nil {
		return nil, fmt.Errorf("%s, %s: %w", f.certKey, f.keyKey, err)
	}
	if err := pemComplete(certPEM); err != nil {
		return nil, fmt.Errorf("%s: %w", f.certKey, err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("%s, %s: %w", f.certKey, f.keyKey, err)
	}
	return &tlsPairSet{cert: cert, sum: fileSum(certPEM, keyPEM)}, nil
}

// loadClientCAs reads ca_file and crl_file; nil when not configured.
func (f *tlsFiles) loadClientCAs() (*tlsClientCASet, error) {
	if f.caFile == "" {
		return nil, nil
	}
	caPEM, err := os.ReadFile(f.caFile)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.caKey, err)
	}
	if err := pemComplete(caPEM); err != nil {
		return nil, fmt.Errorf("%s: %w", f.caKey, err)
	}
	bundle := pemCertificates(caPEM)
	if len(bundle) == 0 {
		return nil, fmt.Errorf("%s: %s holds no PEM certificate", f.caKey, f.caFile)
	}
	set := &tlsClientCASet{pool: x509.NewCertPool()}
	for _, c := range bundle {
		set.pool.AddCert(c)
	}
	var raw []byte
	if f.crlFile != "" {
		if raw, err = os.ReadFile(f.crlFile); err != nil {
			return nil, fmt.Errorf("%s: %w", f.crlKey, err)
		}
		if set.crl, err = parseCRLs(raw, bundle); err != nil {
			return nil, fmt.Errorf("%s: %w", f.crlKey, err)
		}
	}
	set.sum = fileSum(caPEM, raw)
	return set, nil
}

// build is the configuration a pair and a CA set are served with.
func (f *tlsFiles) build(pair *tlsPairSet, cas *tlsClientCASet) *tlsSnapshot {
	cfg := f.template.Clone()
	if pair != nil {
		cfg.Certificates = []tls.Certificate{pair.cert}
	}
	if cas != nil {
		cfg.ClientCAs = cas.pool
		if cas.crl != nil {
			cfg.VerifyConnection = cas.crl.verifyConnection
		}
	}
	return &tlsSnapshot{config: cfg, pair: pair, cas: cas}
}

// reload loads each set again. A set that loads replaces the one in
// force; one that does not is kept, and its error returned. It reports
// whether the configuration changed: a set rewritten with the same
// contents changes nothing.
func (f *tlsFiles) reload() (bool, error) {
	cur := f.current.Load()
	pair, perr := f.loadPair()
	if perr != nil {
		pair = cur.pair
	}
	cas, cerr := f.loadClientCAs()
	if cerr != nil {
		cas = cur.cas
	}
	err := errors.Join(perr, cerr)
	if pair.same(cur.pair) && cas.same(cur.cas) {
		return false, err
	}
	f.current.Store(f.build(pair, cas))
	return true, err
}

func (p *tlsPairSet) same(o *tlsPairSet) bool {
	return p == o || (p != nil && o != nil && p.sum == o.sum)
}

func (c *tlsClientCASet) same(o *tlsClientCASet) bool {
	return c == o || (c != nil && o != nil && c.sum == o.sum)
}

// dirs are the directories holding the configured files: watching a
// directory, rather than a file, sees a file replaced by rename and a
// symlink re-pointed (a Kubernetes secret volume, certbot's live/
// directory) as well as one rewritten in place.
func (f *tlsFiles) dirs() []string {
	var out []string
	for _, p := range []string{f.certFile, f.keyFile, f.caFile, f.crlFile} {
		if p == "" {
			continue
		}
		d := filepath.Dir(p)
		if !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

// watch reloads the files whenever their directories change, once
// they have been quiet for [tlsReloadSettle], until the returned stop
// is called. A reload that swaps is logged at info, a rejected one at
// warn with the reason; the listener keeps serving either way. A
// watch that cannot start is logged, and the listener serves the
// files it has until it restarts.
func (f *tlsFiles) watch(logger *log.Logger) (stop func()) {
	dirs := f.dirs()
	if len(dirs) == 0 {
		return func() {}
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Warn("tls: certificate files are not watched; a replaced file takes effect on restart",
			"service", f.svc, "error", err)
		return func() {}
	}
	for _, d := range dirs {
		if err := w.Add(d); err != nil {
			logger.Warn("tls: certificate directory is not watched; a file replaced in it takes effect on restart",
				"service", f.svc, "dir", d, "error", err)
		}
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		settle := time.NewTimer(tlsReloadSettle)
		settle.Stop()
		defer settle.Stop()
		for {
			select {
			case <-done:
				return
			case _, ok := <-w.Events:
				if !ok {
					return
				}
				settle.Reset(tlsReloadSettle)
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				logger.Warn("tls: watching certificate files", "service", f.svc, "error", err)
			case <-settle.C:
				f.reloadAndLog(logger)
			}
		}
	})
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			_ = w.Close()
			wg.Wait()
		})
	}
}

// reloadAndLog reloads and reports the outcome: each set rejected,
// and whether the configuration changed.
func (f *tlsFiles) reloadAndLog(logger *log.Logger) {
	swapped, err := f.reload()
	if err != nil {
		logger.Warn("tls: reload rejected; still serving the previous files",
			"service", f.svc, "error", err)
	}
	if swapped {
		logger.Info("tls: reloaded certificate files", "service", f.svc)
		f.warnStale(logger)
	}
}

// warnStale warns of a revocation list past its next update: it is
// still enforced, but revocations issued since are not in it.
func (f *tlsFiles) warnStale(logger *log.Logger) {
	snap := f.current.Load()
	if snap == nil || snap.cas == nil || snap.cas.crl == nil {
		return
	}
	if next, stale := snap.cas.crl.stale(time.Now()); stale {
		logger.Warn("tls: revocation list is past its next update; later revocations are not enforced",
			"service", f.svc, "file", f.crlFile, "next_update", next.Format(time.RFC3339))
	}
}

// fileSum identifies the contents of a set's files.
func fileSum(parts ...[]byte) [sha256.Size]byte {
	h := sha256.New()
	for _, p := range parts {
		_, _ = fmt.Fprintf(h, "%d:", len(p))
		_, _ = h.Write(p)
	}
	var out [sha256.Size]byte
	h.Sum(out[:0])
	return out
}

// pemComplete refuses PEM data that ends in a block cut short — a file
// read while it was being written — which pem.Decode would otherwise
// drop silently, serving a chain without its last certificate.
func pemComplete(data []byte) error {
	rest := data
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
	}
	if bytes.Contains(rest, []byte("-----BEGIN")) {
		return errors.New("a PEM block is incomplete (was the file read while being written?)")
	}
	return nil
}

// pemCertificates parses every CERTIFICATE block in data, skipping
// any that does not parse, as x509.CertPool.AppendCertsFromPEM does.
func pemCertificates(data []byte) []*x509.Certificate {
	var out []*x509.Certificate
	for {
		var b *pem.Block
		b, data = pem.Decode(data)
		if b == nil {
			return out
		}
		if b.Type != "CERTIFICATE" || len(b.Headers) != 0 {
			continue
		}
		if c, err := x509.ParseCertificate(b.Bytes); err == nil {
			out = append(out, c)
		}
	}
}
