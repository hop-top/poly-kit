package llm

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"hop.top/kit/go/storage/secret"
)

// KeySourceKind is the kind of place a provider key came from.
type KeySourceKind string

const (
	// KeySourceNone: no key was found, or the scheme takes none.
	KeySourceNone KeySourceKind = ""
	// KeySourceURI: the URI's own api_key param.
	KeySourceURI KeySourceKind = "uri"
	// KeySourceConfig: api_key in an llm.yaml provider block.
	KeySourceConfig KeySourceKind = "config"
	// KeySourceStore: the secret store, under a provider key name.
	KeySourceStore KeySourceKind = "store"
	// KeySourceEnv: a provider key variable in the environment.
	KeySourceEnv KeySourceKind = "env"
	// KeySourceFallback: the universal [FallbackEnvKey], LLM_API_KEY.
	KeySourceFallback KeySourceKind = "fallback"
)

// KeySource says where a resolved provider key came from. It holds
// names, never the key.
type KeySource struct {
	Kind KeySourceKind
	// Name is the store key (KeySourceStore) or variable
	// (KeySourceEnv, KeySourceFallback) that held the key, or the
	// llm.yaml path for KeySourceConfig ("providers.fireworks.api_key").
	// Empty for KeySourceURI and KeySourceNone.
	Name string
}

// String renders the source as "kind:name", or the kind alone.
func (s KeySource) String() string {
	if s.Name == "" {
		return string(s.Kind)
	}
	return string(s.Kind) + ":" + s.Name
}

// KeyResolution is the outcome of resolving a provider URI's key: the
// key, where it came from, and the plan it was resolved against.
type KeyResolution struct {
	// Scheme is the URI's scheme.
	Scheme string
	// Known is false for a scheme no adapter serves: kit lends it only
	// what its own llm.yaml block names.
	Known bool
	// Key is the scheme's credential as [ProviderKeyFor] describes it.
	Key ProviderKey
	// Value is the key, empty when none was found or the scheme takes
	// none. Never log or print it: String, GoString and JSON leave it
	// out.
	Value string `json:"-"`
	// Source is where Value came from.
	Source KeySource
	// StoreErr reports secret-store backend failures met on the way,
	// whether or not a later source supplied the key. It names keys,
	// never values.
	StoreErr error `json:"-"`
}

// Found reports whether a key was resolved.
func (r KeyResolution) Found() bool { return r.Source.Kind != KeySourceNone }

// String describes the resolution without the key.
func (r KeyResolution) String() string {
	return fmt.Sprintf("llm.KeyResolution{Scheme:%q Known:%t Source:%q Found:%t}",
		r.Scheme, r.Known, r.Source.String(), r.Found())
}

// GoString is String: %#v must not print the key either.
func (r KeyResolution) GoString() string { return r.String() }

// ResolveAPIKey resolves the API key a request to uri would carry,
// exactly as [ApplyAPIKey] does, and says where it came from. Use it to
// report a key's source ("set via OPENROUTER_API_KEY") without
// instrumenting the store.
//
// The key resolves highest precedence first: the URI's api_key param;
// llm.yaml api_key, then the variable api_key_env names; the names of
// [ProviderKeyFor], each from store (nil skips it) then environment;
// [FallbackEnvKey] for a required key only.
//
// A required key found nowhere returns the resolution, still
// describing the scheme, with a [*MissingKeyError]. A scheme no adapter
// serves, a scheme taking no key and a local runtime without its key
// return a resolution with no Source and a nil error. Store backend
// failures do not stop the search; they come back in StoreErr and are
// not logged: the caller decides how to surface them. uri must be
// scheme://model.
func ResolveAPIKey(ctx context.Context, store secret.Store, uri string) (KeyResolution, error) {
	parsed, err := ParseURI(uri)
	if err != nil {
		// ParseURI quotes its input, which may carry a key.
		return KeyResolution{}, errors.New("llm: resolve API key: provider URI must be scheme://model")
	}
	return DefaultRegistry.resolveKey(ctx, store, parsed)
}

// resolveKey is [ResolveAPIKey] for a parsed URI.
func (r *Registry) resolveKey(ctx context.Context, store secret.Store, parsed URI) (KeyResolution, error) {
	plan := r.keyPlan(ctx, parsed.Scheme)
	res := KeyResolution{
		Scheme: parsed.Scheme,
		Known:  plan.known,
		Key:    ProviderKey{EnvVars: slices.Clone(plan.key.EnvVars), Optional: plan.key.Optional},
	}
	if v, explicit := parsed.Params["api_key"]; explicit {
		res.Value, res.Source = v, KeySource{Kind: KeySourceURI}
		return res, nil
	}
	if plan.literal != "" {
		res.Value = plan.literal
		res.Source = KeySource{Kind: KeySourceConfig, Name: "providers." + plan.block + ".api_key"}
		return res, nil
	}
	names := plan.key.EnvVars
	if len(names) == 0 {
		return res, nil
	}
	required := plan.known && !plan.key.Optional
	res.Value, res.Source, res.StoreErr = lookupKey(ctx, store, names, required)
	if res.Value == "" && required {
		return res, &MissingKeyError{
			Scheme:   parsed.Scheme,
			Model:    parsed.Model,
			EnvVars:  append(slices.Clone(names), FallbackEnvKey),
			StoreErr: res.StoreErr,
		}
	}
	return res, nil
}
