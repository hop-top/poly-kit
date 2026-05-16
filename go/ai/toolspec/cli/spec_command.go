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
	"github.com/spf13/pflag"
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

// BuildManifest walks root.Cmd and returns the toolspec.Manifest the
// spec subcommand emits. Exported so tests and external introspection
// tools can build the manifest without going through the subcommand.
//
// includeDeprecated controls whether deprecated leaves appear in the
// returned slice; when false (default), they're filtered out so agents
// don't latch onto soon-to-be-removed commands.
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
	walkLeaves(root.Cmd, func(cmd *cobra.Command) {
		// Skip the spec subcommand itself + cobra built-ins (help,
		// completion). The kit/spec-command annotation locks this
		// regardless of name.
		if isSpecOrBuiltin(cmd) {
			return
		}
		isDeprecated := commandIsDeprecated(cmd)
		if isDeprecated && !includeDeprecated {
			return
		}
		m.Commands = append(m.Commands, manifestCommand(cmd, root))
	})
	return m
}

// commandIsDeprecated reports whether cmd carries a deprecation marker:
// cobra.Command.Deprecated set, kit/deprecated-since, or
// kit/removal-target — any of the three triggers the deprecation
// classification (matching the warning emitter's logic).
func commandIsDeprecated(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	if cmd.Deprecated != "" {
		return true
	}
	if cmd.Annotations == nil {
		return false
	}
	return cmd.Annotations["kit/deprecated-since"] != "" ||
		cmd.Annotations["kit/removal-target"] != ""
}

// manifestCommand projects a single cobra leaf into a ManifestCommand.
// The optional root parameter lets the projector consult kit-shipped
// reserved-subcommand snapshots when computing the Reserved field
// . Callers that do not have a Root handy may pass
// nil; the Reserved field then defaults to false.
func manifestCommand(cmd *cobra.Command, root *kitcli.Root) toolspec.ManifestCommand {
	mc := toolspec.ManifestCommand{
		Path:  strings.Fields(cmd.CommandPath()),
		Short: cmd.Short,
		Long:  cmd.Long,
	}
	if se, ok := kitcli.GetSideEffect(cmd); ok {
		mc.SideEffect = string(se)
	}
	if id, ok := kitcli.GetIdempotency(cmd); ok {
		mc.Idempotent = string(id)
	}
	mc.Deprecated = commandIsDeprecated(cmd)
	mc.Hidden = cmd.Hidden
	if cmd.Annotations != nil {
		mc.DeprecatedSince = cmd.Annotations["kit/deprecated-since"]
		mc.RemovalTarget = cmd.Annotations["kit/removal-target"]
		mc.SinceVersion = cmd.Annotations["kit/since"]
		if raw := cmd.Annotations["kit/exit-codes"]; raw != "" {
			mc.ExitCodes = splitCSV(raw)
		}
		if raw := cmd.Annotations["kit/args"]; raw != "" {
			for _, name := range splitCSV(raw) {
				mc.Args = append(mc.Args, toolspec.ManifestArg{
					Name:     strings.TrimSuffix(name, "?"),
					Required: !strings.HasSuffix(name, "?"),
				})
			}
		}
	}
	mc.Flags = collectFlags(cmd)

	// === Schema 1.1 additions ===
	projectLayerAFields(cmd, &mc, root)
	return mc
}

// projectLayerAFields fills the schema-1.1 fields on mc. Split out
// of manifestCommand to keep the projector readable. Decode failures
// on kit/examples / kit/next-steps are silently dropped so the
// projector never fails the manifest as a whole.
func projectLayerAFields(cmd *cobra.Command, mc *toolspec.ManifestCommand, root *kitcli.Root) {
	if cmd == nil || mc == nil {
		return
	}
	mc.Retryable = kitcli.IsRetryable(cmd)
	mc.TopLevelVerb = kitcli.IsTopLevelVerb(cmd)
	mc.Hierarchical = kitcli.IsHierarchical(cmd)
	mc.Passthrough = kitcli.IsPassthrough(cmd)

	if raw, ver, ok := kitcli.GetOutputSchemaJSON(cmd); ok && len(raw) > 0 {
		buf := make([]byte, len(raw))
		copy(buf, raw)
		mc.OutputSchema = toolspec.RawJSON(buf)
		mc.OutputSchemaVersion = ver
	}

	if ex, ok := kitcli.GetExamples(cmd); ok {
		mc.Examples = make([]toolspec.ManifestExample, 0, len(ex))
		for _, e := range ex {
			mc.Examples = append(mc.Examples, toolspec.ManifestExample{
				Title:   e.Title,
				Command: e.Command,
				Output:  e.Output,
			})
		}
	}
	if ns, ok := kitcli.GetNextSteps(cmd); ok {
		mc.NextSteps = make([]toolspec.ManifestNextStep, 0, len(ns))
		for _, n := range ns {
			mc.NextSteps = append(mc.NextSteps, toolspec.ManifestNextStep{
				When:    n.When,
				Suggest: n.Suggest,
				Reason:  n.Reason,
			})
		}
	}

	// kit/destructive-token=required → DestructiveTokenRequired.
	if cmd.Annotations != nil {
		if v := cmd.Annotations["kit/destructive-token"]; v == "required" || v == "true" {
			mc.DestructiveTokenRequired = true
		}
		mc.DryRunRationale = cmd.Annotations["kit/dry-run-rationale"]
	}

	mc.DryRunSupported = kitcli.IsDryRunSupported(cmd)

	if root != nil {
		mc.Reserved = root.IsReserved(firstAncestorName(cmd, root))
	}
}

