package upgrade

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

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
	return &cobra.Command{
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
	}
}

func migrateRunCmd(m *Migrator) *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run pending migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
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
	}
}

func migrateRollbackCmd(m *Migrator) *cobra.Command {
	return &cobra.Command{
		Use:   "rollback",
		Short: "Restore from latest backup (manual mode only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := m.RollbackLatest(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Rollback complete.")
			return nil
		},
	}
}

func migrateHistoryCmd(m *Migrator, v *viper.Viper) *cobra.Command {
	return &cobra.Command{
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
	}
}
