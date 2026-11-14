package openbao

// Writable declares that Set writes a new KV v2 version and Get reads the
// latest, so rotated credentials survive. See secret/oauth.Writability.
func (s *Store) Writable() bool { return true }
