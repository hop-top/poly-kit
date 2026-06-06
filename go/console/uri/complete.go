package uri

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"hop.top/cite/scheme"
)

func completeCmd(cfg Config) *cobra.Command {
	var f completeFlags
	cmd := &cobra.Command{
		Use:   "complete",
		Short: "Print URI completion candidates",
		Long:  "Print completion candidates from configured URI vanity aliases or registered type completers.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			policy, err := resolvePolicy(cfg.Policy, f.PolicyFile)
			if err != nil {
				return err
			}
			if f.Input != "" {
				rows := vanityRows(policy.VanityCandidates(f.Input))
				return renderCompletion(cmd.OutOrStdout(), f.Format, rows)
			}
			if f.Type == "" {
				return fmt.Errorf("uri complete: --type or --input is required")
			}
			registry := registryFromConfig(policy, cfg.Types)
			suggestions, err := registry.Complete(cmd.Context(), f.Type, f.Prefix)
			if err != nil {
				return err
			}
			return renderCompletion(cmd.OutOrStdout(), f.Format, completionRows(f.Type, suggestions))
		},
	}
	cmd.Flags().StringVar(&f.Type, "type", "", "URI type to complete")
	cmd.Flags().StringVar(&f.Prefix, "prefix", "", "Prefix passed to the registered completer")
	cmd.Flags().StringVar(&f.Input, "input", "", "Partial URI used for vanity alias completion")
	cmd.Flags().StringVar(&f.PolicyFile, "policy", "", "Path to URI policy JSON/YAML file")
	cmd.Flags().StringVar(&f.Format, "format", formatLines, "Output format: lines|json|yaml|table")
	annotateRead(cmd)
	return cmd
}

func registryFromConfig(policy scheme.Policy, regs []scheme.TypeRegistration) *scheme.Registry {
	registry := scheme.NewRegistryWithPolicy(policy)
	seen := map[string]bool{}
	for _, reg := range regs {
		if reg.Name == "" || seen[reg.Name] {
			continue
		}
		_ = registry.Register(reg)
		seen[reg.Name] = true
	}
	for name := range policy.SchemeNamespaceSegments {
		if seen[name] {
			continue
		}
		_ = registry.Register(scheme.TypeRegistration{Name: name})
		seen[name] = true
	}
	if len(seen) == 0 {
		for name := range scheme.DefaultPolicy.SchemeNamespaceSegments {
			_ = registry.Register(scheme.TypeRegistration{Name: name})
		}
	}
	return registry
}

func completionRows(typeName string, values []string) []completionRow {
	rows := make([]completionRow, 0, len(values))
	for _, value := range values {
		rows = append(rows, completionRow{Type: typeName, Value: value})
	}
	return rows
}

func vanityRows(candidates []scheme.VanityCandidate) []vanityRow {
	rows := make([]vanityRow, 0, len(candidates))
	for _, candidate := range candidates {
		rows = append(rows, vanityRow{From: candidate.From, To: candidate.To, Distance: candidate.Distance})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Distance != rows[j].Distance {
			return rows[i].Distance < rows[j].Distance
		}
		return rows[i].From < rows[j].From
	})
	return rows
}
