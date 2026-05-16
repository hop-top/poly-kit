// kit-manifest is kit's native machine-readable manifest format —
// the same Manifest shape `<tool> spec` has emitted since toolspec
// shipped, now exposed as a FormatAdapter alongside MCP, OpenAPI,
// and any future format. Default adapter when RegisterSpecCommand
// receives no explicit ordering preference.
//
// kit-manifest is leaf-flat: every Runnable command in the tree
// surfaces as one ManifestCommand entry with its full path. The
// recursive ToolSpec.Children structure is flattened during render.
// Curation (ErrorPatterns, Workflows, StateIntrospection) carries
// through verbatim as top-level Manifest fields.
//
// Output formats: JSON (default, indented), YAML, table. Selected
// per-render via the RenderConfig.Pretty knob and an output-format
// custom key.

package adapters

import (
	"fmt"
	"io"

	"hop.top/kit/go/ai/toolspec"
	"hop.top/kit/go/console/output"
)

const (
	// kitManifestName is the canonical adapter name.
	kitManifestName = "kit-manifest"
	// CustomKeyOutputFormat is the RenderConfig.Custom key the
	// kit-manifest adapter consults to pick between json / yaml /
	// table. Defaults to JSON when unset (the historical
	// `<tool> spec` default; agents are the primary consumer).
	CustomKeyOutputFormat = "kit-manifest:output-format"
	// CustomKeyKitManifestPrebuilt is the RenderConfig.Custom key
	// kit's spec subcommand uses to thread a pre-built
	// toolspec.Manifest into the adapter. The adapter uses the
	// pre-built manifest verbatim instead of projecting from
	// ToolSpec — this preserves all the cobra-annotation-derived
	// fields (kit/exit-codes, kit/since, kit/args, kit/flag-since)
	// that aren't part of toolspec.Command's tree shape.
	//
	// The value must be a toolspec.Manifest (not a pointer). When
	// absent, the adapter falls back to projecting from the spec
	// passed to Render, which loses some annotation-derived fields.
	CustomKeyKitManifestPrebuilt = "kit-manifest:prebuilt"
)

// kitManifestAdapter implements FormatAdapter for kit's native
// Manifest shape. The struct is empty — all configuration flows
// through RenderOption / the spec itself.
type kitManifestAdapter struct{}

// KitManifest returns the kit-manifest adapter. Stateless; safe to
// share across goroutines.
func KitManifest() FormatAdapter {
	return kitManifestAdapter{}
}

// Name implements FormatAdapter.
func (kitManifestAdapter) Name() string { return kitManifestName }

// Aliases implements FormatAdapter. "kit" and "manifest" are short
// ergonomic names; "json" is intentionally NOT an alias because the
// adapter supports yaml and table outputs too — `--format json`
// could legitimately mean "json output of *any* renderable spec
// shape" once more adapters land.
func (kitManifestAdapter) Aliases() []string {
	return []string{"kit", "manifest"}
}

// Description implements FormatAdapter.
func (kitManifestAdapter) Description() string {
	return "kit's native Manifest format (default; agent-driveable JSON/YAML/table)"
}

// ContentType implements FormatAdapter. JSON by default; the
// per-render output format may override (yaml → application/yaml,
// table → text/plain) but reporting application/json here is the
// honest default for HTTP consumers that don't specialise.
func (kitManifestAdapter) ContentType() string { return "application/json" }

