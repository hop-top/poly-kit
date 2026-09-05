# tidb

## What it answers

Track a schema version in a TiDB or MySQL table instead of a local file.
Local stores use the sibling drivers listed in [driver/README.md](../README.md).

## Use it when

- several installs share one database and must agree on the applied version: `New(dsn, table)`
- the schema name must differ from the default `tidb`: `WithName(name)`

## Quick start

```go
_, err := tidb.New("root:pw@tcp(127.0.0.1:4000)/kit", "schema-versions")
fmt.Println(err)
// Output: invalid table name: "schema-versions"
```

With a valid table name `New` pings the server and creates the table;
`integration_test.go` runs that path against a MySQL container.

## Contract

- `New` opens the connection, pings it, and creates `<table>(schema_name VARCHAR(255) PRIMARY KEY, version VARCHAR(255) NOT NULL DEFAULT '')` if absent. The table name must match `^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`.
- `Backup` and `Restore` are no-ops: database backup is managed outside kit.
- Call `Close` when done; the driver owns its `*sql.DB`.
- Imports `github.com/go-sql-driver/mysql`; the DSN follows that driver's format.

## Neighbours

- `go/storage/kv/tidb`: the key-value store that typically shares this DSN.
- `go/core/netpolicy`: `--offline` refuses the dial when the connection goes through a guarded dialer.

## See also

- [upgrade/README.md](../../README.md)
