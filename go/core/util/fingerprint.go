package util

import (
	"crypto/sha256"
	"encoding/hex"
)

// Short returns the first n hex chars of SHA-256(data).
// Default n=8 if 0.
func Short(data []byte, n int) string {
	if n <= 0 {
		n = 8
	}
	h := sha256.Sum256(data)
	s := hex.EncodeToString(h[:])
	if n > len(s) {
		n = len(s)
	}
	return s[:n]
}

// ShortString is Short for string input.
func ShortString(s string, n int) string {
	return Short([]byte(s), n)
}
