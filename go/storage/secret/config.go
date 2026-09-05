package secret

import "fmt"

// Backends lists every backend name the package documents as
// available. Each is registered by the init of its own subpackage, so
// a name only resolves through Open once that subpackage is imported
// (blank imports are the convention).
//
// This is the single source of truth the README and Config.Backend
// document; tests assert every entry resolves through Open, so a
// backend cannot be documented without being registered.
var Backends = []string{
	"env",
	"file",
	"agefile",
	"keyring",
	"onepassword",
	"ghsecrets",
	"openbao",
	"infisical",
	"memory",
}

// Config describes which secret backend to use.
type Config struct {
	// Backend names the store to open. See Backends for the full set:
	// "env", "file", "agefile", "keyring", "onepassword", "ghsecrets",
	// "openbao", "infisical", "memory".
	Backend      string
	Prefix       string // for env adapter
	Dir          string // for file adapter
	Service      string // for keyring adapter
	Addr         string // for openbao/infisical
	Token        string // for openbao/infisical
	Mount        string // for openbao
	Project      string // for infisical
	Env          string // for infisical
	Path         string // for agefile (path to encrypted YAML)
	IdentityFile string // for agefile (age identity file)
	Repo         string // for ghsecrets ("owner/repo"; empty = current repo)
	Vault        string // for onepassword
	ConnectURL   string // for onepassword Connect mode
}

// Opener is a function that creates a MutableStore from config.
// Registered via RegisterBackend.
type Opener func(cfg Config) (MutableStore, error)

var backends = map[string]Opener{}

// RegisterBackend registers a factory for the named backend.
func RegisterBackend(name string, fn Opener) {
	backends[name] = fn
}

// Open creates a MutableStore from config using registered backends.
func Open(cfg Config) (MutableStore, error) {
	fn, ok := backends[cfg.Backend]
	if !ok {
		return nil, fmt.Errorf("secret: unknown backend %q", cfg.Backend)
	}
	return fn(cfg)
}
