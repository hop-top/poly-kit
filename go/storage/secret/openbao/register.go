package openbao

import (
	"fmt"

	"hop.top/kit/go/storage/secret"
)

func init() {
	secret.RegisterBackend("openbao", func(cfg secret.Config) (secret.MutableStore, error) {
		if cfg.Addr == "" {
			return nil, fmt.Errorf("secret: openbao backend requires Addr")
		}
		// New builds an HTTP client and validates the address; it makes
		// no network round-trip. An unreachable server therefore opens
		// cleanly and fails on the first Get/Set, matching the other
		// network-backed backends (infisical, onepassword, ghsecrets).
		// Mount defaults to "secret" inside New when empty.
		return New(cfg.Addr, cfg.Token, cfg.Mount)
	})
}
