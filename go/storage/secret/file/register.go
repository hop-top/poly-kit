package file

import (
	"fmt"

	"hop.top/kit/go/storage/secret"
)

func init() {
	secret.RegisterBackend("file", func(cfg secret.Config) (secret.MutableStore, error) {
		if cfg.Dir == "" {
			return nil, fmt.Errorf("secret: file backend requires Dir")
		}
		// Values are stored in plaintext. Encryption at rest needs a
		// secret.Keeper, which is a live Go value (see local.NewKeeper,
		// which derives its key from an identity.Keypair) and therefore
		// cannot be expressed in secret.Config. Callers wanting an
		// encrypted store construct it directly with New(dir, keeper),
		// the same code-level escape hatch composite documents.
		return New(cfg.Dir, nil), nil
	})
}
