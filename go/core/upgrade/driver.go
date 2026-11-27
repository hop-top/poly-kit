package upgrade

import "context"

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

// Locker is an optional capability of a SchemaDriver whose store several
// processes can migrate at once. The Migrator calls Lock before it reads
// the driver's version and holds the lock for the whole run of that
// schema (version read, baseline, backup, every Up, every version record,
// rollback, prune), then calls the returned unlock.
//
// Lock blocks until the lock is acquired or ctx is done; in the second
// case it returns ctx's error and no lock is held. A Lock error fails the
// schema before anything else. An unlock error is logged, not returned.
// A driver that is not a Locker runs unlocked.
type Locker interface {
	Lock(ctx context.Context) (unlock func() error, err error)
}
