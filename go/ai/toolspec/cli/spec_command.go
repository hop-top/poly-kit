// Package cli implements the `<tool> spec` subcommand and its supporting
// manifest builder. Adopters call RegisterSpecCommand once after
// registering all of the tool's commands; the spec subcommand walks the
// live cobra tree and emits a toolspec.Manifest in the active --format.
//
// See ~/.ops/docs/cli-conventions-with-kit.md §13 for the locked
// machine-readable shape and the capability-negotiation contract.
package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"hop.top/kit/go/ai/cmdreflect"
	"hop.top/kit/go/ai/toolspec"
	"hop.top/kit/go/ai/toolspec/adapters"
	kitcli "hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// versionOnlyFlag is the canonical short-circuit flag used by agents
// that only need to read the schema_version (capability negotiation
// without the full manifest payload).
const versionOnlyFlag = "version"

// includeDeprecatedFlag toggles whether deprecated leaves appear in the
// emitted manifest. Default false: deprecated commands hide so agents
// don't latch onto them. --help-all / --include-deprecated flips it.
const includeDeprecatedFlag = "include-deprecated"

// specCommandAnnotation marks the spec subcommand so the kit/console
// deprecation-warning middleware skips it (warnings would corrupt the
// manifest output stream consumed by agents).
const specCommandAnnotation = "kit/spec-command"

// RegisterSpecCommand attaches a `spec` subcommand to root that emits
// the tool's full machine-readable capability manifest. Adopters call
// it once after registering all commands:
//
//	if err := cli.RegisterSpecCommand(root, "1.0"); err != nil {
//	    return err
//	}
//
// schemaVersion is the tool's CLI schema version (MAJOR.MINOR); see
// ~/.ops/docs/cli-conventions-with-kit.md §13.2. Distinct from the
// tool's binary semver (root.Config.Version): schema evolves on the
// CLI surface, semver evolves on the binary.
//
// The returned error is non-nil only when adapter registration fails
// (e.g. WithFormatAdapter supplies an adapter whose Name() or
// Aliases() collide with a built-in or another extra adapter). The
// subcommand still mounts with whichever adapters did register
// successfully; callers that want to fail-fast should surface the
// error to startup.
//
// The spec subcommand:
//   - Walks root.Cmd, building a toolspec.Manifest entry for every
//     leaf command (path, args, flags, side-effect, idempotency,
//     deprecation status, exit-code set).
//   - Renders via output.Render so --format json|yaml|table all work.
//   - Accepts --version to print only schemaVersion and exit
//     (agents use this for fast capability negotiation).
//   - Accepts --include-deprecated to opt into seeing deprecated
//     leaves; default omits them.
//
// The subcommand is tagged kit/side-effect=read + kit/idempotent=yes so
// it passes Root.Validate. It also carries the kit/spec-command
// annotation so the deprecation-warning middleware skips it.
func RegisterSpecCommand(root *kitcli.Root, schemaVersion string, opts ...RegisterOption) error {
	if root == nil || root.Cmd == nil {
		return nil
	}
	cfg, cfgErr := resolveRegisterConfig(opts)
	cmd := &cobra.Command{
		Use:   "spec",
		Short: "Emit machine-readable capability manifest",
		Long: "Emit the tool's full capability manifest as JSON/YAML/table. " +
			"Agents consume this for capability negotiation and dispatch. " +
			"--version prints only the schema version.",
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			"kit/side-effect":     "read",
			"kit/idempotent":      "yes",
			specCommandAnnotation: "true",
		},
	}
	cmd.Flags().Bool(versionOnlyFlag, false,
		"Print the schema version only (capability negotiation)")
	cmd.Flags().Bool(includeDeprecatedFlag, false,
		"Include deprecated commands in the manifest (default: hidden)")

	cmd.RunE = func(c *cobra.Command, _ []string) error {
		return runSpec(c, root, schemaVersion, cfg)
	}

	// Self-annotate the spec subcommand shape
	// + structured-output schema + agent-facing examples. Errors
	// from the typed setters are dropped; the subcommand still
	// mounts even if the schema reflection fails (defensive — the
	// subcommand has to ship a manifest of itself).
	kitcli.SetTopLevelVerb(cmd)
	_ = kitcli.SetOutputSchema(cmd, kitcli.OutputSchema{
		Type:    &toolspec.Manifest{},
		Version: schemaVersion,
	})
	toolName := root.Config.Name
	_ = kitcli.SetExamples(cmd, []kitcli.Example{
		{Title: "Full capabilities", Command: toolName + " spec --format json"},
		{Title: "Schema probe", Command: toolName + " spec --version"},
		{Title: "Filter to old clients", Command: toolName + " spec --api-version=1.0"},
	})

	root.Cmd.AddCommand(cmd)
	// Late-mount snapshot: the spec subcommand is reserved. Adopters
	// calling RegisterSpecCommand AFTER cli.New still see it land in
	// the IsReserved set.
	root.MarkReserved("spec")
	return cfgErr
}

