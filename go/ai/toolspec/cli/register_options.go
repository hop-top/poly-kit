// Curation-registration options for RegisterSpecCommand. The `<tool>
// spec` subcommand emits a cobra-tree-derived view of the CLI; this
// file lets adopters layer curated knowledge — known error patterns,
// canonical multi-step workflows, and how to introspect the tool's
// state — on top of that view via standard functional options.
//
// Curation is data: the cobra tree describes WHAT a tool can do, but
// it cannot describe what error messages mean, what multi-step
// recipes are idiomatic, or what config commands an agent should run
// to discover a tool's state. Adopters supply that knowledge through
// these options; future format adapters (T-0336) decide how to
// render each piece.
//
// All options are optional. Tools that haven't curated yet pass
// nothing; the resulting spec simply has the curation fields empty.

package cli

import (
	"errors"
	"fmt"

	"hop.top/kit/go/ai/toolspec"
	"hop.top/kit/go/ai/toolspec/adapters"
)

// RegisterOption configures RegisterSpecCommand. Pure functions so
// callers can compose them in any order.
type RegisterOption func(*registerConfig)

// registerConfig holds resolved registration behavior. Internal;
// mutate via RegisterOption functions.
type registerConfig struct {
	errorPatterns      []toolspec.ErrorPattern
	workflows          []toolspec.Workflow
	stateIntrospection *toolspec.StateIntrospection

	// adapterRegistry is populated lazily during resolution: once
	// all adapter-modifying options have been applied, the adapters
	// list is built (kit-manifest + mcp by default, plus extras,
	// minus excluded). nil until resolveRegisterConfig finishes.
	adapterRegistry *adapters.Registry

	// extraAdapters is the list of additional adapters supplied via
	// WithFormatAdapter; applied during resolution after the
	// built-ins are auto-registered.
	extraAdapters []adapters.FormatAdapter

	// excludedAdapters is the set of names supplied via
	// WithoutAdapter; the built-in registration step skips these.
	excludedAdapters map[string]struct{}

	// defaultFormat overrides the registry's first-registered
	// default; empty means "use registry default" (kit-manifest
	// when present, otherwise the first registered adapter).
	defaultFormat string
}

