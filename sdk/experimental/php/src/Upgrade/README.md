# Upgrade

## What it answers

In what order do versioned migrations run, which are pending between a stored and a target version,
and how does one JSON document migrate by its own version field? `Semver` orders versions leniently,
`Registry` holds `Migration`s, `DocumentMigrator` chains `DocStep`s over an in-memory document, and
`Migrator` runs them per schema driver (lock, baseline, backup, apply, record, rollback). Drivers:
`Driver\ConfigFile`, `FsDir`, `Sqlite`, `Postgres`. Self-update stays Go-only.

## Use it when

- you register schema migrations and need the pending ones: `Registry::default()->pending(...)`
- a JSON document carries an integer version and must be brought to the latest shape
- you compare versions the way kit does, not the way `version_compare` does: `Semver::compare()`

## Quick start

```php
// after require vendor/autoload.php
use HopTop\Kit\Upgrade\{DocStep, DocumentMigrator, Migration, Registry, Semver};

$up = static function (): void {};
$reg = new Registry();
$reg->register(new Migration('1.2.0', 'db', $up));
$reg->register(new Migration('1.1.0', 'db', $up));
echo count($reg->pending('db', '1.0.0', '1.2.0')), "\n"; // 2, ascending

echo Semver::compare('v1.9.0', '1.10.0'), "\n"; // -1

$rename = new DocStep(1, 2, static function (array $doc): array {
    $doc['title'] = $doc['name'];
    unset($doc['name']);

    return $doc;
}, note: 'rename name to title');
$r = DocumentMigrator::migrate('{"version":1,"name":"x"}', [$rename]);
echo $r->document, "\n"; // {"title":"x","version":2}
```

Migrator, in short:
```php
use HopTop\Kit\Upgrade\{Migrator, MigrateException, UpgradeErrors};
use HopTop\Kit\Upgrade\Driver\FsDir;
$m = new Migrator('mytool', '1.2.0', registry: $reg, dataDir: $dir); // null: $XDG_DATA_HOME/hop/mytool
$m->addDriver(new FsDir('assets', '/srv/assets', dataDir: $dir));
try {
    $m->run($cancellation); // optional HopTop\Kit\Serve\Cancellation, checked before each up
} catch (MigrateException $e) {
    if (UpgradeErrors::isNotRestored($e)) { /* nothing could undo the run */ }
}
```

## Contract

- Versions: a leading `v` and `+build` are dropped; major, minor and patch are the leading digits of
  their part (missing or non-numeric is 0); a release sorts above its pre-releases
- `pending($schema, $from, $to)`: `from < version <= to`, ascending, versions as registered
- Documents: steps chain by `from`, never downgrade, refuse gaps, a missing or non-integer version
  (unless `missingIsZero`) and malformed steps; on error the input is untouched
- Errors extend `UpgradeException`: `DocNewerException`, `DocVersionException`, `DocGapException`,
  `DocApplyException`, `DocInvalidException`, and `VerifyException` for a failed verify
- An empty JSON object inside a document is an empty `stdClass`, so it re-encodes as `{}`; objects whose
  keys are `0..n-1` decode as lists. Integers beyond `PHP_INT_MAX` are floats
- `run()`: per schema `Locker::lock`, `version()`, baseline, pending, `backup`, each `up`, `verify`, version
  record, final verify, rollback (`down` in reverse, else `restore`), prune, unlock (always, `finally`). A
  version is recorded after each step: in `<dataDir>/<schema>/version` (0600), unless the driver is a
  `VersionStorer`. Backups: `<dataDir>/backups/<schema>/pre-<v>/` plus `pre-<v>.version`
- Capabilities are interfaces checked with `instanceof`: `VersionStorer`, `Locker`. A driver throws
  `BackupUnsupported` from `backup`/`restore`; the run goes on without a backup, and a rollback that needs it
  ends `NotRestored`. `UpgradeErrors::isNotRestored/isBackupUnsupported/isVerify/isCanceled` search the
  `getPrevious()` chain and the failed rollback
- Events go to an optional `EventPublisher` (`kit.core.migration.*`); warnings to a `warn` closure (`error_log`)
- Drivers `ConfigFile`, `FsDir`, `Sqlite` (PDO) and `Postgres` (PDO): [Driver](Driver/README.md)
- Parity: [`contracts/upgrade-v1`](../../../../../contracts/upgrade-v1/README.md), `versions.json`,
  `document.json` and `migrator.json`, run by `tests/Upgrade/Upgrade*ConformanceTest.php`

## See also

- [Go reference: `go/core/upgrade`](../../../../../go/core/upgrade/README.md)
