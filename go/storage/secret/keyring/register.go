package keyring

import "hop.top/kit/go/storage/secret"

func init() {
	secret.RegisterBackend("keyring", func(cfg secret.Config) (secret.MutableStore, error) {
		svc := cfg.Service
		if svc == "" {
			svc = "kit"
		}
		return New(svc), nil
	})
}
