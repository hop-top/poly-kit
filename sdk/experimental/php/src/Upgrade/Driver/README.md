# Driver

## What it answers

How does a `Migrator` back up, restore and lock a config file, a directory, a SQLite file or a Postgres
database, the way the Go drivers do? `ConfigFile` copies named files, `FsDir` archives a directory as tar.gz,
`Sqlite` snapshots a database file, `Postgres` keeps the version in a table. Only `Postgres` keeps its own
version; the others leave it to the migrator's version file.

## Use it when

- a schema is one or more config files: `new ConfigFile([$path], name: 'config', dataDir: $dir)`
- a schema is a directory tree: `new FsDir('assets', $path, dataDir: $dir)`
- a schema is a SQLite file: `new Sqlite($path, name: 'appdb', dataDir: $dir)`, or `db: $pdo` to back up
  through your own handle
- several installs share a Postgres database and must apply each migration once:
  `new Postgres($pdo, 'schema_versions', fn () => new PDO($dsn, $user, $pass), name: 'app')`
- a backup taken by the Go tool must restore here, or the reverse: the layouts match; a PHP and a Go process
  migrating one SQLite file or Postgres database exclude each other

## Quick start

```php
// after require vendor/autoload.php
use HopTop\Kit\Upgrade\Driver\FsDir;

$dir = sys_get_temp_dir() . '/kit-fsdir-' . getmypid();
mkdir("$dir/data", 0750, true);
mkdir("$dir/bk");
file_put_contents("$dir/data/a.txt", "one\n");
$d = new FsDir('assets', "$dir/data");
$d->backup("$dir/bk");                    // writes assets.tar.gz
file_put_contents("$dir/data/a.txt", "two\n");
$d->restore("$dir/bk");
echo file_get_contents("$dir/data/a.txt"); // one
```

## Contract

- `ConfigFile`: backup file name is the cleaned full path with `/` as `_`; missing files are skipped both ways
- `FsDir`: `<name>.tar.gz`, entry names relative, directories included with permissions, no `.` root entry;
  nothing is written when the directory is missing; restore replaces the directory, files take the process
  default permissions. Needs `phar` and `zlib` (`phar.readonly` does not apply to tar)
- Restore checks every entry name first: `path traversal blocked: <name>`, nothing touched
- Fails the backup: symlinks, special files, a path part over 100 bytes (`PharData` writes ustar only)
- `version()` reads `<dataDir>/<name>/version`; `tool` selects the default data dir instead
- `Sqlite` needs `pdo_sqlite` for path-only backups. Backup is `VACUUM INTO ?` (WAL rows included, target bound,
  never quoted) to `<base>.tmp`, then renamed to `<dest>/<db file name>`; a missing file means nothing to back up
- `Sqlite` path only: restore copies beside the db, fsyncs, renames over it, removes `-wal`, `-shm`, `-journal`;
  no open handle on the file. With `db:` the handle is never closed or reconfigured, backup runs on it, and
  `restore` throws `BackupUnsupported` (a failed `up` then ends not restored, no `down` runs)
- `Sqlite` lock: `flock(LOCK_EX | LOCK_NB)` polled every 50ms on `<data dir>/<name>/.lock`, the Go driver's file;
  a cancelled wait throws `CanceledException` and holds nothing
- `Postgres` needs `pdo_pgsql`. Table `^[a-zA-Z_][a-zA-Z0-9_]{0,63}$` (`invalid table name: "<t>"`), created
  under `pg_advisory_xact_lock`; a `VersionStorer`; `backup`/`restore` throw `BackupUnsupported`
  (a failed `up` ends not restored: make it atomic). `$pdo` does version reads and writes, any error mode, never closed
- `Postgres` lock: key `kit-upgrade:<table>:<name>` hashed by the server, on a connection from the callable. The
  wait polls `pg_try_advisory_lock` every 50ms (a blocked `pg_advisory_lock` cannot be cancelled in PHP), so a
  cancelled wait holds nothing. Unlock is `pg_advisory_unlock` on that connection; `false` or an error ends the
  session with `pg_terminate_backend(pg_backend_pid())` and throws
- `Postgres` limits: the callable must return a session of its own each run; one with the caller's backend pid,
  also a persistent connection to the same DSN, is refused. A persistent lock connection keeps its session after
  the object is gone, hence the explicit unlock. No transaction-mode pooler in front
- `Postgres` tests need Docker and run apart: `make test-php-upgrade-integration` (`make test-php-sdk` excludes them)

## See also

- [Upgrade](../README.md)
- [Go drivers: `go/core/upgrade/driver`](../../../../../../go/core/upgrade/README.md)
