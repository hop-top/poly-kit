package etcd

import (
	"context"
	"fmt"

	"hop.top/kit/go/storage/kv"
)

func init() {
	kv.RegisterBackendContext("etcd", func(ctx context.Context, cfg kv.Config) (kv.Store, error) {
		if len(cfg.Endpoints) == 0 {
			return nil, fmt.Errorf("kv: etcd backend requires Endpoints")
		}
		return NewContext(ctx, cfg.Endpoints, cfg.Prefix, configOptions(cfg)...)
	})
}

// configOptions maps the credential fields of cfg to options. A lone
// Username or Password still becomes an option, so NewContext rejects it
// rather than the client ignoring it.
func configOptions(cfg kv.Config) []Option {
	var opts []Option
	if cfg.Username != "" || cfg.Password != "" {
		opts = append(opts, WithAuth(cfg.Username, cfg.Password))
	}
	if cfg.TLS != nil {
		opts = append(opts, WithTLS(cfg.TLS))
	}
	return opts
}
