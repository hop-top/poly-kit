package memory

import "hop.top/kit/go/storage/secret"

func init() {
	secret.RegisterBackend("memory", func(_ secret.Config) (secret.MutableStore, error) {
		return New(), nil
	})
}
