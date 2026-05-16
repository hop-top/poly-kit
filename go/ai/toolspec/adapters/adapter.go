// Package adapters defines the FormatAdapter interface plus the
// built-in adapters kit ships out of the box. Adapters render a
// toolspec.ToolSpec (or, for the leaf-only kit-manifest path, a
// pre-built toolspec.Manifest) to an io.Writer in some named
// machine-readable format. The set of formats is open: kit ships
// kit-manifest and mcp; tools register additional adapters
// (openapi, langchain, anthropic-function-calling, etc.) via
// cli.WithFormatAdapter at RegisterSpecCommand time.
//
// The adapter contract lives here so kit/ai/toolspec/cli can depend
// on it without depending on every individual adapter — the cli
// package only knows about the interface and the registry, while
// concrete renderers live in their own files (mcp.go,
// kit_manifest.go, ...).
//
// # Wire format vs adapter
//
// kit-manifest is itself an *adapter*, not a privileged escape hatch.
// Tools that don't want it strip it via cli.WithoutAdapter("kit-manifest").
// Tools that want only MCP register only mcp. Default registration
// includes both, because both are durable and independently useful;
// adopters opt out of either if they have a reason.
//
// # Aliases and back-compat
//
// Each adapter declares Aliases() — a list of name strings that
// also resolve to it. The mcp adapter aliases "prompt" so tools
// migrating from tlc's prior `tlc prompt` UX can offer
// `tlc spec --format prompt` and have it land on the mcp adapter
// transparently. New adopters write `--format mcp` directly.
//
// # ToolSpec, not Manifest
//
// All adapters render from toolspec.ToolSpec — the recursive tree
// shape with curation. The kit-manifest adapter still emits the
// flat-leaves Manifest wire format, but it derives that internally
// from a ToolSpec rather than taking a pre-built Manifest as input.
// This is what lets curation (ErrorPatterns, Workflows,
// StateIntrospection) flow through to whichever adapter the caller
// picks.

package adapters

import (
	"fmt"
	"io"

	"hop.top/kit/go/ai/toolspec"
)

// FormatAdapter renders a toolspec.ToolSpec to an io.Writer in a
// named machine-readable format.
//
// Implementations MUST be stateless — kit may call Render
// concurrently from multiple goroutines on the same adapter
// instance, and adapters will be cached for the process lifetime by
// the dispatch layer. Per-render configuration belongs in
// RenderOption values; per-adapter configuration belongs in the
// constructor (e.g. MCP(opts ...MCPOption)).
type FormatAdapter interface {
	// Name is the canonical adapter name as users pass it on
	// `<tool> spec --format <name>`. Must be lowercase ASCII,
	// hyphen-separated. Globally unique within a single
	// RegisterSpecCommand invocation; collisions error at
	// registration time.
	Name() string

	// Aliases lists alternative format names that also resolve to
	// this adapter. Useful for back-compat (e.g. mcp aliases
	// "prompt") or for shorter ergonomic names. Aliases share the
	// global namespace with Name(); collisions across adapters or
	// between Name and Alias error at registration time.
	Aliases() []string

	// Description is a one-line human-readable description used by
	// `<tool> spec --format-help`. Should fit on one line at
	// reasonable terminal widths (~80 cols).
	Description() string

	// ContentType is the MIME content-type the adapter produces
	// (e.g. "application/json", "application/yaml"). Used by
	// callers piping output into HTTP responses or content-typed
	// stores; not surfaced on the CLI today.
	ContentType() string

	// Render emits spec to w. Returns any I/O or serialization
	// error. Implementations must NOT close w; the caller owns its
	// lifecycle.
	//
	// opts are zero-or-more RenderOption values; adapters are free
	// to ignore options they don't recognize (forward-compatible
	// with options added by sibling adapters). See RenderOption
	// for the canonical option types kit ships.
	Render(w io.Writer, spec *toolspec.ToolSpec, opts ...RenderOption) error
}

// RenderOption is a per-render configuration knob. Adapters opt in
// to whichever options they understand; unknown options are silently
// ignored so callers can pass shared option sets to multiple
// adapters.
type RenderOption func(*RenderConfig)

