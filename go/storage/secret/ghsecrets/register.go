package ghsecrets

import "hop.top/kit/go/storage/secret"

func init() {
	secret.RegisterBackend("ghsecrets", func(cfg secret.Config) (secret.MutableStore, error) {
		return New(cfg.Repo), nil
	})
}