// runSpec is the spec subcommand's RunE body. Walks the cobra
// tree, applies curation (ErrorPatterns, Workflows,
// StateIntrospection from RegisterSpecCommand options), resolves
// the active --format to a registered FormatAdapter, and
// dispatches.
//
// Resolution prefers adapter names + aliases; legacy kit/output
// format strings (json, yaml, table, csv, text) fall through to
// the kit-manifest adapter so existing CI consumers keep working
// unchanged.
//
// The version-only short-circuit predates the adapter system and
// always emits via output.Render — agents using `--version` for
// capability negotiation expect a tiny JSON/YAML payload, not a
// per-adapter envelope.
func runSpec(cmd *cobra.Command, root *kitcli.Root, schemaVersion string, cfg *registerConfig) error {
	versionOnly, _ := cmd.Flags().GetBool(versionOnlyFlag)
	includeDeprecated, _ := cmd.Flags().GetBool(includeDeprecatedFlag)

	rawFormat, formatChanged := flagValueAndChangedWalk(cmd, "format")
	// When --format wasn't explicitly set by the user, treat it as
	// empty so the adapter registry's default (kit-manifest by
	// default) handles dispatch. Kit's CLI defaults --format to
	// "table" globally; without this, every invocation of `<tool>
	// spec` would route through the table renderer, which has no
	// struct tags for the Manifest type.
	if !formatChanged {
		rawFormat = ""
	}

	if versionOnly {
		// Short-circuit: emit only the schema version. JSON/YAML wrap
		// it in {"schema_version": "..."}; plaintext prints raw.
		legacyFormat := rawFormat
		if legacyFormat == "" {
			legacyFormat = output.JSON
		}
		payload := struct {
			SchemaVersion string `json:"schema_version" yaml:"schema_version"`
		}{SchemaVersion: schemaVersion}
		return renderVersionOnly(cmd, legacyFormat, payload, schemaVersion)
	}

	adapter, adapterOpts := cfg.resolveAdapter(rawFormat)
	if adapter == nil {
		return unknownFormatError(rawFormat, cfg)
	}

	// Apply per-render options threaded through from the dispatch.
	renderOpts := append([]adapters.RenderOption{
		adapters.WithSchemaVersion(schemaVersion),
		adapters.WithIncludeDeprecated(includeDeprecated),
	}, adapterOpts...)

	// kit-manifest gets a pre-built toolspec.Manifest threaded
	// through Custom so it can emit kit's existing wire format
	// without info-loss through ToolSpec. Other adapters consume
	// the curated ToolSpec built from WalkCobra.
	if adapter.Name() == adapters.KitManifest().Name() {
		manifest := BuildManifest(root, schemaVersion, includeDeprecated)
		renderOpts = append(renderOpts,
			adapters.WithCustom(adapters.CustomKeyKitManifestPrebuilt, manifest))
	}

	spec := WalkCobra(root.Cmd)
	spec.Name = root.Config.Name
	spec.SchemaVersion = schemaVersion
	cfg.curatedToolSpec(spec)

	return adapter.Render(cmd.OutOrStdout(), spec, renderOpts...)
}

