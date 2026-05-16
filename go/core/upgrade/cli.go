package upgrade

import (
	"context"
	"fmt"
	"io"
	"os"
)

// CLIOptions configure the interactive CLI upgrade check.
type CLIOptions struct {
	AutoUpgrade bool
	Quiet       bool
	Out         io.Writer
}

// RunCLI checks for updates and optionally upgrades, printing human-readable
// messages to opts.Out.
func RunCLI(ctx context.Context, c *Checker, opts CLIOptions) error {
	out := opts.Out
	if out == nil {
		out = os.Stderr
	}

	r := c.Check(ctx)
	if r.Err != nil {
		return fmt.Errorf("upgrade check: %w", r.Err)
	}

	if !r.UpdateAvail {
		if !opts.Quiet {
			fmt.Fprintf(out, "%s is up to date (%s)\n", c.cfg.BinaryName, r.Current)
		}
		return nil
	}

	fmt.Fprintf(out, "Update available: %s → %s\n", r.Current, r.Latest)
	if r.Notes != "" {
		fmt.Fprintln(out, r.Notes)
	}

	if opts.AutoUpgrade {
		fmt.Fprintf(out, "Installing %s %s…\n", c.cfg.BinaryName, r.Latest)
		if err := c.Upgrade(ctx); err != nil {
			return err
		}
		fmt.Fprintf(out, "Upgraded to %s. Restart to use the new version.\n", r.Latest)
		return nil
	}

	fmt.Fprintf(out, "Upgrade now? [y/N/snooze]: ")
	var ans string
	if _, err := fmt.Fscan(os.Stdin, &ans); err != nil {
		ans = "n"
	}
	switch ans {
	case "y", "Y", "yes":
		fmt.Fprintf(out, "Installing %s %s…\n", c.cfg.BinaryName, r.Latest)
		if err := c.Upgrade(ctx); err != nil {
			return err
		}
		fmt.Fprintf(out, "Upgraded to %s. Restart to use the new version.\n", r.Latest)
	case "snooze", "s":
		if err := c.Snooze(); err != nil {
			return err
		}
		fmt.Fprintf(out, "Snoozed for %s.\n", c.cfg.SnoozeDuration)
	default:
		fmt.Fprintln(out, "Skipped.")
	}
	return nil
}

// NotifyIfAvailable prints a one-liner banner when an update is available and
// the user has not snoozed. Uses a single Check call to avoid redundant
// network requests.
func NotifyIfAvailable(ctx context.Context, c *Checker, out io.Writer) {
	if out == nil {
		out = os.Stderr
	}
	r := c.Check(ctx)
	if r.Err != nil || !r.UpdateAvail {
		return
	}
	snoozed, _ := isSnoozed(c.cfg.StateDir, c.cfg.BinaryName)
	if snoozed {
		return
	}
	fmt.Fprintf(out, "[%s] update available: %s → %s  (run `%s upgrade` to install)\n",
		c.cfg.BinaryName, r.Current, r.Latest, c.cfg.BinaryName)
}
