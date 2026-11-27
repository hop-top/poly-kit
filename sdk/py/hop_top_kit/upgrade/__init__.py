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
from hop_top_kit.upgrade.migration import (
    Migration,
    Registry,
    Step,
    VerifyError,
    default_registry,
    register_migration,
    reset_registry_for_test,
)
from hop_top_kit.upgrade.semver import (
    Semver,
    compare_semver,
    compare_versions,
    parse_semver,
)

__all__ = [
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
    "Migration",
    "Registry",
    "Semver",
    "Step",
    "UpgradeCheckError",
    "UpgradeCheckResult",
    "VerifyError",
    "compare_semver",
    "compare_versions",
    "create_checker",
    "default_registry",
    "migrate_document",
    "migrate_document_map",
    "parse_semver",
    "register_migration",
    "reset_registry_for_test",
]