// unknownFormatError produces the "unknown --format <name>" error
// with the registered adapters listed for discoverability. Mirrors
// kit/output's --format-help convention so adopters get the same
// shape of error message regardless of which subcommand they hit.
func unknownFormatError(format string, cfg *registerConfig) error {
	if cfg == nil || cfg.adapterRegistry == nil {
		return fmt.Errorf("unknown --format %q (no adapters registered)", format)
	}
	names := cfg.adapterRegistry.Names()
	// Append the legacy kit/output formats so the error message
	// reflects what `--format` actually accepts (adapters first,
	// kit/output formats second).
	all := append([]string(nil), names...)
	all = append(all, "json", "yaml", "table", "csv", "text")
	return fmt.Errorf("unknown --format %q (valid: %s)", format, strings.Join(all, ", "))
}

// renderVersionOnly emits the version-only payload. Plaintext prints
// the raw schema version without a wrapper so shell scripts can
// `mytool spec --version --format=table | tr -d '\n'` cleanly.
func renderVersionOnly(cmd *cobra.Command, format string, payload any, schemaVersion string) error {
	switch format {
	case output.JSON, output.YAML:
		return output.Render(cmd.OutOrStdout(), format, payload)
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), schemaVersion)
	return err
}

// EmitManifest builds and returns the toolspec.Manifest for the given
// kit Root, applying the same defaults as `<tool> spec` (deprecated
// commands hidden). Useful for callers that want the manifest
// in-process without going through a subcommand — for example, the
// `kit toolspec` bootstrap subcommand emits its own manifest by
// calling EmitManifest on its own root.
//
// The returned manifest is a value (not a pointer) consistent with
// BuildManifest's signature; callers wanting to mutate it copy first.
func EmitManifest(root *kitcli.Root, schemaVersion string) toolspec.Manifest {
	return BuildManifest(root, schemaVersion, false)
}

// BuildManifest reflects root.Cmd and returns the toolspec.Manifest
// the spec subcommand emits. Exported so tests and external
// introspection tools can build the manifest without going through
// the subcommand.
//
// Reflection is delegated to [hop.top/kit/go/ai/cmdreflect]; this
// function only projects descriptors into the manifest shape and
// applies the manifest's own inclusion policy. See that package for
// the one-descriptor rule.
//
// includeDeprecated controls whether deprecated leaves appear in the
// returned slice; when false (default), they're filtered out so
// agents don't latch onto soon-to-be-removed commands.
func BuildManifest(root *kitcli.Root, schemaVersion string, includeDeprecated bool) toolspec.Manifest {
	if root == nil || root.Cmd == nil {
		return toolspec.Manifest{
			SchemaVersion: schemaVersion,
			Commands:      []toolspec.ManifestCommand{},
		}
	}
	m := toolspec.Manifest{
		Tool:          root.Config.Name,
		Version:       root.Config.Version,
		SchemaVersion: schemaVersion,
		Commands:      []toolspec.ManifestCommand{},
	}

	// The manifest publishes hidden and interactive leaves and its
	// own reserved management verbs — an agent must be able to find
	// the schema of `<tool> spec` itself. Deprecation is the one
	// exclusion the manifest applies, and it is caller-controlled.
	tree := cmdreflect.Reflect(
		root.Cmd,
		cmdreflect.WithReserved(root),
		cmdreflect.AllowHidden(),
		cmdreflect.AllowInteractive(),
		cmdreflect.AllowReserved(),
		cmdreflect.AllowDeprecated(),
	)
	for _, d := range tree.Descriptors {
		if !manifestIncludes(d, includeDeprecated) {
			continue
		}
		m.Commands = append(m.Commands, manifestCommand(d))
	}
	return m
}

// manifestIncludes applies the manifest's inclusion policy to a
// reflected descriptor.
//
// The manifest lists LEAVES: a command group has no invocation of
// its own, so publishing it as a callable tool would be a lie. That
// is exactly what cmdreflect records as ReasonNotRunnable, and what
// ReasonBuiltin records for the framework's own help and completion
// trees. Deprecation is the caller's choice.
func manifestIncludes(d *cmdreflect.Descriptor, includeDeprecated bool) bool {
	if d == nil || d.Cmd == nil || d.IsRoot() {
		return false
	}
	switch d.Reason {
	case cmdreflect.ReasonNotRunnable, cmdreflect.ReasonBuiltin:
		return false
	}
	if d.Surface.HasSubCommands {
		return false
	}
	if d.Surface.Deprecated && !includeDeprecated {
		return false
	}
	return true
}

