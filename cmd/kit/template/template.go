// Package template implements the `kit template` command group:
// `list` (catalog of built-in templates) and `show <name>` (manifest
// detail: variables, hooks, file rules). Both subcommands honor
// --json for machine-readable output.
//
// Spec: ops/docs/superpowers/specs/2026-04-26-kit-init-design.md §17.
package template

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	tmpl "hop.top/kit/internal/template"
)

// GroupCmd returns the `kit template` group with `list` and `show`
// subcommands attached.
func GroupCmd(root *cli.Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template",
		Short: "Inspect and manage kit project templates",
	}
	cmd.AddCommand(listCmd(root), showCmd(root))
	return cmd
}

// row is the structured form emitted by `list` (also serialized to JSON).
type row struct {
	Name        string `json:"name"        yaml:"name"        table:"Name,priority=9"`
	Description string `json:"description" yaml:"description" table:"Description,priority=8"`
	KitVersion  string `json:"kit_version" yaml:"kit_version" table:"Kit_Version,priority=7"`
}

func listCmd(_ *cli.Root) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List built-in templates",
		Long: "List every built-in template available to `kit init` with " +
			"its description and the kit version it targets.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			names, err := tmpl.Available()
			if err != nil {
				return fmt.Errorf("list templates: %w", err)
			}
			bfs, err := tmpl.BuiltIn()
			if err != nil {
				return fmt.Errorf("load built-ins: %w", err)
			}

			rows := make([]row, 0, len(names))
			for _, n := range names {
				m, perr := parseManifestFromFS(bfs, n)
				if perr != nil {
					rows = append(rows, row{Name: n, Description: "(invalid manifest)"})
					continue
				}
				rows = append(rows, row{Name: n, Description: m.Description, KitVersion: m.KitVersion})
			}

			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
			}
			return output.Render(cmd.OutOrStdout(), output.Table, rows)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cli.SetSideEffect(cmd, cli.SideEffectRead)
	cli.SetIdempotency(cmd, cli.IdempotencyYes)
	return cmd
}

func showCmd(_ *cli.Root) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show details of a template (variables, hooks, file rules)",
		Long: "Resolve the named template (built-in, @org/name, git URL, " +
			"or path) and print its manifest: declared variables with " +
			"prompts/defaults, hook scripts, and file-inclusion rules.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec := args[0]
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			registry := tmpl.NewRegistry("", os.TempDir())
			srcFS, err := registry.Resolve(ctx, spec)
			if err != nil {
				return err
			}

			m, err := parseManifestFromFS(srcFS, "")
			if err != nil {
				return err
			}

			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(m)
			}
			return renderManifest(cmd.OutOrStdout(), m)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cli.SetSideEffect(cmd, cli.SideEffectRead)
	cli.SetIdempotency(cmd, cli.IdempotencyYes)
	return cmd
}

// parseManifestFromFS reads kit-template.yaml from srcFS (rooted at
// subdir when non-empty), spills it to a temp file, and runs the
// template.Parse loader. The temp-file dance is needed because Parse
// only accepts a filesystem path.
func parseManifestFromFS(srcFS fs.FS, subdir string) (tmpl.Manifest, error) {
	var m tmpl.Manifest
	target := srcFS
	if subdir != "" {
		sub, err := fs.Sub(srcFS, subdir)
		if err != nil {
			return m, fmt.Errorf("sub fs %q: %w", subdir, err)
		}
		target = sub
	}
	data, err := fs.ReadFile(target, "kit-template.yaml")
	if err != nil {
		return m, fmt.Errorf("read manifest: %w", err)
	}
	tmpDir, err := os.MkdirTemp("", "kit-tmpl-*")
	if err != nil {
		return m, fmt.Errorf("mkdtemp: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	tmpFile := filepath.Join(tmpDir, "kit-template.yaml")
	if werr := os.WriteFile(tmpFile, data, 0o644); werr != nil {
		return m, fmt.Errorf("write temp manifest: %w", werr)
	}
	return tmpl.Parse(tmpFile)
}

// variableRow is the rendered shape of one row in `template show`'s
// Variables section.
type variableRow struct {
	Name     string `json:"name"     yaml:"name"     table:"Name,priority=9"`
	Prompt   string `json:"prompt"   yaml:"prompt"   table:"Prompt,priority=8"`
	Required bool   `json:"required" yaml:"required" table:"Required,priority=7"`
	Default  string `json:"default"  yaml:"default"  table:"Default,priority=6"`
}

// renderManifest writes a human-readable view of m to w.
func renderManifest(w io.Writer, m tmpl.Manifest) error {
	fmt.Fprintf(w, "Name:        %s\n", m.Name)
	fmt.Fprintf(w, "Description: %s\n", m.Description)
	fmt.Fprintf(w, "KitVersion:  %s\n", m.KitVersion)

	if len(m.Variables) > 0 {
		fmt.Fprintln(w, "\nVariables:")
		rows := make([]variableRow, 0, len(m.Variables))
		for _, v := range m.Variables {
			rows = append(rows, variableRow{
				Name:     v.Name,
				Prompt:   v.Prompt,
				Required: v.Required,
				Default:  v.Default,
			})
		}
		if err := output.Render(w, output.Table, rows); err != nil {
			return err
		}
	}

	if len(m.Hooks.PreRender)+len(m.Hooks.PostRender)+len(m.Hooks.PostInit)+len(m.Hooks.PostPush) > 0 {
		fmt.Fprintln(w, "\nHooks:")
		fmt.Fprintf(w, "  pre_render:  %v\n", m.Hooks.PreRender)
		fmt.Fprintf(w, "  post_render: %v\n", m.Hooks.PostRender)
		fmt.Fprintf(w, "  post_init:   %v\n", m.Hooks.PostInit)
		fmt.Fprintf(w, "  post_push:   %v\n", m.Hooks.PostPush)
	}

	if len(m.Files.Exclude)+len(m.Files.Binary) > 0 {
		fmt.Fprintln(w, "\nFile rules:")
		if len(m.Files.Exclude) > 0 {
			fmt.Fprintf(w, "  exclude: %v\n", m.Files.Exclude)
		}
		if len(m.Files.Binary) > 0 {
			fmt.Fprintf(w, "  binary:  %v\n", m.Files.Binary)
		}
	}
	return nil
}
