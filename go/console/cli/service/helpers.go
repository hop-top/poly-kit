package service

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// isDataFormat reports whether cmd's output will be rendered as a data
// document — json or yaml — rather than for a person or a script.
func isDataFormat(cmd *cobra.Command, v *viper.Viper) bool {
	format := cli.ActiveFormat(cmd, v)
	return format == output.JSON || format == output.YAML
}

// setAnnotationTrue sets the boolean annotation key on cmd.
func setAnnotationTrue(cmd *cobra.Command, key string) {
	if cmd == nil {
		return
	}
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[key] = "true"
}
