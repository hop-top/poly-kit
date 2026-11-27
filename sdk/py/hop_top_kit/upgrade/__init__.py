"""hop_top_kit.upgrade: release check, lenient version order, migrations.

The release checker (:func:`create_checker`) fetches the latest GitHub
release and notifies when a newer one exists; see :mod:`.check`.

The migration half is a port of the Go ``core/upgrade`` package, bound to
the shared ``contracts/upgrade-v1`` corpus:

- :func:`compare_versions`: kit's lenient version order, not ``packaging``.
- :class:`Migration`, :class:`Registry`: versioned migrations and their
  registry (:func:`default_registry`, :func:`register_migration`).
- :func:`migrate_document`, :func:`migrate_document_map`: the in-memory
  JSON document migrator.
- :class:`Migrator`: runs migrations across schema drivers (:class:`SchemaDriver`
  plus the optional :class:`Locker` and :class:`VersionStorer`), with
  baseline, per-step version records, backups and events.
"""

from hop_top_kit.upgrade.check import (
    Checker,
    CheckerOptions,
    UpgradeCheckError,
    UpgradeCheckResult,
    create_checker,
)
from hop_top_kit.upgrade.document import (
    DocApplied,
    DocGapError,
    DocInvalidError,
    DocNewerError,
    DocOptions,
    DocReport,
    DocStep,
    DocStepError,
    DocumentError,
    DocVerifyError,
    DocVersionError,
    migrate_document,
    migrate_document_map,
)
from hop_top_kit.upgrade.driver import Cancel, Locker, SchemaDriver, VersionStorer
from hop_top_kit.upgrade.errors import (
    BackupUnsupported,
    Canceled,
    MigrateError,
    NotRestored,
    UpgradeError,
    is_backup_unsupported,
    is_canceled,
    is_not_restored,
    is_verify,
)
from hop_top_kit.upgrade.events import SOURCE, MigrationTopics
from hop_top_kit.upgrade.migration import (
    Migration,
    Registry,
    Step,
    VerifyError,
    default_registry,
    register_migration,
    reset_registry_for_test,
)
from hop_top_kit.upgrade.migrator import (
    AppliedMigration,
    Baseline,
    FinalVerify,
    Migrator,
    SchemaStatus,
)
from hop_top_kit.upgrade.semver import (
    Semver,
    compare_semver,
    compare_versions,
    parse_semver,
)
from hop_top_kit.upgrade.versionfile import read_version_file, read_version_file_in

__all__ = [
    "SOURCE",
    "AppliedMigration",
    "BackupUnsupported",
    "Baseline",
    "Cancel",
    "Canceled",
    "Checker",
    "CheckerOptions",
    "DocApplied",
    "DocGapError",
    "DocInvalidError",
    "DocNewerError",
    "DocOptions",
    "DocReport",
    "DocStep",
    "DocStepError",
    "DocVerifyError",
    "DocVersionError",
    "DocumentError",
    "FinalVerify",
    "Locker",
    "MigrateError",
    "Migration",
    "MigrationTopics",
    "Migrator",
    "NotRestored",
    "Registry",
    "SchemaDriver",
    "SchemaStatus",
    "Semver",
    "Step",
    "UpgradeCheckError",
    "UpgradeCheckResult",
    "UpgradeError",
    "VerifyError",
    "VersionStorer",
    "compare_semver",
    "compare_versions",
    "create_checker",
    "default_registry",
    "is_backup_unsupported",
    "is_canceled",
    "is_not_restored",
    "is_verify",
    "migrate_document",
    "migrate_document_map",
    "parse_semver",
    "read_version_file",
    "read_version_file_in",
    "register_migration",
    "reset_registry_for_test",
]