// resolveRegisterConfig applies opts to a fresh config and returns
// it alongside any adapter-registration errors. Centralized so every
// call site (RegisterSpecCommand and any future spec-emitting helper)
// builds the config the same way.
//
// After applying caller options, resolution builds the adapter
// registry: auto-register kit-manifest and mcp (unless excluded),
// then add any extra adapters supplied via WithFormatAdapter.
// Adapter-name (or alias) collisions are surfaced to the caller as a
// joined error so adopters notice when WithFormatAdapter shadows a
// built-in or another extra adapter.
func resolveRegisterConfig(opts []RegisterOption) (*registerConfig, error) {
	cfg := &registerConfig{
		excludedAdapters: map[string]struct{}{},
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if err := cfg.buildAdapterRegistry(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// buildAdapterRegistry populates cfg.adapterRegistry with the
// configured adapter set. Called once at the end of
// resolveRegisterConfig; safe to call again only if the inputs
// haven't changed (adapters are stateless so re-registration is
// idempotent given the same inputs).
//
// Returns errors.Join of every collision encountered so callers see
// the full set instead of having to redo the registration after each
// fix. Callers that don't care can ignore the error; the registry
// still contains every successfully-registered adapter.
func (cfg *registerConfig) buildAdapterRegistry() error {
	r := adapters.NewRegistry()
	var errs []error

	// Auto-register kit-manifest first (so it's the default unless
	// overridden), then mcp.
	if _, excluded := cfg.excludedAdapters[adapters.KitManifest().Name()]; !excluded {
		if err := r.Register(adapters.KitManifest()); err != nil {
			errs = append(errs, fmt.Errorf("register adapter %q: %w",
				adapters.KitManifest().Name(), err))
		}
	}
	if _, excluded := cfg.excludedAdapters[adapters.MCP().Name()]; !excluded {
		if err := r.Register(adapters.MCP()); err != nil {
			errs = append(errs, fmt.Errorf("register adapter %q: %w",
				adapters.MCP().Name(), err))
		}
	}
	for _, a := range cfg.extraAdapters {
		if a == nil {
			continue
		}
		if _, excluded := cfg.excludedAdapters[a.Name()]; excluded {
			continue
		}
		if err := r.Register(a); err != nil {
			errs = append(errs, fmt.Errorf("register adapter %q: %w",
				a.Name(), err))
		}
	}
	cfg.adapterRegistry = r
	return errors.Join(errs...)
}

// resolveAdapter returns the adapter to dispatch to for the given
// raw format string. Resolution order:
//  1. Empty string → cfg.defaultFormat if set, else registry default.
//  2. Direct name/alias match → that adapter.
//  3. Known kit/output format string (json, yaml, table, csv, text)
//     → kit-manifest adapter with the format threaded as a custom
//     key so the legacy `<tool> spec --format json` workflow keeps
//     working.
//  4. No match → nil. Caller produces the "unknown format" error
//     message with the registered list.
func (cfg *registerConfig) resolveAdapter(format string) (adapters.FormatAdapter, []adapters.RenderOption) {
	r := cfg.adapterRegistry
	if r == nil {
		return nil, nil
	}
	if format == "" {
		if cfg.defaultFormat != "" {
			if a := r.Lookup(cfg.defaultFormat); a != nil {
				return a, nil
			}
		}
		return r.Default(), nil
	}
	if a := r.Lookup(format); a != nil {
		return a, nil
	}
	// Back-compat: kit/output format strings dispatch to
	// kit-manifest with the format threaded through as a custom
	// option. This preserves `<tool> spec --format json/yaml/table`
	// for every CI consumer that pre-dates the adapter system.
	switch format {
	case "json", "yaml", "table", "csv", "text":
		if a := r.Lookup(adapters.KitManifest().Name()); a != nil {
			return a, []adapters.RenderOption{
				adapters.WithCustom(adapters.CustomKeyOutputFormat, format),
			}
		}
	}
	return nil, nil
}

// WithErrorPatterns curates the tool's known error patterns. Each
// entry pairs an error-output regex/substring with a canonical fix
// suggestion. Agents reading the spec use these to recover from
// errors without round-tripping to a human.
//
// Example:
//
//	cli.WithErrorPatterns([]toolspec.ErrorPattern{
//	    {
//	        Pattern: "profile.*not found",
//	        Cause:   "profile id does not exist in $APS_DATA_PATH/profiles",
//	        Fix:     "create with `aps profile add <id>`",
//	    },
//	})
//
// Repeated calls accumulate; callers can build a slice and pass it
// once for clarity.
func WithErrorPatterns(p []toolspec.ErrorPattern) RegisterOption {
	return func(c *registerConfig) {
		c.errorPatterns = append(c.errorPatterns, p...)
	}
}

// WithWorkflows curates canonical multi-step sequences agents are
// expected to execute. Each Workflow names a recipe and orders its
// steps (each step is a shell-invocable command line).
//
// Example:
//
//	cli.WithWorkflows([]toolspec.Workflow{
//	    {
//	        Name: "create-and-launch-profile",
//	        Steps: []string{
//	            "aps profile add <id>",
//	            "aps capability add <id> <capability>",
//	            "aps <id>",
//	        },
//	    },
//	})
//
// Repeated calls accumulate.
func WithWorkflows(w []toolspec.Workflow) RegisterOption {
	return func(c *registerConfig) {
		c.workflows = append(c.workflows, w...)
	}
}

// WithStateIntrospection declares how an agent can discover the
// tool's runtime state — config commands, env vars, auth commands.
// Distinct from per-command output: this is the tool-level "where
// does this tool keep its mind" descriptor.
//
// Example:
//
//	cli.WithStateIntrospection(&toolspec.StateIntrospection{
//	    ConfigCommands: []string{"aps env", "aps version"},
//	    EnvVars:        []string{"APS_DATA_PATH"},
//	    AuthCommands:   []string{"aps auth status"},
//	})
//
// Repeated calls overwrite — there is one tool-level introspection
// descriptor; the latest wins. Pass nil to clear a previously-set
// introspection (rare; mainly for tests).
func WithStateIntrospection(s *toolspec.StateIntrospection) RegisterOption {
	return func(c *registerConfig) {
		c.stateIntrospection = s
	}
}

// curatedToolSpec applies the registerConfig's curation fields to a
// freshly-walked ToolSpec. Adapters that need the curated tree
// (MCP, future OpenAPI) consume the result; the leaf-only Manifest
// emitter today ignores curation (curation is tree-level).
func (cfg *registerConfig) curatedToolSpec(spec *toolspec.ToolSpec) *toolspec.ToolSpec {
	if spec == nil {
		return nil
	}
	if len(cfg.errorPatterns) > 0 {
		spec.ErrorPatterns = append(spec.ErrorPatterns, cfg.errorPatterns...)
	}
	if len(cfg.workflows) > 0 {
		spec.Workflows = append(spec.Workflows, cfg.workflows...)
	}
	if cfg.stateIntrospection != nil {
		spec.StateIntrospection = cfg.stateIntrospection
	}
	return spec
}

// WithFormatAdapter registers an additional FormatAdapter on top of
// the built-ins (kit-manifest, mcp). Use it to publish OpenAPI,
// LangChain tools, function-calling envelopes, or any other custom
// format from the same `<tool> spec` subcommand.
//
// Example:
//
//	cli.RegisterSpecCommand(root, "1.0",
//	    cli.WithFormatAdapter(myCorpAdapter()),
//	    cli.WithFormatAdapter(openapi.Adapter()),
//	)
//
// Then `<tool> spec --format <my-corp-name>` and
// `<tool> spec --format openapi` both dispatch to the new
// adapters. Built-in kit-manifest and mcp continue to work unless
// explicitly excluded via WithoutAdapter.
//
// Repeated calls accumulate. Adapter-name (or alias) collisions are
// surfaced as the error returned by RegisterSpecCommand — the second
// registration is dropped and the caller sees a clear "register
// adapter \"<name>\": ... already registered" message.
func WithFormatAdapter(a adapters.FormatAdapter) RegisterOption {
	return func(c *registerConfig) {
		if a != nil {
			c.extraAdapters = append(c.extraAdapters, a)
		}
	}
}

// WithoutAdapter excludes the named adapter from the registered
// set. Use it to suppress one of the built-ins (kit-manifest, mcp)
// for tools that genuinely don't want it.
//
// Examples:
//
//	// Tool publishes only kit-manifest, no MCP:
//	cli.RegisterSpecCommand(root, "1.0", cli.WithoutAdapter("mcp"))
//
//	// Tool publishes only MCP, no kit-manifest:
//	cli.RegisterSpecCommand(root, "1.0", cli.WithoutAdapter("kit-manifest"))
//
// Excluding both leaves no adapters; `<tool> spec --format <any>`
// then errors with the registered-formats list (which is empty).
// That's a deliberate choice rather than a footgun: kit doesn't
// second-guess adopters who explicitly ask for it.
//
// Repeated calls accumulate.
func WithoutAdapter(name string) RegisterOption {
	return func(c *registerConfig) {
		if c.excludedAdapters == nil {
			c.excludedAdapters = map[string]struct{}{}
		}
		c.excludedAdapters[name] = struct{}{}
	}
}

// WithDefaultFormat overrides the registry's default adapter — the
// one that emits when `<tool> spec` is invoked without `--format`.
// Default is the first-registered adapter (kit-manifest, unless
// excluded).
//
// Example:
//
//	// Tool whose primary consumer is MCP:
//	cli.RegisterSpecCommand(root, "1.0", cli.WithDefaultFormat("mcp"))
//
// name must match a registered adapter's Name() or Aliases();
// unknown names silently fall through to the registry default at
// dispatch time.
//
// Repeated calls overwrite — there is one default; latest wins.
func WithDefaultFormat(name string) RegisterOption {
	return func(c *registerConfig) {
		c.defaultFormat = name
	}
}