// manifestCommand projects one reflected descriptor into a
// ManifestCommand. Every field here is a rename or a re-shape of a
// fact [hop.top/kit/go/ai/cmdreflect] already resolved; nothing is
// re-derived from the cobra command.
func manifestCommand(d *cmdreflect.Descriptor) toolspec.ManifestCommand {
	mc := toolspec.ManifestCommand{
		Path:            append([]string(nil), d.Path...),
		Short:           d.Short,
		Long:            d.Long,
		SideEffect:      d.Safety.DeclaredSideEffect,
		Idempotent:      d.Safety.Idempotent,
		ExitCodes:       d.Safety.ExitCodes,
		Deprecated:      d.Surface.Deprecated,
		DeprecatedSince: d.Surface.DeprecatedSince,
		RemovalTarget:   d.Surface.RemovalTarget,
		SinceVersion:    d.Surface.SinceVersion,
		Hidden:          d.Surface.Hidden,

		Retryable:                d.Safety.Retryable,
		TopLevelVerb:             d.Surface.TopLevelVerb,
		Hierarchical:             d.Surface.Hierarchical,
		Passthrough:              d.Surface.Passthrough,
		DestructiveTokenRequired: d.Safety.DestructiveTokenRequired,
		DryRunSupported:          d.Safety.DryRunSupported,
		DryRunRationale:          d.Safety.DryRunRationale,
		Reserved:                 d.Surface.Reserved,
	}

	for _, a := range d.Args {
		mc.Args = append(mc.Args, toolspec.ManifestArg{
			Name:     a.Name,
			Required: a.Required,
		})
	}
	mc.Flags = manifestFlags(d.Flags)

	if len(d.Output.Schema) > 0 {
		mc.OutputSchema = toolspec.RawJSON(append([]byte(nil), d.Output.Schema...))
		mc.OutputSchemaVersion = d.Output.SchemaVersion
	}
	if len(d.Output.Examples) > 0 {
		mc.Examples = make([]toolspec.ManifestExample, 0, len(d.Output.Examples))
		for _, e := range d.Output.Examples {
			mc.Examples = append(mc.Examples, toolspec.ManifestExample{
				Title: e.Title, Command: e.Command, Output: e.Output,
			})
		}
	}
	if len(d.Output.NextSteps) > 0 {
		mc.NextSteps = make([]toolspec.ManifestNextStep, 0, len(d.Output.NextSteps))
		for _, n := range d.Output.NextSteps {
			mc.NextSteps = append(mc.NextSteps, toolspec.ManifestNextStep{
				When: n.When, Suggest: n.Suggest, Reason: n.Reason,
			})
		}
	}
	return mc
}

// manifestFlags projects reflected flags into ManifestFlag entries.
// Hidden flags are omitted: the manifest keeps per-command flags
// scoped tight, and a hidden flag is not part of the supported
// surface. Persistent globals inherited from the root are excluded
// too — they belong to the tool, not to any one leaf.
func manifestFlags(flags []cmdreflect.Flag) []toolspec.ManifestFlag {
	var out []toolspec.ManifestFlag
	for _, f := range flags {
		if f.Hidden {
			continue
		}
		out = append(out, toolspec.ManifestFlag{
			Name:         f.Name,
			Short:        f.Short,
			Type:         f.Type,
			Description:  f.Description,
			Default:      f.Default,
			SinceVersion: f.SinceVersion,
		})
	}
	return out
}

// flagValueAndChangedWalk also reports whether the flag was
// explicitly set by the user. Adapter dispatch uses this to
// distinguish "user wants the registry default" (flag at its
// default) from "user explicitly asked for the kit/output 'table'
// format" (flag changed). Without this, kit's global --format
// default of "table" would route every spec invocation to
// kit-manifest with output-format=table, which produces empty
// output (Manifest has no table struct tags).
func flagValueAndChangedWalk(cmd *cobra.Command, name string) (string, bool) {
	for c := cmd; c != nil; c = c.Parent() {
		if f := c.PersistentFlags().Lookup(name); f != nil {
			return f.Value.String(), f.Changed
		}
		if f := c.Flags().Lookup(name); f != nil {
			return f.Value.String(), f.Changed
		}
	}
	return "", false
}
