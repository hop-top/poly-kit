package file

// Writable declares that Set overwrites in place and Get reads the latest
// write, so rotated credentials survive. See secret/oauth.Writability.
func (s *Store) Writable() bool { return true }
