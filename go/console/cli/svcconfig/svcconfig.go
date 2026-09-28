// Package svcconfig resolves the middleware configuration of served
// services: the services.<svc>.<block>.<key> keys, with services.all
// as the shared default for every service.
//
// It holds the block registry — every middleware block kit implements
// and the keys it accepts — and the one resolver and validator every
// block reads through, so the lookup order and the unknown-key rule
// live in one place. It imports nothing from kit, so the console and
// transport packages that own the blocks can all use it.
//
// Resolution is per key, specificity before source: the service's key
// from any source viper resolves (flag, environment, config file), then
// the services.all key, then the caller's code option, then the kit
// default. A list value is one value: the most specific list replaces
// the other, never merges with it.
package svcconfig

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/viper"
)

const (
	// Root is the top-level configuration key every service block
	// lives under.
	Root = "services"
	// Shared is the scope holding middleware defaults for every
	// service: services.all. "all" is a reserved service name, so it
	// never collides with a service.
	Shared = "all"
)

// Block is one middleware block: its path inside a service block and
// the keys it accepts.
type Block struct {
	// Name is the block's path inside services.<svc>, dotted for a
	// nested block: "body_limit", "audit.redact".
	Name string
	// Keys are the keys the block accepts. Any other key inside the
	// block is a configuration error.
	Keys []string
	// Note is appended to an unknown-key error, for a block whose
	// shape needs explaining (a security floor with no off switch).
	Note string
}

// blocks is the registry, in the order the contract's block registry
// lists them. A block is added here with its key set when it is
// implemented; reading a key through [Resolver.Lookup] that is not
// registered here escapes validation.
var blocks = []Block{
	{Name: "tracing", Keys: []string{"enabled", "exporter", "endpoint", "headers", "sample_ratio"}},
	{Name: "metrics", Keys: []string{"enabled", "exporter", "endpoint", "headers", "interval"}},
	{Name: "metrics.scrape", Keys: []string{"enabled", "path", "allow_remote"}},
	{Name: "security_headers", Keys: []string{"enabled"}},
	{Name: "health", Keys: []string{"enabled", "path_prefix", "detail"}},
	{Name: "host_check", Keys: []string{"enabled", "allow"}},
	{Name: "origin_check", Keys: []string{"enabled", "allow"}},
	{Name: "body_limit", Keys: []string{"enabled", "max_bytes"}},
	{Name: "compression", Keys: []string{"enabled", "min_bytes"}},
	{
		Name: "audit.redact", Keys: []string{"secret_flags", "patterns"},
		Note: "secret-flag redaction cannot be switched off",
	},
}

// Blocks returns the registered blocks.
func Blocks() []Block {
	out := make([]Block, len(blocks))
	for i, b := range blocks {
		b.Keys = slices.Clone(b.Keys)
		out[i] = b
	}
	return out
}

// Lookup returns the registered block named name.
func Lookup(name string) (Block, bool) {
	for _, b := range blocks {
		if b.Name == name {
			b.Keys = slices.Clone(b.Keys)
			return b, true
		}
	}
	return Block{}, false
}

// Key is the full configuration key services.<scope>.<block>.<key>;
// an empty key names the block itself.
func Key(scope, block, key string) string {
	k := Root + "." + scope + "." + block
	if key != "" {
		k += "." + key
	}
	return k
}

// Resolver resolves and validates middleware keys against one viper
// instance. The zero value, and one over a nil viper, resolves every
// key as unset and validates nothing.
type Resolver struct {
	v *viper.Viper
}

// New returns a resolver over v.
func New(v *viper.Viper) Resolver { return Resolver{v: v} }

// Lookup resolves key of block for service svc:
// services.<svc>.<block>.<key> from any source, then
// services.all.<block>.<key>. It returns the raw value and the full
// key that supplied it, for error messages; ok is false when neither
// is set and the caller's code option or kit default applies.
func (r Resolver) Lookup(svc, block, key string) (value any, from string, ok bool) {
	if r.v == nil {
		return nil, "", false
	}
	for _, scope := range []string{svc, Shared} {
		if scope == "" {
			continue
		}
		k := Key(scope, block, key)
		if r.v.IsSet(k) {
			return r.v.Get(k), k, true
		}
	}
	return nil, "", false
}

