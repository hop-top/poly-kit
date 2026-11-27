package upgrade

import "errors"

// ErrBackupUnsupported is what a SchemaDriver's Backup or Restore
// returns when kit cannot back up or restore its store (a server
// database, a file under a live connection pool): errors.Is(err,
// ErrBackupUnsupported). A Backup that returns it is not a failure: the
// run goes on with no backup. A Restore that returns it restores
// nothing.
var ErrBackupUnsupported = errors.New("upgrade: backup unsupported")

// ErrNotRestored marks a rollback that could not undo a failed run: a
// Down was missing or failed and no backup could restore the schema, so
// the stored version stays at the last step still applied. Run and
// RollbackLatest return it wrapped around the cause (the failure that
// started the rollback stays in the text): errors.Is(err, ErrNotRestored).
// No reverted event is published for such a rollback.
var ErrNotRestored = errors.New("upgrade: not restored")

// notRestored keeps its own text (the vectors pin it, whatever the
// sentinels say) while matching ErrNotRestored and unwrapping to the
// cause, typically ErrBackupUnsupported.
type notRestored struct {
	text  string
	cause error
}

func (e notRestored) Error() string        { return e.text }
func (e notRestored) Unwrap() error        { return e.cause }
func (e notRestored) Is(target error) bool { return target == ErrNotRestored }