// firstAncestorName returns the name of the depth-1 ancestor of cmd
// under root (e.g. for `mytool foo bar baz` returns "foo"). When cmd
// is a depth-1 leaf the function returns cmd.Name() itself; when
// cmd is the root or somehow detached the function returns "".
func firstAncestorName(cmd *cobra.Command, root *kitcli.Root) string {
	if cmd == nil || root == nil || root.Cmd == nil {
		return ""
	}
	if cmd == root.Cmd {
		return ""
	}
	current := cmd
	for current.Parent() != nil && current.Parent() != root.Cmd {
		current = current.Parent()
	}
	return current.Name()
}

// collectFlags returns all flags declared directly on cmd (i.e. local
// flags excluding inherited persistent flags) projected into
// ManifestFlag entries. Persistent globals from the root are excluded
// here; agents discover them via the manifest's tool-level metadata
// when adopters extend the schema. Today we keep per-command flags
// scoped tight so the manifest is compact.
func collectFlags(cmd *cobra.Command) []toolspec.ManifestFlag {
	if cmd == nil {
		return nil
	}
	since := flagSinceForCmd(cmd)
	var out []toolspec.ManifestFlag
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		mf := toolspec.ManifestFlag{
			Name:        f.Name,
			Short:       f.Shorthand,
			Type:        f.Value.Type(),
			Description: f.Usage,
			Default:     f.DefValue,
		}
		if v, ok := since[f.Name]; ok {
			mf.SinceVersion = v
		}
		out = append(out, mf)
	})
	return out
}

// flagSinceForCmd parses kit/flag-since for this command into a flag
// name → MAJOR.MINOR map. Mirrors the apiversion.go parser but lives
// here to avoid an internal-package import; the format is stable per
// §13.
func flagSinceForCmd(cmd *cobra.Command) map[string]string {
	out := map[string]string{}
	if cmd == nil || cmd.Annotations == nil {
		return out
	}
	raw := cmd.Annotations["kit/flag-since"]
	if raw == "" {
		return out
	}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		eq := strings.Index(entry, "=")
		if eq < 0 {
			continue
		}
		out[strings.TrimSpace(entry[:eq])] = strings.TrimSpace(entry[eq+1:])
	}
	return out
}

// splitCSV splits a "a,b,c" string into trimmed non-empty parts.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// walkLeaves invokes fn on every leaf command (no children) under root.
func walkLeaves(root *cobra.Command, fn func(*cobra.Command)) {
	if root == nil {
		return
	}
	if !root.HasSubCommands() {
		fn(root)
		return
	}
	for _, c := range root.Commands() {
		walkLeaves(c, fn)
	}
}

// isSpecOrBuiltin returns true for cobra/fang built-in leaves (help,
// completion bash|zsh|fish|powershell, man, __complete). Match by
// leaf name or by parent name for the completion-shell leaves.
// Adopter-overridden help/completion inherit the same exemption
// since kit treats them as built-ins.
//
// The spec / manifest subcommands themselves are NOT filtered out —
// the manifest must include a self-describing
// entry for `<tool> spec` so agents can locate the schema of every
// other entry. The legacy kit/spec-command annotation is preserved
// for the deprecation-warning middleware (which separately skips
// the spec command to keep the output stream clean), but does not
// remove the leaf from the manifest.
func isSpecOrBuiltin(cmd *cobra.Command) bool {
	if cmd == nil {
		return true
	}
	switch cmd.Name() {
	case "help", "completion", "man", "__complete", "__completeNoDesc":
		return true
	}
	if p := cmd.Parent(); p != nil {
		switch p.Name() {
		case "completion":
			return true
		}
	}
	// Bare-root non-runnable leaves (no children, no Run/RunE)
	// shouldn't appear in the manifest either.
	if !cmd.Runnable() {
		return true
	}
	return false
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
