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
	// Lists names the keys in Keys whose value is a list of entries,
	// with the keys a map entry accepts. An entry is a bare string
	// (a type, every option at its default) or a map of those keys; a
	// string value is one entry or several, comma or space separated,
	// the way an environment variable or -c carries a list. Anything
	// else is a configuration error. The block's owner checks what the
	// entries say.
	Lists map[string][]string
	// Value marks a block that is itself a list of strings —
	// services.<svc>.trusted_proxies: [10.0.0.0/8] — rather than a
	// block of keys. It has no Keys; a string value is one entry or
	// several, comma or space separated, and a map where the list
	// belongs is a configuration error. The block's owner checks
	// what the entries say.
	Value bool
	// Note is appended to an unknown-key error, for a block whose
	// shape needs explaining (a security floor with no off switch).
	Note string
	// Services names the services that apply the block, when not
	// every service does. Set under any other service, the adopter's
	// included, the block would act on nothing, so [Resolver.Validate]
	// refuses it; under services.all it is a default the other
	// services do not read. Empty means every service its reach
	// covers.
	Services []string
	// HTTPKeys names the keys of a block that otherwise reaches every
	// service which act on an HTTP listener alone. Under a service
	// with no HTTP listener [Resolver.ValidateNoHTTP] refuses them, as
	// it refuses an [HTTPOnly] block.
	HTTPKeys []string
	// HTTPValues maps a key to the values of it only an HTTP listener
	// can apply, compared without case. Under a service with no HTTP
	// listener [Resolver.ValidateNoHTTP] refuses them; the key's other
	// values are the service's own to check.
	HTTPValues map[string][]string
}

// blocks is the registry, in the order the contract's block registry
// lists them. A block is added here with its key set when it is
// implemented; reading a key through [Resolver.Lookup] that is not
// registered here escapes validation.
var blocks = []Block{
	{
		Name: "auth", Keys: []string{"mode"},
		HTTPValues: map[string][]string{"mode": {"mtls"}},
	},
	{Name: "auth.mtls", Keys: []string{"ca_file", "principal", "tenant_oid", "tenant_san_pattern"}},
	{Name: "tls", Keys: []string{"enabled", "cert_file", "key_file", "min_version"}},
	{Name: "tls.acme", Keys: []string{"enabled", "domains", "cache_dir", "email", "directory_url"}},
	{
		Name: "timeouts", Keys: []string{"read_header", "read", "write", "idle", "command"},
		HTTPKeys: []string{"read_header", "read", "write", "idle"},
	},
	{Name: "trusted_proxies", Value: true},
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
		Name: "rate_limit", Keys: []string{"enabled"},
		Note: "the per-tier limits are the read, write and destructive blocks, each with per_minute and burst",
	},
	{Name: "rate_limit.read", Keys: []string{"per_minute", "burst"}},
	{Name: "rate_limit.write", Keys: []string{"per_minute", "burst"}},
	{Name: "rate_limit.destructive", Keys: []string{"per_minute", "burst"}},
	{Name: "idempotency", Keys: []string{"enabled", "ttl"}},
	{Name: "cache", Keys: []string{"enabled", "backend", "path", "max_bytes"}, Services: []string{"api"}},
	{
		Name: "audit", Keys: []string{"sinks"},
		Lists: map[string][]string{
			"sinks": {"fsync", "max_bytes", "max_files", "on", "path", "paths", "surfaces", "type"},
		},
	},
	{
		Name: "audit.redact", Keys: []string{"secret_flags", "patterns", "max_field_bytes"},
		Note: "secret-flag redaction cannot be switched off",
	},
}

// httpOnly names the registered blocks that act on an HTTP listener
// and nowhere else — HTTP-plane middleware with no invocation-plane
// half, the listener's TLS, and the client-certificate verification
// only a TLS listener can perform. A service with no HTTP listener
// (the socket service) has nothing for them to act on, so
// [Resolver.ValidateNoHTTP] refuses them under it. A block not named
// here reaches every service, though a key or a value of it may still
// act on an HTTP listener alone (Block.HTTPKeys, Block.HTTPValues):
// the server timeouts, and auth.mode mtls.
var httpOnly = []string{
	"metrics.scrape", "security_headers", "health", "host_check",
	"origin_check", "body_limit", "compression", "trusted_proxies",
	"tls", "tls.acme", "auth.mtls",
}

