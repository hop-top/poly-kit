package stage

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/core/stage"
)

// whyRow is the table/json/yaml shape for `stage why`.
type whyRow struct {
	Rule    string `table:"RULE"    json:"rule"    yaml:"rule"`
	Message string `table:"MESSAGE" json:"message" yaml:"message"`
}

// whyOutput is the json/yaml top-level shape — surfaces the active
// stage alongside the matching rules so agents can introspect both
// without two round-trips.
type whyOutput struct {
	Scope string   `json:"scope" yaml:"scope"`
	Stage string   `json:"stage" yaml:"stage"`
	Rules []whyRow `json:"rules" yaml:"rules"`
}

func whyCmd(cfg Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "why [scope]",
		Short: "List the active stage rules + recent violations",
		Long: `Print the rules currently in effect for the named scope. Output mirrors
the default policy/stage.yaml ruleset (rule name + message); adopters
that ship a custom ruleset see their own rules.

Recent kit.runtime.stage.violated events would also show here, but
that requires a bus log subscriber — left as a follow-up; the
subcommand intentionally only prints the rule set today so it works
without a bus connection.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope := resolveScope(args, cfg)
			if scope == "" {
				return fmt.Errorf("stage: scope required (no project resolver configured)")
			}
			st, err := stage.Read(scope)
			if err != nil {
				return err
			}
			rules := defaultRules(st.Stage)
			out := whyOutput{
				Scope: scope,
				Stage: string(st.Stage),
				Rules: rules,
			}
			format := viper.GetString("format")
			if format == "" {
				format = output.Table
			}
			if format == output.Table {
				cmd.Printf("scope: %s  stage: %s\n", scope, st.Stage)
				return output.Render(cmd.OutOrStdout(), output.Table, rules)
			}
			return output.Render(cmd.OutOrStdout(), format, out)
		},
	}
	return cmd
}

// defaultRules returns the rule names + messages from policy/stage.yaml
// that apply to the given stage. Synced-by-name with the YAML so a
// rule rename there must update this too.
func defaultRules(s stage.Stage) []whyRow {
	switch s {
	case stage.StageActive:
		return []whyRow{}
	case stage.StagePublicFeedback:
		return []whyRow{
			{"public-feedback-allows-feedback-only-tracks",
				"scope is in public_feedback; only feedback-typed tracks may be created"},
		}
	case stage.StageFeatureFreeze:
		return []whyRow{
			{"feature-freeze-blocks-track-create",
				"scope is in feature_freeze; new tracks not allowed (fix/chore tasks ok)"},
			{"feature-freeze-blocks-feature-tasks",
				"scope is in feature_freeze; only fix/chore/docs tasks allowed"},
		}
	case stage.StageMaintenance:
		return []whyRow{
			{"maintenance-blocks-track-create",
				"scope is in maintenance; no new tracks (fix/chore/docs tasks only)"},
		}
	case stage.StageSunset:
		return []whyRow{
			{"sunset-blocks-creates",
				"scope is in sunset; new entities not allowed (updates/deletes ok)"},
		}
	case stage.StageArchived:
		return []whyRow{
			{"archived-blocks-all-mutations",
				"scope is archived; mutations not allowed"},
		}
	}
	return nil
}
