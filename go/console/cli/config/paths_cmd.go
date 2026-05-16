package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	kitcli "hop.top/kit/go/console/cli"
)

// formatText is the default human-readable format -- one path per line
// for `paths`, a single line for `path`. Mirrors `git config
// --list --show-origin` output style.
const (
	formatText = "text"
	formatJSON = "json"
	formatYAML = "yaml"
)

// errNoConfig is the sentinel returned by `path` when no rung in the
// resolution chain exists. The root command catches it for exit
// code 1 by returning the error from RunE; host bridges that
// translate cobra errors to exit codes pick it up automatically.
var errNoConfig = errors.New("no config file found in resolution chain")

// pathCommand prints the highest-precedence existing config file.
// Exits non-zero when no rung exists.
func pathCommand(toolName string, o *options) *cobra.Command {
	var (
		format string
		from   string
	)
	cmd := &cobra.Command{
		Use:   "path",
		Short: "Print the highest-precedence existing " + toolName + " config file",
		Long: `Print the single config file that ` + toolName + ` would load.

The resolution chain is walked highest-precedence first (cwd, project,
workspace, user, system, default). The first rung whose file exists is
printed. If no rung exists, the command prints "no config file found
in resolution chain" to stderr and exits 1.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateFormat(format); err != nil {
				return err
			}
			cwd, err := resolveCwd(from)
			if err != nil {
				return err
			}
			chain := o.resolver(cwd)
			top, ok := highestExisting(chain)
			if !ok {
				fmt.Fprintln(cmd.ErrOrStderr(), errNoConfig.Error())
				return errNoConfig
			}
			return renderOne(cmd.OutOrStdout(), top, format)
		},
	}
	cmd.Flags().StringVar(&format, "format", formatText, "Output format: text|json|yaml")
	cmd.Flags().StringVar(&from, "from", "", "Resolve from this directory instead of os.Getwd()")
	kitcli.SetSideEffect(cmd, kitcli.SideEffectRead)
	kitcli.SetIdempotency(cmd, kitcli.IdempotencyYes)
	return cmd
}

// pathsCommand prints the full precedence chain.
func pathsCommand(toolName string, o *options) *cobra.Command {
	var (
		format string
		from   string
	)
	cmd := &cobra.Command{
		Use:   "paths",
		Short: "Print the full " + toolName + " config resolution chain",
		Long: `Print every rung of the ` + toolName + ` config resolution
chain, highest-precedence first. Each entry includes the source label
(cwd|project|workspace|user|system|default), scope, file path, and
whether the file exists on disk.

In text format (default), one path is printed per line. JSON and YAML
emit the full ResolvedPath shape (path, source, scope, exists) so
callers can drive scripts off precedence metadata.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateFormat(format); err != nil {
				return err
			}
			cwd, err := resolveCwd(from)
			if err != nil {
				return err
			}
			chain := o.resolver(cwd)
			return renderMany(cmd.OutOrStdout(), chain, format)
		},
	}
	cmd.Flags().StringVar(&format, "format", formatText, "Output format: text|json|yaml")
	cmd.Flags().StringVar(&from, "from", "", "Resolve from this directory instead of os.Getwd()")
	kitcli.SetSideEffect(cmd, kitcli.SideEffectRead)
	kitcli.SetIdempotency(cmd, kitcli.IdempotencyYes)
	return cmd
}

func validateFormat(f string) error {
	switch f {
	case formatText, formatJSON, formatYAML:
		return nil
	default:
		return fmt.Errorf("unknown --format %q (want text|json|yaml)", f)
	}
}

// resolveCwd returns from when non-empty, else os.Getwd().
func resolveCwd(from string) (string, error) {
	if strings.TrimSpace(from) != "" {
		return from, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	return cwd, nil
}

// highestExisting returns the first ResolvedPath with Exists=true.
func highestExisting(chain []ResolvedPath) (ResolvedPath, bool) {
	for _, p := range chain {
		if p.Exists {
			return p, true
		}
	}
	return ResolvedPath{}, false
}

// renderOne prints a single ResolvedPath in the requested format.
// Text format prints just the path -- callers piping to `cat` or
// `xargs` do not need to parse anything.
func renderOne(w io.Writer, p ResolvedPath, format string) error {
	switch format {
	case formatText:
		_, err := fmt.Fprintln(w, p.Path)
		return err
	case formatJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(p)
	case formatYAML:
		return yaml.NewEncoder(w).Encode(p)
	}
	return fmt.Errorf("unknown format %q", format)
}

// renderMany prints the precedence chain. Text format is one path per
// line; JSON/YAML emit the full slice so callers see metadata.
func renderMany(w io.Writer, chain []ResolvedPath, format string) error {
	switch format {
	case formatText:
		for _, p := range chain {
			if _, err := fmt.Fprintln(w, p.Path); err != nil {
				return err
			}
		}
		return nil
	case formatJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		// Preserve the empty-array shape rather than emitting `null`
		// when chain is nil, so JSON consumers can iterate
		// unconditionally.
		if chain == nil {
			chain = []ResolvedPath{}
		}
		return enc.Encode(chain)
	case formatYAML:
		if chain == nil {
			chain = []ResolvedPath{}
		}
		return yaml.NewEncoder(w).Encode(chain)
	}
	return fmt.Errorf("unknown format %q", format)
}

// IsNoConfig reports whether err is the sentinel for the empty-chain
// case, so root callers can map to exit code 1.
func IsNoConfig(err error) bool {
	return errors.Is(err, errNoConfig)
}