// RenderConfig is the resolved render configuration adapters consult.
// Adapters call RenderOption.applyTo on a fresh RenderConfig at the
// top of Render; or use ResolveRenderOptions which centralizes the
// loop.
type RenderConfig struct {
	// Pretty controls whether JSON/YAML adapters emit indented
	// output. Default: true (most agent consumers want
	// human-debuggable output; CI consumers can disable for byte
	// stability).
	Pretty bool

	// SchemaVersion overrides the spec's schema version when set.
	// Useful for tests that want to lock a fixture version
	// independent of the live cli.RegisterSpecCommand argument.
	SchemaVersion string

	// IncludeDeprecated controls whether deprecated commands
	// appear. Default: true (the spec preserves deprecation
	// metadata; consumers filter). Set to false to omit deprecated
	// leaves entirely (matches the legacy --include-deprecated=false
	// behavior the kit-manifest adapter mirrors).
	IncludeDeprecated bool

	// Custom is an open map for adapter-specific options that
	// don't justify a typed field. Keys are adapter Name()
	// strings; values are arbitrary. Sparingly used — prefer typed
	// fields when the option is shared across adapters.
	Custom map[string]any
}

// ResolveRenderOptions applies opts to the default RenderConfig and
// returns it. Adapters call this once at the top of Render.
func ResolveRenderOptions(opts []RenderOption) *RenderConfig {
	cfg := &RenderConfig{
		Pretty:            true,
		IncludeDeprecated: true,
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

// WithPretty toggles indented JSON/YAML output. Default true; set
// false for byte-stable output (e.g. CI fixture comparison).
func WithPretty(pretty bool) RenderOption {
	return func(c *RenderConfig) { c.Pretty = pretty }
}

// WithSchemaVersion overrides the spec's schema version. Used by
// kit's spec subcommand to inject the version supplied to
// RegisterSpecCommand without mutating the walked ToolSpec.
func WithSchemaVersion(version string) RenderOption {
	return func(c *RenderConfig) { c.SchemaVersion = version }
}

// WithIncludeDeprecated controls whether deprecated commands appear
// in the rendered output. Default true.
func WithIncludeDeprecated(include bool) RenderOption {
	return func(c *RenderConfig) { c.IncludeDeprecated = include }
}

// WithCustom sets an adapter-specific custom option. Adapters
// document which keys they consume.
func WithCustom(key string, value any) RenderOption {
	return func(c *RenderConfig) {
		if c.Custom == nil {
			c.Custom = map[string]any{}
		}
		c.Custom[key] = value
	}
}

// Registry is a name → adapter lookup populated at
// RegisterSpecCommand time. The cli package owns the canonical
// instance; this type is exported so adapter implementations can
// build their own registries for tests or library consumers that
// don't go through RegisterSpecCommand.
type Registry struct {
	byName map[string]FormatAdapter
	order  []string // insertion order for stable --format-help listing
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byName: map[string]FormatAdapter{}}
}

// Register adds a to r. Returns an error on Name() or any Alias()
// collision with previously-registered adapters. Adapter name must
// be non-empty.
func (r *Registry) Register(a FormatAdapter) error {
	if a == nil {
		return fmt.Errorf("adapters.Registry: cannot register nil adapter")
	}
	name := a.Name()
	if name == "" {
		return fmt.Errorf("adapters.Registry: adapter has empty Name()")
	}
	if _, exists := r.byName[name]; exists {
		return fmt.Errorf("adapters.Registry: name %q already registered", name)
	}
	for _, alias := range a.Aliases() {
		if alias == "" {
			continue
		}
		if _, exists := r.byName[alias]; exists {
			return fmt.Errorf("adapters.Registry: alias %q for adapter %q collides with existing registration",
				alias, name)
		}
	}
	r.byName[name] = a
	for _, alias := range a.Aliases() {
		if alias == "" {
			continue
		}
		r.byName[alias] = a
	}
	r.order = append(r.order, name)
	return nil
}

// Unregister removes the adapter named name (and all its aliases)
// from r. No-op if name was never registered. Used to implement
// cli.WithoutAdapter.
func (r *Registry) Unregister(name string) {
	a, ok := r.byName[name]
	if !ok {
		return
	}
	delete(r.byName, a.Name())
	for _, alias := range a.Aliases() {
		delete(r.byName, alias)
	}
	for i, n := range r.order {
		if n == a.Name() {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
}

// Lookup returns the adapter registered under name (canonical or
// alias) or nil if not found.
func (r *Registry) Lookup(name string) FormatAdapter {
	return r.byName[name]
}

// Names returns the canonical names of every registered adapter, in
// registration order. Aliases are not included; --format-help uses
// this for the primary listing and reads aliases from each adapter
// separately.
func (r *Registry) Names() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Default returns the adapter that should be used when no --format
// flag is provided, or nil if the registry is empty. Today this is
// the first-registered adapter; cli.WithDefaultFormat will let
// adopters override.
func (r *Registry) Default() FormatAdapter {
	if len(r.order) == 0 {
		return nil
	}
	return r.byName[r.order[0]]
}
