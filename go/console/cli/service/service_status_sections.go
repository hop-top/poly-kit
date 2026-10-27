package service

import (
	"fmt"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// Service status rendering. The sections below kit's rows come from
// the providers registered with [cli.Root.RegisterServiceStatusProvider]
// (status_service.go in go/console/cli), collected by
// [cli.ServiceStatusSections].

// serviceStatusOutput is what `service status` and `service restart`
// report.
type serviceStatusOutput struct {
	// Services are kit's rows, one per service the instance started.
	Services []serviceStatusRow `json:"services" yaml:"services"`
	// Sections are the tool's, in priority then title order.
	Sections []cli.StatusSection `json:"sections" yaml:"sections"`
}

// renderServiceStatus prints out in the active format. json and yaml
// carry the whole object; the tabular formats print kit's rows, then,
// when the tool has sections, a blank line and one row per section in
// the root status's SECTION/STATUS/DETAIL projection.
func renderServiceStatus(cmd *cobra.Command, root *cli.Root, out serviceStatusOutput) error {
	if out.Services == nil {
		out.Services = []serviceStatusRow{}
	}
	if out.Sections == nil {
		out.Sections = []cli.StatusSection{}
	}
	format := cli.ActiveFormat(cmd, root.Viper)
	if !cli.IsTabularFormat(format) {
		return output.Dispatch(cmd, root.Viper, out)
	}
	if err := output.Dispatch(cmd, root.Viper, out.Services); err != nil {
		return err
	}
	if len(out.Sections) == 0 {
		return nil
	}
	w := cmd.OutOrStdout()
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	return output.Render(w, format, cli.StatusSectionRows(out.Sections))
}
