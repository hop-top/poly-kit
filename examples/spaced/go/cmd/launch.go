package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	charmlog "charm.land/log/v2"
	"github.com/spf13/cobra"
	"hop.top/kit/examples/spaced/go/data"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/completion"
	kitlog "hop.top/kit/go/console/log"
	"hop.top/kit/go/console/wizard"
	"hop.top/kit/go/runtime/bus"
)

// LaunchCmd returns the `launch <mission>` command.
func LaunchCmd(root *cli.Root, b bus.Bus) *cobra.Command {
	var (
		payload     []string
		orbit       string
		dryRun      bool
		outFile     string
		interactive bool
		tags        *cli.SetFlag
	)

	cmd := &cobra.Command{
		Use:   "launch <mission>",
		Short: "Launch a mission",
		Long:  "Initiate launch sequence for a named mission. Use --dry-run to simulate.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			logger := kitlog.New(root.Viper)

			if interactive {
				return runLaunchWizard(ctx, logger)
			}

			if len(args) == 0 {
				return fmt.Errorf("mission name required (or use --interactive)")
			}
			name := args[0]
			_ = b.Publish(ctx, bus.NewEvent(
				"kit.spaced.launch.initiated", "spaced",
				map[string]any{"mission": name},
			))
			logger.Info("resolving mission", "name", name)
			m, ok := data.FindMission(name)
			if !ok {
				logger.Error("mission not found", "name", name)
				return fmt.Errorf("mission not found: %s", name)
			}

			orbitTarget := orbit
			if orbitTarget == "" {
				orbitTarget = m.Orbit
			}

			// Expand comma-lists: --payload cargo,crew,starlink
			var expanded []string
			for _, p := range payload {
				for _, item := range strings.Split(p, ",") {
					item = strings.TrimSpace(item)
					if item != "" {
						expanded = append(expanded, item)
					}
				}
			}
			payload = expanded
			payloadStr := strings.Join(payload, ", ")
			if payloadStr == "" {
				payloadStr = m.Payload
			}

			tagList := tags.Values()
			tagStr := strings.Join(tagList, ", ")
			if tagStr == "" {
				tagStr = "none"
			}

			logger.Info("launch parameters", "vehicle", m.Vehicle, "orbit", orbitTarget, "tags", tagStr)

			if dryRun {
				logger.Warn("dry run mode — no actual launch")
				fmt.Println()
				fmt.Println("  ── DRY RUN ────────────────────────────────────────────────────────")
				fmt.Printf("  Mission  : %s\n", m.Name)
				fmt.Printf("  Vehicle  : %s\n", m.Vehicle)
				fmt.Printf("  Orbit    : %s\n", orbitTarget)
				fmt.Printf("  Payload  : %s\n", payloadStr)
				fmt.Printf("  Tags     : %s\n", tagStr)
				fmt.Println("  Status   : Would have launched. Probably would have been fine.")
				fmt.Println("  ──────────────────────────────────────────────────────────────────")
				fmt.Println()
				fmt.Println("  Dry run complete. No actual rockets were harmed.")
				return nil
			}

			// "Launch" the mission with progress reporting.
			pr := cli.NewProgressReporter(
				root.Streams.Human, root.Streams.IsTTY,
			)
			pr.Emit(cli.ProgressEvent{
				Phase: "preflight", Step: "systems-check",
				Current: 1, Total: 4, Percent: 25,
				Message: "Verifying vehicle systems",
			})
			pr.Emit(cli.ProgressEvent{
				Phase: "preflight", Step: "fuel-pressurize",
				Current: 2, Total: 4, Percent: 50,
				Message: "Fuel pressurization nominal",
			})
			pr.Emit(cli.ProgressEvent{
				Phase: "launch", Step: "ignition",
				Current: 3, Total: 4, Percent: 75,
				Message: "Main engine start",
			})
			pr.Done("Liftoff!")

			w := root.Streams.Data
			fmt.Fprintln(w)
			fmt.Fprintf(w, "  ▶ LAUNCH SEQUENCE INITIATED: %s\n", m.Name)
			fmt.Fprintf(w, "  Vehicle  : %s\n", m.Vehicle)
			fmt.Fprintf(w, "  Orbit    : %s\n", orbitTarget)
			fmt.Fprintf(w, "  Payload  : %s\n", payloadStr)
			fmt.Fprintf(w, "  Tags     : %s\n", tagStr)
			fmt.Fprintf(w, "  T-0      : %s\n", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))
			fmt.Fprintf(w, "  Outcome  : %s\n", m.Outcome)
			fmt.Fprintf(w, "  Note     : %s\n", data.Pick(m.Notes))
			fmt.Fprintln(w)

			report := map[string]any{
				"mission": m.Name,
				"vehicle": m.Vehicle,
				"orbit":   orbitTarget,
				"payload": payload,
				"tags":    tagList,
				"outcome": string(m.Outcome),
				"note":    data.Pick(m.Notes),
				"ts":      time.Now().UTC().Format(time.RFC3339),
			}

			if outFile != "" {
				f, err := os.Create(outFile)
				if err != nil {
					return fmt.Errorf("open output file: %w", err)
				}
				defer f.Close()
				enc := json.NewEncoder(f)
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					return fmt.Errorf("write report: %w", err)
				}
				fmt.Printf("  Report written to %s\n", outFile)
			}

			_ = b.Publish(ctx, bus.NewEvent(
				"kit.spaced.launch.completed", "spaced", report,
			))

			return nil
		},
	}

	cmd.Flags().StringArrayVar(&payload, "payload", nil, "Payload manifest (comma-list: cargo,crew,starlink)")
	cmd.Flags().StringVar(&orbit, "orbit", "", "Target orbit (leo|geo|lunar|helio|tbd)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Simulate launch without executing")
	cmd.Flags().StringVarP(&outFile, "output", "o", "", "Write JSON report to file")
	cmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "Run interactive launch wizard")
	tags = cli.RegisterSetFlag(cmd, "tag", "Launch tags", cli.FlagDisplayPrefix)

	// Dynamic completions.
	completion.BindFlag(cmd, "orbit", completion.StaticValues(
		"leo", "geo", "lunar", "helio", "tbd",
	))
	completion.BindArgs(cmd, completion.Func(
		func(_ context.Context, prefix string) ([]completion.Item, error) {
			matches := data.SearchMissions(prefix)
			items := make([]completion.Item, len(matches))
			for i, m := range matches {
				items[i] = completion.Item{
					Value:       m.ID,
					Description: m.Name + " (" + m.Vehicle + ")",
				}
			}
			return items, nil
		},
	))

	return cmd
}

