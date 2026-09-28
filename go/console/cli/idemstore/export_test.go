package idemstore

import "time"

// SetNow installs now as the store's time source. Test-only hook so
// TTL expiry can be exercised deterministically without wall-clock
// sleeps.
func SetNow(s Store, now func() time.Time) {
	switch st := s.(type) {
	case *sqliteStore:
		st.now = now
	case *memoryStore:
		st.now = now
	}
}

// SetPurgeBatch bounds how many expired rows one periodic purge of a
// sqlite Store deletes. Test-only hook.
func SetPurgeBatch(s Store, n int) {
	if st, ok := s.(*sqliteStore); ok {
		st.purgeBatch = n
	}
}

// Rows counts every row a sqlite Store holds, expired ones included.
// Test-only hook.
func Rows(s Store) (int, error) {
	st, ok := s.(*sqliteStore)
	if !ok {
		return 0, nil
	}
	var n int
	err := st.db.QueryRow(`select count(*) from idempotency`).Scan(&n)
	return n, err
}
