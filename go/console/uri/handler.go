package uri

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/output"
	"hop.top/cite/handle/generate"
)

func handlerIDCmd(defaults HandlerConfig) *cobra.Command {
	var f handlerFlags
	f.from(defaults)
	cmd := &cobra.Command{
		Use:   "id",
		Short: "Print the stable URI handler ID",
		Long:  "Print the stable language-scoped URI handler artifact identity.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			spec := f.spec()
			id, err := spec.HandlerID()
			if err != nil {
				return err
			}
			if f.Format == formatText || f.Format == "" {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), id)
				return err
			}
			return render(cmd.OutOrStdout(), f.Format, handlerIDRow{HandlerID: id})
		},
	}
	installHandlerFlags(cmd, &f, false)
	annotateRead(cmd)
	return cmd
}

func handlerGenerateCmd(defaults HandlerConfig) *cobra.Command {
	var f handlerFlags
	f.from(defaults)
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate an OS URI handler snippet",
		Long:  "Generate an OS-specific URI handler snippet for Linux, macOS/iOS, or Windows.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			spec := f.spec()
			snippet, err := generate.Snippet(f.Platform, spec)
			if err != nil {
				return err
			}
			id, err := spec.HandlerID()
			if err != nil {
				return err
			}
			row := handlerGenerateRow{Platform: f.Platform, HandlerID: id, Snippet: snippet, Output: f.Output}
			if f.Output != "" && f.Output != "-" {
				if isDryRun(cmd) {
					planFormat := f.Format
					if planFormat == "" || planFormat == formatText {
						planFormat = output.Table
					}
					return output.RenderPlan(cmd.OutOrStdout(), planFormat, dryRunPlan{
						Command: cmd.CommandPath(),
						Args: map[string]any{
							"platform": f.Platform,
							"scheme":   spec.Scheme,
							"output":   f.Output,
						},
						Effects:              []dryRunEffect{{Kind: "write", Target: f.Output, Reversible: true, Detail: "write URI handler snippet"}},
						PrerequisitesChecked: []string{"handler-spec-valid"},
						GeneratedAt:          time.Now().UTC(),
					})
				}
				if err := os.WriteFile(f.Output, []byte(snippet), 0o644); err != nil {
					return fmt.Errorf("uri handler generate: write output: %w", err)
				}
			}
			if f.Output != "" && f.Output != "-" && f.Format == formatText {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), f.Output)
				return err
			}
			if f.Format == formatText || f.Format == "" {
				_, err := fmt.Fprint(cmd.OutOrStdout(), snippet)
				return err
			}
			return render(cmd.OutOrStdout(), f.Format, row)
		},
	}
	installHandlerFlags(cmd, &f, true)
	setSideEffect(cmd, "write-local")
	setIdempotency(cmd, "yes")
	return cmd
}

func installHandlerFlags(cmd *cobra.Command, f *handlerFlags, includeGenerate bool) {
	cmd.Flags().StringVar(&f.Vendor, "vendor", f.Vendor, "Handler vendor namespace")
	cmd.Flags().StringVar(&f.App, "app", f.App, "Handler app name")
	cmd.Flags().StringVar(&f.Language, "language", f.Language, "Handler implementation language: go|ts|py|rs|php")
	cmd.Flags().StringVar(&f.Scheme, "scheme", f.Scheme, "URI scheme handled by the app")
	cmd.Flags().StringVar(&f.AppPath, "app-path", f.AppPath, "Executable path used by OS handler artifacts")
	cmd.Flags().StringVar(&f.Instance, "instance", f.Instance, "Optional app instance identifier")
	cmd.Flags().StringVar(&f.Version, "version", f.Version, "Optional app version metadata")
	cmd.Flags().StringVar(&f.Channel, "channel", f.Channel, "Optional release channel metadata")
	cmd.Flags().StringVar(&f.DisplayName, "display-name", f.DisplayName, "Optional human display name")
	cmd.Flags().StringVar(&f.Format, "format", formatText, "Output format: text|json|yaml|table")
	if includeGenerate {
		cmd.Flags().StringVar(&f.Platform, "platform", f.Platform, "Target platform: linux|macos|ios|windows")
		cmd.Flags().StringVar(&f.Output, "output", f.Output, "Output file path, or - for stdout")
	}
}

func (f *handlerFlags) from(defaults HandlerConfig) {
	f.Vendor = defaults.Vendor
	f.App = defaults.App
	f.Instance = defaults.Instance
	f.Language = string(defaults.Language)
	f.Scheme = defaults.Scheme
	f.Version = defaults.Version
	f.Channel = defaults.Channel
	f.AppPath = defaults.AppPath
	f.DisplayName = defaults.DisplayName
	f.Platform = "linux"
	f.Output = "-"
	f.Format = formatText
}

func (f handlerFlags) spec() generate.HandlerSpec {
	return generate.HandlerSpec{
		Vendor:      f.Vendor,
		App:         f.App,
		Instance:    f.Instance,
		Language:    generate.Language(f.Language),
		Scheme:      f.Scheme,
		Version:     f.Version,
		Channel:     f.Channel,
		AppPath:     f.AppPath,
		DisplayName: f.DisplayName,
	}
}

type dryRunPlan struct {
	Command              string         `json:"command" table:"COMMAND"`
	Args                 map[string]any `json:"args,omitempty"`
	Effects              []dryRunEffect `json:"effects" table:"-"`
	PrerequisitesChecked []string       `json:"prerequisites_checked,omitempty"`
	Warnings             []string       `json:"warnings,omitempty"`
	GeneratedAt          time.Time      `json:"generated_at"`
}

type dryRunEffect struct {
	Kind       string `json:"kind" table:"KIND"`
	Target     string `json:"target" table:"TARGET"`
	Reversible bool   `json:"reversible" table:"REVERSIBLE"`
	Detail     string `json:"detail,omitempty" table:"DETAIL"`
}

func isDryRun(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	if v, err := cmd.Flags().GetBool("dry-run"); err == nil {
		return v
	}
	return false
}
