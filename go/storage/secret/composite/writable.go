package composite

// Writable reports whether every non-read-only member declares write-back
// support (an optional Writable() bool method). Set routes per key to the
// first non-RO member whose Owns predicate matches, and Writable has no key,
// so only "all candidate writers are writable" is safe to claim. A composite
// with no non-RO member is not writable.
func (s *Store) Writable() bool {
	n := 0
	for _, m := range s.members {
		if m.RO {
			continue
		}
		w, ok := m.Store.(interface{ Writable() bool })
		if !ok || !w.Writable() {
			return false
		}
		n++
	}
	return n > 0
}
