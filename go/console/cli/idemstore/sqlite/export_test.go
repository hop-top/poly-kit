package sqlite

import (
	"time"

	"hop.top/kit/go/console/cli/idemstore"
)

// SetNow installs now as the store's time source. Test-only hook so
// TTL expiry can be exercised deterministically without wall-clock
// sleeps.
func SetNow(s idemstore.Store, now func() time.Time) {
	if st, ok := s.(*store); ok {
		st.now = now
	}
}

// SetPurgeBatch bounds how many expired rows one periodic purge of a
// sqlite Store deletes. Test-only hook.
func SetPurgeBatch(s idemstore.Store, n int) {
	if st, ok := s.(*store); ok {
		st.purgeBatch = n
	}
}

// Rows counts every row a sqlite Store holds, expired ones included.
// Test-only hook.
func Rows(s idemstore.Store) (int, error) {
	st, ok := s.(*store)
	if !ok {
		return 0, nil
	}
	var n int
	err := st.db.QueryRow(`select count(*) from idempotency`).Scan(&n)
	return n, err
}