// runLaunchWizard demonstrates the wizard API with a headless driver.
func runLaunchWizard(_ context.Context, logger *charmlog.Logger) error {
	orbitOpts := []wizard.Option{
		{Value: "leo", Label: "LEO", Description: "Low Earth Orbit"},
		{Value: "geo", Label: "GEO", Description: "Geostationary"},
		{Value: "lunar", Label: "Lunar", Description: "Trans-lunar injection"},
		{Value: "helio", Label: "Helio", Description: "Heliocentric"},
		{Value: "tbd", Label: "TBD", Description: "To be determined"},
	}

	w, err := wizard.New(
		wizard.TextInput("mission", "Mission name").WithRequired(),
		wizard.Select("orbit", "Target orbit", orbitOpts),
		wizard.TextInput("payload", "Payload manifest"),
		wizard.Confirm("dry_run", "Dry run?").WithDefault(false),
		wizard.Summary("Launch parameters"),
	)
	if err != nil {
		return fmt.Errorf("wizard setup: %w", err)
	}

	// Headless driver: supply defaults for demonstration.
	defaults := map[string]any{
		"mission": "Starlink-42",
		"orbit":   "leo",
		"payload": "60x Starlink v2 Mini",
		"dry_run": true,
	}

	logger.Info("wizard: advancing through steps with defaults")
	for !w.Done() {
		s := w.Current()
		if s == nil {
			break
		}
		val, ok := defaults[s.Key]
		if !ok {
			// Summary step — advance with nil.
			_, _ = w.Advance(nil)
			continue
		}
		if _, err := w.Advance(val); err != nil {
			return fmt.Errorf("wizard step %q: %w", s.Key, err)
		}
	}

	results := w.Results()
	fmt.Println()
	fmt.Println("  ── WIZARD RESULTS ─────────────────────────────────────────────")
	for k, v := range results {
		fmt.Printf("  %-12s: %v\n", k, v)
	}
	fmt.Println("  ───────────────────────────────────────────────────────────────")
	fmt.Println()
	fmt.Println("  Wizard complete. In a real TUI, these would drive the launch.")
	return nil
}
