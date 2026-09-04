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
