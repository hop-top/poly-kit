// Package uri provides kit-shipped custom URI commands for kit-built CLIs.
package uri

import (
	"strings"

	"github.com/spf13/cobra"
)

// Command returns the top-level URI command with kit annotations attached.
func Command(cfg Config) *cobra.Command {
	name := strings.TrimSpace(cfg.CommandName)
	if name == "" {
		name = "uri"
	}
	disabled := disabledSet(cfg.DisabledCommands)
	cmd := &cobra.Command{
		Use:   name,
		Short: "Inspect and generate custom URI scheme metadata",
		Long: `Inspect custom URI scheme values, resolve action routes, emit completion
candidates, and generate OS handler metadata for kit-powered applications.`,
		Args: cobra.NoArgs,
	}
	setHierarchical(cmd)

	if !disabled["parse"] {
		cmd.AddCommand(parseCmd(cfg))
	}
	if !disabled["resolve"] {
		cmd.AddCommand(resolveCmd(cfg))
	}
	if !disabled["complete"] {
		cmd.AddCommand(completeCmd(cfg))
	}
	if !disabled["handler.id"] || !disabled["handler.generate"] {
		handler := &cobra.Command{
			Use:   "handler",
			Short: "Inspect and generate OS URI handler metadata",
			Long:  "Inspect handler identities and generate OS URI handler snippets.",
			Args:  cobra.NoArgs,
		}
		setHierarchical(handler)
		if !disabled["handler.id"] {
			handler.AddCommand(handlerIDCmd(cfg.Handler))
		}
		if !disabled["handler.generate"] {
			handler.AddCommand(handlerGenerateCmd(cfg.Handler))
		}
		cmd.AddCommand(handler)
	}
	if !disabled["completion"] {
		cmd.AddCommand(shellCompletionCmd())
	}
	return cmd
}

// Register attaches the URI command tree to parent unless all leaves are disabled.
func Register(parent *cobra.Command, cfg Config) {
	if parent == nil {
		return
	}
	parent.AddCommand(Command(cfg))
}

func disabledSet(keys []string) map[string]bool {
	out := make(map[string]bool, len(keys))
	for _, key := range keys {
		out[strings.TrimSpace(key)] = true
	}
	return out
}

func annotateRead(cmd *cobra.Command) {
	setSideEffect(cmd, "read")
	setIdempotency(cmd, "yes")
}

const (
	kitHierarchical = "kit/hierarchical"
	kitSideEffect   = "kit/side-effect"
	kitIdempotent   = "kit/idempotent"
)

func setHierarchical(cmd *cobra.Command) {
	setAnnotation(cmd, kitHierarchical, "true")
}

func setSideEffect(cmd *cobra.Command, value string) {
	setAnnotation(cmd, kitSideEffect, value)
}

func setIdempotency(cmd *cobra.Command, value string) {
	setAnnotation(cmd, kitIdempotent, value)
}

func setAnnotation(cmd *cobra.Command, key, value string) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[key] = value
}