// Render implements FormatAdapter. Two paths:
//
//   - If the caller threaded a pre-built toolspec.Manifest via
//     CustomKeyKitManifestPrebuilt (kit's own spec subcommand does
//     this), use it verbatim. This preserves all the
//     cobra-annotation-derived fields that don't survive the
//     ToolSpec projection (kit/exit-codes, kit/since, kit/args,
//     kit/flag-since).
//
//   - Otherwise, project the supplied ToolSpec into a Manifest
//     (lossy for the cobra-annotation fields, but works for
//     external callers who don't have the live cobra tree).
//
// Either way, output is dispatched via go/console/output in the
// format selected by CustomKeyOutputFormat (default: json).
func (a kitManifestAdapter) Render(w io.Writer, spec *toolspec.ToolSpec, opts ...RenderOption) error {
	if spec == nil {
		return fmt.Errorf("kit-manifest: nil spec")
	}
	cfg := ResolveRenderOptions(opts)

	var manifest toolspec.Manifest
	if pre, ok := cfg.Custom[CustomKeyKitManifestPrebuilt].(toolspec.Manifest); ok {
		manifest = pre
	} else {
		manifest = buildManifestFromSpec(spec, cfg)
	}

	format := output.JSON
	if v, ok := cfg.Custom[CustomKeyOutputFormat].(string); ok && v != "" {
		format = output.Format(v)
	}
	return output.Render(w, format, manifest)
}

// buildManifestFromSpec projects a tree-shaped ToolSpec into the
// flat-leaves Manifest wire format. Walks ToolSpec.Commands
// recursively, emitting one ManifestCommand per leaf (a Command with
// no Children). The traversal preserves command-path order and
// honors the IncludeDeprecated render config.
func buildManifestFromSpec(spec *toolspec.ToolSpec, cfg *RenderConfig) toolspec.Manifest {
	m := toolspec.Manifest{
		Tool:          spec.Name,
		SchemaVersion: spec.SchemaVersion,
		Commands:      []toolspec.ManifestCommand{},
	}
	if cfg.SchemaVersion != "" {
		m.SchemaVersion = cfg.SchemaVersion
	}
	for _, c := range spec.Commands {
		appendManifestLeaves(&m.Commands, []string{spec.Name}, c, cfg)
	}
	return m
}

// appendManifestLeaves walks one toolspec.Command + its descendants
// and appends a ManifestCommand for every leaf. parentPath is the
// path segments from the root down to (but not including) c.
func appendManifestLeaves(out *[]toolspec.ManifestCommand, parentPath []string, c toolspec.Command, cfg *RenderConfig) {
	if c.Deprecated && !cfg.IncludeDeprecated {
		return
	}
	path := append(append([]string(nil), parentPath...), c.Name)
	if len(c.Children) == 0 {
		*out = append(*out, projectManifestCommand(path, c))
		return
	}
	for _, child := range c.Children {
		appendManifestLeaves(out, path, child, cfg)
	}
}

// projectManifestCommand builds one ManifestCommand from a leaf
// toolspec.Command + its full path. Pulls side-effect / idempotency
// from the contract (they were stuffed into Contract during the
// walk), and surfaces the curated fields the wider Manifest doesn't
// have at the per-command level.
func projectManifestCommand(path []string, c toolspec.Command) toolspec.ManifestCommand {
	mc := toolspec.ManifestCommand{
		Path:  path,
		Short: "",
	}
	if c.Contract != nil {
		if len(c.Contract.SideEffects) > 0 {
			mc.SideEffect = c.Contract.SideEffects[0]
		}
		if c.Contract.Idempotent {
			mc.Idempotent = "yes"
		}
	}
	mc.Deprecated = c.Deprecated
	mc.DeprecatedSince = c.DeprecatedSince
	mc.Flags = projectManifestFlags(c.Flags)
	return mc
}

// projectManifestFlags converts toolspec.Flag → ManifestFlag.
// Lossless: every field the Manifest carries about a flag is
// already on toolspec.Flag.
func projectManifestFlags(flags []toolspec.Flag) []toolspec.ManifestFlag {
	if len(flags) == 0 {
		return nil
	}
	out := make([]toolspec.ManifestFlag, len(flags))
	for i, f := range flags {
		out[i] = toolspec.ManifestFlag{
			Name:        f.Name,
			Short:       f.Short,
			Type:        f.Type,
			Description: f.Description,
		}
	}
	return out
}

// Compile-time interface assertion — guards against silent
// signature drift on the FormatAdapter contract.
var _ FormatAdapter = kitManifestAdapter{}
