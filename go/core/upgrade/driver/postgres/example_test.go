package postgres_test

import (
	"database/sql"
	"fmt"

	"hop.top/kit/go/core/upgrade/driver/postgres"
)

// New validates the table name before it touches the database.
func ExampleNew() {
	var db *sql.DB // the caller's handle, opened with any Postgres driver
	_, err := postgres.New(db, "schema-versions")
	fmt.Println(err)
	// Output: invalid table name: "schema-versions"
}
