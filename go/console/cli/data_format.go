package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"hop.top/kit/go/console/output"
)

// ActiveFormat resolves --format the way output.Dispatch does (the
// flag when set on the command line, else the viper value, else the
// flag default, else table), so a command that renders one format
// itself and hands the rest to Dispatch makes the choice Dispatch
// would.
func ActiveFormat(cmd *cobra.Command, v *viper.Viper) output.Format {
	pf := formatFlagOf(cmd)
	switch {
	case pf != nil && pf.Changed:
		return pf.Value.String()
	case v != nil && v.GetString("format") != "":
		return v.GetString("format")
	case pf != nil && pf.DefValue != "":
		return pf.DefValue
	}
	return output.Table
}

// formatFlagOf returns the --format flag visible to cmd, preferring an
// inherited persistent flag as output.Dispatch does; nil when none is
// registered.
func formatFlagOf(cmd *cobra.Command) *pflag.Flag {
	for c := cmd; c != nil; c = c.Parent() {
		if pf := c.PersistentFlags().Lookup("format"); pf != nil {
			return pf
		}
	}
	return cmd.Flags().Lookup("format")
}
