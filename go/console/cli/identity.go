package cli

import (
	"fmt"

	"hop.top/kit/go/core/identity"
)

// IdentityConfig configures automatic identity management.
type IdentityConfig struct {
	// Dir overrides the identity store directory.
	// Default: xdg.DataHome/kit/identity/
	Dir string
	// NoAutoInit disables automatic keypair generation on first run.
	// Zero value (false) means auto-init is enabled (documented default).
	NoAutoInit bool
}

// WithIdentity enables automatic identity management on the CLI root.
// It also mounts the kit-reserved `token create`, `decode` and `claims`
// (see MountTokenCommand), serve plane or not: the self-signed tokens
// another tool's auth.mode jwt accepts once it names this tool's public
// key in auth.jwt.public_key_files.
func WithIdentity(cfg IdentityConfig) func(*Root) {
	return func(r *Root) {
		r.identityCfg = &cfg
	}
}

// IdentityConfigured reports whether WithIdentity was applied to r,
// whatever order the options came in. A nil r has no identity.
func IdentityConfigured(r *Root) bool {
	return r != nil && r.identityCfg != nil
}

// initIdentity resolves the identity keypair based on config.
func (r *Root) initIdentity() error {
	cfg := r.identityCfg
	if cfg == nil {
		return nil
	}

	var store *identity.Store
	var err error
	if cfg.Dir != "" {
		store, err = identity.NewStore(cfg.Dir)
	} else {
		store, err = identity.DefaultStore()
	}
	if err != nil {
		return fmt.Errorf("identity store: %w", err)
	}

	if !cfg.NoAutoInit {
		r.Identity, err = store.LoadOrGenerate()
	} else {
		r.Identity, err = store.Load()
	}
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	return nil
}
