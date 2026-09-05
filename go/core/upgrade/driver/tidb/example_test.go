package tidb_test

import (
	"fmt"

	"hop.top/kit/go/core/upgrade/driver/tidb"
)

// New validates the table name before it opens a connection.
func ExampleNew() {
	_, err := tidb.New("root:pw@tcp(127.0.0.1:4000)/kit", "schema-versions")
	fmt.Println(err)
	// Output: invalid table name: "schema-versions"
}
