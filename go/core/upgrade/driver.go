package upgrade

// SchemaDriver manages version tracking and backup/restore for a named schema.
// Each driver represents a distinct data store (e.g. SQLite DB, config file,
// filesystem directory) that participates in versioned migrations.
type SchemaDriver interface {
	// Name returns the schema identifier, used to match against Migration.Schema.
	Name() string

	// Version returns the currently applied version, or "" if no version is set.
	Version() (string, error)

	// SetVersion records a new version after successful migration.
	SetVersion(version string) error

	// Backup copies the current state to dest directory.
	Backup(dest string) error

	// Restore replaces the current state from a backup at src directory.
	Restore(src string) error
}
