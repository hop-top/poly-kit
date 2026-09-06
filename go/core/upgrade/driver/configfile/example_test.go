package configfile_test

import (
	"fmt"

	"hop.top/kit/go/core/upgrade"
	"hop.top/kit/go/core/upgrade/driver/configfile"
)

// Register the driver with a Migrator; Migration.Schema matches Name().
func ExampleNew() {
	d := configfile.NewWithOptions(
		[]configfile.Option{configfile.WithTool("mytool")},
		"/etc/mytool/config.yaml",
	)
	m := upgrade.NewMigrator("mytool", "1.2.0")
	m.AddDriver(d)
	fmt.Println(d.Name())
	// Output: config
}