// HTTPOnly reports whether block acts on an HTTP listener alone.
func HTTPOnly(block string) bool { return slices.Contains(httpOnly, block) }

// Blocks returns the registered blocks.
func Blocks() []Block {
	out := make([]Block, len(blocks))
	for i, b := range blocks {
		out[i] = b.clone()
	}
	return out
}

// clone deep-copies b, so a caller cannot edit the registry.
func (b Block) clone() Block {
	b.Keys = slices.Clone(b.Keys)
	b.Services = slices.Clone(b.Services)
	b.HTTPKeys = slices.Clone(b.HTTPKeys)
	if b.HTTPValues != nil {
		vals := make(map[string][]string, len(b.HTTPValues))
		for k, v := range b.HTTPValues {
			vals[k] = slices.Clone(v)
		}
		b.HTTPValues = vals
	}
	if b.Lists != nil {
		lists := make(map[string][]string, len(b.Lists))
		for k, v := range b.Lists {
			lists[k] = slices.Clone(v)
		}
		b.Lists = lists
	}
	return b
}

// Lookup returns the registered block named name.
func Lookup(name string) (Block, bool) {
	for _, b := range blocks {
		if b.Name == name {
			return b.clone(), true
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
//     refused, because services.all holds middleware defaults only;
//   - a block set under a service outside its Block.Services is
//     refused, because nothing there would apply it.
//
// Every problem is reported, one per line, in key order.
func (r Resolver) Validate() error {
	keys := r.setKeys()
	errs := r.checkBlocks(keys, nil, blocks)
	errs = append(errs, checkServices(keys)...)
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

// ValidateNoHTTP refuses every key under services.<svc> that lies in
// an [HTTPOnly] block, one error per block, and every key that is one
// of its block's HTTPKeys or holds one of its HTTPValues, one error
// per key: svc is a service with no HTTP listener, so nothing would
// apply the setting and it would be silently ignored. A key lies in the innermost registered
// block containing it, so metrics.scrape is refused while metrics
// itself, which also instruments invocations, is not. services.all
// is never refused here: a shared default a service does not use is
// not an error.
func (r Resolver) ValidateNoHTTP(svc string) error {
	prefix := Root + "." + svc + "."
	var errs []error
	seen := map[string]bool{}
	for _, k := range r.setKeys() {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok {
			continue
		}
		block := innermostBlock(rest)
		if block == "" || seen[block] {
			continue
		}
		if HTTPOnly(block) {
			seen[block] = true
			errs = append(errs, fmt.Errorf(
				"%s: the %s service has no HTTP listener, so %s does not apply to it; "+
					"remove it, or set it under a service that serves HTTP",
				Key(svc, block, ""), svc, block))
			continue
		}
		if err := r.checkHTTPKey(svc, block, strings.TrimPrefix(rest, block+"."), k); err != nil {
			errs = append(errs, err)
		}
	}
	return joinSorted(errs)
}

// checkHTTPKey refuses key k, which is key of block under svc, when
// it is one of the block's HTTPKeys or holds one of its HTTPValues.
func (r Resolver) checkHTTPKey(svc, block, key, k string) error {
	b, ok := Lookup(block)
	if !ok {
		return nil
	}
	if slices.Contains(b.HTTPKeys, key) {
		return fmt.Errorf(
			"%s: the %s service has no HTTP listener, so %s.%s does not apply to it; "+
				"remove it, or set it under a service that serves HTTP",
			k, svc, block, key)
	}
	val := strings.ToLower(strings.TrimSpace(fmt.Sprint(r.v.Get(k))))
	if slices.Contains(b.HTTPValues[key], val) {
		return fmt.Errorf(
			"%s: %q needs an HTTP listener, and the %s service has none; "+
				"remove it, or set it under a service that serves HTTP",
			k, val, svc)
	}
	return nil
}

// checkServices refuses, once per service and block, a block set
// under a service outside its Block.Services.
func checkServices(keys []string) []error {
	var errs []error
	seen := map[string]bool{}
	for _, k := range keys {
		rest, ok := strings.CutPrefix(k, Root+".")
		if !ok {
			continue
		}
		svc, rest, ok := strings.Cut(rest, ".")
		if !ok || svc == Shared {
			continue
		}
		for _, b := range blocks {
			if len(b.Services) == 0 || slices.Contains(b.Services, svc) ||
				(rest != b.Name && !strings.HasPrefix(rest, b.Name+".")) {
				continue
			}
			at := Key(svc, b.Name, "")
			if seen[at] {
				continue
			}
			seen[at] = true
			verb := "applies"
			if len(b.Services) > 1 {
				verb = "apply"
			}
			errs = append(errs, fmt.Errorf(
				"%s: only the %s service %s %s; remove it, or set it under %s",
				at, strings.Join(b.Services, ", "), verb, b.Name, Key(b.Services[0], b.Name, "")))
		}
	}
	return errs
}

// innermostBlock is the most specific registered block that rest, a
// key path inside a service block, is or lies in; "" when none.
func innermostBlock(rest string) string {
	best := ""
	for _, b := range blocks {
		if (rest == b.Name || strings.HasPrefix(rest, b.Name+".")) && len(b.Name) > len(best) {
			best = b.Name
		}
	}
	return best
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
			if b.Value {
				if err := r.checkValue(base, keys); err != nil {
					errs = append(errs, err)
				}
				continue
			}
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
					entryKeys, isList := b.Lists[name]
					full := base + "." + name
					if !isList || seen[full] {
						continue
					}
					if err := r.checkList(full, k, entryKeys); err != nil {
						seen[full] = true
						errs = append(errs, err)
					}
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

// checkValue checks the shape of a [Block.Value] block set at base:
// a string, or a list of strings. A key below base means a map where
// the list belongs.
func (r Resolver) checkValue(base string, keys []string) error {
	shape := fmt.Errorf("%s: must be a list of strings", base)
	for _, k := range keys {
		if strings.HasPrefix(k, base+".") {
			return shape
		}
		if k != base {
			continue
		}
		switch x := r.v.Get(base).(type) {
		case string, []string:
		case []any:
			for i, e := range x {
				if _, ok := e.(string); !ok {
					return fmt.Errorf("%s[%d]: want a string", base, i)
				}
			}
		default:
			return shape
		}
	}
	return nil
}

// checkList checks the shape of the list key full, set as k: k is
// full itself, holding a string or a list whose entries are strings
// or maps of entryKeys. A key below full means a map where the list
// belongs.
func (r Resolver) checkList(full, k string, entryKeys []string) error {
	shape := fmt.Errorf("%s: must be a list of entries, each a type or a map with keys %s",
		full, strings.Join(entryKeys, ", "))
	if k != full {
		return shape
	}
	var entries []any
	switch x := r.v.Get(full).(type) {
	case string:
		return nil
	case []string:
		return nil
	case []any:
		entries = x
	default:
		return shape
	}
	for i, e := range entries {
		var keys []string
		switch m := e.(type) {
		case string:
			continue
		case map[string]any:
			for k := range m {
				keys = append(keys, k)
			}
		case map[any]any:
			for k := range m {
				keys = append(keys, fmt.Sprint(k))
			}
		default:
			return fmt.Errorf("%s[%d]: want a type or a map with keys %s",
				full, i, strings.Join(entryKeys, ", "))
		}
		var unknown []string
		for _, k := range keys {
			if !slices.Contains(entryKeys, k) {
				unknown = append(unknown, k)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return fmt.Errorf("%s[%d]: unknown key %s; an entry accepts %s",
				full, i, strings.Join(unknown, ", "), strings.Join(entryKeys, ", "))
		}
	}
	return nil
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
