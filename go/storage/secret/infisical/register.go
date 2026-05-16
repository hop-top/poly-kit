package infisical

import (
	"fmt"

	"hop.top/kit/go/storage/secret"
)

func init() {
	secret.RegisterBackend("infisical", func(cfg secret.Config) (secret.MutableStore, error) {
		if cfg.Addr == "" {
			return nil, fmt.Errorf("secret: infisical backend requires Addr")
		}
		if cfg.Token == "" {
			return nil, fmt.Errorf("secret: infisical backend requires Token")
		}
		if cfg.Project == "" {
			return nil, fmt.Errorf("secret: infisical backend requires Project")
		}
		if cfg.Env == "" {
			return nil, fmt.Errorf("secret: infisical backend requires Env")
		}
		return New(cfg.Addr, cfg.Token, cfg.Project, cfg.Env), nil
	})
}
