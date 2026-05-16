// Package uxp defines user experience primitives for tool-aware agents.
package uxp

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// ProjectKeyStrategy determines how a project key is derived from a cwd.
type ProjectKeyStrategy string

const (
	// SlashToDash replaces path separators with dashes.
	SlashToDash ProjectKeyStrategy = "slash-to-dash"
	// SHA1 hashes the cwd with SHA-1.
	SHA1 ProjectKeyStrategy = "sha1"
	// SHA256 hashes the cwd with SHA-256.
	SHA256 ProjectKeyStrategy = "sha256"
	// MD5 hashes the cwd with MD5.
	MD5 ProjectKeyStrategy = "md5"
	// BasenameAlias uses the last path component.
	BasenameAlias ProjectKeyStrategy = "basename-alias"
	// Embedded returns the cwd unchanged.
	Embedded ProjectKeyStrategy = "embedded"
	// None returns the cwd unchanged.
	None ProjectKeyStrategy = "none"
)

// String returns the strategy name.
func (s ProjectKeyStrategy) String() string { return string(s) }

// DeriveKey produces a project key from cwd using the given strategy.
func DeriveKey(cwd string, strategy ProjectKeyStrategy) string {
	switch strategy {
	case SlashToDash:
		if cwd == "" {
			return ""
		}
		normalized := filepath.ToSlash(filepath.Clean(cwd))
		replaced := strings.ReplaceAll(normalized, "/", "-")
		return strings.TrimPrefix(replaced, "-")
	case SHA1:
		h := sha1.Sum([]byte(cwd))
		return hex.EncodeToString(h[:])
	case SHA256:
		h := sha256.Sum256([]byte(cwd))
		return hex.EncodeToString(h[:])
	case MD5:
		h := md5.Sum([]byte(cwd))
		return hex.EncodeToString(h[:])
	case BasenameAlias:
		return filepath.Base(cwd)
	case Embedded, None:
		return cwd
	default:
		return cwd
	}
}
