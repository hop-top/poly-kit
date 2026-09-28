package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/core/xdg"
	"hop.top/kit/go/storage/kv"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/authn"
)

// AuthModeAPIKey is the services.<svc>.auth.mode value that makes a
// kit-issued API key the credential: X-API-Key, or Authorization:
// Bearer, checked against the store auth.apikey names.
const AuthModeAPIKey = "apikey"

const authAPIKeyBlock = "auth.apikey"

// DefaultAPIKeyBackend is the kv backend API keys live in unless
// auth.apikey.backend or APIKeysConfig names another. Its driver must
// be imported: hop.top/kit/go/storage/kv/sqlite.
const DefaultAPIKeyBackend = "sqlite"

// apiKeyBackends are the kv backends an API key store may use from
// configuration: file backends, whose Path is the only setting. sqlite
// is shared by every process that opens the file; badger holds an
// exclusive lock, so `token key` must run while the service is
// stopped.
var apiKeyBackends = []string{"sqlite", "badger"}

// APIKeysConfig is the code default for the API key store, which
// services.<svc>.auth.apikey overrides key by key.
type APIKeysConfig struct {
	// Backend is the kv backend; empty means [DefaultAPIKeyBackend].
	// Its driver must be imported.
	Backend string
	// Path is the store's file (sqlite) or directory (badger); empty
	// means <data dir>/<tool>/apikeys.db.
	Path string
}

// WithAPIKeys gives the tool kit-issued API keys: it mounts
// `token key create|list|revoke` on the token command, and sets the
// store's code default. A service serves them under auth.mode: apikey.
// The store's kv driver must be imported by the tool, sqlite by default:
//
//	import _ "hop.top/kit/go/storage/kv/sqlite"
func WithAPIKeys(cfg APIKeysConfig) func(*Root) {
	return func(r *Root) { r.apiKeysCfg = &cfg }
}

// apiKeyStoreConfig resolves the kv store svc's API keys live in:
// services.<svc>.auth.apikey, then services.all, then the code
// default, then the kit default. It opens nothing.
func (t tlsResolver) apiKeyStoreConfig(r *Root) (kv.Config, error) {
	cfg := kv.Config{Backend: DefaultAPIKeyBackend}
	if r != nil && r.apiKeysCfg != nil {
		if r.apiKeysCfg.Backend != "" {
			cfg.Backend = r.apiKeysCfg.Backend
		}
		cfg.Path = r.apiKeysCfg.Path
	}
	backendKey := svcconfig.Key(t.svc, authAPIKeyBlock, "backend")
	if b, k := t.str(authAPIKeyBlock, "backend"); b != "" {
		cfg.Backend, backendKey = b, k
	}
	if p, _ := t.str(authAPIKeyBlock, "path"); p != "" {
		cfg.Path = p
	}
	if !slices.Contains(apiKeyBackends, cfg.Backend) {
		return cfg, fmt.Errorf("%s: %q cannot hold API keys; use %s", backendKey, cfg.Backend,
			strings.Join(apiKeyBackends, " or "))
	}
	if !slices.Contains(kv.Backends(), cfg.Backend) {
		return cfg, fmt.Errorf("%s: kv backend %q is not registered; import hop.top/kit/go/storage/kv/%s",
			backendKey, cfg.Backend, cfg.Backend)
	}
	if cfg.Path == "" {
		tool := "kit"
		if r != nil && r.Config.Name != "" {
			tool = r.Config.Name
		}
		dir, err := xdg.DataDir(tool)
		if err != nil {
			return cfg, fmt.Errorf("%s: %w", svcconfig.Key(t.svc, authAPIKeyBlock, "path"), err)
		}
		cfg.Path = filepath.Join(dir, "apikeys.db")
	}
	return cfg, nil
}

// openAPIKeyStore opens the store cfg names, creating the directory
// it lives in, owner-only.
func openAPIKeyStore(ctx context.Context, cfg kv.Config) (kv.Store, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o700); err != nil {
		return nil, err
	}
	return kv.OpenContext(ctx, cfg)
}

// apiKeyVerifier verifies API keys against a store it opens on the
// first request and keeps open until Close. A request after Close —
// one still draining as the service stops — opens the store for
// itself, so it is judged, not refused.
type apiKeyVerifier struct {
	cfg    kv.Config
	mu     sync.RWMutex
	store  kv.Store
	closed bool
}

func (v *apiKeyVerifier) AuthFunc() api.AuthFunc {
	return func(r *http.Request) (any, error) {
		keys, done, err := v.acquire(r.Context())
		if err != nil {
			return nil, fmt.Errorf("%w: %v", authn.ErrKeySetUnavailable, err)
		}
		defer done()
		return keys.AuthFunc()(r)
	}
}

// acquire returns API keys over the shared store, held open until done
// is called, or over a store of the call's own once v is closed.
func (v *apiKeyVerifier) acquire(ctx context.Context) (*authn.APIKeys, func(), error) {
	for {
		v.mu.RLock()
		if v.store != nil {
			return authn.NewAPIKeys(v.store, nil), v.mu.RUnlock, nil
		}
		v.mu.RUnlock()

		v.mu.Lock()
		if v.closed {
			v.mu.Unlock()
			s, err := openAPIKeyStore(context.WithoutCancel(ctx), v.cfg)
			if err != nil {
				return nil, nil, err
			}
			return authn.NewAPIKeys(s, nil), func() { _ = s.Close() }, nil
		}
		if v.store == nil {
			s, err := openAPIKeyStore(context.WithoutCancel(ctx), v.cfg)
			if err != nil {
				v.mu.Unlock()
				return nil, nil, err
			}
			v.store = s
		}
		v.mu.Unlock()
	}
}

// Close closes the shared store once no request holds it.
func (v *apiKeyVerifier) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.closed = true
	if v.store == nil {
		return nil
	}
	err := v.store.Close()
	v.store = nil
	return err
}
