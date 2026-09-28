package cli

import (
	"fmt"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/transport/api"
)

// compressionBlock is the api service's response-compression block:
// services.api.compression, with shared defaults under
// services.all.compression. Its keys are registered in svcconfig.
const compressionBlock = "compression"

// compressionKey resolves one compression key:
// services.api.compression.<key>, then services.all.compression.<key>.
func (a *apiService) compressionKey(key string) (string, bool) {
	if a.root == nil {
		return "", false
	}
	_, k, ok := svcconfig.New(a.root.Viper).Lookup(APIServiceName, compressionBlock, key)
	return k, ok
}

// compressionEnabled resolves compression.enabled. It is off by
// default on loopback and beyond it alike: a loopback client gains
// nothing from encoding, and a reverse proxy in front of the service
// compresses for the clients beyond it.
func (a *apiService) compressionEnabled() bool {
	if k, ok := a.compressionKey("enabled"); ok {
		return a.root.Viper.GetBool(k)
	}
	return false
}

// compressionMinBytes resolves compression.min_bytes, defaulting to
// api.DefaultCompressMinBytes.
func (a *apiService) compressionMinBytes() int {
	if k, ok := a.compressionKey("min_bytes"); ok {
		return a.root.Viper.GetInt(k)
	}
	return api.DefaultCompressMinBytes
}

// validateCompression refuses an unknown key in either compression
// block and a negative threshold, at validation rather than as a
// silently ignored setting.
func (a *apiService) validateCompression() error {
	if a.root != nil {
		err := svcconfig.New(a.root.Viper).ValidateBlock(compressionBlock, APIServiceName, svcconfig.Shared)
		if err != nil {
			return err
		}
	}
	if n := a.compressionMinBytes(); n < 0 {
		return fmt.Errorf("compression.min_bytes: %d is negative; use 0 to compress every eligible response", n)
	}
	return nil
}

// compressionMiddleware returns the compression middleware for the
// api chain when compression is enabled, and nothing otherwise, so the
// chain can append it unconditionally.
func (a *apiService) compressionMiddleware() []api.Middleware {
	if !a.compressionEnabled() {
		return nil
	}
	return []api.Middleware{api.Compress(api.WithCompressMinBytes(a.compressionMinBytes()))}
}
