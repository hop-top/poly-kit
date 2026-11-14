package keyring

// Writable declares that Set overwrites the item and Get reads it back, so
// rotated credentials survive. See secret/oauth.Writability.
func (s *Store) Writable() bool { return true }
