package data

import "time"

// Pick selects a pseudorandom element from pool using current Unix second as seed.
// Same pool length + time window → same result within a second (screenshot-stable).
func Pick(pool []string) string {
	if len(pool) == 0 {
		return ""
	}
	idx := time.Now().Unix() % int64(len(pool))
	if idx < 0 {
		idx = -idx
	}
	return pool[idx]
}
