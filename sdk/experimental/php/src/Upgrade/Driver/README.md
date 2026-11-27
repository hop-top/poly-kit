# Driver

## What it answers

How does a `Migrator` back up and restore a config file or a directory, the way the Go drivers do? `ConfigFile`
copies named files, `FsDir` archives a directory as tar.gz. Both leave the version to the migrator's version
file; SQL drivers are not ported yet.

## Use it when

- a schema is one or more config files: `new ConfigFile([$path], name: 'config', dataDir: $dir)`
- a schema is a directory tree: `new FsDir('assets', $path, dataDir: $dir)`
- a backup taken by the Go tool must restore here, or the reverse: the layouts match

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

## See also

- [Upgrade](../README.md)
- [Go drivers: `go/core/upgrade/driver`](../../../../../../go/core/upgrade/README.md)
