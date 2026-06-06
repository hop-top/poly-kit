package uri

import (
	"github.com/spf13/cobra"

	"hop.top/cite/scheme"
)

func parseCmd(cfg Config) *cobra.Command {
	var flags parseFlags
	cmd := &cobra.Command{
		Use:   "parse <uri>",
		Short: "Parse a custom URI",
		Long:  "Parse a custom URI into scheme, namespace, id, query, fragment, original, and action fields.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			policy, err := resolvePolicy(cfg.Policy, flags.PolicyFile)
			if err != nil {
				return err
			}
			uri, err := scheme.ParseWithPolicyOptions(args[0], policy, parseOptions(flags)...)
			if err != nil {
				return err
			}
			return render(cmd.OutOrStdout(), flags.Format, uriRowFromURI(uri))
		},
	}
	installParseFlags(cmd, &flags)
	annotateRead(cmd)
	return cmd
}

func resolveCmd(cfg Config) *cobra.Command {
	var flags parseFlags
	cmd := &cobra.Command{
		Use:   "resolve <uri>",
		Short: "Resolve a custom URI action route",
		Long:  "Parse a custom URI and resolve its action query to a command plan without executing it.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			policy, err := resolvePolicy(cfg.Policy, flags.PolicyFile)
			if err != nil {
				return err
			}
			uri, err := scheme.ParseWithPolicyOptions(args[0], policy, parseOptions(flags)...)
			if err != nil {
				return err
			}
			plan, err := policy.ResolveAction(uri)
			if err != nil {
				return err
			}
			return render(cmd.OutOrStdout(), flags.Format, actionRowFromPlan(plan))
		},
	}
	installParseFlags(cmd, &flags)
	annotateRead(cmd)
	return cmd
}

func installParseFlags(cmd *cobra.Command, f *parseFlags) {
	cmd.Flags().StringVar(&f.PolicyFile, "policy", "", "Path to URI policy JSON/YAML file")
	cmd.Flags().BoolVar(&f.Strict, "strict", false, "Disable fuzzy vanity alias matching")
	cmd.Flags().BoolVar(&f.JSONAmbiguity, "json-ambiguity", false, "Return ambiguous fuzzy vanity matches as JSON in the error message")
	cmd.Flags().StringVar(&f.Format, "format", formatTable, "Output format: table|json|yaml")
}

func parseOptions(f parseFlags) []scheme.ParseOption {
	var opts []scheme.ParseOption
	if f.Strict {
		opts = append(opts, scheme.WithStrict())
	}
	if f.JSONAmbiguity {
		opts = append(opts, scheme.WithJSONAmbiguity())
	}
	return opts
}

func uriRowFromURI(uri *scheme.URI) uriRow {
	if uri == nil {
		return uriRow{}
	}
	return uriRow{
		Scheme:    uri.Scheme,
		Namespace: uri.Namespace,
		ID:        uri.ID,
		Query:     uri.Query,
		Fragment:  uri.Fragment,
		Original:  uri.Original,
		Action:    uri.Action,
	}
}

func actionRowFromPlan(plan *scheme.ResolvedAction) actionRow {
	if plan == nil {
		return actionRow{}
	}
	return actionRow{Action: plan.Action, Command: plan.Command, Args: plan.Args}
}
