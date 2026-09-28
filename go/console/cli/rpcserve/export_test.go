package rpcserve

import (
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/transport/rpc"
)

// WithServerOptions is With plus rpc server options, so a test can
// shorten the write timeout a stream must outlive.
func WithServerOptions(cfg Config, opts ...rpc.ServerOption) func(*cli.Root) {
	return with(cfg, opts)
}
