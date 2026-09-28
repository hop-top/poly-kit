package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"hop.top/kit/go/ai/toolspec"
	speccli "hop.top/kit/go/ai/toolspec/cli"
	"hop.top/kit/go/ai/toolspec/policy"
	kitcli "hop.top/kit/go/console/cli"
)

// kitToolspecSchemaVersion is the schema version kit's own manifest
// declares: the newest one. 1.1 adds per-command fields on
// ManifestCommand that surface kit's command annotations; it is
// additive, so 1.0 clients that ignore unknown fields read it
// unchanged. Declaring the newest version means negotiation never
// moves it: kit never downgrades.
const kitToolspecSchemaVersion = toolspec.LatestSchemaVersion

// toolspecCmd is the discovery surface the toolspec contract mandates on the kit
// binary itself — `kit toolspec` emits kit's own capability manifest
// as JSON. Harnesses that want to discover that the kit-toolspec
// contract exists at all start by invoking `kit toolspec` and
// reading the schema_version from the response.
//
// Distinct from `<tool> spec` (the per-binary surface every
// kit-powered CLI gains via RegisterSpecCommand): `kit toolspec` is
// the bootstrap manifest of the kit binary itself, used as the
// well-known anchor for protocol discovery. The legacy
// `<tool> manifest` subcommand was retired in schema 1.1; adopters
// serve `<tool> spec` only.
//
// Implementation is deliberately thin: BuildManifest already does
// the cobra-tree projection. KIT_TOOLSPEC_SCHEMA goes through
// toolspec.NegotiateSchemaVersion, the rule `<tool> spec` applies
// too; --version short-circuits to a
// minimal `{"schema_version": "..."}` payload for capability probes
// that don't need the full manifest.
func toolspecCmd(root *kitcli.Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "toolspec",
		Short: "Emit kit's capability manifest (toolspec discovery anchor)",
		Long: "Emit the kit binary's machine-readable capability " +
			"manifest as JSON. Harnesses (Claude Code, Cursor, MCP " +
			"hosts) use this as the discovery anchor for the kit-" +
			"toolspec consumption contract — see " +
			"docs/adopters/integrations/toolspec-harness-guide.md for the " +
			"protocol, version negotiation, and default policy.",
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			"kit/side-effect":    "read",
			"kit/idempotent":     "yes",
			"kit/spec-command":   "true",
			"kit/top-level-verb": "true",
		},
	}
	cmd.Flags().Bool("version", false,
		"Print the schema version only (capability negotiation)")
	cmd.Flags().Bool("include-deprecated", false,
		"Include deprecated commands in the manifest (default: hidden)")

	cmd.RunE = func(c *cobra.Command, _ []string) error {
		versionOnly, _ := c.Flags().GetBool("version")
		includeDeprecated, _ := c.Flags().GetBool("include-deprecated")
		schema := toolspec.NegotiateSchemaVersion(kitToolspecSchemaVersion, os.Getenv(toolspec.SchemaVersionEnv))

		w := c.OutOrStdout()
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")

		if versionOnly {
			return enc.Encode(map[string]string{"schema_version": schema})
		}

		manifest := speccli.EmitManifest(root, schema)
		if includeDeprecated {
			manifest = speccli.BuildManifest(root, schema, true)
		}
		return enc.Encode(manifest)
	}
	cmd.AddCommand(toolspecPolicyCmd())
	return cmd
}

// toolspecPolicyCmd implements `kit toolspec policy [--file <path>]`.
// Prints the resolved permission policy table (default + optional
// overlay) as JSON to stdout. Used by harness authors to verify the
// rules a custom YAML overlay produces before shipping it.
//
// The flag is named --file rather than --policy because the kit
// global --policy flag is already taken by the runtime policy
// engine (cli.WithPolicy). Adopters wiring an MCP host accept their
// own --policy <file> at server startup; this subcommand is just
// the inspection surface for the resolved table.
func toolspecPolicyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Emit the resolved permission policy table (default + optional overlay)",
		Long: "Emit the policy table the kit-toolspec contract uses to " +
			"map (side_effect, network) tuples to auto-allow|prompt|deny. " +
			"Default ships embedded; --file overlays a custom YAML on top.",
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			"kit/side-effect":  "read",
			"kit/idempotent":   "yes",
			"kit/spec-command": "true",
		},
	}
	// `kit toolspec policy` is depth-2 noun-verb under the reserved
	// `toolspec` group; the shape pass accepts it without an extra
	// annotation. No marker needed.
	cmd.Flags().String("file", "",
		"Custom policy YAML overlaid on the embedded default")
	cmd.RunE = func(c *cobra.Command, _ []string) error {
		path, _ := c.Flags().GetString("file")
		tbl, err := policy.LoadOrDefault(path)
		if err != nil {
			return fmt.Errorf("toolspec: load policy: %w", err)
		}
		enc := json.NewEncoder(c.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(tbl)
	}
	return cmd
}
