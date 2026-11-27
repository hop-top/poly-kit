package upgrade

// SchemaDriver manages version tracking and backup/restore for a named schema.
// Each driver represents a distinct data store (e.g. SQLite DB, config file,
// filesystem directory) that participates in versioned migrations.
type SchemaDriver interface {
	// Name returns the schema identifier, used to match against Migration.Schema.
	Name() string

	// Version returns the currently applied version, or "" if no version is set.
	Version() (string, error)

	// SetVersion records a version: after each applied migration, and
	// the earlier one again after a rollback or restore. A driver that
	// is not a VersionStorer is called after the Migrator wrote the
	// version file.
	SetVersion(version string) error

	// Backup copies the current state to dest directory.
	Backup(dest string) error

	// Restore replaces the current state from a backup at src directory.
	Restore(src string) error
}

// VersionStorer is an optional marker for a driver that keeps its version
// itself (a database table). For such a driver the Migrator calls only
// SetVersion to record a version and writes no version file; Version is
// the one authority. StoresVersion reports whether the marker applies.
type VersionStorer interface {
	StoresVersion() bool
}
