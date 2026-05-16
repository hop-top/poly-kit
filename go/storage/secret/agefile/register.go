package agefile

import (
	"fmt"

	"hop.top/kit/go/storage/secret"
)

func init() {
	secret.RegisterBackend("agefile", func(cfg secret.Config) (secret.MutableStore, error) {
		if cfg.Path == "" {
			return nil, fmt.Errorf("secret: agefile backend requires Path")
		}
		if cfg.IdentityFile == "" {
			return nil, fmt.Errorf("secret: agefile backend requires IdentityFile")
		}
		return New(cfg.Path, cfg.IdentityFile), nil
	})
}
