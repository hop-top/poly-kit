// Package sqlwrap wraps database/sql.DB so queries record a
// Provenance entry against the JSON-pointer path of the output field
// they populate. The wrapper sanitizes the DSN (password stripped) so
// the recorded URL is safe to surface in structured output.
package sqlwrap

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"time"

	"hop.top/kit/go/runtime/provenance"
)

// DB wraps *sql.DB. Every QueryContext / QueryRowContext records a
// Provenance against path with URL=sanitisedDSN, FetchedAt=now.
type DB struct {
	inner *sql.DB
	dsn   string // already sanitized
}

// Wrap returns a DB whose recorded URL is the sanitized form of dsn
// (any user:password credential stripped to user@). Adopters pass the
// original DSN once at startup; subsequent recordings reuse the
// sanitized copy.
func Wrap(db *sql.DB, dsn string) *DB {
	return &DB{inner: db, dsn: sanitiseDSN(dsn)}
}

// Inner returns the wrapped *sql.DB for callers that need to access
// methods not exposed here (Stats, Conn, etc.).
func (d *DB) Inner() *sql.DB { return d.inner }

// QueryRowContext runs query and records a Provenance entry against
// path. Returns the row, the Provenance the caller pairs with a
// wrapper, and any deferred error via *sql.Row.Err().
func (d *DB) QueryRowContext(ctx context.Context, path, query string, args ...any) (*sql.Row, provenance.Provenance) {
	row := d.inner.QueryRowContext(ctx, query, args...)
	prov := d.stamp(ctx, path)
	return row, prov
}

// QueryContext runs query and records a Provenance entry against path.
func (d *DB) QueryContext(ctx context.Context, path, query string, args ...any) (*sql.Rows, provenance.Provenance, error) {
	rows, err := d.inner.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, provenance.Provenance{}, err
	}
	return rows, d.stamp(ctx, path), nil
}

func (d *DB) stamp(ctx context.Context, path string) provenance.Provenance {
	if provenance.CurrentModeFromContext(ctx) == provenance.ModeOff {
		return provenance.Provenance{}
	}
	prov := provenance.Provenance{
		SchemaVersion: provenance.SchemaVersion,
		Source:        provenance.SourceAuthoritative,
		URL:           d.dsn,
		FetchedAt:     time.Now().UTC(),
	}
	tr := provenance.Track(ctx)
	if path != "" {
		_ = tr.Authoritative(path, prov)
	}
	return prov
}

// sanitiseDSN strips password components from common DSN formats so the
// recorded URL can be surfaced without leaking secrets.
//
//   - URL-shaped DSNs ("postgres://user:pass@host/db?sslmode=disable")
//     keep user, strip pass, keep host + path + query.
//   - Key-value DSNs ("user=u password=p host=h") strip password=...
//     and any blank-quoted password value.
//   - Anything else returns the input verbatim (caller is responsible).
func sanitiseDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" {
		if u.User != nil {
			u.User = url.User(u.User.Username())
		}
		return u.String()
	}
	if strings.Contains(dsn, "password=") {
		parts := strings.Fields(dsn)
		out := parts[:0]
		for _, p := range parts {
			if strings.HasPrefix(strings.ToLower(p), "password=") {
				continue
			}
			out = append(out, p)
		}
		return strings.Join(out, " ")
	}
	return dsn
}
