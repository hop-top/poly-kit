package cli

import (
	"fmt"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/transport/api"
)

// compressionBlock is an HTTP listener's response-compression block:
// services.<svc>.compression, with shared defaults under
// services.all.compression. Its keys are registered in svcconfig.
const compressionBlock = "compression"

// compressionKey resolves one compression key:
// services.<svc>.compression.<key>, then services.all.compression.<key>.
func (p httpPlane) compressionKey(key string) (string, bool) {
	k := p.setting(compressionBlock, key)
	return k, k != ""
}

// compressionEnabled resolves compression.enabled. It is off by
// default on loopback and beyond it alike: a loopback client gains
// nothing from encoding, and a reverse proxy in front of the service
// compresses for the clients beyond it.
func (p httpPlane) compressionEnabled() bool {
	if k, ok := p.compressionKey("enabled"); ok {
		return p.root.Viper.GetBool(k)
	}
	return false
}

// compressionMinBytes resolves compression.min_bytes, defaulting to
// api.DefaultCompressMinBytes.
func (p httpPlane) compressionMinBytes() int {
	if k, ok := p.compressionKey("min_bytes"); ok {
		return p.root.Viper.GetInt(k)
	}
	return api.DefaultCompressMinBytes
}

// validateCompression refuses an unknown key in either compression
// block and a negative threshold, at validation rather than as a
// silently ignored setting.
func (p httpPlane) validateCompression() error {
	if v := p.viper(); v != nil {
		err := svcconfig.New(v).ValidateBlock(compressionBlock, p.l.Service, svcconfig.Shared)
		if err != nil {
			return err
		}
	}
	if n := p.compressionMinBytes(); n < 0 {
		return fmt.Errorf("compression.min_bytes: %d is negative; use 0 to compress every eligible response", n)
	}
	return nil
}

// compressionMiddleware returns the compression middleware for the
// chain when compression is enabled, and nothing otherwise, so the
// chain can append it unconditionally. [api.Compress] passes Connect,
// gRPC and gRPC-Web requests through: they negotiate compression per
// message, which the rpc service sets from the same block.
func (p httpPlane) compressionMiddleware() []api.Middleware {
	if !p.compressionEnabled() {
		return nil
	}
	return []api.Middleware{api.Compress(api.WithCompressMinBytes(p.compressionMinBytes()))}
}
