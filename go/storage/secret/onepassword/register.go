package onepassword

import (
	"fmt"

	"hop.top/kit/go/storage/secret"
)

func init() {
	secret.RegisterBackend("onepassword", func(cfg secret.Config) (secret.MutableStore, error) {
		if cfg.Vault == "" {
			return nil, fmt.Errorf("secret: onepassword backend requires Vault")
		}
		if cfg.ConnectURL != "" {
			return NewConnect(cfg.ConnectURL, cfg.Token, cfg.Vault), nil
		}
		return NewCLI(cfg.Vault), nil
	})
}
