package repohost

import (
	"context"
	"fmt"
	"net/http"
	"sync"
)

// Config selects and parameterizes a driver.
type Config struct {
	// Provider is the registered driver name, e.g. "github",
	// "gitlab", "gitea", "bitbucket", or any *-mock variant.
	Provider string
	// BaseURL is the host root for self-hosted instances. Empty
	// means use the driver's SaaS default (where one exists).
	BaseURL string
	// Token is the PAT / OAuth bearer. When empty, drivers fall
	// back to the provider-specific environment variable; if that
	// is also empty, drivers proceed unauthenticated (rate-limited).
	Token string
	// HTTPClient is the transport. When nil, drivers use
	// http.DefaultClient.
	HTTPClient *http.Client
}

// Opener is a factory registered by each driver's init().
type Opener func(cfg Config) (MutableHost, error)

var openers sync.Map // map[string]Opener

// RegisterDriver registers a driver factory under name. The factory
// is invoked by [Open] when Config.Provider matches name. Drivers
// register from their own init() functions.
func RegisterDriver(name string, fn Opener) {
	openers.Store(name, fn)
}

// Open returns a MutableHost for cfg.Provider. Drivers register
// themselves via blank import:
//
//	import _ "hop.top/kit/go/integrations/repohost/github"
//
// Returns an error when no driver is registered for cfg.Provider.
func Open(_ context.Context, cfg Config) (MutableHost, error) {
	v, ok := openers.Load(cfg.Provider)
	if !ok {
		return nil, fmt.Errorf("repohost: unknown provider %q", cfg.Provider)
	}
	fn, ok := v.(Opener)
	if !ok {
		return nil, fmt.Errorf("repohost: invalid driver registration for %q", cfg.Provider)
	}
	return fn(cfg)
}
