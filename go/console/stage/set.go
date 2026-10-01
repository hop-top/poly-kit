package stage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	kitcli "hop.top/kit/go/console/cli"
	"hop.top/kit/go/core/stage"
)

func setCmd(cfg Config) *cobra.Command {
	var (
		reason  string
		until   string
		allow   []string
		deny    []string
		actor   string
		scope   string
		confirm bool
	)
	cmd := &cobra.Command{
		Use:   "set <mode>",
		Short: "Propose + persist a stage transition",
		Long: `Set a scope's stage. The transition is first proposed (synchronously) so
runtime/policy may veto, then persisted. When --until is supplied, the
stage auto-expires at that time (Tick emits expired events).

--until accepts an RFC3339 timestamp ("2026-12-31T00:00:00Z") or a
relative duration ("720h", "30d"). Past timestamps are rejected.

A non-active stage MUST carry --reason for audit clarity.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := stage.Stage(strings.TrimSpace(args[0]))
			if !mode.Valid() {
				return fmt.Errorf("stage: invalid mode %q (want one of: %s)",
					args[0], strings.Join(stageStrings(), ", "))
			}
			if scope == "" && cfg.ProjectResolver != nil {
				scope = cfg.ProjectResolver()
			}
			if scope == "" {
				return errors.New("stage: --scope required (no project resolver configured)")
			}
			if mode != stage.StageActive && strings.TrimSpace(reason) == "" {
				return fmt.Errorf("stage: --reason required for non-active stage %q", mode)
			}

			target := stage.State{
				Stage:  mode,
				Reason: reason,
				Actor:  actor,
				Allow:  allow,
				Deny:   deny,
			}
			if until != "" {
				t, err := parseUntil(until, time.Now())
				if err != nil {
					return fmt.Errorf("stage: --until: %w", err)
				}
				target.Until = &t
			}

			if kitcli.IsDryRun(cmd) {
				// Validated as for the real set; nothing is proposed,
				// so no policy veto is consulted and no event emitted.
				return kitcli.RenderPlan(cmd, kitcli.Plan{
					Args: map[string]any{"scope": scope, "stage": string(mode), "reason": reason, "until": until},
					Effects: []kitcli.Effect{{Kind: "update", Target: "stage:" + scope,
						Reversible: true, Detail: "set stage " + string(mode)}},
					Warnings: []string{"the propose step runs on the real set: runtime policy may still veto it"},
				})
			}

			mgr := stage.NewManager(managerOpts(cfg)...)
			ctx := context.Background()

			if !confirm {
				if err := mgr.Propose(ctx, scope, target); err != nil {
					return err
				}
			}
			if err := mgr.Set(ctx, scope, target); err != nil {
				return err
			}
			cmd.Printf("ok: %s → %s\n", scope, mode)
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for the transition (required for non-active)")
	cmd.Flags().StringVar(&until, "until", "", "Auto-expiry as RFC3339 timestamp or duration (e.g. 720h)")
	cmd.Flags().StringSliceVar(&allow, "allow", nil, "Advisory CEL allow predicate (repeatable)")
	cmd.Flags().StringSliceVar(&deny, "deny", nil, "Advisory CEL deny predicate (repeatable)")
	cmd.Flags().StringVar(&actor, "actor", "", "Identity of the principal making the change")
	cmd.Flags().StringVar(&scope, "scope", "", "Scope to set (defaults to ProjectResolver)")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Skip the propose pre-event (admin override)")
	// Persists the scope's stage and emits stage events.
	kitcli.SetSideEffect(cmd, kitcli.SideEffectWrite)
	return cmd
}

// parseUntil accepts an RFC3339 timestamp or a duration relative to now.
// Returns ErrPast when the parsed timestamp is <= now.
func parseUntil(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		if !t.After(now) {
			return time.Time{}, fmt.Errorf("until %q is not in the future", raw)
		}
		return t, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("not an RFC3339 timestamp or duration: %s", raw)
	}
	if d <= 0 {
		return time.Time{}, fmt.Errorf("duration %q is not positive", raw)
	}
	return now.Add(d), nil
}

// stageStrings returns the AllStages list as a string slice, used in
// error messages.
func stageStrings() []string {
	out := make([]string, 0, 6)
	for _, s := range stage.AllStages() {
		out = append(out, string(s))
	}
	return out
}
