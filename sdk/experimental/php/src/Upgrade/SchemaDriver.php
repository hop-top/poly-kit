<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/**
 * One data store (a database, a config file, a directory) that takes part in
 * versioned migrations. `name()` matches `Migration::$schema`.
 *
 * Every method signals failure by throwing. `backup` and `restore` throw
 * `BackupUnsupported` when kit cannot do it (a server database, a file under
 * a live connection pool); that is not a failure of the run: a backup that is
 * unsupported lets the run go on with no backup, a restore that is
 * unsupported restores nothing.
 *
 * Optional capabilities are separate interfaces the migrator checks with
 * `instanceof`: `VersionStorer` and `Locker`.
 */
interface SchemaDriver
{
    /** Schema identifier. */
    public function name(): string;

    /** The applied version, or '' when none is stored. */
    public function version(): string;

    /**
     * Records a version: after each applied migration, and the earlier one
     * again after a rollback or restore. A driver that is not a
     * `VersionStorer` is called after the migrator wrote the version file.
     */
    public function setVersion(string $version): void;

    /**
     * Copies the current state into the existing directory `$dest`.
     *
     * @throws BackupUnsupported when the store cannot be backed up
     */
    public function backup(string $dest): void;

    /**
     * Replaces the current state from the backup in `$src`.
     *
     * @throws BackupUnsupported when the store cannot be restored
     */
    public function restore(string $src): void;
}
