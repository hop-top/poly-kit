package secret

import (
	"context"
	"time"
)

// StoredMeta is non-secret descriptive info about a stored credential.
//
// Implementations MUST NOT include the secret value itself in any field —
// the whole point is to support "auth status" introspection without
// surfacing live credentials. Adopters render StoredMeta to humans
// (table) or machines (json/yaml) via output.Render.
//
// Fields with zero values are omitted from JSON/YAML when tagged
// omitempty. Source is the only required field; everything else is
// best-effort, depending on what the underlying backend exposes.
type StoredMeta struct {
	// Key is the logical key the secret is stored under (e.g.
	// "github_token"). Always set.
	Key string `json:"key" yaml:"key" table:"Key,priority=9"`

	// ExpiresAt is the credential expiry, when known. nil = unknown
	// or non-expiring.
	ExpiresAt *time.Time `json:"expires_at,omitempty" yaml:"expires_at,omitempty" table:"Expires,priority=6"`

	// Scopes lists permission scopes attached to the credential
	// (e.g. ["repo", "read:org"]). nil = unknown.
	Scopes []string `json:"scopes,omitempty" yaml:"scopes,omitempty" table:"Scopes,priority=4"`

	// Source identifies where the credential lives in human-readable
	// form. Convention: "<backend>/<addressable-locator>". Examples:
	// "keyring/kit", "agefile//path/to/secrets.age",
	// "openbao/secret/data/foo". Always set.
	Source string `json:"source" yaml:"source" table:"Source,priority=8"`

	// AuthMethod describes how the credential was obtained or is
	// presented to a remote (e.g. "bearer", "oauth2", "ssh-key").
	AuthMethod string `json:"auth_method,omitempty" yaml:"auth_method,omitempty" table:"Method,priority=3"`

	// Backend is the secret.Config.Backend value that produced this
	// metadata (e.g. "keyring", "openbao"). Always set when produced
	// via a registered backend.
	Backend string `json:"backend,omitempty" yaml:"backend,omitempty" table:"Backend,priority=7"`

	// UpdatedAt is the last write time of the underlying secret, when
	// the backend exposes it. Zero value when unavailable.
	UpdatedAt time.Time `json:"updated_at" yaml:"updated_at" table:"Updated,priority=5"`
}

// MetadataReader is the optional companion interface stores implement
// to support the `auth status` command. Stores that genuinely cannot
// produce metadata for the underlying backend (e.g. env, memory) MUST
// return ErrNotSupported wrapped with the backend name. Stores that
// know about the key but cannot find it return ErrNotFound.
type MetadataReader interface {
	// Metadata returns non-secret descriptive info about the stored
	// credential. Implementations MUST NOT include the secret value
	// itself. Returns ErrNotFound when the key is absent and
	// ErrNotSupported when the backend does not expose any metadata.
	Metadata(ctx context.Context, key string) (StoredMeta, error)
}
