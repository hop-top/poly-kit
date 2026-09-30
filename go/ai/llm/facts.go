package llm

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"

	"hop.top/aim"
)

// Provider facts come from the aim catalog: key variables, whether the
// provider runs locally, its protocol and default base URL. Kit reads
// only the catalog already cached on disk ([aim.Cache.Load]); key
// resolution and [Resolve] never fetch it. With no cache, adapter
// declarations and the <SCHEME>_API_KEY convention answer alone.

// catalogMemo holds the cached catalog of the registry [Default] last
// returned, so repeated key lookups parse the cache file once.
var catalogMemo struct {
	sync.Mutex
	reg       *aim.Registry
	providers map[string]*aim.Provider
}

// resetCatalogMemo drops the memoised catalog; [SetDefaultRegistry]
// calls it so a new registry is read afresh.
func resetCatalogMemo() {
	catalogMemo.Lock()
	defer catalogMemo.Unlock()
	catalogMemo.reg = nil
	catalogMemo.providers = nil
}

// cachedCatalog returns the provider map cached on disk for the
// [Default] registry, or nil when there is none. It never fetches: a
// cold cache is the offline case, not an error.
func cachedCatalog(ctx context.Context) map[string]*aim.Provider {
	reg, err := Default(ctx)
	if err != nil || reg == nil {
		return nil
	}
	catalogMemo.Lock()
	defer catalogMemo.Unlock()
	if catalogMemo.reg == reg && catalogMemo.providers != nil {
		return catalogMemo.providers
	}
	providers, err := reg.Cache().Load()
	if err != nil || len(providers) == 0 {
		return nil // not memoised: a later fetch may fill the cache
	}
	catalogMemo.reg = reg
	catalogMemo.providers = providers
	return providers
}

// catalogProvider returns the cached catalog entry whose id or curated
// alias is name; a catalog id outranks an alias of the same name, as in
// [aim.Registry.Provider].
func catalogProvider(ctx context.Context, name string) (aim.Provider, bool) {
	providers := cachedCatalog(ctx)
	if providers == nil {
		return aim.Provider{}, false
	}
	if p := providers[name]; p != nil {
		return *p, true
	}
	if p := providers[aim.CanonicalProviderID(name)]; p != nil {
		return *p, true
	}
	return aim.Provider{}, false
}

// factsKey is the credential the catalog describes: its key variables
// in catalog order, optional for a local provider. ok is false when the
// facts name no key variable and the provider is not local, leaving the
// convention to answer.
func factsKey(p aim.Provider) (key ProviderKey, ok bool) {
	vars := p.KeyVars()
	if len(vars) == 0 && !p.IsLocal() {
		return ProviderKey{}, false
	}
	return ProviderKey{EnvVars: vars, Optional: p.IsLocal()}, true
}

// conventionKey is the <SCHEME>_API_KEY variable for scheme, with every
// character other than a letter or digit folded to "_"
// ("acme-cloud" -> ACME_CLOUD_API_KEY).
func conventionKey(scheme string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(scheme) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String() + "_API_KEY"
}

// placeholderRE matches a ${VAR} placeholder in a catalog base URL.
var placeholderRE = regexp.MustCompile(`\$\{([^}]*)\}`)

// catalogBaseURL returns p's default base URL with each ${VAR}
// placeholder expanded from that environment variable. A placeholder may
// name only one of p's settings ([aim.Provider.Settings]): a catalog
// entry cannot make kit copy a key, or any unlisted variable, into a
// URL. The result is never sent with a placeholder left in it.
func catalogBaseURL(scheme string, p aim.Provider) (string, error) {
	if p.API == "" {
		return "", fmt.Errorf("llm: provider %q has no default base URL in the aim catalog; pass base_url", scheme)
	}
	settings := p.Settings()
	var refused, missing []string
	expanded := placeholderRE.ReplaceAllStringFunc(p.API, func(m string) string {
		name := m[2 : len(m)-1]
		if !slices.Contains(settings, name) {
			refused = append(refused, name)
			return m
		}
		v := os.Getenv(name)
		if v == "" {
			missing = append(missing, name)
		}
		return v
	})
	switch {
	case len(refused) > 0:
		return "", fmt.Errorf("llm: provider %q base URL %q names %s, which is not one of its settings; pass base_url",
			scheme, p.API, strings.Join(refused, ", "))
	case len(missing) > 0:
		return "", fmt.Errorf("llm: provider %q base URL %q needs %s set; set it or pass base_url",
			scheme, p.API, strings.Join(missing, ", "))
	}
	u, err := url.Parse(expanded)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.Contains(expanded, "${") {
		return "", fmt.Errorf("llm: provider %q base URL %q does not expand to an http(s) URL; pass base_url", scheme, p.API)
	}
	return expanded, nil
}
