package upgrade

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	kitcli "hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// MigrateCommand returns a cobra subcommand tree for schema migration management.
//
//	<tool> migrate status     — show schema versions + pending count
//	<tool> migrate run        — manually trigger migrations
//	<tool> migrate rollback   — restore from latest backup (manual mode only)
//	<tool> migrate history    — show applied migrations
func MigrateCommand(m *Migrator, v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Manage schema migrations",
		Long: `Manage versioned schema migrations for ` + m.Tool() + `.

Shows status, runs pending migrations, restores from backups, and
displays migration history.`,
	}

	output.RegisterFlags(cmd, v)

	cmd.AddCommand(
		migrateStatusCmd(m, v),
		migrateRunCmd(m),
		migrateRollbackCmd(m),
		migrateHistoryCmd(m, v),
	)
	return cmd
}

func migrateStatusCmd(m *Migrator, v *viper.Viper) *cobra.Command {
	return withSideEffect(kitcli.SideEffectRead, &cobra.Command{
		Use:   "status",
		Short: "Show schema versions and pending migration count",
		RunE: func(cmd *cobra.Command, args []string) error {
			statuses := m.Status()
			if len(statuses) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No schema drivers registered.")
				return nil
			}
			return output.Dispatch(cmd, v, statuses)
		},
	})
}

func migrateRunCmd(m *Migrator) *cobra.Command {
	// Backs up, then migrates each schema in place.
	return withSideEffect(kitcli.SideEffectWriteLocal, &cobra.Command{
		Use:   "run",
		Short: "Run pending migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			if kitcli.IsDryRun(cmd) {
				return planRun(cmd, m)
			}
			if err := m.Run(cmd.Context()); err != nil {
				return err
			}
			applied := m.History()
			if len(applied) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Already up to date.")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Applied %d migration(s).\n", len(applied))
			return nil
		},
	})
}

func migrateRollbackCmd(m *Migrator) *cobra.Command {
	// Overwrites each schema's current state with its latest backup,
	// so it asks first.
	return withSideEffect(kitcli.SideEffectDestructiveLocal, &cobra.Command{
		Use:   "rollback",
		Short: "Restore from latest backup (manual mode only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if kitcli.IsDryRun(cmd) {
				return planRollback(cmd, m)
			}
			if err := m.RollbackLatest(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Rollback complete.")
			return nil
		},
	})
}

func migrateHistoryCmd(m *Migrator, v *viper.Viper) *cobra.Command {
	return withSideEffect(kitcli.SideEffectRead, &cobra.Command{
		Use:   "history",
		Short: "Show applied migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			hist := m.History()
			if len(hist) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No migrations applied this session.")
				return nil
			}
			return output.Dispatch(cmd, v, hist)
		},
	})
}

func withSideEffect(se kitcli.SideEffect, cmd *cobra.Command) *cobra.Command {
	kitcli.SetSideEffect(cmd, se)
	return cmd
}

// planRun is migrate run under --dry-run: each schema with pending
// migrations becomes an effect; nothing is backed up or applied.
func planRun(cmd *cobra.Command, m *Migrator) error {
	plan := kitcli.Plan{Effects: []kitcli.Effect{}}
	for _, st := range m.Status() {
		if st.Err != nil {
			return fmt.Errorf("migrate %s: %w", st.Schema, st.Err)
		}
		if st.Pending == 0 {
			continue
		}
		plan.Effects = append(plan.Effects, kitcli.Effect{Kind: "update",
			Target: "schema:" + st.Schema, Reversible: true,
			Detail: fmt.Sprintf("back up, then apply %d migration(s) %s -> %s", st.Pending, st.Current, st.Target)})
	}
	return kitcli.RenderPlan(cmd, plan)
}

// planRollback is migrate rollback under --dry-run: it refuses where
// the rollback would, and names the backup each schema would be
// restored from without restoring it.
func planRollback(cmd *cobra.Command, m *Migrator) error {
	if m.autoRollback {
		return fmt.Errorf("rollback: only available in manual rollback mode")
	}
	plan := kitcli.Plan{Effects: []kitcli.Effect{}}
	for _, d := range m.drivers {
		latest, err := newBackupOrchestrator(m.tool, d.Name(), m.retention).latestBackup()
		if err != nil {
			return fmt.Errorf("rollback %s: %w", d.Name(), err)
		}
		plan.Effects = append(plan.Effects, kitcli.Effect{Kind: "update",
			Target: "schema:" + d.Name(), Reversible: false,
			Detail: "restore from " + latest})
	}
	return kitcli.RenderPlan(cmd, plan)
}