// IsConfigured reports whether any key under services.<scope> is set,
// from any source. viper's IsSet on the block itself misses a block
// whose keys arrive only from the environment.
func (r Resolver) IsConfigured(scope string) bool {
	if r.v == nil {
		return false
	}
	if r.v.IsSet(Root + "." + scope) {
		return true
	}
	prefix := Root + "." + scope + "."
	for _, k := range r.v.AllKeys() {
		if strings.HasPrefix(k, prefix) && r.v.IsSet(k) {
			return true
		}
	}
	return false
}

// Validate checks every services.* key the configuration sets:
//
//   - inside a registered block, under any service or services.all,
//     a key the block does not accept, or a scalar where the block
//     belongs, is refused;
//   - under services.all, anything outside a registered block —
//     a lifecycle key, a service's own key, an unknown block — is
//     refused, because services.all holds middleware defaults only.
//
// Every problem is reported, one per line, in key order.
func (r Resolver) Validate() error {
	keys := r.setKeys()
	errs := r.checkBlocks(keys, nil, blocks)
	sharedPrefix := Root + "." + Shared + "."
	var names []string
	for _, b := range blocks {
		names = append(names, b.Name)
	}
	for _, k := range keys {
		if k == Root+"."+Shared {
			errs = append(errs, fmt.Errorf("%s: must be a block of middleware blocks", k))
			continue
		}
		rest, ok := strings.CutPrefix(k, sharedPrefix)
		if !ok || inRegisteredBlock(rest) {
			continue
		}
		errs = append(errs, fmt.Errorf(
			"%s: not a middleware key; services.all holds middleware blocks only (%s), "+
				"so set it under the service it belongs to",
			k, strings.Join(names, ", ")))
	}
	return joinSorted(errs)
}

// ValidateBlock checks one registered block: an unknown key inside it,
// or a scalar where the block belongs, under each named scope — every
// service the configuration mentions, services.all included, when no
// scope is named.
func (r Resolver) ValidateBlock(block string, scopes ...string) error {
	b, ok := Lookup(block)
	if !ok {
		return fmt.Errorf("svcconfig: %s is not a registered block", block)
	}
	return joinSorted(r.checkBlocks(r.setKeys(), scopes, []Block{b}))
}

// setKeys is every services.* key set from any source, sorted.
func (r Resolver) setKeys() []string {
	if r.v == nil {
		return nil
	}
	var out []string
	for _, k := range r.v.AllKeys() {
		if (k == Root || strings.HasPrefix(k, Root+".")) && r.v.IsSet(k) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// checkBlocks applies the block rules to keys for the given blocks,
// under the given scopes or, with none, every scope keys mention.
func (r Resolver) checkBlocks(keys, scopes []string, bs []Block) []error {
	if len(scopes) == 0 {
		scopes = scopesOf(keys)
	}
	var errs []error
	seen := map[string]bool{}
	for _, scope := range scopes {
		for _, b := range bs {
			base := Key(scope, b.Name, "")
			for _, k := range keys {
				if k == base {
					errs = append(errs, fmt.Errorf("%s: must be a block with keys %s",
						k, strings.Join(b.Keys, ", ")))
					continue
				}
				rest, ok := strings.CutPrefix(k, base+".")
				if !ok {
					continue
				}
				name, _, _ := strings.Cut(rest, ".")
				if slices.Contains(b.Keys, name) {
					continue
				}
				if _, sub := Lookup(b.Name + "." + name); sub {
					continue // a nested block, checked on its own
				}
				full := base + "." + name
				if seen[full] {
					continue
				}
				seen[full] = true
				msg := fmt.Sprintf("%s: unknown key %q; %s accepts %s",
					full, name, b.Name, strings.Join(b.Keys, ", "))
				if b.Note != "" {
					msg += "; " + b.Note
				}
				errs = append(errs, errors.New(msg))
			}
		}
	}
	return errs
}

// scopesOf is every services.<scope> the keys mention.
func scopesOf(keys []string) []string {
	set := map[string]bool{}
	for _, k := range keys {
		rest, ok := strings.CutPrefix(k, Root+".")
		if !ok {
			continue
		}
		scope, _, _ := strings.Cut(rest, ".")
		set[scope] = true
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// inRegisteredBlock reports whether rest, a key path inside a service
// block, is a registered block or lies inside one.
func inRegisteredBlock(rest string) bool {
	for _, b := range blocks {
		if rest == b.Name || strings.HasPrefix(rest, b.Name+".") {
			return true
		}
	}
	return false
}

// joinSorted joins errs in message order, so the report is stable.
func joinSorted(errs []error) error {
	sort.SliceStable(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
	return errors.Join(errs...)
}
