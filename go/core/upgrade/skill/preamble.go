// Package skill generates upgrade preamble fragments for AI agent skill files.
package skill

import (
	"fmt"
	"strings"
)

// SnoozeLevel controls the auto-upgrade aggressiveness for agents.
type SnoozeLevel int

const (
	SnoozeNever SnoozeLevel = iota
	SnoozeOnce
	SnoozeAlways
)

// PreambleOptions configure preamble generation.
type PreambleOptions struct {
	BinaryName      string
	UpgradeCommand  string
	Snooze          SnoozeLevel
	WhatsNewSection string
}

// Generate returns a markdown preamble fragment for embedding in a skill file.
func Generate(opts PreambleOptions) string {
	if opts.UpgradeCommand == "" {
		opts.UpgradeCommand = opts.BinaryName + " upgrade"
	}

	var b strings.Builder
	b.WriteString("## Upgrade Preamble\n\n")
	fmt.Fprintf(&b, "Before running any task, ensure `%s` is up to date.\n\n", opts.BinaryName)

	switch opts.Snooze {
	case SnoozeNever:
		fmt.Fprintf(&b, "1. Run `%s --auto` silently.\n", opts.UpgradeCommand)
		b.WriteString("2. If upgraded, inform the user to restart before continuing.\n")
	case SnoozeOnce:
		fmt.Fprintf(&b, "1. Run `%s` — accept the upgrade if offered, snooze once if user declines.\n",
			opts.UpgradeCommand)
		b.WriteString("2. Continue with the task regardless of upgrade outcome.\n")
	case SnoozeAlways:
		fmt.Fprintf(&b, "1. Check: `%s --check` and report if an update is available.\n",
			opts.UpgradeCommand)
		b.WriteString("2. Do not install automatically; let the user decide.\n")
	}

	if opts.WhatsNewSection != "" {
		b.WriteString("\n### What's New\n\n")
		b.WriteString(opts.WhatsNewSection)
		b.WriteString("\n")
	}

	return b.String()
}

// InlineFlow returns a compact agent-readable upgrade flow for embedding
// inside running prompts.
func InlineFlow(binaryName, upgradeCmd string, snooze SnoozeLevel) string {
	if upgradeCmd == "" {
		upgradeCmd = binaryName + " upgrade --auto"
	}
	switch snooze {
	case SnoozeNever:
		return fmt.Sprintf(
			"[upgrade] run `%s`; if exit 0 and binary changed, notify user to restart.", upgradeCmd,
		)
	case SnoozeOnce:
		return fmt.Sprintf(
			"[upgrade] run `%s`; on decline, snooze; continue task.", upgradeCmd,
		)
	default:
		return fmt.Sprintf(
			"[upgrade] check `%s --check`; report result; do not install.", upgradeCmd,
		)
	}
}
